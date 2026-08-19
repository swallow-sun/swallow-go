// main.go 是 Swallow-Go 的 HTTP 服务正式入口.
//
// 做的事情:
//  1. 加载 TOML 配置 + 初始化日志.
//  2. 初始化 SQLite 数据库 + 执行版本化迁移.
//  3. 加载数据库运行配置(解密敏感配置覆盖到 cfg).
//  4. 启动埋点系统(telemetry).
//  5. 组装业务依赖(NewDeps:创建三个 Service).
//  6. 注册路由并启动 Hertz HTTP 服务.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/hlog"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"

	"github.com/swallow-sun/swallow-go/biz/handler"
	"github.com/swallow-sun/swallow-go/biz/router"
	"github.com/swallow-sun/swallow-go/internal/config"
	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/debug"
	"github.com/swallow-sun/swallow-go/internal/metrics"
	"github.com/swallow-sun/swallow-go/internal/settings"
	"github.com/swallow-sun/swallow-go/internal/telemetry"
	"github.com/swallow-sun/swallow-go/internal/trace"
)

func main() {
	// run() 把所有初始化和启动逻辑封进一个函数,返回 error 给 main 判断
	// 这样 main 保持极简,只负责"成功就正常退出,失败就打印错误并以非零码退出"
	if err := run(); err != nil {
		// fmt.Fprintf 是 Go 标准库 fmt 包里的格式化输出函数,作用是把格式化后的字符串写到第一个参数指定的 writer 里
		// 这里第一个参数 os.Stderr 是标准错误流(stderr),和 stdout 不同,stderr 专门用来输出错误信息
		// %v 是通用格式化占位符,把 err 按默认格式填进去
		fmt.Fprintf(os.Stderr, "program failed: %v\n", err)
		// os.Exit 是 Go 标准库 os 包里的函数,作用是立即终止程序,参数是退出码
		// 0 表示正常退出,非 0 表示异常退出,这里传 1 表示程序跑失败了
		// 注意:os.Exit 会立即退出,不会执行 defer,所以 defer 必须在 run() 里全部执行完
		os.Exit(1)
	}
}

// run 执行服务初始化与启动,返回非 nil error 表示启动失败.
// 所有初始化阶段的错误都通过 return error 往上抛,由 main 统一处理退出码.
func run() (runErr error) {
	// runErr 是命名返回值,defer 里可以读到它,用来判断 run 是成功还是失败
	// 这就是为什么用命名返回值而不是普通的 return err——defer 需要拿到最终的错误

	// 1. 只从 TOML 文件加载配置,不读取环境变量.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config failed: %w", err)
	}
	// 2. 根据 TOML 中的运行环境、日志等级和目录初始化日志.
	if err := logger.Init(logger.Options{
		Environment: cfg.App.Environment,
		Level:       cfg.Log.Level,
		Directory:   cfg.Log.Directory,
		MaxSizeMB:   cfg.Log.MaxSizeMB,
		MaxBackups:  cfg.Log.MaxBackups,
		MaxAgeDays:  cfg.Log.MaxAgeDays,
		Compress:    *cfg.Log.Compress,
	}); err != nil {
		return fmt.Errorf("init logger failed: %w", err)
	}
	// 清空所有的日志缓存,有些日志没输出,输出出来
	// defer 的执行顺序是 LIFO(后进先出),这个 defer 最先注册,所以最后执行
	// 程序退出前最后一步把 zap 缓冲区里还没写出去的日志全部刷掉
	defer func() { _ = logger.Sync() }()

	// trace.New() 生成一个全局唯一的启动 ID(UUID),用来标记这次程序运行的唯一性
	// 后面所有日志都会带上这个 startup_id,方便排查问题时区分是哪次启动产生的日志
	startupID := trace.New()
	// logger.AddFields 给全局 logger 加一些固定字段,之后每条日志都自动带上这些字段
	// 这里把 startup_id 加进去,这样所有日志都带着这个启动 ID
	logger.AddFields(zap.String("startup_id", startupID))
	logger.Info("program starting",
		zap.String("environment", cfg.App.Environment),
		zap.String("log_level", cfg.Log.Level),
		zap.String("log_directory", cfg.Log.Directory),
		zap.Int("log_max_size_mb", cfg.Log.MaxSizeMB),
		zap.Int("log_max_backups", cfg.Log.MaxBackups),
		zap.Int("log_max_age_days", cfg.Log.MaxAgeDays),
		zap.Bool("log_compress", *cfg.Log.Compress),
	)
	// 遍历所有已经加载的配置文件路径,打 Debug 日志记录下来
	// 举个例子,如果配了 config.toml + config.local.toml,这里会打两条日志
	for _, source := range cfg.LoadedSources {
		logger.Debug("loaded config file", zap.String("path", source))
	}

	// 这个 defer 在 run 返回后才执行,如果 runErr 非 nil(也就是 run 失败了),打一条 Error 日志
	// 注意执行顺序:这个 defer 在 logger.Sync() 之后注册,所以先执行(LIFO)
	defer func() {
		if runErr != nil {
			logger.Error("program startup failed", zap.Error(runErr))
		}
	}()

	// 3. 初始化数据库
	// data.NewSQLite 打开 SQLite 数据库文件 + 执行版本化迁移
	// 返回 repo 是一个 *sqlite_repo,后面所有数据库操作都通过它
	repo, err := data.NewSQLite(cfg.Database.Path, cfg.Database.MigrationsDir)
	if err != nil {
		return fmt.Errorf("init database failed: %w", err)
	}

	// settings.New 创建一个加密配置服务,负责从数据库里读取加密存储的敏感配置(比如 LLM API Key)
	// 第二个参数传数据库文件路径,是因为加密密钥从数据库文件路径派生出来的
	settingsService, err := settings.New(repo, cfg.Database.Path)
	if err != nil {
		// 初始化失败了,数据库已经打开,得先关掉再返回,否则资源泄漏
		// _ = 忽略 Close 的错误,因为现在是在处理更重要的初始化失败
		_ = repo.Close()
		return fmt.Errorf("init settings service failed: %w", err)
	}

	// settingsService.LoadInto 从数据库读取加密的运行配置,解密后覆盖到 cfg 里
	// context.Background() 是一个空的 context,不设超时,不设取消信号
	// 这里用 Background 是因为启动阶段不需要超时控制
	if err := settingsService.LoadInto(context.Background(), cfg); err != nil {
		_ = repo.Close()
		return fmt.Errorf("load runtime config failed: %w", err)
	}

	// cfg.ValidateRuntime 校验运行时必填配置是否都填了(比如 LLM BaseURL,Model 等)
	if err := cfg.ValidateRuntime(); err != nil {
		_ = repo.Close()
		return fmt.Errorf("validate runtime config failed: %w", err)
	}

	// 到这里配置全部加载完毕,再检查一下 LLM API Key 有没有填
	if cfg.LLM.APIKey == "" {
		_ = repo.Close()
		return fmt.Errorf("LLM API Key not configured")
	}

	// 数据库准备完成后再启动埋点,保证退出时能够先刷新事件再关闭数据库.
	// telemetry.Init 初始化埋点系统,参数 256 是事件 channel 的缓冲区大小
	// 埋点事件先写到 channel,后台有 goroutine 异步消费写库,不阻塞业务逻辑
	telemetry.Init(256)
	// telemetry.SetSink 设置埋点事件的落地目标,这里设成 data.EventSinkAdapter
	// EventSinkAdapter 把埋点事件写到 repo(SQLite 数据库)里
	telemetry.SetSink(data.EventSinkAdapter{Repo: repo})

	// trace.SetSpanSink 设置 Span 追踪记录的写库目标
	// SpanSinkAdapter 把 trace.Span 转成 data.Span 写进 spans 表
	trace.SetSpanSink(data.SpanSinkAdapter{Repo: repo})

	// metrics.Init 注册所有 Prometheus 指标到默认 Registerer.
	// 必须在业务代码调 RecordRequest 之前调,否则指标变量是 nil.
	// 内部用 promauto 创建指标,自动注册,重复调不会 panic.
	metrics.Init()

	// 这是关闭资源的 defer,执行顺序在前面两个 defer 之前(LIFO,最后注册最先执行)
	// 程序退出时:先刷新埋点事件(telemetry.Shutdown)再关闭数据库(repo.Close)
	// 顺序不能反:如果先关数据库,埋点事件就写不进去了
	defer func() {
		// context.WithTimeout 创建一个带超时的 context,超时时间 5 秒
		// 第一个参数 context.Background() 是父 context,第二个参数是超时时长
		// 返回一个新 context 和一个 cancel 函数
		// 这个 context 用来控制 telemetry.Shutdown 的执行时间,最多等 5 秒
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		// defer cancel() 保证这个 context 最终会被取消,避免 context 泄漏
		// 即使 shutdown 正常完成,cancel 也要调,这是 Go 的惯例
		defer cancel()

		// telemetry.Shutdown 刷新所有还没写出去的埋点事件到数据库
		// 传入 shutdownCtx,最多等 5 秒,超时就不再等了
		if err := telemetry.Shutdown(shutdownCtx); err != nil {
			logger.Error("telemetry shutdown failed", zap.Error(err))
		}
		// 埋点事件刷完,再关闭数据库连接
		if err := repo.Close(); err != nil {
			logger.Error("database close failed", zap.Error(err))
		}
	}()

	// 4. 组装业务依赖(使用数据库加载并解密后的 llm 配置)
	// 项目的依赖组装工厂——程序启动时调一次,把所有零件拼好塞进一个 Deps 结构体里,后续 handler 直接用
	deps := handler.NewDeps(cfg, repo)

	// 启动 pprof 调试服务(如果 config 里配了 pprof_port > 0)
	// 返回 shutdown 函数,放到 defer 里在程序退出时优雅关闭
	// 生产环境 pprof_port=0 不会启动,开发时在 config.local.toml 里设成 6060 即可
	pprofShutdown := debug.Start(cfg.Debug.PProfPort)
	if pprofShutdown != nil {
		defer pprofShutdown()
	}

	// 启动 Prometheus metrics 服务(如果 config 里配了 metrics_port > 0)
	// 返回 shutdown 函数,放到 defer 里在程序退出时优雅关闭
	// 生产环境 metrics_port=0 不会启动,开发时在 config.local.toml 里设成 9100 即可
	// curl http://localhost:9100/metrics 能看到所有指标
	metricsShutdown := metrics.Start(cfg.Metrics.MetricsPort)
	if metricsShutdown != nil {
		defer metricsShutdown()
	}

	// Hertz 框架自带一套日志系统(hlog),这里把它转发到项目唯一的全局 logger
	// hlog.SetLogger 接收一个实现了 hlog.FullLogger 接口的适配器
	// logger.NewHertzAdapter() 返回一个适配器,把 Hertz 的日志调用转发到 zap
	// 这样整个项目只有一套日志输出,不会出现 Hertz 日志和业务日志各打各的
	hlog.SetLogger(logger.NewHertzAdapter())

	// server.Default 是 Hertz 框架提供的函数,创建一个默认配置的 HTTP 服务实例
	// server.WithHostPorts 是 Hertz 的可选配置项,设置监听地址和端口
	// fmt.Sprintf(":%d", cfg.Server.Port) 拼出类似 ":8080" 的监听地址
	// 冒号前面没有 IP,表示监听所有网卡(0.0.0.0)
	httpServer := server.Default(
		server.WithHostPorts(fmt.Sprintf(":%d", cfg.Server.Port)),
		// 限制请求体最大 1MB, 防止恶意大包消耗内存
		server.WithMaxRequestBodySize(handler.MaxRequestBodySize),
	)

	// router.Register 把所有业务路由注册到 httpServer 上
	// 传入 deps 是因为 handler 需要 llm/memory/identity 等依赖
	router.Register(httpServer, deps)

	logger.Info("HTTP server started", zap.Int("port", cfg.Server.Port))
	// httpServer.Spin 是 Hertz 框架提供的方法,作用是启动 HTTP 服务并阻塞当前 goroutine
	// 开始监听端口,接收请求,处理请求,直到收到关闭信号才返回
	// Spin 返回意味着服务要关闭了,接下来执行上面的 defer,最后 run 返回 nil
	httpServer.Spin()
	return nil
}
