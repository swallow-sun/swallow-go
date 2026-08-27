// detector.go 放提醒检测器: 从用户输入里扫描提醒意图.
//
// 做的事情:
//  1. DetectReminders: 用确定性正则规则扫描用户输入, 提取提醒线索.
//  2. 返回 ReminderHint 切片, 每个元素包含内容和时间表达.
//  3. 纯函数, 不访问数据库, 只做文本匹配.
//
// 设计原则:
//   - 第一版只用 2-3 个简单正则, 不做复杂 NLP.
//   - 漏检比误检好: 宁可不提取, 也不能乱提取.
//   - 时间解析不在这一层做, 这一层只提取原始文本.
//
// 方案 16.12.6 节: 从对话提取 + 用户确认.
package reminder

import (
	"regexp"
	"strings"
)

// reminderPatterns 是提醒检测的正则规则列表.
// 每条规则包含一个编译好的正则和一个提取函数, 用来从匹配结果里拿出内容和时间表达.
// 用 var 声明在包级别, 避免每次调用都重新编译正则.
var reminderPatterns = []reminderPattern{
	// 规则 1: "提醒我..." 或 "帮我记住..." 后面跟时间表达和内容.
	// 匹配示例:
	//   "提醒我明天下午3点开会"     → 内容="开会",     When="明天下午3点"
	//   "帮我记住后天要交报告"       → 内容="要交报告",  When="后天"
	//   "提醒我下周一带伞"          → 内容="带伞",      When="下周一"
	// 正则解释:
	//   - (?:提醒我|帮我记住)      非捕获分组, 匹配 "提醒我" 或 "帮我记住"
	//   - (时间表达)               捕获分组 1, 匹配时间部分
	//   - (.+)                    捕获分组 2, 匹配剩余内容
	{
		pattern: regexp.MustCompile(`(?:提醒我|帮我记住)([^\s，。,]+)(.+)`),
		extract: func(m []string) (content, when string, ok bool) {
			// m[0] 是整个匹配, m[1] 是第一个捕获组(时间), m[2] 是第二个捕获组(内容)
			if len(m) < 3 {
				return "", "", false
			}
			when = strings.TrimSpace(m[1])
			content = strings.TrimSpace(m[2])
			if content == "" || when == "" {
				return "", "", false
			}
			return content, when, true
		},
	},
	// 规则 2: "...的时候提醒我..." (比如 "下班的时候提醒我买牛奶").
	// 匹配示例:
	//   "下班的时候提醒我买牛奶" → 内容="买牛奶", When="下班的时候"
	// 正则解释:
	//   - (.+的时候)             捕获分组 1, 匹配 "XXX的时候" 形式的时间条件
	//   - (?:提醒我)?            非捕获分组, "提醒我" 可有可无
	//   - (.+)                   捕获分组 2, 匹配要提醒的内容
	{
		pattern: regexp.MustCompile(`(.+的时候)(?:提醒我)?(.+)`),
		extract: func(m []string) (content, when string, ok bool) {
			if len(m) < 3 {
				return "", "", false
			}
			when = strings.TrimSpace(m[1])
			content = strings.TrimSpace(m[2])
			if content == "" || when == "" {
				return "", "", false
			}
			return content, when, true
		},
	},
	// 规则 3: "X分钟后/Y分钟后/明天/后天/下周一" 后面跟动作动词.
	// 匹配示例:
	//   "10分钟后提醒我喝水" → 内容="喝水", When="10分钟后"
	//   "明天记得交报告"    → 内容="交报告", When="明天"
	// 正则解释:
	//   - (时间表达)         捕获分组 1, 匹配常见时间词
	//   - (?:提醒我|记得)?   非捕获分组, 引导词可有可无
	//   - (.+)              捕获分组 2, 匹配动作内容
	{
		pattern: regexp.MustCompile(`(\d+分钟后|\d+小时后|明天|后天|下周一|下周二|下周三|下周四|下周五|下周六|下周日)(?:提醒我|记得)?(.+)`),
		extract: func(m []string) (content, when string, ok bool) {
			if len(m) < 3 {
				return "", "", false
			}
			when = strings.TrimSpace(m[1])
			content = strings.TrimSpace(m[2])
			if content == "" || when == "" {
				return "", "", false
			}
			return content, when, true
		},
	},
}

// reminderPattern 是一条提醒检测规则.
// pattern 是编译好的正则表达式, extract 是从匹配结果里提取内容和时间表达的函数.
type reminderPattern struct {
	pattern *regexp.Regexp
	extract func(m []string) (content, when string, ok bool)
}

// DetectReminders 用确定性正则规则扫描用户输入, 提取提醒线索.
// 这是一个纯函数, 不访问数据库, 只做文本匹配.
// 返回 ReminderHint 切片; 没检测到就返回空切片(不是 nil).
//
// 参数:
//   - content: 用户输入的原始文本
//
// 返回值:
//   - []ReminderHint: 检测到的提醒线索列表, 没有就返回空切片
func DetectReminders(content string) []ReminderHint {
	// 输入为空直接返回空切片, 不做任何匹配
	if strings.TrimSpace(content) == "" {
		return []ReminderHint{}
	}

	// 预分配结果切片, 容量等于规则数量(最多每条规则命中一次)
	hints := make([]ReminderHint, 0, len(reminderPatterns))

	// 遍历所有检测规则, 逐条匹配
	for _, rule := range reminderPatterns {
		// rule.pattern.FindStringSubmatch 在 content 里找第一个匹配
		// 返回的切片: m[0] 是整个匹配, m[1] 是第一个捕获组, 依此类推
		// 没匹配到返回 nil
		m := rule.pattern.FindStringSubmatch(content)
		if m == nil {
			continue
		}

		// 调用规则自带的提取函数, 从匹配结果里拿出内容和时间表达
		reminderContent, when, ok := rule.extract(m)
		if !ok {
			continue
		}

		// 去掉内容和时间里的首尾空白后追加到结果切片
		hints = append(hints, ReminderHint{
			Content: strings.TrimSpace(reminderContent),
			When:    strings.TrimSpace(when),
		})
	}

	// 返回结果, 可能为空切片
	return hints
}
