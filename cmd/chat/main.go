// main.go 是 Phase 2 的 CLI 对话调试入口（不是正式服务入口，正式入口是 cmd/server）。
//
// 做的事情：
//  1. 加载 TOML 配置 + 初始化日志。
//  2. 初始化 SQLite 数据库 + 执行版本化迁移。
//  3. 加载数据库运行配置（解密敏感配置覆盖到 cfg）。
//  4. 启动埋点系统（telemetry）。
//  5. 用户登录 + 创建会话（identity.Manager）。
//  6. 创建 LLM Provider、Memory Store、Agent。
//  7. 循环读取用户输入 → 流式对话 → 逐块打印 → 持久化助手回复。
//
// 流式对话 + SQLite 持久化（重启不丢历史）。
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
	"github.com/swallow-sun/swallow-go/internal/settings"
	"github.com/swallow-sun/swallow-go/internal/telemetry"
	"github.com/swallow-sun/swallow-go/internal/trace"
)

func main() {
	// run() 把所有初始化和对话循环逻辑封进一个函数，返回 error 给 main 判断
	// 这样 main 保持极简，只负责"成功就正常退出，失败就打印错误并以非零码退出"
	if err := run(); err != nil {
		// fmt.Fprintf 是 Go 标准库 fmt 包里的格式化输出函数，作用是把格式化后的字符串写到第一个参数指定的 writer 里
		// 这里第一个参数 os.Stderr 是标准错误流（stderr），和 stdout 不同，stderr 专门用来输出错误信息
		// %v 是通用格式化占位符，把 err 按默认格式填进去
		fmt.Fprintf(os.Stderr, "程序运行失败: %v\n", err)
		// os.Exit 是 Go 标准库 os 包里的函数，作用是立即终止程序，参数是退出码
		// 0 表示正常退出，非 0 表示异常退出，这里传 1 表示程序跑失败了
		// 注意：os.Exit 会立即退出，不会执行 defer，所以 defer 必须在 run() 里全部执行完
		os.Exit(1)
	}
}

// run 执行 CLI 初始化与对话循环。
// 初始化失败返回非 nil error（由 main 置非零退出码）；
// 用户主动退出（quit / EOF）返回 nil。
func run() (runErr error) {
	// runErr 是命名返回值，defer 里可以读到它，用来判断 run 是成功还是失败
	// 这就是为什么用命名返回值而不是普通的 return err——defer 需要拿到最终的错误

	// flag.String 是 Go 标准库 flag 包里的函数，用来定义一个命令行字符串参数
	// 三个参数依次是：参数名 "user"、默认值 "owner"、用法说明
	// 返回一个 *string（字符串指针），因为 flag 要能在 parse 后修改它的值
	// 举个例子：在命令行执行 `go run ./cmd/chat -user alice`，userName 指向的值就变成 "alice"
	userName := flag.String("user", "owner", "用户名（默认 owner）")
	// flag.Parse 解析命令行参数，把 -user 后面跟的值填到 userName 指向的变量里
	// 必须在定义完所有 flag 之后调用，否则参数解析不到
	flag.Parse()

	// 1. 只从 TOML 文件加载配置，不读取环境变量。
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	// 2. 根据 TOML 中的运行环境初始化日志。
	if err := logger.Init(cfg.App.Environment); err != nil {
		return fmt.Errorf("初始化日志失败: %w", err)
	}
	// 清空所有的日志缓存，有些日志没输出，输出出来
	// defer 的执行顺序是 LIFO（后进先出），这个 defer 最先注册，所以最后执行
	// 程序退出前最后一步把 zap 缓冲区里还没写出去的日志全部刷掉
	defer func() { _ = logger.Sync() }()

	// trace.New() 生成一个全局唯一的启动 ID（UUID），用来标记这次程序运行的唯一性
	// 后面所有日志都会带上这个 startup_id，方便排查问题时区分是哪次启动产生的日志
	startupID := trace.New()
	// logger.AddFields 给全局 logger 加一些固定字段，之后每条日志都自动带上这些字段
	// 这里把 startup_id 加进去，这样所有日志都带着这个启动 ID
	logger.AddFields(zap.String("startup_id", startupID))
	logger.Info("程序开始启动")
	// 遍历所有已经加载的配置文件路径，打 Debug 日志记录下来
	// 举个例子，如果配了 config.toml + config.local.toml，这里会打两条日志
	for _, source := range cfg.LoadedSources {
		logger.Debug("已加载配置文件", zap.String("path", source))
	}

	// 这个 defer 在 run 返回后才执行，如果 runErr 非 nil（也就是 run 失败了），打一条 Error 日志
	// 注意执行顺序：这个 defer 在 logger.Sync() 之后注册，所以先执行（LIFO）
	defer func() {
		if runErr != nil {
			logger.Error("程序启动失败", zap.Error(runErr))
		}
	}()

	// 3. 初始化数据库
	// data.NewSQLite 打开 SQLite 数据库文件 + 执行版本化迁移
	// 返回 repo 是一个 *sqlite_repo，后面所有数据库操作都通过它
	repo, err := data.NewSQLite(cfg.Database.Path, cfg.Database.MigrationsDir)
	if err != nil {
		return fmt.Errorf("初始化数据库失败: %w", err)
	}

	// settings.New 创建一个加密配置服务，负责从数据库里读取加密存储的敏感配置（比如 LLM API Key）
	// 第二个参数传数据库文件路径，是因为加密密钥从数据库文件路径派生出来的
	settingsService, err := settings.New(repo, cfg.Database.Path)
	if err != nil {
		// 初始化失败了，数据库已经打开，得先关掉再返回，否则资源泄漏
		// _ = 忽略 Close 的错误，因为现在是在处理更重要的初始化失败
		_ = repo.Close()
		return fmt.Errorf("初始化加密配置服务失败: %w", err)
	}

	// settingsService.LoadInto 从数据库读取加密的运行配置，解密后覆盖到 cfg 里
	// context.Background() 是一个空的 context，不设超时，不设取消信号
	// 这里用 Background 是因为启动阶段不需要超时控制
	if err := settingsService.LoadInto(context.Background(), cfg); err != nil {
		_ = repo.Close()
		return fmt.Errorf("加载数据库运行配置失败: %w", err)
	}

	// cfg.ValidateRuntime 校验运行时必填配置是否都填了（比如 LLM BaseURL、Model 等）
	if err := cfg.ValidateRuntime(); err != nil {
		_ = repo.Close()
		return fmt.Errorf("校验数据库运行配置失败: %w", err)
	}

	// 到这里配置全部加载完毕，再检查一下 LLM API Key 有没有填
	if cfg.LLM.APIKey == "" {
		_ = repo.Close()
		return fmt.Errorf("LLM API Key 未配置")
	}

	// 数据库准备完成后再启动埋点，保证退出时能够先刷新事件再关闭数据库。
	// telemetry.Init 初始化埋点系统，参数 256 是事件 channel 的缓冲区大小
	// 埋点事件先写到 channel，后台有 goroutine 异步消费写库，不阻塞业务逻辑
	telemetry.Init(256)
	// telemetry.SetSink 设置埋点事件的落地目标，这里设成 data.EventSinkAdapter
	// EventSinkAdapter 把埋点事件写到 repo（SQLite 数据库）里
	telemetry.SetSink(data.EventSinkAdapter{Repo: repo})

	// 这是关闭资源的 defer，执行顺序在前面两个 defer 之前（LIFO，最后注册最先执行）
	// 程序退出时：先刷新埋点事件（telemetry.Shutdown）再关闭数据库（repo.Close）
	// 顺序不能反：如果先关数据库，埋点事件就写不进去了
	defer func() {
		// context.WithTimeout 创建一个带超时的 context，超时时间 5 秒
		// 第一个参数 context.Background() 是父 context，第二个参数是超时时长
		// 返回一个新 context 和一个 cancel 函数
		// 这个 context 用来控制 telemetry.Shutdown 的执行时间，最多等 5 秒
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		// defer cancel() 保证这个 context 最终会被取消，避免 context 泄漏
		// 即使 shutdown 正常完成，cancel 也要调，这是 Go 的惯例
		defer cancel()

		// telemetry.Shutdown 刷新所有还没写出去的埋点事件到数据库
		// 传入 shutdownCtx，最多等 5 秒，超时就不再等了
		if err := telemetry.Shutdown(shutdownCtx); err != nil {
			logger.Error("关闭埋点系统失败", zap.Error(err))
		}
		// 埋点事件刷完，再关闭数据库连接
		if err := repo.Close(); err != nil {
			logger.Error("关闭数据库失败", zap.Error(err))
		}
	}()

	// 4. 用户登录 + 创建会话
	// identity.New 创建身份管理器，传入 repo 用于数据库读写
	// 身份管理器负责用户登录/创建、会话创建、会话活跃时间刷新
	idm := identity.New(repo)

	// context.Background() 是一个空的 context，不设超时，不设取消信号
	// 这里用 Background 是因为登录和创建会话不需要超时控制
	ctx := context.Background()

	// idm.LoginOrCreateUser 尝试按用户名登录，用户不存在则自动创建
	// *userName 是解引用 flag.String 返回的指针，拿到实际的字符串值
	// 返回 user 是用户结构体，里面有 ID、Name 等字段
	user, err := idm.LoginOrCreateUser(ctx, *userName)
	if err != nil {
		return fmt.Errorf("用户初始化失败: %w", err)
	}

	// idm.NewSession 为该用户创建一个新会话，返回会话 ID（UUID 字符串）
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
	// llm.NewOpenAICompat 创建一个兼容 OpenAI 接口的 LLM Provider
	// 传入 BaseURL、APIKey、Model 三个配置，后面用这个 Provider 调 LLM 接口
	llmProvider := llm.NewOpenAICompat(llm.Config{
		BaseURL: cfg.LLM.BaseURL,
		APIKey:  cfg.LLM.APIKey,
		Model:   cfg.LLM.Model,
	})

	// memory.New 创建记忆管理器，传入 repo 用于读写历史消息
	mem := memory.New(repo)

	// agent.NewWithDB 创建对话 Agent，参数依次是：
	// llmProvider：调 LLM 用的 provider
	// cfg.LLM.Model：模型名称
	// "prompts/system.md"：系统提示词文件路径
	// mem：记忆管理器，用于读写历史消息
	// sessionID：会话 ID，用于区分不同会话
	// user.ID：用户 ID，用于区分不同用户
	ag, err := agent.NewWithDB(
		llmProvider, cfg.LLM.Model, "prompts/system.md",
		mem, sessionID, user.ID,
	)
	if err != nil {
		return fmt.Errorf("初始化 Agent 失败: %w", err)
	}

	// 6. 循环读取用户输入
	// bufio.NewReader 创建一个带缓冲的读取器，从 os.Stdin（标准输入）读数据
	// 带缓冲的意思是：底层一次性读一块数据放到内存里，上层按需取，减少系统调用次数
	// 举个例子，用户在命令行打字，一行一行地输入，bufio.NewReader 能按行读出来
	scanner := bufio.NewReader(os.Stdin)

	// fmt.Printf 是 Go 标准库 fmt 包里的格式化输出函数，作用是把格式化后的字符串打印到标准输出（stdout）
	// %s 是字符串占位符，把 user.Name 和 sessionID[:8] 填进去
	// sessionID[:8] 是切片操作，取 sessionID 的前 8 个字符，避免 UUID 太长看着乱
	fmt.Printf("Swallow CLI 已启动（用户: %s，会话: %s）\n", user.Name, sessionID[:8])
	fmt.Println("输入消息开始对话（quit 退出）：")

	// 无限循环，每次读一行用户输入，跟 AI 对一轮话
	// 退出条件：用户输入 quit 或读到 EOF（比如 Ctrl+D）
	for {
		// 打印提示符，让用户知道该输入了
		fmt.Print("\n你: ")
		// scanner.ReadString 按分隔符读数据，这里传 '\n' 表示读到换行符为止
		// 返回的字符串包含换行符本身，后面要用 trimNewline 去掉
		// err 非 nil 通常是 EOF（比如 Ctrl+D 或输入流关闭），表示没有更多输入了
		input, err := scanner.ReadString('\n')
		if err != nil {
			// 输入读完了，Debug 日志记一下，正常退出
			logger.Debug("命令行输入结束", zap.Error(err))
			return nil // EOF，正常退出
		}

		// 去掉末尾的换行符和回车符，拿到干净的用户输入
		input = trimNewline(input)

		// 用户输入 quit，主动退出
		if input == "quit" {
			return nil // 用户主动退出
		}
		// 空输入直接跳过，不调 LLM，免得浪费 token
		if input == "" {
			continue
		}

		// 7. 流式对话
		// context.WithTimeout 创建一个带超时的 context，超时时间 60 秒
		// 第一个参数 context.Background() 是父 context
		// 返回一个新 context 和一个 cancel 函数
		// 这个 context 用来控制 LLM 流式调用的超时，60 秒内没返回就取消
		chatCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		// ag.ChatStream 发起流式对话请求，返回一个流式读取器 streamReader
		// 后面通过 streamReader.Next() 逐块读取 AI 的回复
		streamReader, err := ag.ChatStream(chatCtx, input)
		if err != nil {
			// LLM 调用失败，打 Error 日志，给用户提示，然后继续循环不退出程序
			logger.Error("LLM 流式调用失败", zap.Error(err))
			fmt.Printf("助手: 出错了：%v\n", err)
			// cancel 取消刚才创建的 context，避免 context 泄漏
			// 虽然调用失败了，但 context 已经创建，不 cancel 的话会泄漏
			cancel()
			continue // 运行时错误，不退出程序
		}

		// agent.GetTraceID 从流式读取器里取出这次调用的 trace ID
		// trace ID 用于追踪一次完整的 LLM 调用，方便排查问题
		traceID := agent.GetTraceID(streamReader)

		// 8. 逐块读取并实时打印
		// 先打印 "助手: " 前缀，后面 AI 返回的内容直接接着打印，不换行
		fmt.Print("助手: ")
		// strings.Builder 是 Go 标准库里高效的字符串拼接器
		// 比 += 拼接字符串高效，因为 += 每次都会分配新内存，Builder 内部用缓冲区
		var replyBuilder strings.Builder
		// success 标记这次流式读取是否成功，默认 true，出错时改成 false
		success := true

		// 内层循环，逐块读取 AI 的回复
		for {
			// streamReader.Next 返回三个值：
			// chunk：这一块的内容（一段文本）
			// done：是否读完了（true 表示 AI 说完了，这次对话结束）
			// err：读取出错时的错误信息
			chunk, done, err := streamReader.Next()
			if err != nil {
				// 读取出错，打日志，标记失败，跳出循环
				logger.Error("流式读取失败", zap.Error(err), zap.String("trace_id", traceID))
				fmt.Printf("\n[读取错误：%v]\n", err)
				success = false
				break
			}
			if done {
				// AI 说完了，跳出循环
				break
			}
			// 把这一块内容实时打印出来（不换行，让回复连成一段）
			fmt.Print(chunk)
			// 同时拼到 replyBuilder 里，后面持久化用
			replyBuilder.WriteString(chunk)
		}
		// 回复打印完，换一行
		fmt.Println()

		// 流读取结束后，取得 token 用量。
		// usage 里有 prompt_tokens、completion_tokens、total_tokens 等字段
		usage := streamReader.Usage()

		// 取得首字延迟和完整生成耗时。
		// metrics 里有 FirstTokenMs（首字延迟，毫秒）和 TotalDurationMs（总耗时，毫秒）
		metrics := agent.GetStreamMetrics(streamReader)

		// 关闭底层 HTTP 响应体。
		// streamReader 底层是一个 HTTP 长连接，读完数据后要关掉，否则连接泄漏
		streamReader.Close()

		// 9. 持久化助手回复 + 刷新会话活跃时间
		// trace.WithID 创建一个带 trace ID 的 context
		// 这样后面持久化操作的日志都能带上这次对话的 trace ID，方便串联排查
		finishCtx := trace.WithID(context.Background(), traceID)
		// 只有流式读取成功时才持久化，失败的对话不保存
		if success {
			// ag.FinishStream 把助手回复写入数据库，同时保存 token 用量和性能指标
			// 参数依次是：context、回复内容、token 用量、性能指标
			if err := ag.FinishStream(
				finishCtx,
				replyBuilder.String(),
				usage,
				metrics,
			); err != nil {
				// 保存失败不退出程序，只打日志，用户已经看到回复了
				logger.Error(
					"保存助手回复失败",
					zap.Error(err),
					zap.String("trace_id", traceID),
				)
			}
		}
		// idm.TouchSession 刷新会话的活跃时间（updated_at），标记这个会话刚刚用过
		// 不处理返回值，因为刷新失败不影响主流程
		idm.TouchSession(finishCtx, sessionID)
		// 取消这次对话的 context，释放资源
		cancel()

		// 打一条 Info 日志，记录这次 LLM 调用的完整信息
		// 包括 trace ID、模型名、回复字数、token 用量、性能指标等
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

// trimNewline 去掉字符串末尾的换行符（\n）和回车符（\r）。
// 因为 scanner.ReadString('\n') 返回的字符串包含换行符，需要去掉才能拿到干净的用户输入。
// Windows 下换行是 \r\n，Linux/Mac 下是 \n，所以两种都要处理。
func trimNewline(s string) string {
	// 只要字符串还有内容，且最后一个字符是 \n 或 \r，就继续去掉
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		// 切片操作，去掉最后一个字符
		// s[:len(s)-1] 表示取从开头到倒数第二个字符的部分
		s = s[:len(s)-1]
	}
	return s
}
