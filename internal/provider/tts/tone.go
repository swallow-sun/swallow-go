// tone.go 放 TTS 语气 (tone) 相关的辅助函数.
//
// 做的事情:
//  1. ApplyTonePrefix: 根据助手语气标签, 在 TTS 合成文本前加 CosyVoice2 情感指令前缀.
//
// CosyVoice2 支持自然语言情感控制: 在 input 字段的文本前加情感指令,
// 格式: "用温和的语气说 <|endofprompt|>实际文本"
// TTS 模型会按指令的语气来合成语音, 而不是用默认平淡语气.
package tts

import "strings"

// toneToPrompt 把 assistant_tone 标签映射成 CosyVoice2 情感指令文本.
// LLM 输出的 tone 取值和对应的中文情感指令:
//
// 中性/正面:
//
//	calm      → 用平静的语气说
//	warm      → 用温和的语气说
//	cheerful  → 用愉快的语气说
//	serious   → 用严肃的语气说
//	concerned → 用关切的语气说
//	gentle    → 用轻柔的语气说
//	energetic → 用充满活力的语气说
//
// 负面/复杂:
//
//	apologetic → 用抱歉的语气说
//	sad       → 用伤心的语气说
//	frustrated → 用无奈的语气说
//	angry     → 用生气的语气说
//	disappointed → 用失望的语气说
//	coquettish → 用撒娇的语气说
//	wronged   → 用委屈的语气说
//	exasperated → 用又气又急的语气说
//	melancholy → 用忧郁的语气说
//	smug      → 用得意的语气说
var toneToPrompt = map[string]string{
	// 中性/正面
	"calm":      "用平静的语气说",
	"warm":      "温柔、亲近、自然地说",
	"cheerful":  "用愉快的语气说",
	"serious":   "用严肃的语气说",
	"concerned": "用关切的语气说",
	"gentle":    "轻柔、温暖地说，语速稍慢",
	"energetic": "用充满活力的语气说",
	// 负面/复杂
	"apologetic":   "用抱歉的语气说",
	"sad":          "用伤心的语气说",
	"frustrated":   "用无奈的语气说",
	"angry":        "用生气的语气说",
	"disappointed": "用失望的语气说",
	"coquettish":   "用撒娇的语气说",
	"wronged":      "用委屈的语气说",
	"exasperated":  "用又气又急的语气说",
	"melancholy":   "用忧郁的语气说",
	"smug":         "用得意的语气说",
}

// endOfPrompt 是 CosyVoice2 情感指令和实际文本之间的分隔符.
// TTS 模型遇到这个标记后, 后面的内容才是要合成语音的实际文本.
const endOfPrompt = "<|endofprompt|>"

// ApplyTonePrefix 根据助手语气标签, 在文本前加 CosyVoice2 情感指令前缀.
//
// 参数:
//   - text: 要合成语音的纯净文本 (已剥离 <tags> 块)
//   - tone: 助手语气标签 (如 "warm", "cheerful"), 空串表示不加情感前缀
//
// 返回值:
//   - 拼好情感前缀的文本, 直接放入 TTS 请求的 input 字段
//
// 示例:
//
//	ApplyTonePrefix("你好啊", "warm") → "用温和的语气说 <|endofprompt|>你好啊"
//	ApplyTonePrefix("你好啊", "")    → "你好啊" (不加前缀, 用默认语气)
func ApplyTonePrefix(text, tone string) string {
	// tone 为空或不在映射表里, 不加前缀, 用 TTS 默认语气
	prompt, ok := toneToPrompt[strings.ToLower(strings.TrimSpace(tone))]
	if !ok {
		return text
	}

	// 官方示例让指令与正文紧贴 <|endofprompt|>，中间不插入空格。
	// 多余空格会影响韵律解析，也会让不同句子的起音稳定性变差。
	return prompt + endOfPrompt + text
}
