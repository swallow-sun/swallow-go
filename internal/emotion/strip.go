// strip.go 放从文本中剥离 <tags> 块并提取 assistant_tone 的工具函数.
//
// 做的事情:
//  1. StripTagsAndTone: 从 LLM 完整回复中解析 <tags> 块, 提取 assistant_tone,
//     并返回去掉 <tags> 块的纯净文本.
//  2. 这个函数主要给 TTS handler 用: C++ 发来的 text 是 LLM 完整回复 (含 <tags> 块),
//     TTS 只需要纯净文本 + 语气标签, 不能把 JSON 标签当文字朗读.
package emotion

import "strings"

// StripTagsAndTone 从 LLM 完整回复中提取 assistant_tone 并剥离 <tags> 块.
//
// 参数:
//   - content: LLM 的完整回复文本, 可能包含 <tags>{...}</tags> 块
//
// 返回值:
//   - cleanText: 去掉 <tags> 块后的纯净回复文本
//   - tone: 从 tags 里解析出的 assistant_tone 值 (如 "warm", "cheerful"), 没有就空串
func StripTagsAndTone(content string) (cleanText, tone string) {
	tagsContent, blockStart, blockEnd, ok := extractTagsBlock(content)
	if !ok {
		// 没有完整标签块时保留原文，避免误删正文。
		return content, ""
	}

	// 标签内若混入正文，只删除实际 JSON 元数据并恢复前面的正文。
	recoveredText := tagsContent
	tags, metadataStart, parsed := parseTagsContent(tagsContent)
	if parsed {
		tone = tags.AssistantTone
		recoveredText = tagsContent[:metadataStart]
	}
	recoveredText = strings.TrimSpace(recoveredText)

	cleanText = content[:blockStart]
	cleanText = appendReadableText(cleanText, recoveredText)
	cleanText = appendReadableText(cleanText, content[blockEnd:])

	// 去掉末尾可能残留的空白和换行 (tags 块通常在回复末尾, 前面会有换行)
	cleanText = strings.TrimRight(cleanText, " \t\r\n")

	return cleanText, tone
}

// appendReadableText 避免英文或数字在标签边界处粘连。
func appendReadableText(target, value string) string {
	if value == "" {
		return target
	}
	if target != "" && !strings.ContainsAny(target[len(target)-1:], " \t\r\n") &&
		!strings.ContainsAny(value[:1], " \t\r\n") {
		target += " "
	}
	return target + value
}
