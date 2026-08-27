// profile_repo.go 放阶段 4.5 五张表的 SQLite 数据访问方法.
//
// 做的事情:
//  1. 对话标签明细 (dialogue_tags): InsertDialogueTag / GetDialogueTags / CountDialogueTagsByUser
//  2. 标签统计 (tag_statistics): UpsertTagStatistic / GetTagStatistics
//  3. 情绪持续段 (emotion_sessions): InsertEmotionSession / UpdateEmotionSession / GetLatestEmotionSession / GetEmotionSessions
//  4. 用户画像 (user_profiles): GetUserProfile / UpsertUserProfile
//  5. 待办提醒 (reminders): InsertReminder / GetReminders / GetReminder / UpdateReminder / GetPendingRemindersDue
//
// 设计要点:
//   - 所有方法挂在 *sqliteRepo 上, 和 memory_repo / candidate_repo 同一套模式.
//   - ORM 模型和业务对象通过 xxxToORM / xxxFromORM 互转.
//   - 可空字段用指针, 转换时调 stringToPtr / ptrToString / timeToPtr / ptrToTime 等辅助函数.
//   - SELECT 用显式列名常量, 不用 SELECT *.
//   - 日志: 写操作成功打 Debug, 失败打 Error, 密钥密文不打日志.
package data

import (
	"context"
	"fmt"
	"time"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ============================================================
// 对话标签明细 (dialogue_tags)
// ============================================================

// TableName 指定 dialogue_tags 表名.
func (ormDialogueTag) TableName() string { return "dialogue_tags" }

// InsertDialogueTag 创建一条对话标签明细.
// 执行完 GORM 自动回填 ID 和 CreatedAt.
func (r *sqliteRepo) InsertDialogueTag(ctx context.Context, tag DialogueTag) (DialogueTag, error) {
	model := dialogueTagToORM(tag)

	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		logger.Error("dialogue_tags insert failed",
			zap.Int64("user_id", tag.UserID),
			zap.String("tag_dim", tag.TagDim),
			zap.String("tag_value", tag.TagValue),
			zap.Error(err),
		)
		return DialogueTag{}, fmt.Errorf("insert dialogue tag: %w", err)
	}

	logger.Debug("dialogue_tags insert succeeded",
		zap.Int64("tag_id", model.ID),
		zap.Int64("user_id", model.UserID),
		zap.String("tag_dim", model.TagDim),
		zap.String("tag_value", model.TagValue),
	)
	return dialogueTagFromORM(model), nil
}

// GetDialogueTags 按用户 ID 查标签明细, 按时间倒序, 限制条数.
func (r *sqliteRepo) GetDialogueTags(ctx context.Context, userID int64, limit int) ([]DialogueTag, error) {
	if limit <= 0 {
		limit = 100
	}

	var models []ormDialogueTag
	if err := r.db.WithContext(ctx).Select(dialogueTagColumns).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Limit(limit).
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("get dialogue tags: %w", err)
	}

	result := make([]DialogueTag, 0, len(models))
	for _, m := range models {
		result = append(result, dialogueTagFromORM(m))
	}
	return result, nil
}

// CountDialogueTagsByUser 统计用户当前的对话轮数 (最大的 round 值).
// 返回 0 表示该用户还没有标签记录.
func (r *sqliteRepo) CountDialogueTagsByUser(ctx context.Context, userID int64) (int, error) {
	var maxRound int
	err := r.db.WithContext(ctx).Model(&ormDialogueTag{}).
		Where("user_id = ?", userID).
		Select("COALESCE(MAX(round), 0)").
		Scan(&maxRound).Error
	if err != nil {
		return 0, fmt.Errorf("count dialogue tags by user: %w", err)
	}
	return maxRound, nil
}

// dialogueTagToORM 把业务对象 DialogueTag 转成 ORM 模型.
func dialogueTagToORM(t DialogueTag) ormDialogueTag {
	return ormDialogueTag{
		ID:            t.ID,
		UserID:        t.UserID,
		SessionID:     t.SessionID,
		TraceID:       stringToPtr(t.TraceID),
		Round:         t.Round,
		TagDim:        t.TagDim,
		TagValue:      t.TagValue,
		TagExtra:      float64ToPtr(t.TagExtra),
		TriggerReason: t.TriggerReason,
		Source:        t.Source,
		CreatedAt:     t.CreatedAt,
	}
}

// dialogueTagFromORM 把 ORM 模型转回业务对象.
func dialogueTagFromORM(m ormDialogueTag) DialogueTag {
	return DialogueTag{
		ID:            m.ID,
		UserID:        m.UserID,
		SessionID:     m.SessionID,
		TraceID:       ptrToString(m.TraceID),
		Round:         m.Round,
		TagDim:        m.TagDim,
		TagValue:      m.TagValue,
		TagExtra:      ptrToFloat64(m.TagExtra),
		TriggerReason: m.TriggerReason,
		Source:        m.Source,
		CreatedAt:     m.CreatedAt,
	}
}

// ============================================================
// 标签统计 (tag_statistics)
// ============================================================

// TableName 指定 tag_statistics 表名.
func (ormTagStatistic) TableName() string { return "tag_statistics" }

// UpsertTagStatistic UPSERT 一条按天聚合的标签统计.
// 复合主键 (user_id, tag_dim, tag_value, period) 冲突时累加 hit_count 并更新 last_round.
func (r *sqliteRepo) UpsertTagStatistic(ctx context.Context, stat TagStatistic) error {
	model := ormTagStatistic{
		UserID:    stat.UserID,
		TagDim:    stat.TagDim,
		TagValue:  stat.TagValue,
		Period:    stat.Period,
		HitCount:  stat.HitCount,
		LastRound: stat.LastRound,
		UpdatedAt: stat.UpdatedAt,
	}

	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "user_id"}, {Name: "tag_dim"}, {Name: "tag_value"}, {Name: "period"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"hit_count":  gorm.Expr("tag_statistics.hit_count + excluded.hit_count"),
			"last_round": gorm.Expr("excluded.last_round"),
			"updated_at": gorm.Expr("excluded.updated_at"),
		}),
	}).Create(&model)

	if result.Error != nil {
		logger.Error("tag_statistics upsert failed",
			zap.Int64("user_id", stat.UserID),
			zap.String("tag_dim", stat.TagDim),
			zap.String("tag_value", stat.TagValue),
			zap.Error(result.Error),
		)
		return fmt.Errorf("upsert tag statistic: %w", result.Error)
	}

	logger.Debug("tag_statistics upsert succeeded",
		zap.Int64("user_id", stat.UserID),
		zap.String("tag_dim", stat.TagDim),
		zap.String("tag_value", stat.TagValue),
		zap.String("period", stat.Period),
	)
	return nil
}

// GetTagStatistics 查标签统计.
// tagDim 不为空时只查指定维度, 为空时查所有维度.
// since > 0 时只查 last_round > since 的记录, 否则查全部.
func (r *sqliteRepo) GetTagStatistics(ctx context.Context, userID int64, tagDim string, since int) ([]TagStatistic, error) {
	query := r.db.WithContext(ctx).Select(tagStatisticColumns).
		Where("user_id = ?", userID)

	if tagDim != "" {
		query = query.Where("tag_dim = ?", tagDim)
	}
	if since > 0 {
		query = query.Where("last_round > ?", since)
	}

	query = query.Order("hit_count DESC, last_round DESC")

	var models []ormTagStatistic
	if err := query.Find(&models).Error; err != nil {
		return nil, fmt.Errorf("get tag statistics: %w", err)
	}

	result := make([]TagStatistic, 0, len(models))
	for _, m := range models {
		result = append(result, tagStatisticFromORM(m))
	}
	return result, nil
}

// tagStatisticFromORM 把 ORM 模型转回业务对象.
func tagStatisticFromORM(m ormTagStatistic) TagStatistic {
	return TagStatistic{
		UserID:    m.UserID,
		TagDim:    m.TagDim,
		TagValue:  m.TagValue,
		Period:    m.Period,
		HitCount:  m.HitCount,
		LastRound: m.LastRound,
		UpdatedAt: m.UpdatedAt,
	}
}

// ============================================================
// 情绪持续段 (emotion_sessions)
// ============================================================

// TableName 指定 emotion_sessions 表名.
func (ormEmotionSession) TableName() string { return "emotion_sessions" }

// InsertEmotionSession 创建一条情绪持续段.
func (r *sqliteRepo) InsertEmotionSession(ctx context.Context, session EmotionSession) (EmotionSession, error) {
	model := emotionSessionToORM(session)

	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		logger.Error("emotion_sessions insert failed",
			zap.Int64("user_id", session.UserID),
			zap.String("emotion", session.Emotion),
			zap.Error(err),
		)
		return EmotionSession{}, fmt.Errorf("insert emotion session: %w", err)
	}

	logger.Debug("emotion_sessions insert succeeded",
		zap.Int64("session_id", model.ID),
		zap.Int64("user_id", model.UserID),
		zap.String("emotion", model.Emotion),
	)
	return emotionSessionFromORM(model), nil
}

// UpdateEmotionSession 更新一条情绪持续段 (延长或结束).
// fields 是要更新的字段映射, 比如 {"end_round": 10, "end_at": now, "duration_minutes": 5.2}.
func (r *sqliteRepo) UpdateEmotionSession(ctx context.Context, id int64, fields map[string]any) error {
	result := r.db.WithContext(ctx).Model(&ormEmotionSession{}).
		Where("id = ?", id).
		Updates(fields)

	if result.Error != nil {
		logger.Error("emotion_sessions update failed",
			zap.Int64("session_id", id),
			zap.Error(result.Error),
		)
		return fmt.Errorf("update emotion session %d: %w", id, result.Error)
	}

	if result.RowsAffected == 0 {
		return fmt.Errorf("update emotion session %d: not found", id)
	}

	logger.Debug("emotion_sessions update succeeded",
		zap.Int64("session_id", id),
	)
	return nil
}

// GetLatestEmotionSession 取用户最近一条情绪持续段.
// 可能是进行中的 (end_at IS NULL) 或已结束的.
// 查不到返回 sql.ErrNoRows.
func (r *sqliteRepo) GetLatestEmotionSession(ctx context.Context, userID int64) (EmotionSession, error) {
	var model ormEmotionSession
	if err := r.db.WithContext(ctx).Select(emotionSessionColumns).
		Where("user_id = ?", userID).
		Order("start_at DESC").
		First(&model).Error; err != nil {
		return EmotionSession{}, fmt.Errorf("get latest emotion session: %w", repositoryError(err))
	}
	return emotionSessionFromORM(model), nil
}

// GetEmotionSessions 查情绪持续段列表.
// since > 0 时只查 start_round > since 的记录, 否则查全部.
// limit 限制返回条数, 0 或负数用默认值 50.
func (r *sqliteRepo) GetEmotionSessions(ctx context.Context, userID int64, since int, limit int) ([]EmotionSession, error) {
	if limit <= 0 {
		limit = 50
	}

	query := r.db.WithContext(ctx).Select(emotionSessionColumns).
		Where("user_id = ?", userID)

	if since > 0 {
		query = query.Where("start_round > ?", since)
	}

	query = query.Order("start_at DESC").Limit(limit)

	var models []ormEmotionSession
	if err := query.Find(&models).Error; err != nil {
		return nil, fmt.Errorf("get emotion sessions: %w", err)
	}

	result := make([]EmotionSession, 0, len(models))
	for _, m := range models {
		result = append(result, emotionSessionFromORM(m))
	}
	return result, nil
}

// emotionSessionToORM 把业务对象 EmotionSession 转成 ORM 模型.
func emotionSessionToORM(s EmotionSession) ormEmotionSession {
	return ormEmotionSession{
		ID:              s.ID,
		UserID:          s.UserID,
		Emotion:         s.Emotion,
		Intensity:       s.Intensity,
		Urgency:         s.Urgency,
		Cooperation:     s.Cooperation,
		Trigger:         s.Trigger,
		StartRound:      s.StartRound,
		EndRound:        s.EndRound,
		StartAt:         s.StartAt,
		EndAt:           s.EndAt,
		DurationMinutes: s.DurationMinutes,
		TraceID:         stringToPtr(s.TraceID),
		CreatedAt:       s.CreatedAt,
	}
}

// emotionSessionFromORM 把 ORM 模型转回业务对象.
func emotionSessionFromORM(m ormEmotionSession) EmotionSession {
	return EmotionSession{
		ID:              m.ID,
		UserID:          m.UserID,
		Emotion:         m.Emotion,
		Intensity:       m.Intensity,
		Urgency:         m.Urgency,
		Cooperation:     m.Cooperation,
		Trigger:         m.Trigger,
		StartRound:      m.StartRound,
		EndRound:        m.EndRound,
		StartAt:         m.StartAt,
		EndAt:           m.EndAt,
		DurationMinutes: m.DurationMinutes,
		TraceID:         ptrToString(m.TraceID),
		CreatedAt:       m.CreatedAt,
	}
}

// ============================================================
// 用户画像 (user_profiles)
// ============================================================

// TableName 指定 user_profiles 表名.
func (ormUserProfile) TableName() string { return "user_profiles" }

// GetUserProfile 按用户 ID 查画像.
// 查不到返回 sql.ErrNoRows.
func (r *sqliteRepo) GetUserProfile(ctx context.Context, userID int64) (UserProfile, error) {
	var model ormUserProfile
	if err := r.db.WithContext(ctx).Select(userProfileColumns).
		Where("user_id = ?", userID).
		First(&model).Error; err != nil {
		return UserProfile{}, fmt.Errorf("get user profile: %w", repositoryError(err))
	}
	return userProfileFromORM(model), nil
}

// UpsertUserProfile 创建或更新用户画像.
// user_profiles 表对 user_id 有唯一索引, 冲突时更新所有字段.
func (r *sqliteRepo) UpsertUserProfile(ctx context.Context, profile UserProfile) (UserProfile, error) {
	now := time.Now()
	model := ormUserProfile{
		ID:             profile.ID,
		UserID:         profile.UserID,
		ProfileJSON:    profile.ProfileJSON,
		AnalyzedRounds: profile.AnalyzedRounds,
		AnalysisCount:  profile.AnalysisCount,
		UpdatedAt:      now,
		CreatedAt:      now,
	}

	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"profile_json":     gorm.Expr("excluded.profile_json"),
			"analyzed_rounds":  gorm.Expr("excluded.analyzed_rounds"),
			"analysis_count":   gorm.Expr("excluded.analysis_count"),
			"updated_at":       gorm.Expr("excluded.updated_at"),
		}),
	}).Create(&model)

	if result.Error != nil {
		logger.Error("user_profiles upsert failed",
			zap.Int64("user_id", profile.UserID),
			zap.Error(result.Error),
		)
		return UserProfile{}, fmt.Errorf("upsert user profile: %w", result.Error)
	}

	logger.Debug("user_profiles upsert succeeded",
		zap.Int64("user_id", model.UserID),
		zap.Int("analyzed_rounds", model.AnalyzedRounds),
		zap.Int("analysis_count", model.AnalysisCount),
	)
	return userProfileFromORM(model), nil
}

// userProfileFromORM 把 ORM 模型转回业务对象.
func userProfileFromORM(m ormUserProfile) UserProfile {
	return UserProfile{
		ID:             m.ID,
		UserID:         m.UserID,
		ProfileJSON:    m.ProfileJSON,
		AnalyzedRounds: m.AnalyzedRounds,
		AnalysisCount:  m.AnalysisCount,
		UpdatedAt:      m.UpdatedAt,
		CreatedAt:      m.CreatedAt,
	}
}

// ============================================================
// 待办提醒 (reminders)
// ============================================================

// TableName 指定 reminders 表名.
func (ormReminder) TableName() string { return "reminders" }

// InsertReminder 创建一条待办提醒.
func (r *sqliteRepo) InsertReminder(ctx context.Context, reminder Reminder) (Reminder, error) {
	model := reminderToORM(reminder)

	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		logger.Error("reminders insert failed",
			zap.Int64("user_id", reminder.UserID),
			zap.Error(err),
		)
		return Reminder{}, fmt.Errorf("insert reminder: %w", err)
	}

	logger.Debug("reminders insert succeeded",
		zap.Int64("reminder_id", model.ID),
		zap.Int64("user_id", model.UserID),
	)
	return reminderFromORM(model), nil
}

// GetReminders 按用户 ID 和状态查提醒列表.
// status 为空时查所有状态, 按提醒时间升序返回.
func (r *sqliteRepo) GetReminders(ctx context.Context, userID int64, status string) ([]Reminder, error) {
	query := r.db.WithContext(ctx).Select(reminderColumns).
		Where("user_id = ?", userID)

	if status != "" {
		query = query.Where("status = ?", status)
	}

	query = query.Order("remind_at ASC")

	var models []ormReminder
	if err := query.Find(&models).Error; err != nil {
		return nil, fmt.Errorf("get reminders: %w", err)
	}

	result := make([]Reminder, 0, len(models))
	for _, m := range models {
		result = append(result, reminderFromORM(m))
	}
	return result, nil
}

// GetReminder 按 ID 查一条提醒.
// 查不到返回 sql.ErrNoRows.
func (r *sqliteRepo) GetReminder(ctx context.Context, id int64) (Reminder, error) {
	var model ormReminder
	if err := r.db.WithContext(ctx).Select(reminderColumns).
		First(&model, id).Error; err != nil {
		return Reminder{}, fmt.Errorf("get reminder %d: %w", id, repositoryError(err))
	}
	return reminderFromORM(model), nil
}

// UpdateReminder 更新一条提醒 (改状态/改时间/改内容).
// fields 是要更新的字段映射, 比如 {"status": "delivered", "delivered_at": now}.
func (r *sqliteRepo) UpdateReminder(ctx context.Context, id int64, fields map[string]any) error {
	result := r.db.WithContext(ctx).Model(&ormReminder{}).
		Where("id = ?", id).
		Updates(fields)

	if result.Error != nil {
		logger.Error("reminders update failed",
			zap.Int64("reminder_id", id),
			zap.Error(result.Error),
		)
		return fmt.Errorf("update reminder %d: %w", id, result.Error)
	}

	if result.RowsAffected == 0 {
		return fmt.Errorf("update reminder %d: not found", id)
	}

	logger.Debug("reminders update succeeded",
		zap.Int64("reminder_id", id),
	)
	return nil
}

// GetPendingRemindersDue 查已到期但尚未投递的提醒.
// 条件: status = 'pending' AND remind_at <= now.
// 用于后台定时扫描, 把到期提醒投递给用户.
func (r *sqliteRepo) GetPendingRemindersDue(ctx context.Context, now time.Time) ([]Reminder, error) {
	var models []ormReminder
	if err := r.db.WithContext(ctx).Select(reminderColumns).
		Where("status = ? AND remind_at <= ?", ReminderStatusPending, now).
		Order("remind_at ASC").
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("get pending reminders due: %w", err)
	}

	result := make([]Reminder, 0, len(models))
	for _, m := range models {
		result = append(result, reminderFromORM(m))
	}
	return result, nil
}

// reminderToORM 把业务对象 Reminder 转成 ORM 模型.
func reminderToORM(r Reminder) ormReminder {
	return ormReminder{
		ID:             r.ID,
		UserID:         r.UserID,
		SessionID:      r.SessionID,
		TraceID:        stringToPtr(r.TraceID),
		Content:        r.Content,
		RemindAt:       r.RemindAt,
		Status:         r.Status,
		Source:         r.Source,
		CreatedAt:      r.CreatedAt,
		DeliveredAt:    r.DeliveredAt,
		AcknowledgedAt: r.AcknowledgedAt,
	}
}

// reminderFromORM 把 ORM 模型转回业务对象.
func reminderFromORM(m ormReminder) Reminder {
	return Reminder{
		ID:             m.ID,
		UserID:         m.UserID,
		SessionID:      m.SessionID,
		TraceID:        ptrToString(m.TraceID),
		Content:        m.Content,
		RemindAt:       m.RemindAt,
		Status:         m.Status,
		Source:         m.Source,
		CreatedAt:      m.CreatedAt,
		DeliveredAt:    m.DeliveredAt,
		AcknowledgedAt: m.AcknowledgedAt,
	}
}

// ============================================================
// 指针辅助函数 (float64 和 int 版本)
// ============================================================

// float64ToPtr 把 float64 转成 *float64, 0 返回 nil.
// 用于 ORM 模型中允许 NULL 的 float64 字段 (如 tag_extra, duration_minutes).
func float64ToPtr(v float64) *float64 {
	if v == 0 {
		return nil
	}
	return &v
}

// ptrToFloat64 把 *float64 转成 float64, nil 返回 0.
func ptrToFloat64(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}
