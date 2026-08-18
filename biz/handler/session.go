// session.go 放 POST /api/session 接口的 handler。
//
// 做的事情：
//  1. 解析客户端发来的 JSON 请求体（user_name，可选，默认 "owner"）。
//  2. 调 SessionService.CreateSession 完成用户登录和会话创建。
//  3. 把 service 返回的结果序列化成 JSON 响应发给客户端。
package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// CreateSession POST /api/session
// 客户端调这个接口开启一段新对话。
// 流程：解析 HTTP 请求 → 调 SessionService.CreateSession → 把结果写成 JSON 返回。
func (d *Deps) CreateSession(ctx context.Context, c *app.RequestContext) {
	// 声明一个空的结构体变量，准备接收客户端传来的 JSON
	var req createSessionReq

	// 把请求体里的 JSON 解析到 req 里，同时做参数校验。
	// &req 是取地址，因为要往里面写数据。
	// JSON 格式不对或字段类型不对就返回 400
	if err := c.BindAndValidate(&req); err != nil {
		// 解析失败，返回 HTTP 400 + 错误信息，return 结束函数，不往下走
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// 调 SessionService 完成用户登录 + 会话创建
	result, err := d.session.CreateSession(ctx, req.UserName)
	if err != nil {
		// 底层用 fmt.Errorf 往上抛，到入口层（handler）统一打日志
		logger.Error("会话创建失败", zap.String("user", req.UserName), zap.Error(err))
		// 给客户端返回 500 + 笼统错误信息，不把具体错误泄露出去
		c.JSON(consts.StatusInternalServerError, map[string]string{"error": "session create failed"})
		return
	}

	// 返回 HTTP 200 + JSON 响应体。
	// createSessionResp 结构体会被序列化成 JSON 发给客户端。
	// 客户端拿到这三个字段，其中最重要的是 SessionID，后续调 /api/chat 要带上它
	c.JSON(consts.StatusOK, createSessionResp{
		SessionID: result.SessionID,
		UserName:  result.UserName,
		UserID:    result.UserID,
	})
}
