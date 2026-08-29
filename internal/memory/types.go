// types.go 放 memory 包的类型定义.
//
// 做的事情:
//  1. 定义 Store 和长期记忆各业务组件持有的依赖.
//  2. 定义候选、查询结果和安全检测结果等领域类型.
//  3. 定义敏感信息类别和检测规则常量.
package memory

import "github.com/swallow-sun/swallow-go/internal/data"

const (
	// DefaultSearchLimit 是长期记忆查询没有指定有效上限时使用的默认条数.
	DefaultSearchLimit = 10

	// SensitiveKindAPIKey 表示内容中疑似包含 API Key 或访问令牌.
	SensitiveKindAPIKey = "api_key"
	// SensitiveKindPassword 表示内容中疑似包含密码或登录口令.
	SensitiveKindPassword = "password"
	// SensitiveKindVerificationCode 表示内容中疑似包含短信或登录验证码.
	SensitiveKindVerificationCode = "verification_code"
	// SensitiveKindPrivateKey 表示内容中包含私钥区块.
	SensitiveKindPrivateKey = "private_key"
	// SensitiveKindBankCard 表示内容中疑似包含通过校验的银行卡号.
	SensitiveKindBankCard = "bank_card"
	// SensitiveKindNationalID 表示内容中疑似包含中国大陆身份证号.
	SensitiveKindNationalID = "national_id"
	// SensitiveKindCredentialURL 表示 URL 中包含用户名和密码等凭据.
	SensitiveKindCredentialURL = "credential_url"

	// sensitivePrivateKeyPattern 匹配 PEM 私钥区块的开始标记.
	sensitivePrivateKeyPattern = `(?i)-----BEGIN[ A-Z0-9_-]*PRIVATE KEY-----`
	// sensitiveAPIKeyPattern 匹配常见 API Key、GitHub Token、Bearer Token 和 JWT.
	sensitiveAPIKeyPattern = `(?i)(sk-[a-z0-9_-]{16,}|gh[pousr]_[a-z0-9]{20,}|bearer\s+[a-z0-9._~+/-]{16,}|eyJ[a-z0-9_-]{8,}\.[a-z0-9_-]{8,}\.[a-z0-9_-]{8,})`
	// sensitivePasswordPattern 匹配明确写出密码、口令或 password 值的文本.
	sensitivePasswordPattern = `(?i)(密码|口令|password|passwd|pwd)\s*(是|为|[:=])\s*\S{4,}`
	// sensitiveVerificationCodePattern 匹配带验证码语义的 4 到 8 位数字.
	sensitiveVerificationCodePattern = `(?i)(验证码|校验码|动态码|otp|verification\s*code)\D{0,8}[0-9]{4,8}`
	// sensitiveNationalIDPattern 匹配中国大陆 18 位身份证号码的基本格式.
	sensitiveNationalIDPattern = `(?i)(^|[^0-9])[1-9][0-9]{5}(18|19|20)[0-9]{2}(0[1-9]|1[0-2])(0[1-9]|[12][0-9]|3[01])[0-9]{3}[0-9x]([^0-9x]|$)`
	// sensitiveCredentialURLPattern 匹配 scheme://user:password@host 形式的带凭据 URL.
	sensitiveCredentialURLPattern = `(?i)[a-z][a-z0-9+.-]*://[^\s/:@]+:[^\s/@]+@[^\s]+`

	// CandidateDecisionConfirm 表示用户确认保存候选.
	CandidateDecisionConfirm = "confirm"
	// CandidateDecisionReject 表示用户明确拒绝候选.
	CandidateDecisionReject = "reject"
	// CandidateDecisionDefer 表示暂不处理，稍后再次展示.
	CandidateDecisionDefer = "defer"

	// CandidateDecisionErrorNotFound 对外统一隐藏候选不存在和跨用户访问.
	CandidateDecisionErrorNotFound = "memory_candidate_not_found"
	// CandidateDecisionErrorConflict 表示候选已被另一端处理或客户端 revision 过期.
	CandidateDecisionErrorConflict = "memory_candidate_conflict"
	// CandidateDecisionErrorInvalid 表示决策参数不合法.
	CandidateDecisionErrorInvalid = "invalid_memory_decision"
)

// Store 管理当前会话对话历史的存取.
// 只持有一个 repo 字段(data.Repository 接口),所有数据库操作都通过它来做.
type Store struct {
	repo                data.Repository
	safetyFilterEnabled bool // 是否在长期记忆写入前执行敏感信息过滤
}

// CandidateSpec 是创建长期记忆候选时使用的领域参数。
type CandidateSpec struct {
	UserID     int64
	SessionID  string
	TraceID    string
	Content    string
	MemoryType string
	Source     string
	Reason     string
	UsageHint  string
}

// SearchResult 是长期记忆检索结果和数量统计。
type SearchResult struct {
	Rows     []data.Memory
	Limit    int
	Returned int
}

// SafetyResult 是一轮记忆安全检测的结果.
// Allowed 为 false 时只允许记录 Kind, 不能把命中的敏感原文写入日志或事件.
type SafetyResult struct {
	Allowed bool
	Kind    string
}

// safetyCheck 是一条敏感信息检测规则.
// pattern 是不包含用户数据的固定正则, kind 是命中后允许记录的敏感类别.
type safetyCheck struct {
	pattern string
	kind    string
}

// SafetyError 表示记忆候选因为包含禁止保存的敏感信息而被拒绝.
// Kind 只描述敏感信息类别, 不保存或返回命中的原文.
type SafetyError struct {
	Kind string
}

// CandidateDecisionError 是设备候选审核的稳定领域错误.
// Handler 只依据 Code 映射 HTTP 状态，不需要解析仓库错误字符串.
type CandidateDecisionError struct {
	Code string
}

func (e *CandidateDecisionError) Error() string { return e.Code }

// CandidateDecisionResult 是一次候选审核的最终状态.
// Memory 仅在 confirm 时存在；Replayed 表示请求命中了此前已完成的同类决策.
type CandidateDecisionResult struct {
	Candidate data.MemoryCandidate
	Memory    *data.Memory
	Decision  string
	Replayed  bool
}

type CandidateService struct {
	repo                data.Repository // 候选和正式记忆数据仓库
	policy              *Policy         // 确定性候选生成规则
	safetyFilterEnabled bool            // 是否执行候选写入和确认安全过滤
}

type Service struct {
	repo                data.Repository /* 正式记忆数据仓库 */
	safetyFilterEnabled bool            /* 是否执行正式记忆编辑安全过滤 */
}
type Retriever struct {
	repo data.Repository /* 记忆检索数据仓库 */
}
type Policy struct{}
