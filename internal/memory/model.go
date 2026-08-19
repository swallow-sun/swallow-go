// model.go 放长期记忆领域类型的行为方法，结构体统一定义在 types.go。
//
// 做的事情:
//  1. 定义 CandidateSpec: 记忆候选的规格, 给 candidate_service.CreateCandidate 用.
//  2. 定义 SearchResult: 记忆检索的结果, 给 retrieval.Search 用.
//
// 方案 16.11 节: 受控长期记忆.
// 核心原则: 模型不能未经确认自动写入长期记忆.
package memory

import "github.com/swallow-sun/swallow-go/internal/data"

// ToMemoryCandidate 把 CandidateSpec 转成 data.MemoryCandidate, 给 repo 用.
// Status 初始为 pending, CreatedAt 由数据库自动填.
func (s CandidateSpec) ToMemoryCandidate() data.MemoryCandidate {
	return data.MemoryCandidate{
		UserID:     s.UserID,
		SessionID:  s.SessionID,
		TraceID:    s.TraceID,
		Content:    s.Content,
		MemoryType: s.MemoryType,
		Source:     s.Source,
		Reason:     s.Reason,
		UsageHint:  s.UsageHint,
		Status:     data.MemoryCandidateStatusPending,
	}
}
