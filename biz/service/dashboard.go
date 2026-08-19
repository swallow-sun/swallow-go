// dashboard.go 放看板查询服务.
//
// 做的事情:
//  1. 提供 DashboardService.GetModelUsage: 按日期范围查日聚合用量数据.
//  2. 返回 DashboardResult 给 handler 序列化成 JSON 响应.
//
// 设计要点:
//   - 方案 15.7 节: 看板由 Go 服务端提供只读聚合接口, 不允许前端直接连数据库.
//   - 方案 15.7 节: 大范围查询使用预聚合表(model_usage_daily), 不能每次扫描原始事件.
//   - 阶段 2 目标: 先做数据库查询验证, 不急着开发完整 Web 看板.
package service

import (
	"context"
	"fmt"
	"time"
)

// NewDashboardService 创建一个 DashboardService.
func NewDashboardService(deps *Deps) *DashboardService {
	return &DashboardService{deps: deps}
}

// OwnerToken 返回启动时已经从加密数据库配置加载的主人令牌。
func (s *DashboardService) OwnerToken() string {
	return s.deps.cfg.Auth.OwnerToken
}

// Error 返回可安全展示给客户端的参数错误说明。
func (e *DashboardValidationError) Error() string { return e.Message }

// GetModelUsage 按日期范围查模型用量日聚合数据.
// dateFrom 和 dateTo 格式为 YYYY-MM-DD, 返回按日期倒序排列的记录.
// 方案 15.7 节: GET /api/v1/dashboard/model-usage?from=...&to=...
func (s *DashboardService) GetModelUsage(ctx context.Context, dateFrom, dateTo string) (DashboardModelUsageResult, error) {
	// 参数校验: 日期不能为空
	if dateFrom == "" || dateTo == "" {
		return DashboardModelUsageResult{}, &DashboardValidationError{Message: "from and to are required"}
	}

	// 校验日期格式: 必须是 YYYY-MM-DD
	// time.Parse 解析日期字符串, 解析失败说明格式不对
	from, err := time.Parse("2006-01-02", dateFrom)
	if err != nil {
		return DashboardModelUsageResult{}, &DashboardValidationError{Message: "invalid from date format, want YYYY-MM-DD"}
	}
	to, err := time.Parse("2006-01-02", dateTo)
	if err != nil {
		return DashboardModelUsageResult{}, &DashboardValidationError{Message: "invalid to date format, want YYYY-MM-DD"}
	}
	if from.After(to) {
		return DashboardModelUsageResult{}, &DashboardValidationError{Message: "from must not be after to"}
	}
	if int(to.Sub(from).Hours()/24)+1 > MaxDashboardRangeDays {
		return DashboardModelUsageResult{}, &DashboardValidationError{Message: "date range exceeds maximum allowed days"}
	}

	// 调 repo 查日聚合数据
	rows, err := s.deps.repo.GetDailyUsage(ctx, dateFrom, dateTo)
	if err != nil {
		return DashboardModelUsageResult{}, fmt.Errorf("query daily usage: %w", err)
	}

	// 转成 handler 用的结构体
	items := make([]DashboardModelUsageItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, DashboardModelUsageItem{
			Date:                row.Date,
			UserID:              row.UserID,
			Provider:            row.Provider,
			Model:               row.Model,
			Operation:           row.Operation,
			RequestCount:        row.RequestCount,
			FailedCount:         row.FailedCount,
			InputTokens:         row.InputTokens,
			OutputTokens:        row.OutputTokens,
			CachedInputTokens:   row.CachedInputTokens,
			EstimatedCostMicros: row.EstimatedCostMicros,
			Currency:            row.Currency,
		})
	}

	return DashboardModelUsageResult{
		From:  dateFrom,
		To:    dateTo,
		Items: items,
	}, nil
}
