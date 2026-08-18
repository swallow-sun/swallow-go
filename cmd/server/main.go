// main.go 是 Swallow-Go 的 HTTP 服务正式入口。
//
// 做的事情：
//  1. 加载 TOML 配置 + 初始化日志。
//  2. 初始化 SQLite 数据库 + 执行版本化迁移。
//  3. 加载数据库运行配置（解密敏感配置覆盖到 cfg）。
//  4. 启动埋点系统（telemetry）。
//  5. 组装业务依赖（NewDeps：创建三个 Service）。
//  6. 注册路由并启动 Hertz HTTP 服务。
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
	"github.com/swallow-sun/swallow-go/internal/settings"
	"github.com/swallow-sun/swallow-go/internal/telemetry"
	"github.com/swallow-sun/swallow-go/internal/trace"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "程序运行失败: %v\n", err)
		os.Exit(1)
	}
}

// run 执行服务初始化与启动，返回非 nil error 表示启动失败。
// 所有初始化阶段的错误都通过 return error 往上抛，由 main 统一处理退出码。
func run() (runErr error) {
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
	defer func() { _ = logger.Sync() }()
	startupID := trace.New()
	logger.AddFields(zap.String("startup_id", startupID))
	logger.Info("程序开始启动")
	for _, source := range cfg.LoadedSources {
		logger.Debug("已加载配置文件", zap.String("path", source))
	}
	defer func() {
		if runErr != nil {
			logger.Error("程序启动失败", zap.Error(runErr))
		}
	}()
	// 3. 初始化数据库
	repo, err := data.NewSQLite(cfg.Database.Path, cfg.Database.MigrationsDir)
	if err != nil {
		return fmt.Errorf("初始化数据库失败: %w", err)
	}
	settingsService, err := settings.New(repo, cfg.Database.Path)
	if err != nil {
		_ = repo.Close()
		return fmt.Errorf("初始化加密配置服务失败: %w", err)
	}
	if err := settingsService.LoadInto(context.Background(), cfg); err != nil {
		_ = repo.Close()
		return fmt.Errorf("加载数据库运行配置失败: %w", err)
	}
	if err := cfg.ValidateRuntime(); err != nil {
		_ = repo.Close()
		return fmt.Errorf("校验数据库运行配置失败: %w", err)
	}
	if cfg.LLM.APIKey == "" {
		_ = repo.Close()
		return fmt.Errorf("LLM API Key 未配置")
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

	// 4. 组装业务依赖（使用数据库加载并解密后的 llm 配置）
	// 项目的依赖组装工厂——程序启动时调一次，把所有零件拼好塞进一个 Deps 结构体里，后续 handler 直接用
	deps := handler.NewDeps(cfg, repo)

	// Hertz 框架日志直接转发到项目唯一的全局 logger。
	hlog.SetLogger(logger.NewHertzAdapter())

	httpServer := server.Default(server.WithHostPorts(fmt.Sprintf(":%d", cfg.Server.Port)))

	router.Register(httpServer, deps)

	logger.Info("HTTP 服务启动", zap.Int("port", cfg.Server.Port))
	httpServer.Spin()
	return nil
}
