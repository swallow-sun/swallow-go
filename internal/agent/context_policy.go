package agent

import (
	"strings"
	"unicode"
)

// standaloneSocialTurns 是不依赖旧话题也能完整理解的简短社交表达。
// 这类输入如果仍携带整段历史，模型容易把寒暄误判成“继续上一个任务”。
var standaloneSocialTurns = map[string]struct{}{
	"你好": {}, "你好啊": {}, "您好": {}, "嗨": {}, "嗨你好": {}, "哈喽": {}, "嘿": {},
	"早": {}, "早上好": {}, "上午好": {}, "中午好": {}, "下午好": {}, "晚上好": {},
	"晚安": {}, "在吗": {}, "喂": {}, "hello": {}, "hi": {}, "hey": {},
}

// isStandaloneSocialTurn 判断当前输入是否应临时脱离旧话题。
// 只匹配完整短语；“你好，继续刚才的代码”不会被误判为独立寒暄。
func isStandaloneSocialTurn(input string) bool {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, strings.TrimSpace(input))
	_, ok := standaloneSocialTurns[normalized]
	return ok
}
