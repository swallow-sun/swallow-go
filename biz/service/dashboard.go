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

// DashboardService 负责看板查询(只读聚合).
type DashboardService struct {
	deps *Deps
}

// NewDashboardService 创建一个 DashboardService.
func NewDashboardService(deps *Deps) *DashboardService {
	return &DashboardService{deps: deps}
}

// DashboardModelUsageItem 是一条日聚合记录, 对应 model_usage_daily 表里的一行.
type DashboardModelUsageItem struct {
	Date              string `json:"date"`                // 聚合日期, 格式 YYYY-MM-DD
	UserID            int64  `json:"user_id"`             // 用户 ID
	Provider          string `json:"provider"`            // 供应商名称
	Model             string `json:"model"`               // 模型名
	Operation         string `json:"operation"`           // 操作类型: chat/embedding/vision/asr/tts
	RequestCount      int64  `json:"request_count"`       // 请求总数
	FailedCount       int64  `json:"failed_count"`        // 失败请求数
	InputTokens       int64  `json:"input_tokens"`        // 输入 Token 总量
	OutputTokens      int64  `json:"output_tokens"`       // 输出 Token 总量
	CachedInputTokens int64  `json:"cached_input_tokens"` // 缓存命中输入 Token 总量
}

// DashboardModelUsageResult 是 GetModelUsage 的返回值.
type DashboardModelUsageResult struct {
	From  string                   `json:"from"`  // 查询起始日期
	To    string                   `json:"to"`    // 查询结束日期
	Items []DashboardModelUsageItem `json:"items"` // 日聚合记录列表
}

// GetModelUsage 按日期范围查模型用量日聚合数据.
// dateFrom 和 dateTo 格式为 YYYY-MM-DD, 返回按日期倒序排列的记录.
// 方案 15.7 节: GET /api/v1/dashboard/model-usage?from=...&to=...
func (s *DashboardService) GetModelUsage(ctx context.Context, dateFrom, dateTo string) (DashboardModelUsageResult, error) {
	// 参数校验: 日期不能为空
	if dateFrom == "" || dateTo == "" {
		return DashboardModelUsageResult{}, fmt.Errorf("from and to are required")
	}

	// 校验日期格式: 必须是 YYYY-MM-DD
	// time.Parse 解析日期字符串, 解析失败说明格式不对
	if _, err := time.Parse("2006-01-02", dateFrom); err != nil {
		return DashboardModelUsageResult{}, fmt.Errorf("invalid from date format, want YYYY-MM-DD: %w", err)
	}
	if _, err := time.Parse("2006-01-02", dateTo); err != nil {
		return DashboardModelUsageResult{}, fmt.Errorf("invalid to date format, want YYYY-MM-DD: %w", err)
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
			Date:              row.Date,
			UserID:            row.UserID,
			Provider:          row.Provider,
			Model:             row.Model,
			Operation:         row.Operation,
			RequestCount:      row.RequestCount,
			FailedCount:       row.FailedCount,
			InputTokens:       row.InputTokens,
			OutputTokens:      row.OutputTokens,
			CachedInputTokens: row.CachedInputTokens,
		})
	}

	return DashboardModelUsageResult{
		From:  dateFrom,
		To:    dateTo,
		Items: items,
	}, nil
}
