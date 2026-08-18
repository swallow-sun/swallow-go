// pprof.go 放 pprof 调试服务器的启动逻辑.
//
// 做的事情:
//  1. Start 根据 config 里的 pprof_port 决定是否启动 pprof HTTP 服务.
//  2. 启动后在单独的 goroutine 里跑一个标准库 http.Server,注册 net/http/pprof 的 handler.
//  3. 返回一个 shutdown 函数,程序退出时调用,优雅关闭 pprof 服务.
//
// 为什么要单独端口:
//   pprof 暴露的接口能看 goroutine 堆栈,堆内存分配等敏感信息,
//   不能和业务端口混在一起,生产环境应该保持 pprof_port=0(不启动).
package debug

import (
	"context"
	"fmt"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// Start 根据 pprofPort 启动 pprof HTTP 服务.
// pprofPort <= 0 时不启动,返回 nil.
// pprofPort > 0 时在单独 goroutine 里启动,返回 shutdown 函数.
// shutdown 函数用于优雅关闭 pprof 服务,程序退出时调用.
func Start(pprofPort int) func() {
	// 端口 <= 0 说明没开 pprof,直接返回 nil 的 shutdown 函数
	if pprofPort <= 0 {
		return nil
	}

	// mux 是 HTTP 路由器(标准库 http.ServeMux)
	// pprof 的 handler 是标准库自带的,通过 init() 自动注册到 http.DefaultServeMux
	// 但我们不想用 DefaultServeMux(可能被别的代码注册了乱七八糟的东西),
	// 所以自己建一个 mux,手动把 pprof 的 handler 挂上去
	mux := http.NewServeMux()

	// 把 pprof 的各种 handler 挂到 mux 上
	// pprof.Index 是 pprof 的首页,列出所有可用的 profile
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	// pprof.Cmdline 输出当前进程的命令行参数
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	// pprof.Profile 生成 CPU profile(go tool pprof 默认抓的就是这个)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	// pprof.Symbol 查找符号地址(给 pprof 工具用的辅助接口)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	// pprof.Trace 生成执行追踪(go tool pprof <url>/debug/pprof/trace?seconds=5)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	// 手动挂 heap 和 goroutine 的 handler
	// 这两个是最常用的:heap 看内存分配,goroutine 看 goroutine 堆栈
	// pprof.Handler("heap") 返回一个能抓堆内存快照的 handler
	// pprof.Handler("goroutine") 返回一个能抓 goroutine 堆栈的 handler
	mux.Handle("/debug/pprof/heap", pprof.Handler("heap"))
	mux.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
	mux.Handle("/debug/pprof/allocs", pprof.Handler("allocs"))
	mux.Handle("/debug/pprof/block", pprof.Handler("block"))
	mux.Handle("/debug/pprof/mutex", pprof.Handler("mutex"))
	mux.Handle("/debug/pprof/threadcreate", pprof.Handler("threadcreate"))

	// addr 拼出监听地址,比如 ":6060"
	addr := fmt.Sprintf(":%d", pprofPort)

	// 创建一个标准库的 http.Server
	// ReadHeaderTimeout 设 10 秒,防止慢速攻击(客户端故意慢慢发 header 占连接)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// 在单独 goroutine 里启动 pprof 服务
	// pprof 服务和业务服务并行跑,互不影响
	go func() {
		logger.Info("pprof debug server started", zap.String("addr", addr))
		// srv.ListenAndServe 阻塞运行,直到被 Shutdown 或出错
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// 非"正常关闭"的错误才打日志
			logger.Error("pprof server exited unexpectedly", zap.Error(err))
		}
	}()

	// 返回 shutdown 函数,程序退出时调用
	return func() {
		// srv.Shutdown 优雅关闭:等正在处理的请求跑完,不再接新请求
		// 5 秒超时,超时就强关
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logger.Error("pprof server shutdown failed", zap.Error(err))
		}
	}
}
