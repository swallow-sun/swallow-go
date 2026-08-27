// store.go 放 emotion.Store 的写入和查询方法.
//
// 做的事情:
//  1. RecordTags: 把 parser 解析出的标签写库——先写对话标签明细, 再更新或新建情绪持续段.
//  2. GetRecentSessions: 查用户最近的情绪持续段列表, 给 injector 注入 system prompt 用.
//  3. GetLatestSession: 查用户最近一条情绪持续段, RecordTags 内部判断要不要延长或新建.
//
// 方案 16.12.6 节: 连续相同情绪合并为一段, 记录开始/结束/持续时长.
// 降级模式: 情绪追踪出错只打日志不返回 error, 不阻断正常对话.
package emotion

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// TagDimEmotion 是对话标签的维度名, 表示这一行记录的是情绪标签.
// 对话标签表 dialogue_tags 的 tag_dim 字段用这个值区分不同维度.
const TagDimEmotion = "emotion"

// RecordTags 把 parser 解析出的标签写入数据库.
//
// 这个方法做两件事:
//  1. 写一条对话标签明细 (dialogue_tags 表), tag_dim="emotion", 记录这一轮的情绪.
//  2. 更新或新建情绪持续段 (emotion_sessions 表):
//     - 如果最近一条情绪持续段的情绪和本轮相同, 而且还没结束 (EndAt == nil),
//       就延长它——更新 EndRound, EndAt, DurationMinutes.
//     - 如果最近一条情绪不同, 或者已经结束了, 就新建一条情绪持续段.
//
// 降级模式: 所有错误只打 Error 日志, 不返回 error.
// 情绪追踪不应该阻断正常对话, 哪怕写库失败, 对话也能继续.
//
// 参数:
//   - ctx: 上下文, 支持超时取消
//   - userID: 哪个用户的情绪
//   - sessionID: 来源会话 ID
//   - traceID: 链路追踪 ID, 方便回溯情绪标签来源
//   - round: 第几轮对话
//   - tags: parser 解析出的标签
func (s *Store) RecordTags(
	ctx context.Context,
	userID int64,
	sessionID, traceID string,
	round int,
	tags ParsedTags,
) error {
	// 第一步: 写一条对话标签明细到 dialogue_tags 表.
	// tag_dim 固定为 "emotion", tag_value 是情绪标签(如 "frustrated"),
	// tag_extra 存强度数值(如 0.6), trigger_reason 存触发原因, source 标记来自 LLM.
	tag := data.DialogueTag{
		UserID:        userID,
		SessionID:     sessionID,
		TraceID:       traceID,
		Round:         round,
		TagDim:        TagDimEmotion,
		TagValue:      tags.Emotion,
		TagExtra:      tags.Intensity,
		TriggerReason: tags.Trigger,
		Source:        data.TagSourceLLM,
	}
	if _, err := s.repo.InsertDialogueTag(ctx, tag); err != nil {
		// 写对话标签失败, 打 Error 日志但不返回错误, 继续尝试写情绪持续段
		logger.Error("emotion: insert dialogue tag failed",
			zap.Int64("user_id", userID),
			zap.String("emotion", tags.Emotion),
			zap.Int("round", round),
			zap.Error(err),
		)
	}

	// 第二步: 更新或新建情绪持续段.
	// 先查最近一条情绪持续段, 判断要不要延长还是新建.
	latest, err := s.repo.GetLatestEmotionSession(ctx, userID)
	if err != nil {
		// GetLatestEmotionSession 查不到记录时会返回 sql.ErrNoRows(由 repositoryError 转换),
		// 这种情况说明用户还没有情绪持续段, 直接新建一条.
		if errors.Is(err, sql.ErrNoRows) {
			s.createEmotionSession(ctx, userID, traceID, round, tags)
			return nil
		}
		// 其他查询错误(数据库连接断了等), 打日志降级, 不阻断对话
		logger.Error("emotion: get latest session failed",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		return nil
	}

	// 判断: 最近一条情绪持续段和本轮情绪相同, 而且还没结束 (EndAt == nil), 就延长它.
	// latest.EndAt == nil 表示进行中, 还没设结束时间.
	if latest.Emotion == tags.Emotion && latest.EndAt == nil {
		s.extendEmotionSession(ctx, latest, round, tags)
		return nil
	}

	// 最近一条情绪不同, 或者已经结束了, 新建一条情绪持续段.
	s.createEmotionSession(ctx, userID, traceID, round, tags)
	return nil
}

// createEmotionSession 新建一条情绪持续段.
// 降级模式: 失败只打 Error 日志, 不向上抛错误.
func (s *Store) createEmotionSession(ctx context.Context, userID int64, traceID string, round int, tags ParsedTags) {
	now := time.Now()
	session := data.EmotionSession{
		UserID:      userID,
		Emotion:     tags.Emotion,
		Intensity:   tags.Intensity,
		Urgency:     tags.Urgency,
		Cooperation: tags.Cooperation,
		Trigger:     tags.Trigger,
		StartRound:  round,
		EndRound:    &round, // 新建的段, 开始轮就是结束轮
		StartAt:     now,
		EndAt:       &now, // 新建的段, 开始时间就是结束时间
		// DurationMinutes: 新建时持续时长为 0, 用 nil 表示(数据库存 NULL)
		TraceID: traceID,
	}

	if _, err := s.repo.InsertEmotionSession(ctx, session); err != nil {
		logger.Error("emotion: create session failed",
			zap.Int64("user_id", userID),
			zap.String("emotion", tags.Emotion),
			zap.Int("round", round),
			zap.Error(err),
		)
		return
	}

	logger.Debug("emotion: create session succeeded",
		zap.Int64("user_id", userID),
		zap.String("emotion", tags.Emotion),
		zap.Int("round", round),
	)
}

// extendEmotionSession 延长一条进行中的情绪持续段.
// 更新 EndRound 为当前轮, EndAt 为当前时间, 算出持续时长.
// 降级模式: 失败只打 Error 日志, 不向上抛错误.
func (s *Store) extendEmotionSession(ctx context.Context, latest data.EmotionSession, round int, tags ParsedTags) {
	now := time.Now()

	// 算持续时长: 当前时间减去开始时间, 转成分钟.
	// now.Sub(latest.StartAt) 是 time.Duration 类型(纳秒), .Minutes() 转成分钟浮点数.
	durationMinutes := now.Sub(latest.StartAt).Minutes()

	// 构造要更新的字段映射, 交给 repo.UpdateEmotionSession 执行.
	// repo 层用 GORM 的 Updates 方法, 只更新 fields 里的字段.
	fields := map[string]any{
		"end_round":        round,
		"end_at":            now,
		"duration_minutes":  durationMinutes,
		"intensity":         tags.Intensity, // 沿用最新一轮的强度
		"urgency":           tags.Urgency,
		"cooperation":       tags.Cooperation,
	}

	if err := s.repo.UpdateEmotionSession(ctx, latest.ID, fields); err != nil {
		logger.Error("emotion: extend session failed",
			zap.Int64("session_id", latest.ID),
			zap.Int("round", round),
			zap.Error(err),
		)
		return
	}

	logger.Debug("emotion: extend session succeeded",
		zap.Int64("session_id", latest.ID),
		zap.Int("round", round),
		zap.Float64("duration_minutes", durationMinutes),
	)
}

// GetRecentSessions 查用户最近的情绪持续段列表.
// 结果按开始时间倒序, 最多 limit 条, 给 injector 注入 system prompt 用.
//
// 参数:
//   - ctx: 上下文
//   - userID: 哪个用户的情绪段
//   - limit: 最多返回几条, 0 或负数由 repo 层用默认值
//
// 返回值:
//   - []data.EmotionSession: 情绪段列表, 空结果由 repo 层返回空切片
//   - error: 查询失败时返回错误
func (s *Store) GetRecentSessions(ctx context.Context, userID int64, limit int) ([]data.EmotionSession, error) {
	// since=0 表示不限制起始轮, 查全部最近的情绪段
	return s.repo.GetEmotionSessions(ctx, userID, 0, limit)
}

// GetLatestSession 查用户最近一条情绪持续段.
// 可能是进行中的 (EndAt == nil) 或已结束的.
// 查不到时返回 sql.ErrNoRows, 由调用方决定怎么处理.
//
// 参数:
//   - ctx: 上下文
//   - userID: 哪个用户的情绪段
//
// 返回值:
//   - data.EmotionSession: 查到的情绪持续段
//   - error: 查询失败时返回错误, 记录不存在时返回 sql.ErrNoRows
func (s *Store) GetLatestSession(ctx context.Context, userID int64) (data.EmotionSession, error) {
	return s.repo.GetLatestEmotionSession(ctx, userID)
}
