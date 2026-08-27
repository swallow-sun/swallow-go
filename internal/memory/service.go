// service.go 放正式记忆的 CRUD 业务逻辑.
//
// 做的事情:
//  1. 定义 Service 结构体: 持有 repo, 负责正式记忆的查询, 编辑和删除.
//  2. ListMemories: 按用户 ID 查 active 记忆列表.
//  3. GetMemory: 按记忆 ID 查单条(带用户归属校验).
//  4. UpdateMemory: 编辑记忆内容 + 关键词, 内部调 repo.UpdateMemory 写版本记录.
//  5. DeleteMemory: 软删记忆 + 写 tombstone.
//  6. 正式记忆编辑前重新执行敏感信息检查, 防止从更新接口绕过安全策略.
//
// 设计要点:
//   - 方案 16.11.4 节: "删除记忆后普通查询和缓存都不再返回它".
//   - 方案 16.11.4 节: "记忆中的命令性文本不能修改系统提示, 权限和工具调用规则".
//     这里的编辑只改 content 和 keywords, 不改 memory_type 和 user_id.
//   - 所有方法接收 userID 做用户归属校验.
//
// 命名说明:
//   - 这个文件叫 service.go 是因为方案 16.11.2 节里规定的文件名.
//   - biz/service/memory_service.go 是 handler 层调的编排层, 和这个不同.
//   - 这个 Service 是 memory 包内部的, 给 biz/service/memory_service.go 调.
package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// NewService 创建一个 Service.
func NewService(repo data.Repository, safetyFilterEnabled ...bool) *Service {
	return &Service{
		repo:                repo,
		safetyFilterEnabled: resolveSafetyFilterEnabled(safetyFilterEnabled),
	}
}

// ListMemories 按用户 ID 查 active 记忆列表.
// 方案 16.11.1 节: GET /api/v1/memories.
// 返回按更新时间倒序排列的记忆列表, 空结果返回空切片.
func (s *Service) ListMemories(ctx context.Context, userID int64) ([]data.Memory, error) {
	rows, err := s.repo.GetMemories(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}

	// 空结果返回空切片, 不是 nil
	if rows == nil {
		return []data.Memory{}, nil
	}
	return rows, nil
}

// GetMemory 按记忆 ID 查单条.
// 带用户归属校验, 防止跨用户读取.
//
// 方案 16.11.4 节: "用户 A 的查询永远不会返回用户 B 的私人记忆".
func (s *Service) GetMemory(ctx context.Context, id, userID int64) (data.Memory, error) {
	memory, err := s.repo.GetMemory(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return data.Memory{}, fmt.Errorf("memory not found: %w", err)
		}
		return data.Memory{}, fmt.Errorf("get memory: %w", err)
	}

	// 安全校验: 记忆必须属于这个用户
	if memory.UserID != userID {
		return data.Memory{}, fmt.Errorf("memory %d does not belong to user %d", id, userID)
	}

	return memory, nil
}

// UpdateMemory 编辑记忆内容 + 关键词.
// 内部调 repo.UpdateMemory, repo 层会在事务里:
//   - 更新 memories 表的 content, keywords, updated_at
//   - 写一条版本记录到 memory_versions
//
// 方案 16.11.4 节: "记忆中的命令性文本不能修改系统提示, 权限和工具调用规则".
// 这里的编辑只改 content 和 keywords, 不改 memory_type 和 user_id.
// memory_type 在候选确认时就定了, 后续编辑不能改类型.
func (s *Service) UpdateMemory(ctx context.Context, id, userID int64, content, keywords string) (data.Memory, error) {
	// 编辑正式记忆也必须经过安全检测, 防止从更新接口绕过候选阶段的保护.
	if s.safetyFilterEnabled {
		if safety := CheckMemorySafety(content + "\n" + keywords); !safety.Allowed {
			emitMemoryCandidateBlocked(ctx, userID, safety.Kind)
			return data.Memory{}, &SafetyError{Kind: safety.Kind}
		}
	}

	// 先查出记忆, 做用户归属校验
	memory, err := s.GetMemory(ctx, id, userID)
	if err != nil {
		return data.Memory{}, fmt.Errorf("get memory for update: %w", err)
	}

	// 安全校验: 只有 active 状态的记忆才能编辑
	// repo.UpdateMemory 内部也会校验, 但这里提前校验, 给用户更清晰的错误
	if memory.Status != data.MemoryStatusActive {
		return data.Memory{}, fmt.Errorf("memory %d is not active (current: %s)", id, memory.Status)
	}

	// 调 repo 做事务性更新
	updated, err := s.repo.UpdateMemory(ctx, id, userID, content, keywords)
	if err != nil {
		return data.Memory{}, fmt.Errorf("update memory: %w", err)
	}

	logger.Debug("memory updated",
		zap.Int64("memory_id", id),
		zap.Int64("user_id", userID),
	)

	return updated, nil
}

// DeleteMemory 软删记忆(status=deleted) + 写 tombstone.
// 内部调 repo.DeleteMemory, repo 层会在事务里:
//   - 更新 memories 表的 status 为 deleted
//   - 写一条 tombstone 到 memory_tombstones
//
// 方案 16.11.4 节: "删除记忆后普通查询和缓存都不再返回它".
// tombstone 的作用: 防止已删除的记忆通过同步机制重新出现.
func (s *Service) DeleteMemory(ctx context.Context, id, userID int64) error {
	// 先查出记忆, 做用户归属校验
	_, err := s.GetMemory(ctx, id, userID)
	if err != nil {
		return fmt.Errorf("get memory for delete: %w", err)
	}

	// 调 repo 做事务性软删
	if err := s.repo.DeleteMemory(ctx, id, userID); err != nil {
		return fmt.Errorf("delete memory: %w", err)
	}

	logger.Debug("memory deleted",
		zap.Int64("memory_id", id),
		zap.Int64("user_id", userID),
	)

	return nil
}
