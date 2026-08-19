// retrieval.go 放长期记忆的检索逻辑.
//
// 做的事情:
//  1. 定义 Retriever 结构体: 持有 repo, 按 user_id + 关键词检索正式记忆.
//  2. Search: 按 user_id + keywords 检索 active 记忆, 返回 SearchResult.
//  3. 检索结果发 memory_query 事件(含限制数, 返回数, 状态, 耗时).
//  4. 检索结果作为不可信参考数据, 注入模型时不修改系统提示.
//
// 设计要点:
//   - 方案 16.11.1 节: "第一版先使用结构化字段, 关键词和时间排序".
//   - 方案 16.11.4 节: "memory_query 事件包含限制数, 返回数, 状态和耗时".
//   - 方案 16.11.4 节: "查询无结果时正常返回空集合, 不制造虚假记忆".
//   - 方案 16.11.4 节: "用户 A 的查询永远不会返回用户 B 的私人记忆".
//   - 第一版用 LIKE, 不用向量检索.
package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/metrics"
	"github.com/swallow-sun/swallow-go/internal/telemetry"
	"github.com/swallow-sun/swallow-go/internal/trace"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// NewRetriever 创建一个 Retriever.
func NewRetriever(repo data.Repository) *Retriever {
	return &Retriever{repo: repo}
}

// Search 按 user_id + keywords 检索 active 记忆.
// 入参:
//   - ctx: 上下文, 带有 trace ID
//   - userID: 哪个用户在查
//   - keywords: 搜索关键词, 空字符串表示返回所有 active 记忆
//   - limit: 最大返回数, 0 或负数用默认值 DefaultSearchLimit
//
// 返回 SearchResult, 包含匹配的记忆列表和查询统计.
//
// 方案 16.11.4 节:
//   - "用户 A 的查询永远不会返回用户 B 的私人记忆"(SearchMemories 按 user_id 过滤)
//   - "查询无结果时正常返回空集合, 不制造虚假记忆"(没查到就返回空切片)
//   - "memory_query 事件包含限制数, 返回数, 状态和耗时"
func (s *Retriever) Search(
	ctx context.Context,
	userID int64,
	keywords string,
	limit int,
) (SearchResult, error) {
	// limit 默认值
	if limit <= 0 {
		limit = DefaultSearchLimit
	}

	// 记录查询开始时间, 后面算耗时
	start := time.Now()

	// 调 repo 检索, repo.SearchMemories 内部:
	//   - 按 user_id 过滤(跨用户隔离)
	//   - 只查 status=active 的记录(排除已删除)
	//   - keywords 不为空时用 LIKE 匹配 content 和 keywords 字段
	rows, err := s.repo.SearchMemories(ctx, userID, keywords, limit)

	// time.Since(start) 算出从 start 到现在过了多久, 就是数据库查询耗时
	elapsed := time.Since(start)

	// 从 context 里取 trace ID
	traceID := trace.FromContext(ctx)

	if err != nil {
		// 查询失败也要发埋点, 方便看板统计错误率
		telemetry.Emit(ctx, telemetry.EventMemoryQuery, map[string]any{
			"user_id":                 userID,
			"query_chars":             len([]rune(keywords)),
			"limit":                   limit,
			"trace_id":                traceID,
			telemetry.FieldStatus:     telemetry.StatusError,
			telemetry.FieldDurationMS: elapsed.Milliseconds(),
			"error":                   err.Error(),
		})

		// Prometheus 指标: 记忆查询失败
		metrics.RecordMemoryQuery(metrics.StatusFailed, float64(elapsed.Milliseconds()))

		return SearchResult{}, fmt.Errorf("search memories: %w", err)
	}

	// 查询成功, 发埋点
	// 方案 16.11.4 节: "memory_query 事件包含限制数, 返回数, 状态和耗时"
	telemetry.Emit(ctx, telemetry.EventMemoryQuery, map[string]any{
		"user_id":                 userID,
		"query_chars":             len([]rune(keywords)),
		"limit":                   limit,
		"rows_returned":           len(rows),
		"trace_id":                traceID,
		telemetry.FieldStatus:     telemetry.StatusOK,
		telemetry.FieldDurationMS: elapsed.Milliseconds(),
	})

	// Prometheus 指标: 记忆查询成功
	metrics.RecordMemoryQuery(metrics.StatusOK, float64(elapsed.Milliseconds()))

	logger.Debug("memory search completed",
		zap.Int64("user_id", userID),
		zap.Int("query_chars", len([]rune(keywords))),
		zap.Int("limit", limit),
		zap.Int("returned", len(rows)),
		zap.Int64("duration_ms", elapsed.Milliseconds()),
	)

	// 返回检索结果
	// 方案 16.11.4 节: "查询无结果时正常返回空集合, 不制造虚假记忆"
	// rows 为空时 Rows 是 nil 切片, 这里转成空切片方便调用方处理
	result := SearchResult{
		Rows:     rows,
		Limit:    limit,
		Returned: len(rows),
	}
	if result.Rows == nil {
		result.Rows = []data.Memory{}
	}
	return result, nil
}
