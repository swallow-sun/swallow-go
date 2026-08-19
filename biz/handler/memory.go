// memory.go 放记忆相关接口的 handler.
//
// 做的事情:
//  1. CreateCandidate: POST /api/v1/memory-candidates, 手动提交记忆候选.
//  2. ListCandidates: GET /api/v1/memory-candidates?status=pending, 查候选列表.
//  3. ConfirmCandidate: POST /api/v1/memory-candidates/{id}/confirm, 确认候选.
//  4. RejectCandidate: POST /api/v1/memory-candidates/{id}/reject, 拒绝候选.
//  5. ListMemories: GET /api/v1/memories, 查正式记忆列表.
//  6. UpdateMemory: PATCH /api/v1/memories/{id}, 编辑记忆.
//  7. DeleteMemory: DELETE /api/v1/memories/{id}, 软删记忆.
//
// 方案 16.11.1 节的第一批接口.
// handler 只做 HTTP 解析和 JSON 序列化, 业务逻辑在 service 层.
//
// 用户 ID 来源:
//   当前阶段没有认证中间件, 暂时从 URL query 参数 user_id 取.
//   后续阶段加认证后, userID 从 JWT/Session 里取.
package handler

import (
	"context"
	"strconv"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// createCandidateReq 是 POST /api/v1/memory-candidates 的请求体.
type createCandidateReq struct {
	SessionID  string `json:"session_id"`  // 来源会话 ID
	TraceID    string `json:"trace_id"`    // 来源对话的 trace ID, 可选
	Content    string `json:"content"`     // 候选记忆内容
	MemoryType string `json:"memory_type"` // 记忆类型: preference/fact/instruction/persona
	Reason     string `json:"reason"`      // 为什么建议保存, 可选
	UsageHint  string `json:"usage_hint"`  // 保存后可能如何使用, 可选
}

// updateMemoryReq 是 PATCH /api/v1/memories/{id} 的请求体.
type updateMemoryReq struct {
	Content  string `json:"content"`  // 编辑后的记忆内容
	Keywords string `json:"keywords"` // 编辑后的关键词
}

// CreateCandidate POST /api/v1/memory-candidates
// 手动提交一条记忆候选.
// 需要在 URL query 里带 user_id, 在请求体里带候选内容.
func (d *Deps) CreateCandidate(ctx context.Context, c *app.RequestContext) {
	// 从 URL query 取 user_id
	// 当前阶段没有认证中间件, 暂时从 query 参数取
	userID, ok := parseUserID(c)
	if !ok {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "valid user_id is required"})
		return
	}

	// 解析请求体
	var req createCandidateReq
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// 校验必填字段
	if req.Content == "" || req.MemoryType == "" {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "content and memory_type are required"})
		return
	}

	// 调 MemoryService 创建候选
	result, err := d.memory.CreateCandidate(ctx, userID, req.SessionID, req.TraceID, req.Content, req.MemoryType, req.Reason, req.UsageHint)
	if err != nil {
		logger.Error("create candidate failed", zap.Int64("user_id", userID), zap.Error(err))
		c.JSON(consts.StatusInternalServerError, map[string]string{"error": "create candidate failed"})
		return
	}

	c.JSON(consts.StatusOK, result)
}

// ListCandidates GET /api/v1/memory-candidates?user_id=1&status=pending
// 按用户 ID 和状态查候选列表.
// status 为空时查所有状态.
func (d *Deps) ListCandidates(ctx context.Context, c *app.RequestContext) {
	userID, ok := parseUserID(c)
	if !ok {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "valid user_id is required"})
		return
	}

	// status 可选, 不传时查所有状态
	status := string(c.Query("status"))

	result, err := d.memory.ListCandidates(ctx, userID, status)
	if err != nil {
		logger.Error("list candidates failed", zap.Int64("user_id", userID), zap.Error(err))
		c.JSON(consts.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	c.JSON(consts.StatusOK, result)
}

// ConfirmCandidate POST /api/v1/memory-candidates/{id}/confirm
// 确认候选, 写入正式记忆.
func (d *Deps) ConfirmCandidate(ctx context.Context, c *app.RequestContext) {
	userID, ok := parseUserID(c)
	if !ok {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "valid user_id is required"})
		return
	}

	// 从路径参数取候选 ID
	// c.Param("id") 是 Hertz 框架取路径参数的方法
	candidateID, ok := parsePathID(c, "id")
	if !ok {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "valid candidate id is required"})
		return
	}

	result, err := d.memory.ConfirmCandidate(ctx, candidateID, userID)
	if err != nil {
		logger.Error("confirm candidate failed",
			zap.Int64("candidate_id", candidateID),
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		c.JSON(consts.StatusInternalServerError, map[string]string{"error": "confirm candidate failed"})
		return
	}

	c.JSON(consts.StatusOK, result)
}

// RejectCandidate POST /api/v1/memory-candidates/{id}/reject
// 拒绝候选.
func (d *Deps) RejectCandidate(ctx context.Context, c *app.RequestContext) {
	userID, ok := parseUserID(c)
	if !ok {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "valid user_id is required"})
		return
	}

	candidateID, ok := parsePathID(c, "id")
	if !ok {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "valid candidate id is required"})
		return
	}

	if err := d.memory.RejectCandidate(ctx, candidateID, userID); err != nil {
		logger.Error("reject candidate failed",
			zap.Int64("candidate_id", candidateID),
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		c.JSON(consts.StatusInternalServerError, map[string]string{"error": "reject candidate failed"})
		return
	}

	c.JSON(consts.StatusOK, map[string]string{"status": "rejected"})
}

// ListMemories GET /api/v1/memories?user_id=1
// 按用户 ID 查正式记忆列表.
func (d *Deps) ListMemories(ctx context.Context, c *app.RequestContext) {
	userID, ok := parseUserID(c)
	if !ok {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "valid user_id is required"})
		return
	}

	result, err := d.memory.ListMemories(ctx, userID)
	if err != nil {
		logger.Error("list memories failed", zap.Int64("user_id", userID), zap.Error(err))
		c.JSON(consts.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	c.JSON(consts.StatusOK, result)
}

// UpdateMemory PATCH /api/v1/memories/{id}
// 编辑记忆内容 + 关键词.
func (d *Deps) UpdateMemory(ctx context.Context, c *app.RequestContext) {
	userID, ok := parseUserID(c)
	if !ok {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "valid user_id is required"})
		return
	}

	memoryID, ok := parsePathID(c, "id")
	if !ok {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "valid memory id is required"})
		return
	}

	var req updateMemoryReq
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.Content == "" {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "content is required"})
		return
	}

	result, err := d.memory.UpdateMemory(ctx, memoryID, userID, req.Content, req.Keywords)
	if err != nil {
		logger.Error("update memory failed",
			zap.Int64("memory_id", memoryID),
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		c.JSON(consts.StatusInternalServerError, map[string]string{"error": "update memory failed"})
		return
	}

	c.JSON(consts.StatusOK, result)
}

// DeleteMemory DELETE /api/v1/memories/{id}
// 软删记忆.
func (d *Deps) DeleteMemory(ctx context.Context, c *app.RequestContext) {
	userID, ok := parseUserID(c)
	if !ok {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "valid user_id is required"})
		return
	}

	memoryID, ok := parsePathID(c, "id")
	if !ok {
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "valid memory id is required"})
		return
	}

	if err := d.memory.DeleteMemory(ctx, memoryID, userID); err != nil {
		logger.Error("delete memory failed",
			zap.Int64("memory_id", memoryID),
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		c.JSON(consts.StatusInternalServerError, map[string]string{"error": "delete memory failed"})
		return
	}

	c.JSON(consts.StatusOK, map[string]string{"status": "deleted"})
}

// parseUserID 从 URL query 参数里取 user_id 并转成 int64.
// 返回 userID 和是否成功.
// 当前阶段没有认证中间件, 暂时从 query 参数取.
func parseUserID(c *app.RequestContext) (int64, bool) {
	raw := string(c.Query("user_id"))
	if raw == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// parsePathID 从路径参数里取 ID 并转成 int64.
// param 是路径参数名, 比如 "id".
// 返回 ID 和是否成功.
func parsePathID(c *app.RequestContext, param string) (int64, bool) {
	raw := c.Param(param)
	if raw == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
