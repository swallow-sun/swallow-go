// service.go 放画像分析的后台异步服务.
//
// 做的事情:
//  1. 定义 Service 结构体: 持有 store, llm provider, model 名和配置阈值.
//  2. CheckAndAnalyze: 检查用户是否达到分析阈值, 达到就异步调 LLM 归纳画像.
//  3. analyze: 实际调 LLM 分析画像的内部方法, 在后台 goroutine 里跑.
//  4. 降级模式: 分析失败只打日志, 不影响对话.
//
// 方案 16.12.6 节: 每轮对话后检查阈值, 达到阈值后台异步调 LLM 归纳画像, 不阻断对话.
// 增量更新: 保留已有特征, 只增不改, 除非统计数据有明确反例.
// 计数器清零: 分析成功后 analyzed_rounds 更新为当前总轮数.
package profile

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/swallow-sun/swallow-go/internal/config"
	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/provider/llm"
	"github.com/swallow-sun/swallow-go/internal/trace"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// Service 是画像分析的后台异步服务.
// 持有 store (数据存取), llm provider (调模型), model 名和配置阈值.
type Service struct {
	store  *Store
	llm    llm.Provider
	model  string
	config config.ProfileConfig
}

// NewService 创建一个画像分析服务.
//
// 参数:
//   - store: 画像数据存储
//   - provider: LLM 提供方, 用于调模型归纳画像
//   - model: 模型名, 如 "deepseek-chat"
//   - cfg: 画像分析配置, 包含分析阈值
func NewService(store *Store, provider llm.Provider, model string, cfg config.ProfileConfig) *Service {
	return &Service{
		store:  store,
		llm:    provider,
		model:  model,
		config: cfg,
	}
}

// CheckAndAnalyze 检查用户是否达到分析阈值, 达到就异步调 LLM 归纳画像.
// 这个方法不阻塞, 分析在后台 goroutine 里跑, 失败不影响对话.
//
// 流程:
//  1. 查当前总轮数 (CountUserRounds)
//  2. 查已分析轮数 (GetAnalyzedRounds)
//  3. 差值 >= 阈值 → 启动后台分析
//  4. 后台 goroutine: 查统计数据 → 调 LLM → 解析 JSON → 更新画像 → 更新已分析轮数
//
// 降级模式: 检查失败或分析失败都只打日志, 不返回 error.
//
// 参数:
//   - ctx: 上下文 (分析在后台 goroutine 跑, 用 trace.WithID 保持 trace 关联)
//   - userID: 哪个用户要分析
func (s *Service) CheckAndAnalyze(ctx context.Context, userID int64) {
	// 阈值防御: 小于等于 0 不分析
	threshold := s.config.AnalysisThreshold
	if threshold <= 0 {
		return
	}

	// 查当前总轮数
	totalRounds, err := s.store.CountUserRounds(ctx, userID)
	if err != nil {
		logger.Error("profile: count user rounds failed",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		return
	}

	// 没有对话标签, 不分析
	if totalRounds == 0 {
		return
	}

	// 查已分析轮数
	analyzedRounds, err := s.store.GetAnalyzedRounds(ctx, userID)
	if err != nil {
		// 记录不存在 (还没分析过) 时 analyzedRounds 为 0, 这是正常的
		// 只有非 not-found 错误才需要打日志
		analyzedRounds = 0
	}

	// 差值 = 当前总轮数 - 已分析轮数
	// 差值 >= 阈值才分析
	pendingRounds := totalRounds - analyzedRounds
	if pendingRounds < threshold {
		return
	}

	// 达到阈值, 启动后台分析
	// 用新的 context.Background() 而不是复用对话的 ctx, 因为对话结束后 ctx 会被取消
	// trace.WithID 保持 trace 关联
	traceID := trace.FromContext(ctx)
	analyzeCtx := trace.WithID(context.Background(), traceID)

	go s.analyze(analyzeCtx, userID, analyzedRounds, totalRounds)

	logger.Info("profile analysis triggered",
		zap.Int64("user_id", userID),
		zap.String("trace_id", traceID),
		zap.Int("analyzed_rounds", analyzedRounds),
		zap.Int("total_rounds", totalRounds),
		zap.Int("pending_rounds", pendingRounds),
		zap.Int("threshold", threshold),
	)
}

// analyze 在后台 goroutine 里执行画像分析.
// 流程: 查统计数据 → 调 LLM → 解析 JSON → 更新画像 → 更新已分析轮数.
// 降级模式: 任何步骤失败都只打日志, 不 panic.
//
// 参数:
//   - ctx: 上下文 (后台 context, 不受对话生命周期影响)
//   - userID: 哪个用户要分析
//   - analyzedRounds: 上次分析的轮数 (查统计数据时作为 since 参数)
//   - totalRounds: 当前总轮数 (分析成功后写入 analyzed_rounds)
func (s *Service) analyze(ctx context.Context, userID int64, analyzedRounds, totalRounds int) {
	// 1. 查统计数据: 只查上次分析之后的轮次 (since = analyzedRounds)
	stats, err := s.store.GetTagStatistics(ctx, userID, "", analyzedRounds)
	if err != nil {
		logger.Error("profile analysis: get tag statistics failed",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		return
	}

	// 没有统计数据, 不分析
	if len(stats) == 0 {
		logger.Warn("profile analysis: no tag statistics found",
			zap.Int64("user_id", userID),
			zap.Int("since", analyzedRounds),
		)
		return
	}

	// 2. 格式化统计数据为 LLM 输入文本
	statsText := FormatStatsForLLM(stats)

	// 3. 查已有画像 (用于增量更新)
	existingProfile := ""
	if existing, err := s.store.GetUserProfile(ctx, userID); err == nil {
		existingProfile = existing.ProfileJSON
	}

	// 4. 构造 LLM 请求并调用
	msgs := buildAnalysisMessages(existingProfile, statsText)
	req := llm.ChatRequest{
		Model:    s.model,
		Messages: msgs,
	}

	resp, err := s.llm.Complete(ctx, req)
	if err != nil {
		logger.Error("profile analysis: LLM call failed",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		return
	}

	// 5. 验证 LLM 返回的 JSON 格式
	var pd ProfileData
	if err := json.Unmarshal([]byte(resp.Content), &pd); err != nil {
		logger.Error("profile analysis: parse LLM response as JSON failed",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		return
	}

	// 6. 重新序列化 (规范化 JSON 格式, 存到数据库)
	profileJSON, err := json.Marshal(pd)
	if err != nil {
		logger.Error("profile analysis: marshal profile data failed",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		return
	}

	// 7. 更新画像 + 分析轮数
	_, err = s.store.UpsertProfile(ctx, data.UserProfile{
		UserID:         userID,
		ProfileJSON:    string(profileJSON),
		AnalyzedRounds: totalRounds,
	})
	if err != nil {
		logger.Error("profile analysis: upsert profile failed",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		return
	}

	logger.Info("profile analysis completed",
		zap.Int64("user_id", userID),
		zap.Int("total_rounds", totalRounds),
		zap.Int("stats_count", len(stats)),
	)
}

// UpdateAnalyzedRounds 更新已分析轮数.
// 在 UpsertProfile 里已经一起更新了, 这个方法给外部调用备用.
func (s *Service) UpdateAnalyzedRounds(ctx context.Context, userID int64, analyzedRounds int) error {
	// 先查已有画像
	existing, err := s.store.GetUserProfile(ctx, userID)
	if err != nil {
		return fmt.Errorf("get profile for update analyzed rounds: %w", err)
	}

	// 更新 analyzed_rounds
	_, err = s.store.UpsertProfile(ctx, data.UserProfile{
		UserID:         userID,
		ProfileJSON:    existing.ProfileJSON,
		AnalyzedRounds: analyzedRounds,
	})
	if err != nil {
		return fmt.Errorf("upsert profile for update analyzed rounds: %w", err)
	}
	return nil
}
