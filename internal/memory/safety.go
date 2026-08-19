// safety.go 放长期记忆写入前的敏感信息安全检测.
//
// 做的事情:
//  1. 检测 API Key、密码、验证码、私钥、银行卡号、身份证号和带凭据 URL.
//  2. 只返回是否允许和敏感类别, 不返回命中的敏感原文.
//  3. 为自动候选和手动候选提供同一套写库前安全边界.
package memory

import (
	"regexp"
	"strings"
	"unicode"
)

// resolveSafetyFilterEnabled 解析构造函数的可选安全开关.
// 没有传值时默认开启,多余值忽略,保持旧调用方安全兼容.
func resolveSafetyFilterEnabled(values []bool) bool {
	if len(values) == 0 {
		return true
	}
	return values[0]
}

// Error 返回稳定的领域错误说明, 让 *SafetyError 满足 Go 标准 error 接口.
// 错误文本不包含用户原文, 可以安全地被上层包装和记录.
func (e *SafetyError) Error() string {
	return "memory candidate contains prohibited sensitive information: " + e.Kind
}

// CheckMemorySafety 检查文本是否允许进入长期记忆候选表.
// 检测顺序从高确定性特征开始, 命中第一条规则后立即拒绝.
func CheckMemorySafety(content string) SafetyResult {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return SafetyResult{Allowed: true}
	}

	checks := []safetyCheck{
		{pattern: sensitivePrivateKeyPattern, kind: SensitiveKindPrivateKey},
		{pattern: sensitiveCredentialURLPattern, kind: SensitiveKindCredentialURL},
		{pattern: sensitiveAPIKeyPattern, kind: SensitiveKindAPIKey},
		{pattern: sensitivePasswordPattern, kind: SensitiveKindPassword},
		{pattern: sensitiveVerificationCodePattern, kind: SensitiveKindVerificationCode},
		{pattern: sensitiveNationalIDPattern, kind: SensitiveKindNationalID},
	}
	for _, check := range checks {
		matched, err := regexp.MatchString(check.pattern, trimmed)
		if err == nil && matched {
			return SafetyResult{Allowed: false, Kind: check.kind}
		}
	}

	if containsBankCardNumber(trimmed) {
		return SafetyResult{Allowed: false, Kind: SensitiveKindBankCard}
	}
	return SafetyResult{Allowed: true}
}

// CheckCandidateSafety 检查手动候选中所有会持久化的业务文本字段.
// 字段拼接只存在于当前内存中, 不会写入日志、事件或错误响应.
func CheckCandidateSafety(spec CandidateSpec) SafetyResult {
	return CheckMemorySafety(strings.Join([]string{
		spec.Content,
		spec.MemoryType,
		spec.Reason,
		spec.UsageHint,
	}, "\n"))
}

// containsBankCardNumber 查找可能的银行卡数字段, 并使用 Luhn 校验减少普通长数字误报.
// 只接受 13 到 19 位数字, 中间允许空格或短横线.
func containsBankCardNumber(content string) bool {
	var candidate strings.Builder
	flush := func() bool {
		digits := candidate.String()
		candidate.Reset()
		return len(digits) >= 13 && len(digits) <= 19 && passesLuhn(digits)
	}

	for _, char := range content {
		switch {
		case unicode.IsDigit(char) && char <= unicode.MaxASCII:
			candidate.WriteRune(char)
		case (char == ' ' || char == '-') && candidate.Len() > 0:
			continue
		default:
			if flush() {
				return true
			}
		}
	}
	return flush()
}

// passesLuhn 校验一段纯数字是否符合银行卡常用的 Luhn 校验规则.
func passesLuhn(digits string) bool {
	sum := 0
	doubleDigit := false
	for index := len(digits) - 1; index >= 0; index-- {
		value := int(digits[index] - '0')
		if doubleDigit {
			value *= 2
			if value > 9 {
				value -= 9
			}
		}
		sum += value
		doubleDigit = !doubleDigit
	}
	return sum > 0 && sum%10 == 0
}
