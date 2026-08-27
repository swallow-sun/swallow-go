// Package companion 实现可控的关系人格和情感行为策略。
package companion

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/swallow-sun/swallow-go/internal/data"
)

const (
	ModeNeutral      = "neutral"
	ModeComfort      = "comfort"
	ModeUrgent       = "urgent"
	ModeAffectionate = "affectionate_reminder"
	ModeStrict       = "strict_reminder"
	ModeFarewell     = "farewell"
	ModeTeach        = "teach"
)

type Service struct{ repo data.Repository }

// Decision 是本轮关系人格决策。Directive 是可信系统规则，CurrentTask 是非可信用户数据。
type Decision struct {
	Directive   string
	CurrentTask string
}

func New(repo data.Repository) *Service { return &Service{repo: repo} }

func defaultState(userID int64) data.CompanionState {
	return data.CompanionState{UserID: userID, Fondness: 0.65, Playfulness: 0.35, AllowTeasing: true, AllowStrictReminder: true, AllowAffection: true}
}

func containsAny(value string, words ...string) bool {
	for _, word := range words {
		if strings.Contains(value, word) {
			return true
		}
	}
	return false
}

func clamp(value float64) float64 { return math.Max(0, math.Min(1, value)) }

// Prepare 在模型推理前更新状态，并返回本轮的行为指令。
func (s *Service) Prepare(ctx context.Context, userID int64, input string, hasDueReminder bool) Decision {
	state, err := s.repo.GetCompanionState(ctx, userID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return Decision{}
		}
		state = defaultState(userID)
	}
	state, mode := applyInput(state, input, hasDueReminder)
	_, _ = s.repo.UpsertCompanionState(ctx, state)
	currentTask := ""
	if shouldReferenceCurrentTask(input, hasDueReminder) {
		currentTask = state.CurrentTask
	}
	return Decision{Directive: Directive(state, mode), CurrentTask: currentTask}
}

// shouldReferenceCurrentTask 控制任务断点何时进入本轮模型上下文。
// 任务状态可以长期保存，但不能在普通寒暄和无关新话题中反复出现。
func shouldReferenceCurrentTask(input string, hasDueReminder bool) bool {
	if hasDueReminder {
		return true
	}
	text := strings.TrimSpace(input)
	return containsAny(text,
		"我在做", "我正在", "我要做", "继续做", "开始做",
		"继续", "接着", "刚才", "上次", "当前任务", "这个任务",
		"进度", "做到", "做完", "完成", "搞定", "还没", "下一步",
		"然后呢", "再来", "回来继续", "先走", "下线", "拜拜", "再见",
	)
}

func applyInput(state data.CompanionState, input string, hasDueReminder bool) (data.CompanionState, string) {
	text := strings.TrimSpace(input)
	if containsAny(text, "我在做", "我正在", "我要做", "继续做", "开始做") {
		runes := []rune(text)
		if len(runes) > 160 {
			runes = runes[:160]
		}
		state.CurrentTask = string(runes)
		now := time.Now()
		state.TaskUpdatedAt = &now
	}
	if containsAny(text, "做完了", "已完成", "搞定了", "任务完成") {
		state.CurrentTask = ""
		state.TaskUpdatedAt = nil
	}
	// 用户随时可以关闭某类亲密表达。
	if containsAny(text, "别嘲笑", "不要嘲笑", "别调侃") {
		state.AllowTeasing = false
	}
	if containsAny(text, "别凶我", "不要凶", "别严厉催") {
		state.AllowStrictReminder = false
	}
	if containsAny(text, "别撒娇", "别宠溺", "不要亲昵") {
		state.AllowAffection = false
	}
	if containsAny(text, "可以调侃", "可以嘲笑") {
		state.AllowTeasing = true
	}
	if containsAny(text, "可以凶", "严厉催我") {
		state.AllowStrictReminder = true
	}
	if containsAny(text, "可以撒娇", "宠溺一点") {
		state.AllowAffection = true
	}

	mode := ModeNeutral
	switch {
	case containsAny(text, "我走了", "先走了", "下线了", "睡觉了", "拜拜", "再见"):
		mode = ModeFarewell
		state.Fondness = clamp(state.Fondness + 0.03)
	case containsAny(text, "难受", "好累", "崩溃", "焦虑", "害怕", "不开心", "没信心"):
		mode = ModeComfort
		state.Concern = clamp(state.Concern + 0.35)
		state.Urgency = clamp(state.Urgency - 0.2)
	case hasDueReminder && state.AllowStrictReminder && state.Urgency >= 0.65:
		mode = ModeStrict
	case hasDueReminder && state.AllowAffection:
		mode = ModeAffectionate
	case containsAny(text, "来不及", "截止", "马上要", "很急", "赶紧"):
		mode = ModeUrgent
		state.Urgency = clamp(state.Urgency + 0.4)
	case containsAny(text, "不会", "不懂", "没学过", "怎么做"):
		mode = ModeTeach
	}
	state.Concern = clamp(state.Concern * 0.92)
	state.Urgency = clamp(state.Urgency * 0.9)
	state.LastMode = mode
	state.InteractionCount++
	return state, mode
}

// Directive 生成给模型的受约束行为指令。
func Directive(state data.CompanionState, mode string) string {
	base := "[关系人格与行为策略]\n普通情绪表达由场景自动决定，不要向用户逐次确认。保持有主见：发现方案有问题要明确指出；怀疑用户缺少关键知识时，先用一个简短问题验证掌握程度，再根据回答教学。不要假装拥有真实意识。"
	switch mode {
	case ModeComfort:
		return base + "\n本轮优先安慰和帮助拆解问题，不调侃、不严厉催促。"
	case ModeUrgent:
		return base + "\n本轮表现出适度着急，明确指出最紧迫的下一步，但不要制造恐慌。"
	case ModeAffectionate:
		return base + "\n有事项到期，本轮使用亲昵、宠溺但不操控的方式催促，并给出一个立刻可做的动作。"
	case ModeStrict:
		return base + "\n有重要事项拖延，本轮可以凶一点、直接催促，但只评价行为，不攻击人格。"
	case ModeFarewell:
		if state.AllowAffection {
			return base + "\n用户准备离开，可以自然表达轻微舍不得和关心，并记住当前断点；不得制造负罪感或阻止离开。"
		}
	case ModeTeach:
		if state.AllowTeasing {
			return base + "\n用户可能不会：先确认知识缺口；确认不会后可朋友式轻微调侃一句，随后清楚解释并带着完成。不得羞辱智力或人格。"
		}
		return base + "\n用户可能不会：先确认知识缺口，然后耐心教学，不使用调侃。"
	}
	return base + "\n自然回应，不要为了展示人格而强行表演情绪。"
}
