// parser.go 放 LLM 输出的 <tags> JSON 块解析器.
//
// 做的事情:
//  1. ParseTags: 从 LLM 回复文本里找到 <tags>...</tags> 标记, 解析里面的 JSON.
//  2. 解析成功返回 (ParsedTags, true); 找不到标记或 JSON 解析失败返回 (零值, false).
//
// LLM 每轮回复的格式是这样的:
//
//	这是助手的正常回复正文...
//	<tags>{"emotion":"frustrated","intensity":0.6,"urgency":"normal","cooperation":"cooperative","trigger":"code not compiling"}</tags>
//
// parser 只关心 <tags> 块, 不碰正文. 正文由 agent 层负责截取和展示.
package emotion

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// ParseTags 从 LLM 回复文本里解析 <tags>...</tags> JSON 块.
//
// 扫描流程:
//  1. 用 strings.Index 找到 <tags> 标记的起始位置, 找不到说明没有标签块, 返回 false.
//  2. 从 <tags> 之后找 </tags> 结束标记, 找不到也返回 false.
//  3. 取两个标记之间的文本, 用 encoding/json 解析成 ParsedTags.
//  4. JSON 解析失败(格式不对)也返回 false, 调用方按没有标签处理.
//
// 参数:
//   - content: LLM 的完整回复文本, 包含正文和 <tags> 块
//
// 返回值:
//   - ParsedTags: 解析出的标签, 解析失败时是零值
//   - bool: true 表示解析成功, false 表示没有标签块或解析失败
func ParseTags(content string) (ParsedTags, bool) {
	tagsContent, _, _, ok := extractTagsBlock(content)
	if !ok {
		return ParsedTags{}, false
	}

	// 正常情况下 tagsContent 就是 JSON。模型偶尔会把回复正文也包进标签，
	// 因此从右向左寻找最后一个能解析且包含标签字段的 JSON 对象。
	tags, _, ok := parseTagsContent(tagsContent)
	if !ok {
		logger.Warn("ParseTags: 标签块中没有有效的元数据 JSON")
		return ParsedTags{}, false
	}

	tags = CanonicalizeTags(tags)
	logger.Info("ParseTags: tags parsed",
		zap.String("emotion", tags.Emotion),
		zap.Float64("intensity", tags.Intensity),
		zap.String("urgency", tags.Urgency),
		zap.String("cooperation", tags.Cooperation),
		zap.String("trigger", tags.Trigger),
		zap.String("assistant_tone", tags.AssistantTone),
		zap.Float64("speaking_rate", tags.SpeakingRate),
		zap.String("scene", tags.Scene),
		zap.Int("performance_actions", len(tags.Performance.Actions)),
	)

	return tags, true
}

// extractTagsBlock 返回标签内部文本及整个标签块在原文中的字节范围。
func extractTagsBlock(content string) (tagsContent string, blockStart, blockEnd int, ok bool) {
	blockStart = strings.Index(content, "<tags>")
	if blockStart < 0 {
		return "", 0, 0, false
	}
	contentStart := blockStart + len("<tags>")
	endRel := strings.Index(content[contentStart:], "</tags>")
	if endRel < 0 {
		return "", 0, 0, false
	}
	contentEnd := contentStart + endRel
	return content[contentStart:contentEnd], blockStart, contentEnd + len("</tags>"), true
}

// parseTagsContent 从标签内容末尾寻找结构化 JSON，并返回它的起始位置。
// 起始位置之前的内容可能是模型误包进标签的可朗读正文。
func parseTagsContent(content string) (ParsedTags, int, bool) {
	searchEnd := len(content)
	for searchEnd > 0 {
		jsonStart := strings.LastIndex(content[:searchEnd], "{")
		if jsonStart < 0 {
			break
		}
		candidate := strings.TrimSpace(content[jsonStart:])

		// 先检查是否至少包含一个约定字段，避免把正文里的普通 JSON 当标签。
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(candidate), &fields); err == nil {
			_, hasEmotion := fields["emotion"]
			_, hasTone := fields["assistant_tone"]
			_, hasRate := fields["speaking_rate"]
			_, hasPerformance := fields["performance"]
			if hasEmotion || hasTone || hasRate || hasPerformance {
				var tags ParsedTags
				if err := json.Unmarshal([]byte(candidate), &tags); err == nil {
					return tags, jsonStart, true
				}
			}
		}
		searchEnd = jsonStart
	}
	return ParsedTags{}, 0, false
}

var allowedScenes = map[string]bool{
	"idle": true, "conversation": true, "comfort": true, "celebration": true,
	"explanation": true, "apology": true, "warning": true, "farewell": true,
}
var allowedPostures = map[string]bool{
	"neutral": true, "open": true, "lean_in": true, "reserved": true,
	"confident": true, "relaxed": true,
}
var allowedGazes = map[string]bool{
	"user": true, "away": true, "down": true, "up": true, "side": true,
}
var allowedActions = map[string]bool{
	"nod": true, "shake_head": true, "wave": true, "bow": true,
	"hand_to_chest": true, "open_hands": true, "point": true,
	"shrug": true, "cheer": true, "step_in": true, "step_back": true,
	"sway": true, "think": true, "weight_shift": true, "look_around": true,
	"laugh": true, "dance": true, "surprise": true, "listen": true,
	"explain": true,
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// CanonicalizeTags 把模型输出收敛到客户端支持的有限动作协议。
func CanonicalizeTags(tags ParsedTags) ParsedTags {
	tags.Intensity = clamp01(tags.Intensity)
	if tags.SpeakingRate == 0 {
		tags.SpeakingRate = 1
	}
	if tags.SpeakingRate < 0.8 {
		tags.SpeakingRate = 0.8
	}
	if tags.SpeakingRate > 1.2 {
		tags.SpeakingRate = 1.2
	}
	if !allowedScenes[tags.Scene] {
		tags.Scene = "conversation"
	}
	if !allowedPostures[tags.Performance.Posture] {
		tags.Performance.Posture = "neutral"
	}
	if !allowedGazes[tags.Performance.Gaze] {
		tags.Performance.Gaze = "user"
	}
	tags.Performance.Energy = clamp01(tags.Performance.Energy)
	valid := make([]PerformanceAction, 0, len(tags.Performance.Actions))
	for _, action := range tags.Performance.Actions {
		if len(valid) >= 6 || !allowedActions[action.Type] {
			continue
		}
		action.Start = clamp01(action.Start)
		action.Duration = clamp01(action.Duration)
		if action.Duration < 0.08 {
			action.Duration = 0.08
		}
		if action.Start+action.Duration > 1 {
			action.Duration = 1 - action.Start
		}
		if action.Duration <= 0 {
			continue
		}
		action.Intensity = clamp01(action.Intensity)
		valid = append(valid, action)
	}
	tags.Performance.Actions = valid
	return tags
}
