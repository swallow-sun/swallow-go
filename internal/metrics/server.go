// server.go 放 Prometheus /metrics HTTP 端点的启动逻辑。
//
// 做的事情：
//  1. Start 根据 config 里的 metrics_port 决定是否启动 metrics HTTP 服务。
//  2. 启动后在单独的 goroutine 里跑一个标准库 http.Server，注册 promhttp.Handler。
//  3. 返回一个 shutdown 函数，程序退出时调用，优雅关闭 metrics 服务。
//
// 为什么要单独端口：
//   /metrics 暴露的是运行时指标数据，虽然不如 pprof 敏感，但也不适合和业务端口混在一起。
//   生产环境如果不需要外部抓取，可以设 metrics_port=0（不启动），
//   只在需要 Prometheus 抓取时才开。
//   开发时设成 9100，Prometheus 或 curl http://localhost:9100/metrics 就能拿到指标。
package metrics

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// Start 根据 metricsPort 启动 Prometheus metrics HTTP 服务。
// metricsPort <= 0 时不启动，返回 nil。
// metricsPort > 0 时在单独 goroutine 里启动，返回 shutdown 函数。
// shutdown 函数用于优雅关闭 metrics 服务，程序退出时调用。
func Start(metricsPort int) func() {
	// 端口 <= 0 说明没开 metrics，返回 nil 的 shutdown 函数
	if metricsPort <= 0 {
		return nil
	}

	// mux 是 HTTP 路由器（标准库 http.ServeMux）
	// 只注册一个 /metrics 路径，由 promhttp.Handler() 处理
	// promhttp.Handler() 返回一个 HTTP handler，访问 /metrics 时输出所有 Prometheus 指标
	// 输出格式是 Prometheus exposition format（纯文本），Prometheus server 能直接抓取
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	// addr 拼出监听地址，比如 ":9100"
	addr := fmt.Sprintf(":%d", metricsPort)

	// 创建一个标准库的 http.Server
	// ReadHeaderTimeout 设 10 秒，防止慢速攻击（客户端故意慢慢发 header 占连接）
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// 在单独 goroutine 里启动 metrics 服务
	// metrics 服务和业务服务并行跑，互不影响
	go func() {
		logger.Info("Prometheus metrics 服务启动", zap.String("addr", addr))
		// srv.ListenAndServe 阻塞运行，直到被 Shutdown 或出错
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// 非"正常关闭"的错误才打日志
			logger.Error("metrics 服务异常退出", zap.Error(err))
		}
	}()

	// 返回 shutdown 函数，程序退出时调用
	return func() {
		// srv.Shutdown 优雅关闭：等正在处理的请求跑完，不再接新请求
		// 5 秒超时，超时就强关
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logger.Error("关闭 metrics 服务失败", zap.Error(err))
		}
	}
}
