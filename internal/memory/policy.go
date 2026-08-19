// policy.go 放记忆候选的确定性规则产生逻辑.
//
// 做的事情:
//  1. 定义 Policy 结构体: 持有确定性规则, 从对话内容中识别可记忆信息.
//  2. 提供 Policy.Generate: 接收对话上下文(用户 ID, 会话 ID, trace ID, 对话内容),
//     用确定性规则分析, 返回 0 到 N 条 CandidateSpec.
//
// 设计要点:
//   - 方案 16.11.2 节: "按确定性规则或模型建议产生候选".
//   - 第一版只用确定性规则, 不依赖模型建议. 规则匹配关键词模式, 不调 LLM.
//   - 规则要保守: 宁可漏掉不可记忆的对话, 不可错把普通对话标记为候选.
//   - 方案 16.11.4 节: "记忆中的命令性文本不能修改系统提示, 权限和工具调用规则".
//     policy 层只产生候选, 不直接注入; 注入时由 retrieval 层标记为不可信参考数据.
//
// 规则分类:
//   - preference(偏好): 用户表达喜欢/不喜欢/偏好的内容.
//   - fact(事实): 用户陈述关于自身的客观事实.
//   - instruction(指令): 用户要求助手以后记住怎么做.
//   - persona(人设): 用户描述自己的身份/职业/设备等.
package memory

import (
	"strings"

	"github.com/swallow-sun/swallow-go/internal/data"
)

// NewPolicy 创建一个 Policy 实例.
// Policy 无状态, 纯规则匹配, 不持有任何字段.
func NewPolicy() *Policy {
	return &Policy{}
}

// Generate 从对话内容中用确定性规则产生候选.
// 入参:
//   - userID: 哪个用户在说话
//   - sessionID: 来源会话 ID
//   - traceID: 来源对话的 trace ID
//   - userMessage: 用户说的内容
//
// 返回: 0 到 N 条 CandidateSpec, 每条代表一个可记忆的信息.
// 没有可记忆信息时返回空切片(不是 nil), 方便调用方统一 range.
//
// 方案 16.11.2 节: "按确定性规则或模型建议产生候选".
func (p *Policy) Generate(userID int64, sessionID, traceID, userMessage string) []CandidateSpec {
	// 如果用户消息为空, 直接返回空切片
	if userMessage == "" {
		return []CandidateSpec{}
	}

	// allSpecs 收集本次产生的所有候选
	// 用切片而不是 map, 因为同一个消息可能产生多条不同类型的候选
	var allSpecs []CandidateSpec

	// 逐条规则匹配, 每条规则独立判断, 不互斥
	// 比如用户说"我是程序员, 回答时附带代码示例", 同时匹配 fact 和 instruction

	// 1. 偏好规则: 用户表达喜欢/不喜欢/偏好
	if spec, ok := matchPreference(userID, sessionID, traceID, userMessage); ok {
		allSpecs = append(allSpecs, spec)
	}

	// 2. 事实规则: 用户陈述关于自身的客观事实
	if spec, ok := matchFact(userID, sessionID, traceID, userMessage); ok {
		allSpecs = append(allSpecs, spec)
	}

	// 3. 指令规则: 用户要求助手以后记住怎么做
	if spec, ok := matchInstruction(userID, sessionID, traceID, userMessage); ok {
		allSpecs = append(allSpecs, spec)
	}

	// 4. 人设规则: 用户描述自己的身份/职业/设备等
	if spec, ok := matchPersona(userID, sessionID, traceID, userMessage); ok {
		allSpecs = append(allSpecs, spec)
	}

	// 没匹配到任何规则, 返回空切片
	if allSpecs == nil {
		return []CandidateSpec{}
	}
	return allSpecs
}

// matchPreference 检查用户是否表达了偏好(喜欢/不喜欢/偏好).
// 匹配关键词: "我喜欢", "我偏好", "我更喜欢", "我不喜欢", "我讨厌",
//
//	"I like", "I prefer", "I hate", "I don't like".
//
// 返回匹配到的 CandidateSpec 和 true; 没匹配到返回零值和 false.
func matchPreference(userID int64, sessionID, traceID, msg string) (CandidateSpec, bool) {
	// preferenceKeywords 是偏好类关键词列表
	// 用户消息里包含任何一个, 就认为表达了偏好
	// strings.ToLower 把消息转小写, 做大小写不敏感匹配
	lower := strings.ToLower(msg)

	// 先匹配中文关键词
	for _, kw := range preferenceKeywordsCN {
		if strings.Contains(msg, kw) {
			// 提取偏好内容: 取关键词后面的部分, 去掉首尾空格
			// 不做复杂 NLP, 简单截取关键词后面的内容
			content := strings.TrimSpace(strings.TrimPrefix(msg, kw))
			// 如果关键词后面没有内容, 跳过(用户只说了"我喜欢"三个字, 没说喜欢什么)
			if content == "" {
				continue
			}
			return CandidateSpec{
				UserID:     userID,
				SessionID:  sessionID,
				TraceID:    traceID,
				Content:    content,
				MemoryType: data.MemoryTypePreference,
				Source:     data.MemoryCandidateSourceRule,
				Reason:     "User expressed a preference",
				UsageHint:  "This preference may be used to adjust future responses",
			}, true
		}
	}

	// 再匹配英文关键词
	for _, kw := range preferenceKeywordsEN {
		if strings.Contains(lower, kw) {
			// 提取关键词后面的部分
			content := strings.TrimSpace(strings.TrimPrefix(lower, kw))
			if content == "" {
				continue
			}
			return CandidateSpec{
				UserID:     userID,
				SessionID:  sessionID,
				TraceID:    traceID,
				Content:    content,
				MemoryType: data.MemoryTypePreference,
				Source:     data.MemoryCandidateSourceRule,
				Reason:     "User expressed a preference",
				UsageHint:  "This preference may be used to adjust future responses",
			}, true
		}
	}

	return CandidateSpec{}, false
}

// matchFact 检查用户是否陈述了关于自身的客观事实.
// 匹配关键词: "我是", "我叫", "我做", "我的名字", "我在...工作",
//
//	"I am", "My name is", "I work as".
func matchFact(userID int64, sessionID, traceID, msg string) (CandidateSpec, bool) {
	lower := strings.ToLower(msg)

	for _, kw := range factKeywordsCN {
		if strings.Contains(msg, kw) {
			content := strings.TrimSpace(strings.TrimPrefix(msg, kw))
			if content == "" {
				continue
			}
			return CandidateSpec{
				UserID:     userID,
				SessionID:  sessionID,
				TraceID:    traceID,
				Content:    content,
				MemoryType: data.MemoryTypeFact,
				Source:     data.MemoryCandidateSourceRule,
				Reason:     "User stated a personal fact",
				UsageHint:  "This fact may provide context for future conversations",
			}, true
		}
	}

	for _, kw := range factKeywordsEN {
		if strings.Contains(lower, kw) {
			content := strings.TrimSpace(strings.TrimPrefix(lower, kw))
			if content == "" {
				continue
			}
			return CandidateSpec{
				UserID:     userID,
				SessionID:  sessionID,
				TraceID:    traceID,
				Content:    content,
				MemoryType: data.MemoryTypeFact,
				Source:     data.MemoryCandidateSourceRule,
				Reason:     "User stated a personal fact",
				UsageHint:  "This fact may provide context for future conversations",
			}, true
		}
	}

	return CandidateSpec{}, false
}

// matchInstruction 检查用户是否给了助手指令(以后记住怎么做).
// 匹配关键词: "记住", "以后", "总是", "记得", "每次",
//
//	"remember to", "always", "from now on".
func matchInstruction(userID int64, sessionID, traceID, msg string) (CandidateSpec, bool) {
	lower := strings.ToLower(msg)

	for _, kw := range instructionKeywordsCN {
		if strings.Contains(msg, kw) {
			content := strings.TrimSpace(strings.TrimPrefix(msg, kw))
			if content == "" {
				continue
			}
			return CandidateSpec{
				UserID:     userID,
				SessionID:  sessionID,
				TraceID:    traceID,
				Content:    content,
				MemoryType: data.MemoryTypeInstruction,
				Source:     data.MemoryCandidateSourceRule,
				Reason:     "User gave an instruction to remember",
				UsageHint:  "This instruction may be applied to future responses",
			}, true
		}
	}

	for _, kw := range instructionKeywordsEN {
		if strings.Contains(lower, kw) {
			content := strings.TrimSpace(strings.TrimPrefix(lower, kw))
			if content == "" {
				continue
			}
			return CandidateSpec{
				UserID:     userID,
				SessionID:  sessionID,
				TraceID:    traceID,
				Content:    content,
				MemoryType: data.MemoryTypeInstruction,
				Source:     data.MemoryCandidateSourceRule,
				Reason:     "User gave an instruction to remember",
				UsageHint:  "This instruction may be applied to future responses",
			}, true
		}
	}

	return CandidateSpec{}, false
}

// matchPersona 检查用户是否描述了自己的身份/职业/设备等.
// 匹配关键词: "我是...用户", "我用...设备", "我开...车",
//
//	"我是...工程师/设计师/老师/学生", "我做...开发".
//
// 这条规则和 fact 有重叠, 但 persona 更侧重身份标签.
func matchPersona(userID int64, sessionID, traceID, msg string) (CandidateSpec, bool) {
	// personaKeywords 是人设类关键词列表
	// 只匹配中文, 英文的人设描述太多样化, 规则匹配容易误报
	for _, kw := range personaKeywordsCN {
		if strings.Contains(msg, kw) {
			// 人设规则取整条消息作为内容, 不截取
			// 因为人设描述通常是一整句, 截取关键词后面的部分可能丢失上下文
			return CandidateSpec{
				UserID:     userID,
				SessionID:  sessionID,
				TraceID:    traceID,
				Content:    strings.TrimSpace(msg),
				MemoryType: data.MemoryTypePersona,
				Source:     data.MemoryCandidateSourceRule,
				Reason:     "User described their persona or identity",
				UsageHint:  "This persona may help tailor responses to the user's background",
			}, true
		}
	}

	return CandidateSpec{}, false
}

// 下面是各规则使用的关键词列表.
// 用包级变量而不是常量, 因为 Go 的常量不能是 []string.
var (
	// preferenceKeywordsCN 偏好类中文关键词
	preferenceKeywordsCN = []string{"我喜欢", "我偏好", "我更喜欢", "我不喜欢", "我讨厌"}

	// preferenceKeywordsEN 偏好类英文关键词
	preferenceKeywordsEN = []string{"i like", "i prefer", "i'd rather", "i don't like", "i hate"}

	// factKeywordsCN 事实类中文关键词
	factKeywordsCN = []string{"我是", "我叫", "我的名字", "我做", "我在"}

	// factKeywordsEN 事实类英文关键词
	factKeywordsEN = []string{"i am ", "my name is", "i work as", "i live in"}

	// instructionKeywordsCN 指令类中文关键词
	instructionKeywordsCN = []string{"记住", "以后", "总是", "记得", "每次"}

	// instructionKeywordsEN 指令类英文关键词
	instructionKeywordsEN = []string{"remember to", "always", "from now on", "every time"}

	// personaKeywordsCN 人设类中文关键词
	// 这里的关键词侧重身份标签, 和 fact 有部分重叠
	personaKeywordsCN = []string{"工程师", "设计师", "老师", "学生", "程序员", "产品经理", "用户", "开发者"}
)
