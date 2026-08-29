package data

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm/clause"
)

func (ormCompanionState) TableName() string { return "companion_states" }

// GetCompanionState 读取指定用户的关系人格状态。
func (r *gormRepo) GetCompanionState(ctx context.Context, userID int64) (CompanionState, error) {
	var model ormCompanionState
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&model).Error; err != nil {
		return CompanionState{}, repositoryError(err)
	}
	return companionStateFromORM(model), nil
}

// UpsertCompanionState 按用户覆盖更新，使情感表达能跨会话延续。
func (r *gormRepo) UpsertCompanionState(ctx context.Context, state CompanionState) (CompanionState, error) {
	model := ormCompanionState{
		UserID:              state.UserID,
		Concern:             state.Concern,
		Urgency:             state.Urgency,
		Fondness:            state.Fondness,
		Playfulness:         state.Playfulness,
		AllowTeasing:        state.AllowTeasing,
		AllowStrictReminder: state.AllowStrictReminder,
		AllowAffection:      state.AllowAffection,
		LastMode:            state.LastMode,
		CurrentTask:         state.CurrentTask,
		TaskUpdatedAt:       state.TaskUpdatedAt,
		InteractionCount:    state.InteractionCount,
		UpdatedAt:           time.Now(),
	}
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"concern", "urgency", "fondness", "playfulness", "allow_teasing", "allow_strict_reminder", "allow_affection", "last_mode", "current_task", "task_updated_at", "interaction_count", "updated_at"}),
	}).Create(&model)
	if result.Error != nil {
		return CompanionState{}, fmt.Errorf("upsert companion state: %w", result.Error)
	}
	return companionStateFromORM(model), nil
}

func companionStateFromORM(model ormCompanionState) CompanionState {
	return CompanionState{
		UserID:              model.UserID,
		Concern:             model.Concern,
		Urgency:             model.Urgency,
		Fondness:            model.Fondness,
		Playfulness:         model.Playfulness,
		AllowTeasing:        model.AllowTeasing,
		AllowStrictReminder: model.AllowStrictReminder,
		AllowAffection:      model.AllowAffection,
		LastMode:            model.LastMode,
		CurrentTask:         model.CurrentTask,
		TaskUpdatedAt:       model.TaskUpdatedAt,
		InteractionCount:    model.InteractionCount,
		UpdatedAt:           model.UpdatedAt,
	}
}
