// prompt.go 放画像分析的 LLM prompt 模板.
//
// 做的事情:
//  1. 定义画像分析的 system prompt: 指导 LLM 从统计数据归纳用户画像.
//  2. 定义增量更新指令: 保留已有特征, 只增不改, 除非统计数据有明确反例.
//  3. 提供构造完整分析请求消息的辅助函数.
//
// 方案 16.12.6 节: 达到阈值后用统计数据调 LLM 归纳画像, 保留已有特征只增不改.
package profile

import (
	"fmt"

	"github.com/swallow-sun/swallow-go/internal/provider/llm"
)

// analysisSystemPrompt 是画像分析的 system prompt.
// 指导 LLM 从标签统计数据归纳出用户画像, 输出 JSON 格式.
const analysisSystemPrompt = `你是用户画像分析助手. 你的任务是根据对话标签统计数据, 归纳出用户画像.

输出格式: 只输出一个 JSON 对象, 不要输出其他内容. JSON 字段如下:
{
  "communication_style": "沟通风格, 比如 direct, concise",
  "interests": ["兴趣领域1", "兴趣领域2"],
  "expertise": "专业水平, 比如 beginner/intermediate/expert programmer",
  "personality_traits": ["性格特点1", "性格特点2"],
  "preferred_topics": ["偏好话题1", "偏好话题2"],
  "summary": "一句话概括用户特征"
}

规则:
1. 只根据统计数据归纳, 不要凭空编造.
2. 命中次数高的标签权重更大.
3. 增量更新: 如果已有画像存在, 保留已有特征, 只增不改, 除非统计数据有明确反例.
4. summary 不超过两句话.
5. 所有字段用中文.`

// buildAnalysisMessages 构造发给 LLM 的分析请求消息列表.
//
// 消息结构:
// [system: 分析指令] + [user: 已有画像 (如果有) + 统计数据]
//
// 参数:
//   - existingProfile: 已有的画像 JSON, 空串表示首次分析
//   - statsText: FormatStatsForLLM 格式化后的统计文本
//
// 返回值:
//   - []llm.ChatMessage: 发给 LLM 的消息列表
func buildAnalysisMessages(existingProfile, statsText string) []llm.ChatMessage {
	msgs := []llm.ChatMessage{
		{Role: llm.RoleSystem, Content: analysisSystemPrompt},
	}

	// 构造 user 消息: 已有画像 (如果有) + 统计数据
	var userContent string
	if existingProfile != "" && existingProfile != "{}" {
		userContent = fmt.Sprintf("已有画像:\n%s\n\n%s\n请增量更新, 保留已有特征.", existingProfile, statsText)
	} else {
		userContent = fmt.Sprintf("%s\n请根据以上统计数据归纳用户画像.", statsText)
	}

	msgs = append(msgs, llm.ChatMessage{
		Role:    llm.RoleUser,
		Content: userContent,
	})

	return msgs
}
