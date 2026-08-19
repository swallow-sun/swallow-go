// memory_service.go 放记忆相关的业务编排层.
//
// 做的事情:
//  1. 定义 MemoryService 结构体: 持有 memory 包的三个组件(CandidateService/Retriever/Service).
//  2. 提供 7 个方法, 对应 7 个 API 接口, 给 handler 层调.
//  3. 方法签名只用基础类型和 data 包类型, 不依赖 HTTP 框架.
//
// 分层说明:
//   - internal/memory 包: 领域逻辑(候选产生, 检索, CRUD), 不依赖 biz 层.
//   - biz/service/memory_service.go: 编排层, 组合 memory 包的三个组件, 给 handler 调.
//   - biz/handler/memory.go: HTTP 入口, 解析请求参数, 调 MemoryService, 写 JSON 响应.
//
// 方案 16.11.1 节的 7 个 API:
//   POST   /api/v1/memory-candidates         → CreateCandidate
//   GET    /api/v1/memory-candidates           → ListCandidates
//   POST   /api/v1/memory-candidates/{id}/confirm → ConfirmCandidate
//   POST   /api/v1/memory-candidates/{id}/reject  → RejectCandidate
//   GET    /api/v1/memories                     → ListMemories
//   PATCH  /api/v1/memories/{id}               → UpdateMemory
//   DELETE /api/v1/memories/{id}               → DeleteMemory
package service

import (
	"context"
	"fmt"

	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/memory"
)

// MemoryService 是记忆相关的业务编排层.
// 持有 memory 包的三个组件:
//   - candidate: 候选创建/确认/拒绝/查询
//   - retriever: 记忆检索
//   - memService: 记忆 CRUD + 编辑 + 删除
type MemoryService struct {
	candidate *memory.CandidateService
	retriever *memory.Retriever
	memService *memory.Service
}

// NewMemoryService 创建一个 MemoryService.
// 入参是 service.Deps, 里面有 repo, 用 repo 构造 memory 包的三个组件.
func NewMemoryService(deps *Deps) *MemoryService {
	// 创建确定性规则引擎
	policy := memory.NewPolicy()
	// 创建候选管理服务, 传入 repo 和 policy
	candidate := memory.NewCandidateService(deps.repo, policy)
	// 创建检索器
	retriever := memory.NewRetriever(deps.repo)
	// 创建正式记忆 CRUD 服务
	memService := memory.NewService(deps.repo)

	return &MemoryService{
		candidate:  candidate,
		retriever:  retriever,
		memService: memService,
	}
}

// CreateCandidateResult 是 CreateCandidate 的返回值.
type CreateCandidateResult struct {
	Candidate data.MemoryCandidate `json:"candidate"`
}

// CreateCandidate 手动提交一条记忆候选.
// 方案 16.11.1 节: POST /api/v1/memory-candidates.
func (s *MemoryService) CreateCandidate(
	ctx context.Context,
	userID int64,
	sessionID, traceID, content, memoryType, reason, usageHint string,
) (CreateCandidateResult, error) {
	// 构造 CandidateSpec
	spec := memory.CandidateSpec{
		UserID:      userID,
		SessionID:   sessionID,
		TraceID:     traceID,
		Content:     content,
		MemoryType:  memoryType,
		Source:      data.MemoryCandidateSourceRule,
		Reason:      reason,
		UsageHint:   usageHint,
	}

	candidate, err := s.candidate.CreateCandidate(ctx, spec)
	if err != nil {
		return CreateCandidateResult{}, fmt.Errorf("create candidate: %w", err)
	}

	return CreateCandidateResult{Candidate: candidate}, nil
}

// ListCandidatesResult 是 ListCandidates 的返回值.
type ListCandidatesResult struct {
	Items []data.MemoryCandidate `json:"items"`
}

// ListCandidates 按用户 ID 和状态查候选列表.
// status 为空时查所有状态.
// 方案 16.11.1 节: GET /api/v1/memory-candidates?status=pending.
func (s *MemoryService) ListCandidates(
	ctx context.Context,
	userID int64,
	status string,
) (ListCandidatesResult, error) {
	candidates, err := s.candidate.ListCandidates(ctx, userID, status)
	if err != nil {
		return ListCandidatesResult{}, fmt.Errorf("list candidates: %w", err)
	}

	// 空结果返回空切片
	if candidates == nil {
		candidates = []data.MemoryCandidate{}
	}

	return ListCandidatesResult{Items: candidates}, nil
}

// ConfirmCandidateResult 是 ConfirmCandidate 的返回值.
type ConfirmCandidateResult struct {
	Memory data.Memory `json:"memory"`
}

// ConfirmCandidate 确认候选, 写入正式记忆.
// 方案 16.11.1 节: POST /api/v1/memory-candidates/{id}/confirm.
func (s *MemoryService) ConfirmCandidate(
	ctx context.Context,
	candidateID, userID int64,
) (ConfirmCandidateResult, error) {
	memory, err := s.candidate.ConfirmCandidate(ctx, candidateID, userID)
	if err != nil {
		return ConfirmCandidateResult{}, fmt.Errorf("confirm candidate: %w", err)
	}

	return ConfirmCandidateResult{Memory: memory}, nil
}

// RejectCandidate 拒绝候选.
// 方案 16.11.1 节: POST /api/v1/memory-candidates/{id}/reject.
func (s *MemoryService) RejectCandidate(
	ctx context.Context,
	candidateID, userID int64,
) error {
	if err := s.candidate.RejectCandidate(ctx, candidateID, userID); err != nil {
		return fmt.Errorf("reject candidate: %w", err)
	}
	return nil
}

// ListMemoriesResult 是 ListMemories 的返回值.
type ListMemoriesResult struct {
	Items []data.Memory `json:"items"`
}

// ListMemories 按用户 ID 查正式记忆列表.
// 方案 16.11.1 节: GET /api/v1/memories.
func (s *MemoryService) ListMemories(
	ctx context.Context,
	userID int64,
) (ListMemoriesResult, error) {
	rows, err := s.memService.ListMemories(ctx, userID)
	if err != nil {
		return ListMemoriesResult{}, fmt.Errorf("list memories: %w", err)
	}

	// 空结果返回空切片
	if rows == nil {
		rows = []data.Memory{}
	}

	return ListMemoriesResult{Items: rows}, nil
}

// UpdateMemoryResult 是 UpdateMemory 的返回值.
type UpdateMemoryResult struct {
	Memory data.Memory `json:"memory"`
}

// UpdateMemory 编辑记忆内容 + 关键词.
// 方案 16.11.1 节: PATCH /api/v1/memories/{id}.
func (s *MemoryService) UpdateMemory(
	ctx context.Context,
	memoryID, userID int64,
	content, keywords string,
) (UpdateMemoryResult, error) {
	updated, err := s.memService.UpdateMemory(ctx, memoryID, userID, content, keywords)
	if err != nil {
		return UpdateMemoryResult{}, fmt.Errorf("update memory: %w", err)
	}

	return UpdateMemoryResult{Memory: updated}, nil
}

// DeleteMemory 软删记忆.
// 方案 16.11.1 节: DELETE /api/v1/memories/{id}.
func (s *MemoryService) DeleteMemory(
	ctx context.Context,
	memoryID, userID int64,
) error {
	if err := s.memService.DeleteMemory(ctx, memoryID, userID); err != nil {
		return fmt.Errorf("delete memory: %w", err)
	}
	return nil
}

// SearchMemories 检索记忆(供对话流程注入用, 不直接暴露为 API).
// 返回 memory.SearchResult.
func (s *MemoryService) SearchMemories(
	ctx context.Context,
	userID int64,
	keywords string,
	limit int,
) (memory.SearchResult, error) {
	result, err := s.retriever.Search(ctx, userID, keywords, limit)
	if err != nil {
		return memory.SearchResult{}, fmt.Errorf("search memories: %w", err)
	}
	return result, nil
}
