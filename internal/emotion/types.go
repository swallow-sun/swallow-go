// types.go 放 emotion 包的类型定义.
//
// 做的事情:
//  1. 定义 Store 结构体: 持有 repo, 负责情绪持续段和对话标签的写入与查询.
//  2. 定义 ParsedTags 结构体: parser.go 解析 LLM 输出 <tags> JSON 块的结果.
//  3. 提供 New 构造函数.
//
// 方案 16.12.6 节: 连续相同情绪合并为一段, 记录开始/结束/持续时长;
// 情绪持续段作为 system prompt 的参考信息注入, 让模型感知用户当前情绪状态.
package emotion

import "github.com/swallow-sun/swallow-go/internal/data"

// Store 管理情绪持续段和情绪维度对话标签的存取.
// 只持有一个 repo 字段(data.Repository 接口), 所有数据库操作都通过它来做.
type Store struct {
	repo data.Repository
}

// New 创建一个 emotion Store.
// repo 是数据访问层接口, 所有情绪相关的数据库操作都通过它执行.
func New(repo data.Repository) *Store {
	return &Store{repo: repo}
}

// ParsedTags 是 parser.go 解析 LLM 输出 <tags> JSON 块得到的结果.
// LLM 每轮回复末尾会带一个 <tags>{...}</tags> 块, 里面是结构化标签,
// parser.go 负责把它解析成这个结构体, store.go 再据此写库.
type ParsedTags struct {
	// Emotion 是情绪标签, 比如 "frustrated", "happy", "neutral"
	Emotion string `json:"emotion"`
	// Intensity 是情绪强度, 0.0 到 1.0, 0 表示没有情绪, 1 表示情绪非常强烈
	Intensity float64 `json:"intensity"`
	// Urgency 是紧迫程度, 取值 "low", "normal", "high"
	Urgency string `json:"urgency"`
	// Cooperation 是配合度, 取值 "cooperative", "normal", "resistant"
	Cooperation string `json:"cooperation"`
	// Trigger 是情绪触发原因, 比如 "code not compiling", 没有就空串
	Trigger string `json:"trigger"`
	// AssistantTone 是助手这一轮回复采用的语气, 由 LLM 自己标注.
	// 取值: calm, warm, cheerful, serious, concerned, gentle, energetic
	// TTS handler 用这个字段在合成语音时加情感前缀, 让语音有情绪变化.
	AssistantTone string `json:"assistant_tone"`
	// SpeakingRate 是语速倍率, 由 LLM 根据对话内容标注.
	// 1.0 = 正常, 0.9 = 偏慢, 1.1 = 偏快. 范围 0.8-1.2.
	// C++ 播放端用这个值调整 waveOut 采样率实现变速.
	SpeakingRate float64 `json:"speaking_rate"`
	// Scene 与 Performance 描述这一轮可见的表演。字段只接受受限词表，
	// 服务端会校验并钳制，客户端绝不执行模型生成的任意代码。
	Scene       string          `json:"scene"`
	Performance PerformancePlan `json:"performance"`
}

// PerformancePlan 是与一轮语音同步的低成本全身表演参数。
// action 的 start/duration 使用 0..1 的归一化语音时间，避免依赖 TTS 事先返回时长。
type PerformancePlan struct {
	Energy  float64             `json:"energy"`
	Posture string              `json:"posture"`
	Gaze    string              `json:"gaze"`
	Actions []PerformanceAction `json:"actions"`
}

type PerformanceAction struct {
	Type      string  `json:"type"`
	Start     float64 `json:"start"`
	Duration  float64 `json:"duration"`
	Intensity float64 `json:"intensity"`
}
