// store.go 放 profile.Store 的写入和查询方法.
//
// 做的事情:
//  1. RecordTags: 把 LLM 标签解析结果中的非情绪维度 (urgency/cooperation) 写入 dialogue_tags 表,
//     并对所有维度 (含情绪) 做按天聚合统计 upsert.
//  2. GetTagStatistics: 查标签统计数据, 给 stats.go 格式化后调 LLM 分析用.
//  3. GetUserProfile: 查用户画像, 给 injector.go 注入 system prompt 用.
//  4. GetAnalyzedRounds: 查已分析轮数, 给 service.go 判断是否达到阈值用.
//  5. UpsertProfile: 存更新后的画像和分析轮数.
//  6. CountUserRounds: 查用户当前最大 round, 即总对话轮数.
//
// 方案 16.12.6 节: 每轮对话打标签, 标签累积统计, 达到阈值后调 LLM 归纳画像.
// emotion 维度的 dialogue_tag 由 emotion.Store.RecordTags 写, 其他维度由这里写.
// tag_statistics 的所有维度都由这里统一 upsert, 因为统计表不区分来源模块.
package profile

import (
	"context"
	"fmt"
	"time"

	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// RecordTags 把 LLM 标签解析结果写入数据库.
//
// 做两件事:
//  1. 写非情绪维度的对话标签明细 (dialogue_tags 表):
//     emotion 维度由 emotion.Store.RecordTags 负责写, 这里只写 urgency 和 cooperation.
//  2. 对所有维度 (含情绪) 做按天聚合统计 upsert (tag_statistics 表):
//     每个维度+值对应一行统计, 每轮 +1, 同时更新 last_round.
//
// 降级模式: 所有错误只打 Error 日志, 不返回 error.
// 画像追踪不应该阻断正常对话, 哪怕写库失败, 对话也能继续.
//
// 参数:
//   - ctx: 上下文, 支持超时取消
//   - userID: 哪个用户的标签
//   - sessionID: 来源会话 ID
//   - traceID: 链路追踪 ID
//   - round: 第几轮对话
//   - tags: 从 LLM <tags> 解析出的标签数据
func (s *Store) RecordTags(
	ctx context.Context,
	userID int64,
	sessionID, traceID string,
	round int,
	tags TagInput,
) {
	// 第一步: 写非情绪维度的对话标签明细到 dialogue_tags 表.
	// emotion 维度由 emotion.Store 写, 这里写 urgency 和 cooperation 两个维度.
	s.writeTag(ctx, userID, sessionID, traceID, TagDimUrgency, tags.Urgency, round, 0, tags.Trigger)
	s.writeTag(ctx, userID, sessionID, traceID, TagDimCooperation, tags.Cooperation, round, 0, tags.Trigger)

	// 第二步: 对所有维度做按天聚合统计 upsert.
	// 每个维度+值对应一行统计, 每轮 +1.
	period := time.Now().Format("2006-01-02")
	s.upsertStatistic(ctx, userID, "emotion", tags.Emotion, period, round)
	s.upsertStatistic(ctx, userID, TagDimUrgency, tags.Urgency, period, round)
	s.upsertStatistic(ctx, userID, TagDimCooperation, tags.Cooperation, period, round)
}

// writeTag 写一条对话标签明细到 dialogue_tags 表.
// 降级模式: 失败只打 Error 日志, 不向上抛错误.
func (s *Store) writeTag(
	ctx context.Context,
	userID int64,
	sessionID, traceID, tagDim, tagValue string,
	round int,
	tagExtra float64,
	triggerReason string,
) {
	// 空值跳过: 某些维度可能没有值 (如 LLM 没输出 cooperation)
	if tagValue == "" {
		return
	}

	tag := data.DialogueTag{
		UserID:        userID,
		SessionID:     sessionID,
		TraceID:       traceID,
		Round:         round,
		TagDim:        tagDim,
		TagValue:      tagValue,
		TagExtra:      tagExtra,
		TriggerReason: triggerReason,
		Source:        data.TagSourceLLM,
	}
	if _, err := s.repo.InsertDialogueTag(ctx, tag); err != nil {
		logger.Error("profile: insert dialogue tag failed",
			zap.Int64("user_id", userID),
			zap.String("tag_dim", tagDim),
			zap.String("tag_value", tagValue),
			zap.Int("round", round),
			zap.Error(err),
		)
	}
}

// upsertStatistic UPSERT 一条按天聚合的标签统计.
// 同一 (user_id, tag_dim, tag_value, period) 存在就 hit_count+1, 不存在就插入.
// 同时更新 last_round 为当前轮.
// 降级模式: 失败只打 Error 日志, 不向上抛错误.
func (s *Store) upsertStatistic(
	ctx context.Context,
	userID int64,
	tagDim, tagValue, period string,
	round int,
) {
	// 空值跳过
	if tagValue == "" {
		return
	}

	stat := data.TagStatistic{
		UserID:    userID,
		TagDim:    tagDim,
		TagValue:  tagValue,
		Period:    period,
		HitCount:  1,
		LastRound: round,
		UpdatedAt: time.Now(),
	}
	if err := s.repo.UpsertTagStatistic(ctx, stat); err != nil {
		logger.Error("profile: upsert tag statistic failed",
			zap.Int64("user_id", userID),
			zap.String("tag_dim", tagDim),
			zap.String("tag_value", tagValue),
			zap.String("period", period),
			zap.Error(err),
		)
	}
}

// GetTagStatistics 查标签统计数据.
// 可按维度过滤 (tagDim 为空时查所有维度), since > 0 时只查 last_round > since 的记录.
// 给 stats.go 格式化后调 LLM 分析用.
//
// 参数:
//   - ctx: 上下文
//   - userID: 哪个用户的统计
//   - tagDim: 按维度过滤, 空串表示不过滤
//   - since: 只查 last_round > since 的记录, 0 表示不限制
//
// 返回值:
//   - []data.TagStatistic: 统计列表, 空结果返回空切片
//   - error: 查询失败时返回错误
func (s *Store) GetTagStatistics(ctx context.Context, userID int64, tagDim string, since int) ([]data.TagStatistic, error) {
	stats, err := s.repo.GetTagStatistics(ctx, userID, tagDim, since)
	if err != nil {
		return nil, fmt.Errorf("get tag statistics: %w", err)
	}
	if stats == nil {
		return []data.TagStatistic{}, nil
	}
	return stats, nil
}

// GetUserProfile 查用户画像.
// 查不到时返回 sql.ErrNoRows (由 repositoryError 转换), 调用方按"没有画像"处理.
//
// 参数:
//   - ctx: 上下文
//   - userID: 哪个用户的画像
//
// 返回值:
//   - data.UserProfile: 画像记录, profile_json 字段存 ProfileData 的 JSON
//   - error: 查询失败时返回错误, 记录不存在时返回 sql.ErrNoRows
func (s *Store) GetUserProfile(ctx context.Context, userID int64) (data.UserProfile, error) {
	return s.repo.GetUserProfile(ctx, userID)
}

// UpsertProfile 创建或更新用户画像.
// 存储时 profile_json 已经是 LLM 返回的 JSON 字符串, 直接存.
//
// 参数:
//   - ctx: 上下文
//   - profile: 画像记录, 至少要有 UserID 和 ProfileJSON
//
// 返回值:
//   - data.UserProfile: 更新后的画像记录
//   - error: 写入失败时返回错误
func (s *Store) UpsertProfile(ctx context.Context, profile data.UserProfile) (data.UserProfile, error) {
	updated, err := s.repo.UpsertUserProfile(ctx, profile)
	if err != nil {
		return data.UserProfile{}, fmt.Errorf("upsert profile: %w", err)
	}
	return updated, nil
}

// CountUserRounds 查用户当前最大 round, 即总对话轮数.
// 给 service.go 判断是否达到分析阈值用.
//
// 参数:
//   - ctx: 上下文
//   - userID: 哪个用户的轮数
//
// 返回值:
//   - int: 最大 round 值, 0 表示还没有对话标签
//   - error: 查询失败时返回错误
func (s *Store) CountUserRounds(ctx context.Context, userID int64) (int, error) {
	return s.repo.CountDialogueTagsByUser(ctx, userID)
}

// GetAnalyzedRounds 查已分析轮数.
// user_profiles.analyzed_rounds 字段记录上次分析后清零的计数器值.
// service.go 用当前总轮数减去已分析轮数, 判断是否达到阈值.
//
// 参数:
//   - ctx: 上下文
//   - userID: 哪个用户的已分析轮数
//
// 返回值:
//   - int: 已分析轮数, 0 表示还没分析过
//   - error: 查询失败时返回错误 (包括记录不存在的 sql.ErrNoRows)
func (s *Store) GetAnalyzedRounds(ctx context.Context, userID int64) (int, error) {
	profile, err := s.repo.GetUserProfile(ctx, userID)
	if err != nil {
		return 0, err
	}
	return profile.AnalyzedRounds, nil
}
