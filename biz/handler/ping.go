// ping.go 放 GET /ping 健康检查接口的 handler.
//
// 做的事情:
//  返回 "pong" 表示服务还活着, 客户端定期调这个接口做存活检测.
package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// Ping GET /ping
// 健康检查接口, 返回 {"message": "pong"}, 给负载均衡或监控探活用的.
func Ping(ctx context.Context, c *app.RequestContext) {
	// c.JSON 写一个 JSON 响应给客户端, 第一个参数是 HTTP 状态码
	// consts.StatusOK 就是 200
	// utils.H 是 Hertz 提供的工具类型, 本质是 map[string]interface{}, 方便快速构造 JSON 对象
	c.JSON(consts.StatusOK, utils.H{
		"message": "pong", // 固定返回 "pong", 客户端收到就说明服务还活着
	})
}
