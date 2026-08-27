// profile_service.go 放画像和对话标签相关的业务编排层.
//
// 做的事情:
//  1. 定义 NewProfileService, NewEmotionService 工厂函数.
//  2. ProfileService.GetProfile: 查用户画像.
//  3. ProfileService.ListTags: 查对话标签列表.
//  4. ProfileService.ListTagStatistics: 查标签统计.
//  5. EmotionService.ListEmotionSessions: 查情绪持续段列表.
//  6. EmotionService.GetLatestEmotionSession: 查最近一条情绪段.
//
// 方案 16.12.6 节的 API:
//   GET /api/v1/profiles          → GetProfile
//   GET /api/v1/tags              → ListTags
//   GET /api/v1/tag-statistics    → ListTagStatistics
//   GET /api/v1/emotion-sessions  → ListEmotionSessions
package service

import (
	"context"
	"fmt"

	"github.com/swallow-sun/swallow-go/internal/data"
)

// NewProfileService 创建一个 ProfileService.
func NewProfileService(deps *Deps) *ProfileService {
	return &ProfileService{store: deps.profileStore, repo: deps.repo}
}

// NewEmotionService 创建一个 EmotionService.
func NewEmotionService(deps *Deps) *EmotionService {
	return &EmotionService{store: deps.emotionStore}
}

// GetProfile 查用户画像.
// 方案 16.12.6 节: GET /api/v1/profiles.
//
// 参数:
//   - ctx: 上下文
//   - userID: 哪个用户的画像
//
// 返回值:
//   - ProfileResult: 画像查询结果
//   - error: 查询失败时返回错误
func (s *ProfileService) GetProfile(ctx context.Context, userID int64) (ProfileResult, error) {
	profile, err := s.store.GetUserProfile(ctx, userID)
	if err != nil {
		return ProfileResult{}, fmt.Errorf("get profile: %w", err)
	}
	return ProfileResult{
		ProfileJSON:    profile.ProfileJSON,
		AnalyzedRounds: profile.AnalyzedRounds,
		AnalysisCount:  profile.AnalysisCount,
	}, nil
}

// ListTags 查对话标签列表.
// 方案 16.12.6 节: GET /api/v1/tags.
//
// 参数:
//   - ctx: 上下文
//   - userID: 哪个用户的标签
//   - limit: 最多返回几条, 0 或负数由 repo 层用默认值
//
// 返回值:
//   - ListTagsResult: 标签列表结果
//   - error: 查询失败时返回错误
func (s *ProfileService) ListTags(ctx context.Context, userID int64, limit int) (ListTagsResult, error) {
	tags, err := s.repo.GetDialogueTags(ctx, userID, limit)
	if err != nil {
		return ListTagsResult{}, fmt.Errorf("list tags: %w", err)
	}
	if tags == nil {
		tags = []data.DialogueTag{}
	}
	return ListTagsResult{Items: tags}, nil
}

// ListTagStatistics 查标签统计列表.
// 方案 16.12.6 节: GET /api/v1/tag-statistics.
//
// 参数:
//   - ctx: 上下文
//   - userID: 哪个用户的统计
//   - tagDim: 按维度过滤, 空串表示不过滤
//   - since: 只查 last_round > since 的记录, 0 表示不限制
//
// 返回值:
//   - ListTagStatisticsResult: 统计列表结果
//   - error: 查询失败时返回错误
func (s *ProfileService) ListTagStatistics(ctx context.Context, userID int64, tagDim string, since int) (ListTagStatisticsResult, error) {
	stats, err := s.store.GetTagStatistics(ctx, userID, tagDim, since)
	if err != nil {
		return ListTagStatisticsResult{}, fmt.Errorf("list tag statistics: %w", err)
	}
	return ListTagStatisticsResult{Items: stats}, nil
}

// ListEmotionSessions 查情绪持续段列表.
// 方案 16.12.6 节: GET /api/v1/emotion-sessions.
//
// 参数:
//   - ctx: 上下文
//   - userID: 哪个用户的情绪段
//   - limit: 最多返回几条
//
// 返回值:
//   - ListEmotionSessionsResult: 情绪段列表结果
//   - error: 查询失败时返回错误
func (s *EmotionService) ListEmotionSessions(ctx context.Context, userID int64, limit int) (ListEmotionSessionsResult, error) {
	sessions, err := s.store.GetRecentSessions(ctx, userID, limit)
	if err != nil {
		return ListEmotionSessionsResult{}, fmt.Errorf("list emotion sessions: %w", err)
	}
	if sessions == nil {
		sessions = []data.EmotionSession{}
	}
	return ListEmotionSessionsResult{Items: sessions}, nil
}

// GetLatestEmotionSession 查最近一条情绪持续段.
// 方案 16.12.6 节: GET /api/v1/emotion-sessions/latest.
//
// 参数:
//   - ctx: 上下文
//   - userID: 哪个用户的情绪段
//
// 返回值:
//   - data.EmotionSession: 最近一条情绪段
//   - error: 查询失败时返回错误
func (s *EmotionService) GetLatestEmotionSession(ctx context.Context, userID int64) (data.EmotionSession, error) {
	session, err := s.store.GetLatestSession(ctx, userID)
	if err != nil {
		return data.EmotionSession{}, fmt.Errorf("get latest emotion session: %w", err)
	}
	return session, nil
}
