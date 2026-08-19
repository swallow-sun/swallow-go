// memory_repo.go 放正式记忆的 SQLite 数据访问方法.
//
// 做的事情:
//  1. 实现 InsertMemory: 创建一条正式记忆.
//  2. 实现 GetMemory: 按 ID 查一条记忆.
//  3. 实现 GetMemories: 按用户 ID 查记忆列表(只返回 active).
//  4. 实现 SearchMemories: 按用户 ID + 关键词检索(第一版用 LIKE).
//  5. 实现 UpdateMemory: 编辑记忆内容 + 写版本记录.
//  6. 实现 DeleteMemory: 软删(status=deleted) + 写 tombstone.
//
// 设计要点:
//   - 检索只查 status=active 的记录, deleted 的不返回.
//   - 方案 16.11.4 节: 删除记忆后普通查询和缓存都不再返回它.
//   - 编辑记忆时写一条版本记录到 memory_versions, 方便回溯.
//   - 删除记忆时写一条 tombstone 到 memory_tombstones, 防止同步时重新出现.
package data

import (
	"context"
	"fmt"
	"time"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// InsertMemory 创建一条正式记忆.
// 通常由 ConfirmMemoryCandidate 内部调, 也可以手动创建.
// 执行完 GORM 自动回填 ID, CreatedAt, UpdatedAt.
// TableName 指定 memories 表名。
func (ormMemory) TableName() string { return "memories" }

// TableName 指定 memory_versions 表名。
func (ormMemoryVersion) TableName() string { return "memory_versions" }

// TableName 指定 memory_tombstones 表名。
func (ormMemoryTombstone) TableName() string { return "memory_tombstones" }

func (r *sqliteRepo) InsertMemory(ctx context.Context, m Memory) (Memory, error) {
	// 业务对象转 ORM 模型
	model := memoryToORM(m)

	// .WithContext(ctx) 挂 context
	// .Create(&model) 执行 INSERT INTO memories (...) VALUES (...)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		logger.Error("memories insert failed",
			zap.Int64("user_id", m.UserID),
			zap.String("memory_type", m.MemoryType),
			zap.Error(err),
		)
		return Memory{}, fmt.Errorf("insert memory: %w", err)
	}

	// 写库成功后打 Debug 日志
	logger.Debug("memories insert succeeded",
		zap.Int64("memory_id", model.ID),
		zap.Int64("user_id", model.UserID),
		zap.String("memory_type", model.MemoryType),
	)
	return memoryFromORM(model), nil
}

// GetMemory 按 ID 查一条记忆.
// 查不到返回 sql.ErrNoRows.
// 不论 active 还是 deleted 都能查到(给编辑/删除用).
func (r *sqliteRepo) GetMemory(ctx context.Context, id int64) (Memory, error) {
	// .Select(memoryColumns) 只查需要的列, 不用 SELECT *
	// .First(&model, id) 按主键查一条
	var model ormMemory
	if err := r.db.WithContext(ctx).Select(memoryColumns).First(&model, id).Error; err != nil {
		return Memory{}, fmt.Errorf("get memory %d: %w", id, repositoryError(err))
	}
	return memoryFromORM(model), nil
}

// GetMemories 按用户 ID 查正式记忆列表, 只返回 status=active 的记录.
// 按更新时间倒序返回(最近编辑的在前).
func (r *sqliteRepo) GetMemories(ctx context.Context, userID int64) ([]Memory, error) {
	// .Select(memoryColumns) 只查需要的列
	// .Where("user_id = ? AND status = ?", userID, MemoryStatusActive) 只查 active 记忆
	// .Order("updated_at DESC") 按更新时间倒序
	var models []ormMemory
	if err := r.db.WithContext(ctx).Select(memoryColumns).
		Where("user_id = ? AND status = ?", userID, MemoryStatusActive).
		Order("updated_at DESC").
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("get memories: %w", err)
	}

	// ORM 模型切片转业务对象切片
	result := make([]Memory, 0, len(models))
	for _, m := range models {
		result = append(result, memoryFromORM(m))
	}
	return result, nil
}

// SearchMemories 按用户 ID + 关键词检索记忆, 只搜 status=active 的记录.
// 第一版用 LIKE, 不用向量. 方案 16.11.1 节: "第一版先使用结构化字段, 关键词和时间排序".
//
// 搜索逻辑: keywords 字段或 content 字段中包含搜索词的记录都会返回.
// limit 限制返回条数, 0 或负数用默认值 20.
func (r *sqliteRepo) SearchMemories(ctx context.Context, userID int64, keywords string, limit int) ([]Memory, error) {
	// limit 默认值 20
	if limit <= 0 {
		limit = 20
	}

	// keywords 为空时返回所有 active 记忆, 按 updated_at 倒序, 取 limit 条
	// keywords 不为空时用 LIKE 搜 content 和 keywords 两个字段
	// %keyword% 是 SQL LIKE 的通配符匹配, 匹配包含 keyword 的内容
	// OR 连接两个条件: content LIKE 或 keywords LIKE
	query := r.db.WithContext(ctx).Select(memoryColumns).
		Where("user_id = ? AND status = ?", userID, MemoryStatusActive)

	if keywords != "" {
		// 搜索词在 content 或 keywords 字段中
		likePattern := "%" + keywords + "%"
		query = query.Where("(content LIKE ? OR keywords LIKE ?)", likePattern, likePattern)
	}

	query = query.Order("updated_at DESC").Limit(limit)

	var models []ormMemory
	if err := query.Find(&models).Error; err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}

	// ORM 模型切片转业务对象切片
	result := make([]Memory, 0, len(models))
	for _, m := range models {
		result = append(result, memoryFromORM(m))
	}
	return result, nil
}

// UpdateMemory 编辑记忆内容和关键词, 同时写一条版本记录到 memory_versions.
// 这是事务性操作: 更新记忆 + 写版本记录, 要么全成功要么全失败.
//
// 方案 16.11.4 节: 记忆中的命令性文本不能修改系统提示, 权限和工具调用规则.
// 这里的编辑只改 content 和 keywords, 不改 memory_type 和 user_id.
func (r *sqliteRepo) UpdateMemory(ctx context.Context, id int64, content, keywords string) (Memory, error) {
	var updatedMemory Memory

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 查出当前记忆, 确认它存在且是 active 状态
		var model ormMemory
		if err := tx.Select(memoryColumns).Where("id = ? AND status = ?", id, MemoryStatusActive).First(&model).Error; err != nil {
			return fmt.Errorf("get memory for update %d: %w", id, repositoryError(err))
		}

		// 2. 算新版本号: 当前最大版本号 + 1
		// COALESCE(MAX(version), 0) 如果没有版本记录就从 0 开始
		var maxVersion int
		if err := tx.Model(&ormMemoryVersion{}).Where("memory_id = ?", id).
			Select("COALESCE(MAX(version), 0)").Scan(&maxVersion).Error; err != nil {
			return fmt.Errorf("get max version: %w", err)
		}
		newVersion := maxVersion + 1

		// 3. 更新记忆的 content, keywords, updated_at
		now := time.Now()
		if err := tx.Model(&ormMemory{}).Where("id = ?", id).
			Updates(map[string]any{
				"content":    content,
				"keywords":   keywords,
				"updated_at": now,
			}).Error; err != nil {
			return fmt.Errorf("update memory: %w", err)
		}

		// 4. 写一条版本记录
		ormVer := ormMemoryVersion{
			MemoryID:  id,
			Version:   newVersion,
			Content:   content,
			Keywords:  keywords,
			EditedBy:  "user",
			CreatedAt: now,
		}
		if err := tx.Create(&ormVer).Error; err != nil {
			return fmt.Errorf("create memory version: %w", err)
		}

		// 5. 查出更新后的完整记录返回
		if err := tx.Select(memoryColumns).Where("id = ?", id).First(&model).Error; err != nil {
			return fmt.Errorf("reload memory %d: %w", id, err)
		}
		updatedMemory = memoryFromORM(model)
		return nil
	})

	if err != nil {
		logger.Error("update memory failed",
			zap.Int64("memory_id", id),
			zap.Error(err),
		)
		return Memory{}, fmt.Errorf("update memory %d: %w", id, err)
	}

	return updatedMemory, nil
}

// DeleteMemory 软删记忆(status=deleted), 同时写一条 tombstone.
// 这是事务性操作: 更新状态 + 写 tombstone, 要么全成功要么全失败.
//
// 方案 16.11.4 节: "删除记忆后普通查询和缓存都不再返回它".
// tombstone 的作用: 防止已删除的记忆通过同步机制重新出现.
func (r *sqliteRepo) DeleteMemory(ctx context.Context, id int64, userID int64) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 查出记忆, 确认它存在且属于这个用户
		var model ormMemory
		if err := tx.Select(memoryColumns).Where("id = ? AND user_id = ?", id, userID).First(&model).Error; err != nil {
			return fmt.Errorf("get memory for delete %d: %w", id, repositoryError(err))
		}

		// 已经是 deleted 状态就不用重复删
		if model.Status == MemoryStatusDeleted {
			return nil
		}

		// 2. 软删: status 改成 deleted
		now := time.Now()
		if err := tx.Model(&ormMemory{}).Where("id = ?", id).
			Updates(map[string]any{
				"status":     MemoryStatusDeleted,
				"updated_at": now,
			}).Error; err != nil {
			return fmt.Errorf("update memory status: %w", err)
		}

		// 3. 写一条 tombstone
		ormTomb := ormMemoryTombstone{
			MemoryID:    id,
			UserID:      userID,
			SyncVersion: model.SyncVersion,
			DeletedAt:   now,
		}
		if err := tx.Create(&ormTomb).Error; err != nil {
			return fmt.Errorf("create tombstone: %w", err)
		}

		return nil
	})

	if err != nil {
		logger.Error("delete memory failed",
			zap.Int64("memory_id", id),
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		return fmt.Errorf("delete memory %d: %w", id, err)
	}

	return nil
}

// memoryToORM 把业务对象 Memory 转成 ORM 模型.
// string 类型的可空字段转成 *string(空字符串 → nil).
// int64 类型的可空字段转成 *int64(0 → nil).
func memoryToORM(m Memory) ormMemory {
	return ormMemory{
		ID:              m.ID,
		UserID:          m.UserID,
		CandidateID:     int64ToPtr(m.CandidateID),
		SourceSessionID: stringToPtr(m.SourceSessionID),
		Content:         m.Content,
		MemoryType:      m.MemoryType,
		Keywords:        m.Keywords,
		SyncVersion:     m.SyncVersion,
		Status:          m.Status,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
	}
}

// memoryFromORM 把 ORM 模型转回业务对象.
// 指针字段转成普通类型: nil → 零值.
func memoryFromORM(m ormMemory) Memory {
	return Memory{
		ID:              m.ID,
		UserID:          m.UserID,
		CandidateID:     ptrToInt64(m.CandidateID),
		SourceSessionID: ptrToString(m.SourceSessionID),
		Content:         m.Content,
		MemoryType:      m.MemoryType,
		Keywords:        m.Keywords,
		SyncVersion:     m.SyncVersion,
		Status:          m.Status,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
	}
}
