// cmd/server 是 Swallow-Go 的 HTTP 服务入口。
// 负责初始化基础设施、组装 Agent、注册路由并启动 Hertz 服务。
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/hlog"
	zaplog "github.com/hertz-contrib/logger/zap"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/swallow-sun/swallow-go/biz/handler"
	"github.com/swallow-sun/swallow-go/biz/router"
	"github.com/swallow-sun/swallow-go/internal/config"
	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/telemetry"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "程序运行失败: %v\n", err)
		os.Exit(1)
	}
}

// run 执行服务初始化与启动，返回非 nil error 表示启动失败。
// 所有初始化阶段的错误都通过 return error 上抛，由 main 统一处理退出码。
func run() error {
	// 1. 初始化日志 + 埋点
	if err := logger.Init(); err != nil {
		return fmt.Errorf("初始化日志失败: %w", err)
	}
	// 清空所有的日志缓存，有些日志没输出，输出出来
	defer func() { _ = logger.Sync() }()
	// 2. 加载配置
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
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

	// 4. 组装业务依赖（llm + memory + identity）
	// 项目的依赖组装工厂——程序启动时调一次，把所有零件拼好塞进一个 Deps 结构体里，后续 handler 直接用
	deps := handler.NewDeps(cfg, repo)

	// hertz 框架日志桥接到 zap，格式与业务 logger 统一
	hlog.SetLogger(zaplog.NewLogger(
		zaplog.WithCoreEnc(zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())),
	))

	httpServer := server.Default(server.WithHostPorts(fmt.Sprintf(":%d", cfg.Server.Port)))

	router.Register(httpServer, deps)

	logger.Info("HTTP 服务启动", zap.Int("port", cfg.Server.Port))
	httpServer.Spin()
	return nil
}
