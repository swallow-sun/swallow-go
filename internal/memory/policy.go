// policy.go 实现保守的长期记忆候选规则。
//
// 规则层只处理含义明确、可以从单句直接证明的表达。疑问、长段技术问题和
// 依赖上下文推断的事实全部留给后续异步语义提取，避免误写长期记忆。
package memory

import (
	"strings"
	"unicode/utf8"

	"github.com/swallow-sun/swallow-go/internal/data"
)

const maxRuleCandidateRunes = 120

// NewPolicy 创建无状态的确定性候选规则引擎。
func NewPolicy() *Policy { return &Policy{} }

// Generate 从用户原话中生成零到多条候选。
// 句号和分号可以分隔独立陈述；问号保留在子句里，用于拒绝疑问句。
func (p *Policy) Generate(userID int64, sessionID, traceID, userMessage string) []CandidateSpec {
	message := strings.TrimSpace(userMessage)
	if message == "" {
		return []CandidateSpec{}
	}

	result := make([]CandidateSpec, 0, 2)
	seen := make(map[string]struct{})
	for _, clause := range splitMemoryClauses(message) {
		matchers := []func(int64, string, string, string) (CandidateSpec, bool){
			matchPreference,
			matchInstruction,
			matchPersona,
			matchFact,
		}
		for _, matcher := range matchers {
			spec, ok := matcher(userID, sessionID, traceID, clause)
			if !ok {
				continue
			}
			key := spec.MemoryType + "\x00" + normalizeMemoryText(spec.Content)
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, spec)
		}
	}
	return result
}

// splitMemoryClauses 只在陈述性分隔符处分句，不能删除问号，否则疑问句会被
// 误当作事实。例如“那我叫什么名字，你知道？”必须完整进入 isQuestion。
func splitMemoryClauses(message string) []string {
	parts := strings.FieldsFunc(message, func(r rune) bool {
		return r == '。' || r == '；' || r == ';' || r == '\n' || r == '\r'
	})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func isQuestion(statement string) bool {
	trimmed := strings.TrimSpace(statement)
	if strings.ContainsAny(trimmed, "?？") {
		return true
	}
	for _, ending := range []string{"吗", "么", "呢", "是不是", "对不对"} {
		if strings.HasSuffix(trimmed, ending) {
			return true
		}
	}
	return false
}

func validRuleStatement(statement string) bool {
	return statement != "" && !isQuestion(statement) &&
		!strings.ContainsAny(statement, "\r\n") &&
		utf8.RuneCountInString(statement) <= maxRuleCandidateRunes
}

func cleanRuleValue(value string) string {
	return strings.Trim(strings.TrimSpace(value), "：:，,。.!！ \t")
}

// extractPrefixValue 只接受句首模式。旧实现先 Contains 再 TrimPrefix，导致关键词
// 出现在长句中间时整段原话被错误保存。
func extractPrefixValue(statement string, prefixes []string) (string, bool) {
	for _, prefix := range prefixes {
		if strings.HasPrefix(statement, prefix) {
			value := cleanRuleValue(strings.TrimPrefix(statement, prefix))
			if value != "" && utf8.RuneCountInString(value) <= maxRuleCandidateRunes {
				return value, true
			}
		}
	}
	return "", false
}

func candidateSpec(
	userID int64,
	sessionID, traceID, content, memoryType, reason, usageHint string,
) CandidateSpec {
	return CandidateSpec{
		UserID:     userID,
		SessionID:  sessionID,
		TraceID:    traceID,
		Content:    content,
		MemoryType: memoryType,
		Source:     data.MemoryCandidateSourceRule,
		Reason:     reason,
		UsageHint:  usageHint,
	}
}

func matchPreference(userID int64, sessionID, traceID, statement string) (CandidateSpec, bool) {
	statement = strings.TrimSpace(statement)
	if !validRuleStatement(statement) {
		return CandidateSpec{}, false
	}
	if _, ok := extractPrefixValue(statement, preferencePrefixesCN); ok {
		return candidateSpec(userID, sessionID, traceID, statement,
			data.MemoryTypePreference, "用户明确表达了偏好", "用于调整后续回复或交互方式"), true
	}
	lower := strings.ToLower(statement)
	if _, ok := extractPrefixValue(lower, preferencePrefixesEN); ok {
		return candidateSpec(userID, sessionID, traceID, statement,
			data.MemoryTypePreference, "User explicitly expressed a preference", "Used to tailor future responses"), true
	}
	return CandidateSpec{}, false
}

func matchInstruction(userID int64, sessionID, traceID, statement string) (CandidateSpec, bool) {
	statement = strings.TrimSpace(statement)
	if !validRuleStatement(statement) {
		return CandidateSpec{}, false
	}
	if value, ok := extractPrefixValue(statement, instructionPrefixesCN); ok {
		return candidateSpec(userID, sessionID, traceID, value,
			data.MemoryTypeInstruction, "用户明确要求以后这样做", "在相关的后续对话中应用"), true
	}
	lower := strings.ToLower(statement)
	if value, ok := extractPrefixValue(lower, instructionPrefixesEN); ok {
		return candidateSpec(userID, sessionID, traceID, value,
			data.MemoryTypeInstruction, "User explicitly requested a future behavior", "Applied to relevant future conversations"), true
	}
	return CandidateSpec{}, false
}

func matchPersona(userID int64, sessionID, traceID, statement string) (CandidateSpec, bool) {
	statement = strings.TrimSpace(statement)
	if !validRuleStatement(statement) {
		return CandidateSpec{}, false
	}
	value, ok := extractPrefixValue(statement, personaPrefixesCN)
	if !ok {
		return CandidateSpec{}, false
	}
	identity := false
	for _, keyword := range personaKeywordsCN {
		if strings.Contains(value, keyword) {
			identity = true
			break
		}
	}
	if !identity {
		return CandidateSpec{}, false
	}
	return candidateSpec(userID, sessionID, traceID, "用户是"+value,
		data.MemoryTypePersona, "用户明确描述了自己的身份", "用于调整解释深度和背景信息"), true
}

func matchFact(userID int64, sessionID, traceID, statement string) (CandidateSpec, bool) {
	statement = strings.TrimSpace(statement)
	if !validRuleStatement(statement) {
		return CandidateSpec{}, false
	}
	if value, ok := extractPrefixValue(statement, preferredNamePrefixesCN); ok {
		return candidateSpec(userID, sessionID, traceID, "用户希望被称为"+value,
			data.MemoryTypeFact, "用户明确提供了称呼", "用于后续自然地称呼用户"), true
	}
	lower := strings.ToLower(statement)
	if value, ok := extractPrefixValue(lower, preferredNamePrefixesEN); ok {
		return candidateSpec(userID, sessionID, traceID, "User prefers to be called "+value,
			data.MemoryTypeFact, "User explicitly provided a preferred name", "Used to address the user naturally"), true
	}
	return CandidateSpec{}, false
}

var (
	preferencePrefixesCN = []string{"我更喜欢", "我不喜欢", "我喜欢", "我偏好", "我讨厌"}
	preferencePrefixesEN = []string{"i don't like ", "i'd rather ", "i prefer ", "i like ", "i hate "}

	instructionPrefixesCN = []string{"请记住", "记住", "以后请", "以后要", "以后", "每次请", "每次都", "总是请"}
	instructionPrefixesEN = []string{"please remember to ", "remember to ", "from now on ", "every time ", "always "}

	preferredNamePrefixesCN = []string{"我的名字叫", "我的名字是", "你可以叫我", "请叫我", "我叫"}
	preferredNamePrefixesEN = []string{"please call me ", "you can call me ", "my name is "}

	personaPrefixesCN = []string{"我是一名", "我是一个", "我是个", "我是"}
	personaKeywordsCN = []string{"工程师", "设计师", "老师", "学生", "程序员", "产品经理", "开发者"}
)
