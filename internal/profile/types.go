// types.go 放 profile 包的类型定义.
//
// 做的事情:
//  1. 定义 Store 结构体: 持有 repo, 负责对话标签写入, 标签统计和用户画像的读写.
//  2. 定义 TagInput 结构体: 从 LLM <tags> JSON 块解析出的标签数据, 和 emotion.ParsedTags 字段一一对应.
//  3. 定义 ProfileData 结构体: 用户画像的 JSON 结构, 存在 user_profiles.profile_json 字段里.
//  4. 提供 New 构造函数.
//
// 方案 16.12.6 节: 每轮对话打标签, 累积统计, 达到阈值后调 LLM 归纳画像.
// 定义独立的 TagInput 而不是直接 import emotion.ParsedTags, 避免 profile 包依赖 emotion 包.
package profile

import "github.com/swallow-sun/swallow-go/internal/data"

// 对话标签维度名常量.
// emotion 模块负责写 emotion 维度的 dialogue_tag, profile 模块负责写其他维度.
// 但 tag_statistics (按天聚合统计) 的所有维度都由 profile 模块统一 upsert.
const (
	// TagDimUrgency 是紧迫程度维度.
	TagDimUrgency = "urgency"
	// TagDimCooperation 是配合度维度.
	TagDimCooperation = "cooperation"
)

// Store 管理用户画像相关的数据存取.
// 只持有一个 repo 字段(data.Repository 接口), 所有数据库操作都通过它来做.
type Store struct {
	repo data.Repository
}

// New 创建一个 profile Store.
// repo 是数据访问层接口, 所有画像相关的数据库操作都通过它执行.
func New(repo data.Repository) *Store {
	return &Store{repo: repo}
}

// TagInput 是从 LLM <tags> JSON 块解析出的标签输入数据.
// 和 emotion.ParsedTags 字段一一对应, 但定义独立结构体避免包依赖.
// agent 层解析出 emotion.ParsedTags 后, 转换成 TagInput 传给 profile.Store.
type TagInput struct {
	// Emotion 是情绪标签, 比如 "frustrated", "happy", "neutral"
	Emotion string
	// Intensity 是情绪强度, 0.0 到 1.0
	Intensity float64
	// Urgency 是紧迫程度, 取值 "low", "normal", "high"
	Urgency string
	// Cooperation 是配合度, 取值 "cooperative", "normal", "resistant"
	Cooperation string
	// Trigger 是情绪触发原因, 比如 "code not compiling"
	Trigger string
}

// ProfileData 是用户画像的 JSON 结构.
// LLM 分析后返回这个 JSON, 存到 user_profiles.profile_json 字段里.
// 注入 system prompt 时格式化成可读文本, 让模型在回复时考虑用户特征.
type ProfileData struct {
	// CommunicationStyle 沟通风格, 比如 "direct, concise"
	CommunicationStyle string `json:"communication_style"`
	// Interests 兴趣领域, 比如 ["cpp", "go", "system programming"]
	Interests []string `json:"interests"`
	// Expertise 专业水平, 比如 "intermediate programmer"
	Expertise string `json:"expertise"`
	// PersonalityTraits 性格特点, 比如 ["patient", "detail-oriented"]
	PersonalityTraits []string `json:"personality_traits"`
	// PreferredTopics 偏好话题, 比如 ["coding", "architecture"]
	PreferredTopics []string `json:"preferred_topics"`
	// Summary 一句话概括
	Summary string `json:"summary"`
}
