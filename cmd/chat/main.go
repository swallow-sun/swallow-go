// cmd/chat/main.go 是 Phase 2 的 CLI 对话调试入口。
// 流式对话 + SQLite 持久化（重启不丢历史）。
// 不是正式服务入口，正式入口是 cmd/server。

package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"

	"github.com/swallow-sun/swallow-go/internal/agent"
	"github.com/swallow-sun/swallow-go/internal/config"
	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/identity"
	"github.com/swallow-sun/swallow-go/internal/memory"
	"github.com/swallow-sun/swallow-go/internal/provider/llm"
	"github.com/swallow-sun/swallow-go/internal/telemetry"
	"github.com/swallow-sun/swallow-go/internal/trace"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "程序运行失败: %v\n", err)
		os.Exit(1)
	}
}

// run 执行 CLI 初始化与对话循环。
// 初始化失败返回非 nil error（由 main 置非零退出码）；
// 用户主动退出（quit / EOF）返回 nil。
func run() error {
	userName := flag.String("user", "owner", "用户名（默认 owner）")
	flag.Parse()

	// 1. 初始化日志 + 埋点
	if err := logger.Init(); err != nil {
		return fmt.Errorf("初始化日志失败: %w", err)
	}
	defer func() { _ = logger.Sync() }()
	// 2. 加载配置
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	if cfg.LLM.APIKey == "" {
		return fmt.Errorf("LLM API Key 未配置")
	}

	// 3. 初始化数据库
	repo, err := data.NewSQLite(cfg.Database.Path, cfg.Database.MigrationsDir)
	if err != nil {
		return fmt.Errorf("初始化数据库失败: %w", err)
	}
	// 数据库准备完成后再启动埋点，保证退出时能够先刷新事件再关闭数据库。
	telemetry.Init(256)
	telemetry.SetSink(data.EventSinkAdapter{Repo: repo})
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := telemetry.Shutdown(shutdownCtx); err != nil {
			logger.Error("关闭埋点系统失败", zap.Error(err))
		}
		if err := repo.Close(); err != nil {
			logger.Error("关闭数据库失败", zap.Error(err))
		}
	}()

	// 4. 用户登录 + 创建会话
	idm := identity.New(repo)
	ctx := context.Background()
	user, err := idm.LoginOrCreateUser(ctx, *userName)
	if err != nil {
		return fmt.Errorf("用户初始化失败: %w", err)
	}
	sessionID, err := idm.NewSession(ctx, user.ID)
	if err != nil {
		return fmt.Errorf("创建会话失败: %w", err)
	}

	logger.Info("会话已创建",
		zap.String("user", user.Name),
		zap.Int64("user_id", user.ID),
		zap.String("session_id", sessionID),
	)

	// 5. 创建 LLM Provider、Memory、Agent
	llmProvider := llm.NewOpenAICompat(llm.Config{
		BaseURL: cfg.LLM.BaseURL,
		APIKey:  cfg.LLM.APIKey,
		Model:   cfg.LLM.Model,
	})

	mem := memory.New(repo)

	ag, err := agent.NewWithDB(
		llmProvider, cfg.LLM.Model, "prompts/system.md",
		mem, sessionID, user.ID,
	)
	if err != nil {
		return fmt.Errorf("初始化 Agent 失败: %w", err)
	}

	// 6. 循环读取用户输入
	scanner := bufio.NewReader(os.Stdin)
	fmt.Printf("Swallow CLI 已启动（用户: %s，会话: %s）\n", user.Name, sessionID[:8])
	fmt.Println("输入消息开始对话（quit 退出）：")

	for {
		fmt.Print("\n你: ")
		input, err := scanner.ReadString('\n')
		if err != nil {
			logger.Debug("命令行输入结束", zap.Error(err))
			return nil // EOF，正常退出
		}

		input = trimNewline(input)

		if input == "quit" {
			return nil // 用户主动退出
		}
		if input == "" {
			continue
		}

		// 7. 流式对话
		chatCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		streamReader, err := ag.ChatStream(chatCtx, input)
		if err != nil {
			logger.Error("LLM 流式调用失败", zap.Error(err))
			fmt.Printf("助手: 出错了：%v\n", err)
			cancel()
			continue // 运行时错误，不退出程序
		}

		traceID := agent.GetTraceID(streamReader)

		// 8. 逐块读取并实时打印
		fmt.Print("助手: ")
		var replyBuilder strings.Builder
		success := true

		for {
			chunk, done, err := streamReader.Next()
			if err != nil {
				logger.Error("流式读取失败", zap.Error(err), zap.String("trace_id", traceID))
				fmt.Printf("\n[读取错误：%v]\n", err)
				success = false
				break
			}
			if done {
				break
			}
			fmt.Print(chunk)
			replyBuilder.WriteString(chunk)
		}
		fmt.Println()

		// 流读取结束后，取得 token 用量。
		usage := streamReader.Usage()

		// 取得首字延迟和完整生成耗时。
		metrics := agent.GetStreamMetrics(streamReader)

		// 关闭底层 HTTP 响应体。
		streamReader.Close()

		// 9. 持久化助手回复 + 刷新会话活跃时间
		finishCtx := trace.WithID(context.Background(), traceID)
		if success {
			if err := ag.FinishStream(
				finishCtx,
				replyBuilder.String(),
				usage,
				metrics,
			); err != nil {
				logger.Error(
					"保存助手回复失败",
					zap.Error(err),
					zap.String("trace_id", traceID),
				)
			}
		}
		idm.TouchSession(finishCtx, sessionID)
		cancel()

		logger.Info("LLM 流式调用完成",
			zap.String("trace_id", traceID),
			zap.String("model", cfg.LLM.Model),
			zap.Int("chars", replyBuilder.Len()),
			zap.Int("prompt_tokens", usage.PromptTokens),
			zap.Int("completion_tokens", usage.CompletionTokens),
			zap.Int("total_tokens", usage.TotalTokens),
			zap.Int("cache_hit_tokens", usage.CacheHitTokens()),
			zap.Int("cache_miss_tokens", usage.CacheMissTokens()),
			zap.Int("reasoning_tokens", usage.CompletionTokensDetails.ReasoningTokens),
			zap.Int64("first_token_ms", metrics.FirstTokenMs),
			zap.Int64("total_duration_ms", metrics.TotalDurationMs),
		)
	}
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
