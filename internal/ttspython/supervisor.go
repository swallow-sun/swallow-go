// supervisor.go 管理 tts_server.py 子进程的生命周期.
//
// 做的事情:
//  1. Start: 用 exec.CommandContext 启动 Python 子进程 (tts_server.py),
//     后台 goroutine 读 stdout/stderr, 逐行解析后通过 Go logger 输出.
//  2. 等待模型加载: 轮询 /health 端点, 直到 tts_server.py 就绪或超时.
//  3. Stop: ctx 取消后子进程收到 os.Interrupt, 优雅退出.
//
// 设计思路:
//   - Go 进程作为父进程, Python 作为子进程, 通过 ctx 绑定生命周期.
//   - Python 的日志 (stdout/stderr) 被 Go 逐行读取, 通过 zap logger 统一输出,
//     格式和 Go 自身日志完全一致 (JSON + ISO8601 时间戳).
//   - Python 解释器路径和脚本路径写死为常量, 不走配置.
package ttspython

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// pythonExe 是启动 tts_server.py 用的 Python 解释器路径.
const pythonExe = "C:/Users/redmi/.local/share/TeleAgent/runtimes/python/python.exe"

// ttsScript 是 tts_server.py 相对于项目根目录的路径.
const ttsScript = "tts/tts_server.py"

var (
	// ansiRegex 匹配 ANSI 转义序列, 如 \x1b[1;31m, \x1b[0m.
	// ONNX Runtime 等库在 Windows 上输出带颜色码的日志, 需要清理掉.
	ansiRegex = regexp.MustCompile("\x1b\\[[0-9;]*m")
)

// Supervisor 管理 tts_server.py 子进程.
// 生命周期: Start() 启动 -> 阻塞等模型加载 -> 随 ctx 退出而关闭.
type Supervisor struct {
	cmd    *exec.Cmd
	healthURL string // 健康检查地址, 如 http://127.0.0.1:9880/health
}

// Start 启动 tts_server.py 子进程并等待模型加载完成.
//
// 参数:
//   - ctx: 控制子进程生命周期, ctx 取消时子进程被 kill
//   - pythonPath: Python 解释器路径
//   - scriptPath: tts_server.py 脚本路径
//   - modelDir: CosyVoice2 模型目录
//   - refWav: 参考音频路径
//   - refText: 参考音频转录文本
//   - port: tts_server.py 监听端口
//
// 返回 *Supervisor, 调用方不需要显式 Stop, ctx 取消会自动 kill 子进程.
func Start(
	ctx context.Context,
	modelDir string,
	refWav string,
	refText string,
	port int,
) (*Supervisor, error) {
	// 组装命令行参数, 和 tts_server.py 的 argparse 参数一一对应.
	args := []string{
		ttsScript,
		"--model_dir", modelDir,
		"--ref_wav", refWav,
		"--ref_text", refText,
		"--port", fmt.Sprintf("%d", port),
	}

	// exec.CommandContext: ctx 取消时自动发 os.Kill 给子进程.
	// 在 Windows 上, ctx 取消 -> cmd.Process.Kill() 强制终止.
	cmd := exec.CommandContext(ctx, pythonExe, args...)

	// Windows 上 Python 默认用 GBK 编码输出, Go 按 UTF-8 读取会乱码.
	// 强制设 PYTHONIOENCODING=utf-8 让 Python 用 UTF-8 输出.
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")

	// 不让子进程继承父进程的 stdin, 避免阻塞.
	cmd.Stdin = nil

	// 分别获取 stdout 和 stderr 的管道, 后台 goroutine 逐行读取转 logger.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}

	logger.Info("正在启动 tts_server.py 子进程",
		zap.String("model_dir", modelDir),
		zap.String("ref_wav", refWav),
		zap.Int("port", port),
	)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start tts_server.py: %w", err)
	}

	sup := &Supervisor{
		cmd:       cmd,
		healthURL: fmt.Sprintf("http://127.0.0.1:%d/health", port),
	}

	// 后台 goroutine 转发 Python stdout/stderr -> Go logger.
	// Python 的日志被解析成 Go 的结构化格式, 和 Go 自身日志完全一致.
	go sup.pumpLogs(stdout)
	go sup.pumpLogs(stderr)

	// 后台 goroutine 等待子进程退出 (正常退出或被 kill).
	// 如果子进程意外退出, 记录日志, 调用方通过 health 检查发现服务不可用.
	go func() {
		err := cmd.Wait()
		if ctx.Err() != nil {
			// ctx 被取消, 属于正常关闭流程.
			logger.Info("tts_server.py 子进程已停止（context 已取消）")
			return
		}
		if err != nil {
			logger.Error("tts_server.py 子进程异常退出", zap.Error(err))
		} else {
			logger.Info("tts_server.py 子进程正常退出")
		}
	}()

	// 等待模型加载完成: 轮询 /health 端点.
	// CosyVoice2 模型加载到 GPU 需要约 10-30 秒.
	if err := sup.waitForReady(ctx, 120*time.Second); err != nil {
		// 模型加载超时, kill 子进程并返回错误.
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("tts_server.py not ready: %w", err)
	}

	logger.Info("tts_server.py 已就绪, 开始服务")
	return sup, nil
}

// pumpLogs 逐行读取管道输出, 解析后通过 Go logger 统一输出.
// Python 侧日志格式为 "LEVEL message", Go 只需按前缀分流级别.
//
// 噪音块过滤: torch import 时探测 torio/FFmpeg 扩展失败会输出多行 traceback.
// traceback 走 stdout 不走 stderr, Python 侧 PrefixedStderr 拦不到.
// 所以在 Go 侧做块级过滤: 遇到 "Traceback (most recent call last):" 开始缓冲,
// 块结束时整块扫描是否含噪音关键词, 是则整块丢弃.
func (s *Supervisor) pumpLogs(pipe io.ReadCloser) {
	scanner := bufio.NewScanner(pipe)
	// Python 模型加载时可能输出很长的 traceback, 增大 buffer 防止截断.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var tracebackBuf []string // 非 nil 表示正在缓冲一个 traceback 块

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		// 清理 null 字节和 ANSI 转义序列.
		cleaned := cleanLogLine(line)
		if cleaned == "" {
			continue
		}

		// 正在缓冲 traceback 块.
		if tracebackBuf != nil {
			tracebackBuf = append(tracebackBuf, cleaned)
			// traceback 块结束标志: 空行, 或最终的 Error 行 (如 "RuntimeError: ...").
			if isTracebackEnd(cleaned) {
				if isNoiseBlock(tracebackBuf) {
					tracebackBuf = nil
					continue
				}
				for _, tbLine := range tracebackBuf {
					level, msg := splitLogLevel(tbLine)
					emitLog(level, msg)
				}
				tracebackBuf = nil
			}
			continue
		}

		// 检测 traceback 块开始.
		if strings.Contains(cleaned, "Traceback (most recent call last):") {
			tracebackBuf = []string{cleaned}
			continue
		}

		// 单行噪音过滤.
		if isNoiseLog(cleaned) {
			continue
		}

		// Python 日志格式: "LEVEL message".
		level, msg := splitLogLevel(cleaned)
		emitLog(level, msg)
	}

	// 管道关闭时如果还有未结束的 traceback 块, 输出掉防止丢日志.
	for _, tbLine := range tracebackBuf {
		if isNoiseLog(tbLine) {
			continue
		}
		level, msg := splitLogLevel(tbLine)
		emitLog(level, msg)
	}
}

// emitLog 按级别输出日志.
func emitLog(level, msg string) {
	switch level {
	case "error":
		logger.Error(msg)
	case "warn":
		logger.Warn(msg)
	case "debug":
		logger.Debug(msg)
	default:
		logger.Info(msg)
	}
}

// isTracebackEnd 判断一行是否为 traceback 块的结束行.
// Python traceback 以空行或最终的异常行 (如 "RuntimeError: ...") 结束.
func isTracebackEnd(line string) bool {
	if strings.TrimSpace(line) == "" {
		return true
	}
	// 异常类型行不以空格开头 (顶格), 且含冒号.
	// traceback 内部的行都以空格缩进 (File "...", self._handle, ^^^^ 等).
	stripped := strings.TrimLeft(line, " \t")
	if stripped == line && strings.Contains(line, ":") {
		return true
	}
	return false
}

// isNoiseBlock 判断整个 traceback 块是否为噪音: 块中任意一行含噪音关键词即为噪音.
func isNoiseBlock(lines []string) bool {
	for _, line := range lines {
		if isNoiseLog(line) {
			return true
		}
	}
	return false
}

// isNoiseLog 判断是否为第三方库直接写 stderr 的噪音日志.
// 这些日志不走 Python logging, 格式不可控, 我们已经在 Python 侧主动检测并输出了状态,
// 这些原始噪音直接丢弃.
func isNoiseLog(line string) bool {
	// ONNX Runtime CUDA 加载失败日志 (含 provider_bridge_ort 或 onnxruntime_providers_cuda).
	if strings.Contains(line, "provider_bridge_ort") ||
		strings.Contains(line, "onnxruntime_providers_cuda") {
		return true
	}
	// torio/FFmpeg 加载失败, 项目用 scipy 替代 torchaudio, 不影响功能.
	lower := strings.ToLower(line)
	if strings.Contains(lower, "torio") ||
		strings.Contains(lower, "ffmpeg") ||
		strings.Contains(lower, "libtorio") {
		return true
	}
	return false
}

// waitForReady 轮询 /health 端点, 直到返回 200 或超时.
// CosyVoice2 模型加载需要 10-30 秒, 设 120 秒超时留足余量.
func (s *Supervisor) waitForReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	logger.Info("等待 tts_server.py 加载模型...")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout after %s", timeout)
			}
			// 用短超时 HTTP 请求探测健康端点.
			// 模型还在加载时 uvicorn 返回 503 ( cosyvoice is None ).
			client := &http.Client{Timeout: 3 * time.Second}
			resp, err := client.Get(s.healthURL)
			if err != nil {
				// 连接被拒绝说明服务还没启动, 继续等.
				continue
			}
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil // 就绪
			}
			// 非 200 (如 503) 说明服务已启动但模型还在加载, 继续等.
		}
	}
}

// IsRunning 检查子进程是否仍在运行.
// 通过尝试 HTTP 请求判断, 因为 Process.Kill 后 cmd.ProcessState 可能还没更新.
func (s *Supervisor) IsRunning() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(s.healthURL)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// splitLogLevel 从 "LEVEL message" 格式的日志行中提取级别和消息.
// Python 侧 logging.basicConfig format='%(levelname)s %(message)s' 输出此格式.
// 例: "INFO loading model..." -> ("info", "loading model...")
// 没有 LEVEL 前缀的行是第三方库直接写 stderr 的, 按关键词推断级别:
//   含 ERROR/TRACEBACK -> error, 含 WARNING/WARN -> warn, 其余 -> debug.
func splitLogLevel(line string) (string, string) {
	upper := strings.ToUpper(line)
	for _, prefix := range []string{"ERROR", "WARNING", "INFO", "DEBUG"} {
		if strings.HasPrefix(upper, prefix+" ") {
			return strings.ToLower(prefix), line[len(prefix)+1:]
		}
	}
	// 不是 Python logging 格式, 是第三方库直接写 stderr 的, 按关键词推断.
	if strings.Contains(upper, "ERROR") || strings.Contains(upper, "TRACEBACK") {
		return "error", line
	}
	if strings.Contains(upper, "WARNING") || strings.Contains(upper, "WARN") {
		return "warn", line
	}
	return "debug", line
}

// cleanLogLine 清理 Python 日志行中的 null 字节和 ANSI 转义序列.
// ONNX Runtime 在 Windows 上输出 UTF-16 编码的日志, 经过管道读取后带有大量 null 字节.
func cleanLogLine(line string) string {
	// 去除 null 字节.
	s := strings.ReplaceAll(line, "\x00", "")
	// 去除 ANSI 转义序列.
	s = ansiRegex.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}
