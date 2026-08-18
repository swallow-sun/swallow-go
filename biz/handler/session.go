// session.go 放 POST /api/session 接口的 handler.
//
// 做的事情:
//  1. 解析客户端发来的 JSON 请求体(user_name, 可选, 默认 "owner").
//  2. 调 SessionService.CreateSession 完成用户登录和会话创建.
//  3. 把 service 返回的结果序列化成 JSON 响应发给客户端.
package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// CreateSession POST /api/session
// 客户端调这个接口开启一段新对话.
// 流程: 解析 HTTP 请求 → 调 SessionService.CreateSession → 把结果写成 JSON 返回.
//
// 参数说明:
//   - d: *Deps 指针, 持有三个 Service, d.session 就是会话服务
//   - ctx: 上下文, 能传递超时和取消信号
//   - c: Hertz 的请求上下文 *app.RequestContext, 每个 HTTP 请求对应一个,
//       能读请求体, 写响应, 操作 HTTP 头
func (d *Deps) CreateSession(ctx context.Context, c *app.RequestContext) {
	// 声明一个空的结构体变量, 准备接收客户端传来的 JSON
	var req createSessionReq

	// c.BindAndValidate 是 Hertz 框架的方法, 干两件事:
	//   1. 读请求体里的 JSON, 按 struct tag(比如 json:"user_name")填到 req 里
	//   2. 跑一遍参数校验(如果结构体字段上加了校验 tag 的话)
	// &req 是取地址, 因为要往里面写数据, 得传指针
	// JSON 格式不对或字段类型不对就返回 error
	if err := c.BindAndValidate(&req); err != nil {
		// 解析失败, 返回 HTTP 400 + 错误信息, return 结束函数, 不往下走
		// c.JSON 是 Hertz 框架的方法, 把第二个参数序列化成 JSON 写进响应体
		// consts.StatusBadRequest = 400, 表示客户端发的请求格式有问题
		// map[string]string 是 Go 内置的 map 类型, 这里当简单的 JSON 对象用
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// 调 SessionService 完成用户登录 + 会话创建
	// d.session 是 Deps 里持有的 *service.SessionService 指针
	// req.UserName 是客户端传的用户名, 可能为空, service 层会兜底填 "owner"
	result, err := d.session.CreateSession(ctx, req.UserName)
	if err != nil {
		// 底层用 fmt.Errorf 往上抛, 到入口层(handler)统一打日志
		logger.Error("session create failed", zap.String("user", req.UserName), zap.Error(err))
		// 给客户端返回 500 + 笼统错误信息, 不把具体错误泄露出去
		// consts.StatusInternalServerError = 500, 表示服务端内部出错
		c.JSON(consts.StatusInternalServerError, map[string]string{"error": "session create failed"})
		return
	}

	// 返回 HTTP 200 + JSON 响应体.
	// c.JSON 把 createSessionResp 结构体序列化成 JSON 写进响应体
	// consts.StatusOK = 200, 表示请求成功
	// createSessionResp 的字段上有 json tag, 序列化出来的 JSON 字段名由 tag 决定
	// 客户端拿到这三个字段, 其中最重要的是 SessionID, 后续调 /api/chat 要带上它
	c.JSON(consts.StatusOK, createSessionResp{
		SessionID: result.SessionID,
		UserName:  result.UserName,
		UserID:    result.UserID,
	})
}
