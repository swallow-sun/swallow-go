// model.go 放长期记忆的领域类型定义.
//
// 做的事情:
//  1. 定义 CandidateSpec: 记忆候选的规格, 给 candidate_service.CreateCandidate 用.
//  2. 定义 SearchResult: 记忆检索的结果, 给 retrieval.Search 用.
//
// 方案 16.11 节: 受控长期记忆.
// 核心原则: 模型不能未经确认自动写入长期记忆.
package memory

import (
	"github.com/swallow-sun/swallow-go/internal/data"
)

// CandidateSpec 是创建记忆候选的规格.
// 对话产生候选时, policy 层构造这个对象, 传给 candidate_service.CreateCandidate.
type CandidateSpec struct {
	// UserID 哪个用户的候选
	UserID int64
	// SessionID 来源会话 ID
	SessionID string
	// TraceID 来源对话的 trace ID
	TraceID string
	// Content 候选记忆内容, 比如"用户喜欢简短的回答"
	Content string
	// MemoryType 记忆类型: preference/fact/instruction/persona
	// 用 data 包的常量: data.MemoryTypePreference 等
	MemoryType string
	// Source 来源: data.MemoryCandidateSourceRule(规则) / data.MemoryCandidateSourceModel(模型)
	Source string
	// Reason 为什么建议保存, 给用户看的解释
	Reason string
	// UsageHint 保存后可能如何使用, 给用户看的解释
	UsageHint string
}

// SearchResult 是记忆检索的结果.
// 给 retrieval.Search 返回, 包含匹配的记忆列表和查询统计.
type SearchResult struct {
	// Rows 返回的记忆列表, 按相关性排序
	Rows []data.Memory
	// Limit 查询时限制的最大返回数
	Limit int
	// Returned 实际返回的数量
	Returned int
}

// ToMemoryCandidate 把 CandidateSpec 转成 data.MemoryCandidate, 给 repo 用.
// Status 初始为 pending, CreatedAt 由数据库自动填.
func (s CandidateSpec) ToMemoryCandidate() data.MemoryCandidate {
	return data.MemoryCandidate{
		UserID:      s.UserID,
		SessionID:   s.SessionID,
		TraceID:     s.TraceID,
		Content:     s.Content,
		MemoryType:  s.MemoryType,
		Source:      s.Source,
		Reason:      s.Reason,
		UsageHint:   s.UsageHint,
		Status:      data.MemoryCandidateStatusPending,
	}
}
