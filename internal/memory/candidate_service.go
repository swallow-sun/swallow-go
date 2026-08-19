// candidate_service.go 放记忆候选的业务逻辑.
//
// 做的事情:
//  1. 定义 CandidateService 结构体: 持有 repo 和 policy, 负责候选的生命周期.
//  2. CreateCandidates: 对话完成后调 policy 产生候选, 批量写入.
//  3. CreateCandidate: 手动提交一条候选(API 直接调).
//  4. ListCandidates: 按用户 ID + 状态查候选列表.
//  5. GetCandidate: 按候选 ID 查单条.
//  6. ConfirmCandidate: 确认候选 → 调 repo.ConfirmMemoryCandidate 写正式记忆 + 发埋点.
//  7. RejectCandidate: 拒绝候选 → 调 repo.RejectMemoryCandidate.
//  8. 在新建和确认候选前执行敏感信息检查, 禁止敏感原文进入正式长期记忆.
//
// 设计要点:
//   - 方案 16.11.3 节: "用户说的话不直接写 memories, 先产生 pending 候选".
//   - ConfirmCandidate 成功后发 memory_confirmed 事件.
//   - 所有方法接收 userID 做用户归属校验, 防止跨用户操作.
//   - 方案 16.11.4 节: "用户 A 的查询永远不会返回用户 B 的私人记忆".
package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/telemetry"
	"github.com/swallow-sun/swallow-go/internal/trace"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// NewCandidateService 创建一个 CandidateService.
// repo 是数据访问层接口, policy 是候选产生规则引擎.
func NewCandidateService(repo data.Repository, policy *Policy, safetyFilterEnabled ...bool) *CandidateService {
	return &CandidateService{
		repo:                repo,
		policy:              policy,
		safetyFilterEnabled: resolveSafetyFilterEnabled(safetyFilterEnabled),
	}
}

// CreateCandidates 在对话完成后调 policy 产生候选并批量写入.
// 入参:
//   - ctx: 上下文, 带有 trace ID
//   - userID: 哪个用户的对话
//   - sessionID: 来源会话 ID
//   - userMessage: 用户说的内容
//
// 返回: 成功写入的候选列表. 没有候选时返回空切片.
//
// 方案 16.11.2 节: "对话完成 → 按确定性规则或模型建议产生候选 → 保存 memory_candidate".
func (s *CandidateService) CreateCandidates(
	ctx context.Context,
	userID int64,
	sessionID, userMessage string,
) ([]data.MemoryCandidate, error) {
	// 从 context 里取 trace ID
	traceID := trace.FromContext(ctx)

	// 安全检测必须发生在规则提取和数据库写入之前.
	// 命中时不记录原文, 只记录敏感类别和必要的审计标识.
	if s.safetyFilterEnabled {
		if safety := CheckMemorySafety(userMessage); !safety.Allowed {
			emitMemoryCandidateBlocked(ctx, userID, safety.Kind)
			return []data.MemoryCandidate{}, nil
		}
	}

	// 调 policy 用确定性规则产生候选
	specs := s.policy.Generate(userID, sessionID, traceID, userMessage)

	// 没有候选, 返回空切片
	if len(specs) == 0 {
		return []data.MemoryCandidate{}, nil
	}

	// 逐条写入数据库
	// 不用批量插入, 因为 SQLite 每条 INSERT 独立执行, GORM 的批量插入在 SQLite 上行为不一致
	candidates := make([]data.MemoryCandidate, 0, len(specs))
	for _, spec := range specs {
		// CandidateSpec.ToMemoryCandidate 把 spec 转成 data.MemoryCandidate
		candidate, err := s.repo.InsertMemoryCandidate(ctx, spec.ToMemoryCandidate())
		if err != nil {
			// 写入失败不中断后续候选, 但打日志
			// 一条失败不应该影响其他候选的保存
			logger.Error("create memory candidate failed",
				zap.Int64("user_id", userID),
				zap.String("memory_type", spec.MemoryType),
				zap.Error(err),
			)
			continue
		}
		candidates = append(candidates, candidate)
	}

	logger.Debug("memory candidates created",
		zap.Int64("user_id", userID),
		zap.Int("count", len(candidates)),
	)

	return candidates, nil
}

// CreateCandidate 手动提交一条记忆候选(API 直接调).
// 入参是 CandidateSpec, 返回数据库写入后的完整记录.
func (s *CandidateService) CreateCandidate(ctx context.Context, spec CandidateSpec) (data.MemoryCandidate, error) {
	// 手动接口和自动候选共用同一安全边界, 防止绕过对话规则直接写入敏感候选.
	if s.safetyFilterEnabled {
		if safety := CheckCandidateSafety(spec); !safety.Allowed {
			emitMemoryCandidateBlocked(ctx, spec.UserID, safety.Kind)
			return data.MemoryCandidate{}, &SafetyError{Kind: safety.Kind}
		}
	}

	// 调 repo 写入
	candidate, err := s.repo.InsertMemoryCandidate(ctx, spec.ToMemoryCandidate())
	if err != nil {
		return data.MemoryCandidate{}, fmt.Errorf("create candidate: %w", err)
	}

	logger.Debug("memory candidate created manually",
		zap.Int64("candidate_id", candidate.ID),
		zap.Int64("user_id", candidate.UserID),
	)

	return candidate, nil
}

// emitMemoryCandidateBlocked 记录一次候选安全拒绝.
// 日志和事件只包含敏感类别, 禁止加入用户消息或正则命中的具体值.
func emitMemoryCandidateBlocked(ctx context.Context, userID int64, kind string) {
	telemetry.Emit(ctx, telemetry.EventMemoryCandidateBlocked, map[string]any{
		"user_id":             userID,
		"sensitive_kind":      kind,
		telemetry.FieldStatus: telemetry.StatusRejected,
	})
	logger.Warn("memory candidate blocked by safety policy",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.Int64("user_id", userID),
		zap.String("sensitive_kind", kind),
	)
}

// ListCandidates 按用户 ID 和状态查候选列表.
// status 为空时查所有状态.
// 方案 16.11.1 节: GET /api/v1/memory-candidates?status=pending.
func (s *CandidateService) ListCandidates(ctx context.Context, userID int64, status string) ([]data.MemoryCandidate, error) {
	candidates, err := s.repo.GetMemoryCandidates(ctx, userID, status)
	if err != nil {
		return nil, fmt.Errorf("list candidates: %w", err)
	}
	return candidates, nil
}

// GetCandidate 按候选 ID 查单条.
// 供 handler 查详情用.
func (s *CandidateService) GetCandidate(ctx context.Context, id, userID int64) (data.MemoryCandidate, error) {
	candidate, err := s.repo.GetMemoryCandidate(ctx, id)
	if err != nil {
		return data.MemoryCandidate{}, fmt.Errorf("get candidate: %w", err)
	}

	// 安全校验: 候选必须属于这个用户
	if candidate.UserID != userID {
		return data.MemoryCandidate{}, fmt.Errorf("candidate %d does not belong to user %d", id, userID)
	}

	return candidate, nil
}

// ConfirmCandidate 把候选状态从 pending 改成 confirmed, 同时写入正式记忆.
// 返回新建的正式记忆记录.
//
// 方案 16.11.3 节: "memory_candidates 变为 confirmed → memories 新增 active → events 写入 memory_confirmed".
func (s *CandidateService) ConfirmCandidate(ctx context.Context, id, userID int64) (data.Memory, error) {
	start := time.Now()

	// 确认前重新检查候选, 防止历史数据或数据库外部写入绕过新建阶段的安全检测.
	candidate, err := s.GetCandidate(ctx, id, userID)
	if err != nil {
		return data.Memory{}, fmt.Errorf("get candidate for safety check: %w", err)
	}
	if s.safetyFilterEnabled {
		if safety := CheckMemorySafety(candidate.Content); !safety.Allowed {
			// 敏感候选保持 pending 会被反复展示, 因此发现后立即转成 rejected.
			if rejectErr := s.repo.RejectMemoryCandidate(ctx, id, userID); rejectErr != nil {
				return data.Memory{}, fmt.Errorf("reject unsafe candidate: %w", rejectErr)
			}
			emitMemoryCandidateBlocked(ctx, userID, safety.Kind)
			return data.Memory{}, &SafetyError{Kind: safety.Kind}
		}
	}

	// 调 repo 做事务性确认: 改候选状态 + 写 memories + 写 memory_versions
	memory, err := s.repo.ConfirmMemoryCandidate(ctx, id, userID)
	if err != nil {
		// 如果是 sql.ErrNoRows, 说明候选不存在
		if errors.Is(err, sql.ErrNoRows) {
			return data.Memory{}, fmt.Errorf("candidate not found: %w", err)
		}
		return data.Memory{}, fmt.Errorf("confirm candidate: %w", err)
	}

	elapsed := time.Since(start)

	// 发埋点: 记录记忆确认事件
	// 方案 16.11.3 节: "events 写入 memory_confirmed"
	telemetry.Emit(ctx, telemetry.EventMemoryConfirmed, map[string]any{
		"candidate_id":            id,
		"memory_id":               memory.ID,
		"user_id":                 userID,
		telemetry.FieldStatus:     telemetry.StatusOK,
		telemetry.FieldDurationMS: elapsed.Milliseconds(),
	})

	logger.Debug("memory candidate confirmed",
		zap.Int64("candidate_id", id),
		zap.Int64("memory_id", memory.ID),
		zap.Int64("user_id", userID),
		zap.Int64("duration_ms", elapsed.Milliseconds()),
	)

	return memory, nil
}

// RejectCandidate 把候选状态从 pending 改成 rejected.
// 拒绝后不写正式记忆, 同一候选不会再次弹出.
//
// 方案 16.11.4 节: "用户拒绝候选后, 不因重新登录再次弹出同一候选".
func (s *CandidateService) RejectCandidate(ctx context.Context, id, userID int64) error {
	// Repository 在同一条 UPDATE 中同时校验用户归属和 pending 状态，避免先查后改的竞态窗口。
	if err := s.repo.RejectMemoryCandidate(ctx, id, userID); err != nil {
		return fmt.Errorf("reject candidate: %w", err)
	}

	logger.Debug("memory candidate rejected",
		zap.Int64("candidate_id", id),
		zap.Int64("user_id", userID),
	)

	return nil
}
