// memory_candidate_repo.go 放记忆候选的 SQLite 数据访问方法.
//
// 做的事情:
//  1. 定义 ormMemoryCandidate 的 TableName 方法(在 types.go 里已定义, 这里放实现方法).
//  2. 实现 InsertMemoryCandidate: 创建一条 pending 候选.
//  3. 实现 GetMemoryCandidate: 按 ID 查一条候选.
//  4. 实现 GetMemoryCandidates: 按用户 ID + 状态查候选列表.
//  5. 实现 ConfirmMemoryCandidate: 候选确认(改状态 + 写正式记忆).
//  6. 实现 RejectMemoryCandidate: 候选拒绝.
//
// 设计要点:
//   - 方案 16.11.3 节: 用户说的话不直接写 memories, 先产生 pending 候选.
//   - ConfirmMemoryCandidate 是事务性的: 改候选状态 + 写 memories, 要么全成功要么全失败.
package data

import (
	"context"
	"fmt"
	"time"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// InsertMemoryCandidate 创建一条记忆候选.
// status 初始为 pending, created_at 自动填.
// 执行完 GORM 自动回填 ID 和 CreatedAt.
// TableName 指定 memory_candidates 表名。
func (ormMemoryCandidate) TableName() string { return "memory_candidates" }

func (r *sqliteRepo) InsertMemoryCandidate(ctx context.Context, c MemoryCandidate) (MemoryCandidate, error) {
	// 业务对象转 ORM 模型
	model := memoryCandidateToORM(c)

	// .WithContext(ctx) 挂 context
	// .Create(&model) 执行 INSERT INTO memory_candidates (...) VALUES (...)
	// 执行完 GORM 自动回填 ID(自增主键)和 CreatedAt(autoCreateTime)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		logger.Error("memory_candidates insert failed",
			zap.Int64("user_id", c.UserID),
			zap.String("memory_type", c.MemoryType),
			zap.Error(err),
		)
		return MemoryCandidate{}, fmt.Errorf("insert memory candidate: %w", err)
	}

	// 只记录标识和类型，不把候选记忆正文写入日志。
	logger.Debug("memory_candidates insert succeeded",
		zap.Int64("candidate_id", model.ID),
		zap.Int64("user_id", model.UserID),
		zap.String("memory_type", model.MemoryType),
	)
	return memoryCandidateFromORM(model), nil
}

// GetMemoryCandidate 按 ID 查一条候选.
// 查不到返回 sql.ErrNoRows.
func (r *sqliteRepo) GetMemoryCandidate(ctx context.Context, id int64) (MemoryCandidate, error) {
	// .Select(memoryCandidateColumns) 只查需要的列, 不用 SELECT *
	// .First(&model, id) 按主键查一条
	var model ormMemoryCandidate
	if err := r.db.WithContext(ctx).Select(memoryCandidateColumns).First(&model, id).Error; err != nil {
		return MemoryCandidate{}, fmt.Errorf("get memory candidate %d: %w", id, repositoryError(err))
	}
	return memoryCandidateFromORM(model), nil
}

// GetMemoryCandidates 按用户 ID 和状态查候选列表.
// status 为空时查所有状态, 按创建时间倒序返回.
func (r *sqliteRepo) GetMemoryCandidates(ctx context.Context, userID int64, status string) ([]MemoryCandidate, error) {
	// .Select(memoryCandidateColumns) 只查需要的列
	// .Where("user_id = ?", userID) 按用户过滤
	// .Order("created_at DESC") 按创建时间倒序(最新的在前)
	// status 不为空时加 .Where("status = ?", status) 过滤状态
	query := r.db.WithContext(ctx).Select(memoryCandidateColumns).Where("user_id = ?", userID).Order("created_at DESC")
	if status != "" {
		query = query.Where("status = ?", status)
	}

	var models []ormMemoryCandidate
	if err := query.Find(&models).Error; err != nil {
		return nil, fmt.Errorf("get memory candidates: %w", err)
	}

	// ORM 模型切片转业务对象切片
	result := make([]MemoryCandidate, 0, len(models))
	for _, m := range models {
		result = append(result, memoryCandidateFromORM(m))
	}
	return result, nil
}

// ConfirmMemoryCandidate 把候选状态从 pending 改成 confirmed, 同时写入正式记忆.
// 这是事务性操作: 改候选状态 + 写 memories, 要么全成功要么全失败.
// 返回新建的 Memory 记录.
//
// 方案 16.11.3 节: memory_candidates 变为 confirmed → memories 新增 active → events 写入 memory_confirmed.
func (r *sqliteRepo) ConfirmMemoryCandidate(ctx context.Context, id int64, userID int64) (Memory, error) {
	// 先查出这条候选, 确认它存在且属于这个用户
	candidate, err := r.GetMemoryCandidate(ctx, id)
	if err != nil {
		return Memory{}, fmt.Errorf("get candidate for confirm: %w", err)
	}

	// 安全校验: 候选必须属于这个用户
	// 方案 16.11.4 节: "用户 A 的查询永远不会返回用户 B 的私人记忆"
	if candidate.UserID != userID {
		return Memory{}, fmt.Errorf("candidate %d does not belong to user %d", id, userID)
	}

	// 状态校验: 只有 pending 状态的候选才能确认
	// 已经 confirmed 或 rejected 的不能再确认
	if candidate.Status != MemoryCandidateStatusPending {
		return Memory{}, fmt.Errorf("candidate %d is not pending (current: %s)", id, candidate.Status)
	}

	// 用事务保证原子性: 改候选状态 + 写正式记忆
	// r.db.Transaction 开始一个事务, 回调里执行所有操作
	// 回调返回 nil 则提交, 返回 error 则回滚
	var memory Memory
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 把候选状态改成 confirmed, 记录 resolved_at
		now := time.Now()
		result := tx.Model(&ormMemoryCandidate{}).
			Where("id = ? AND user_id = ? AND status = ?", id, userID, MemoryCandidateStatusPending).
			Updates(map[string]any{
				"status":      MemoryCandidateStatusConfirmed,
				"resolved_at": now,
			})
		if result.Error != nil {
			return fmt.Errorf("update candidate status: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("candidate %d was already resolved", id)
		}

		// 2. 写入正式记忆
		ormMem := ormMemory{
			UserID:          candidate.UserID,
			CandidateID:     &candidate.ID,
			SourceSessionID: stringToPtr(candidate.SessionID),
			Content:         candidate.Content,
			MemoryType:      candidate.MemoryType,
			Keywords:        "", // 候选确认时没有关键词, 后续编辑时再加
			SyncVersion:     0,
			Status:          MemoryStatusActive,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if err := tx.Create(&ormMem).Error; err != nil {
			return fmt.Errorf("create memory from candidate: %w", err)
		}

		// 3. 写一条初始版本记录到 memory_versions (version=1)
		ormVer := ormMemoryVersion{
			MemoryID:  ormMem.ID,
			Version:   1,
			Content:   candidate.Content,
			Keywords:  "",
			EditedBy:  "system",
			CreatedAt: now,
		}
		if err := tx.Create(&ormVer).Error; err != nil {
			return fmt.Errorf("create initial memory version: %w", err)
		}

		// 把创建的 ORM 模型转成业务对象
		memory = memoryFromORM(ormMem)
		return nil
	})

	if err != nil {
		logger.Error("confirm memory candidate failed",
			zap.Int64("candidate_id", id),
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		return Memory{}, fmt.Errorf("confirm memory candidate %d: %w", id, err)
	}

	logger.Debug("memory candidate confirmed",
		zap.Int64("candidate_id", id),
		zap.Int64("memory_id", memory.ID),
		zap.Int64("user_id", userID),
	)
	return memory, nil
}

// RejectMemoryCandidate 把候选状态从 pending 改成 rejected.
// 拒绝后不写正式记忆, 同一候选不会再次弹出.
// 方案 16.11.4 节: "用户拒绝候选后, 不因重新登录再次弹出同一候选".
func (r *sqliteRepo) RejectMemoryCandidate(ctx context.Context, id int64, userID int64) error {
	// .Model(&ormMemoryCandidate{}) 指定操作 memory_candidates 表
	// .Where("id = ? AND status = ?", id, MemoryCandidateStatusPending) 只改 pending 状态的
	// .Updates(...) 更新 status 和 resolved_at
	now := time.Now()
	result := r.db.WithContext(ctx).Model(&ormMemoryCandidate{}).
		Where("id = ? AND user_id = ? AND status = ?", id, userID, MemoryCandidateStatusPending).
		Updates(map[string]any{
			"status":      MemoryCandidateStatusRejected,
			"resolved_at": now,
		})

	if result.Error != nil {
		logger.Error("memory_candidates reject failed",
			zap.Int64("candidate_id", id),
			zap.Error(result.Error),
		)
		return fmt.Errorf("reject memory candidate %d: %w", id, result.Error)
	}

	// RowsAffected 不等于 1 说明状态不是 pending(可能已经 confirmed 或 rejected)
	if result.RowsAffected != 1 {
		return fmt.Errorf("reject memory candidate %d: not pending or not found", id)
	}

	return nil
}

// memoryCandidateToORM 把业务对象 MemoryCandidate 转成 ORM 模型.
// string 类型的可空字段转成 *string(空字符串 → nil).
func memoryCandidateToORM(c MemoryCandidate) ormMemoryCandidate {
	return ormMemoryCandidate{
		ID:         c.ID,
		UserID:     c.UserID,
		SessionID:  c.SessionID,
		TraceID:    stringToPtr(c.TraceID),
		Content:    c.Content,
		MemoryType: c.MemoryType,
		Source:     c.Source,
		Reason:     c.Reason,
		UsageHint:  c.UsageHint,
		Status:     c.Status,
		CreatedAt:  c.CreatedAt,
		ResolvedAt: timeToPtr(c.ResolvedAt),
	}
}

// memoryCandidateFromORM 把 ORM 模型转回业务对象.
// 指针字段转成普通类型: nil → 零值.
func memoryCandidateFromORM(m ormMemoryCandidate) MemoryCandidate {
	return MemoryCandidate{
		ID:         m.ID,
		UserID:     m.UserID,
		SessionID:  m.SessionID,
		TraceID:    ptrToString(m.TraceID),
		Content:    m.Content,
		MemoryType: m.MemoryType,
		Source:     m.Source,
		Reason:     m.Reason,
		UsageHint:  m.UsageHint,
		Status:     m.Status,
		CreatedAt:  m.CreatedAt,
		ResolvedAt: ptrToTime(m.ResolvedAt),
	}
}

// timeToPtr 把 time.Time 转成 *time.Time, 零值返回 nil.
func timeToPtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// ptrToTime 把 *time.Time 转成 time.Time, nil 返回零值.
func ptrToTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
