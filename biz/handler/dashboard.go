// dashboard.go 放 GET /api/v1/dashboard/model-usage 接口的 handler.
//
// 做的事情:
//  1. 从 URL 查询参数取 from 和 to 两个日期.
//  2. 调 DashboardService.GetModelUsage 查模型用量日聚合数据.
//  3. 把 service 返回的结果直接序列化成 JSON 响应发给客户端.
//
// 方案 15.7 节: 看板由 Go 服务端提供只读聚合接口, 不允许前端直接连数据库.
package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// GetModelUsage GET /api/v1/dashboard/model-usage?from=YYYY-MM-DD&to=YYYY-MM-DD
// 客户端在 URL 里传 from 和 to 两个日期参数, 返回这个日期范围内的模型用量日聚合数据.
func (d *Deps) GetModelUsage(ctx context.Context, c *app.RequestContext) {
	// 从 URL 查询参数里取 from 和 to, 比如 ?from=2026-08-01&to=2026-08-18
	// c.Query 返回 []byte, 用 string() 转成 Go 字符串
	dateFrom := string(c.Query("from"))
	dateTo := string(c.Query("to"))

	// 没传日期参数, 返回 400, 告诉客户端这两个参数是必填的
	if dateFrom == "" || dateTo == "" {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "from and to are required"})
		return
	}

	// 调 DashboardService 查模型用量日聚合数据.
	// d.dashboard 是 Deps 里的 DashboardService 指针, 在 NewDeps 时创建好的.
	// 返回两个值: result 是查询结果, err 是错误
	// result 里包含 From, To 和 Items(日聚合记录列表)
	result, err := d.dashboard.GetModelUsage(ctx, dateFrom, dateTo)
	if err != nil {
		// 打日志, 记录是哪个日期范围查失败了, 方便排查
		logger.Error("query model usage failed",
			zap.String("from", dateFrom),
			zap.String("to", dateTo),
			zap.Error(err),
		)
		// 返回 500 + 笼统信息, 不把数据库错误细节泄露给客户端
		c.JSON(consts.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	// 返回 HTTP 200 + JSON 响应体.
	// result 已经是带 json tag 的结构体, 直接序列化发给客户端.
	c.JSON(consts.StatusOK, result)
}
