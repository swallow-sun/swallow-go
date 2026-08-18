// ping.go 放 GET /ping 健康检查接口的 handler。
//
// 做的事情：
//  返回 "pong" 表示服务还活着，客户端定期调这个接口做存活检测。
package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// Ping GET /ping
// 健康检查接口，返回 {"message": "pong"}，给负载均衡或监控探活用的。
func Ping(ctx context.Context, c *app.RequestContext) {
	c.JSON(consts.StatusOK, utils.H{
		"message": "pong",
	})
}
