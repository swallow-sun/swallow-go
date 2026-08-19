// types.go 放 apperror 包共用的类型和常量定义.
//
// 做的事情:
//  1. 定义所有稳定业务错误码, 供 handler 和客户端统一判断错误类型.
//  2. 定义 Error 统一应用错误结构, 携带 HTTP 状态码、重试标记和 trace ID.
//  3. 约束错误码使用 snake_case, 错误码发布后保持稳定, Message 允许调整.
package apperror

// 稳定业务错误码.
// 这些值会返回给调用端, 调用端根据 Code 判断错误类型, 不依赖可能调整的 Message.
// 新增错误场景时先在这里定义常量, handler 中不直接书写错误码字符串.
const (
	// CodeUnauthorized 表示没有提供主人令牌或者令牌校验失败.
	CodeUnauthorized = "unauthorized"
	// CodeNotFound 表示请求的资源不存在.
	CodeNotFound = "not_found"
	// CodeConflict 表示当前请求与已有资源或状态冲突.
	CodeConflict = "conflict"
	// CodeInternal 表示服务端发生未向客户端暴露细节的内部错误.
	CodeInternal = "internal_error"
	// CodeServiceUnavailable 表示服务暂时不可用或者必要配置缺失.
	CodeServiceUnavailable = "service_unavailable"
	// CodeInvalidRequestBody 表示请求体不是合法 JSON 或者字段格式错误.
	CodeInvalidRequestBody = "invalid_request_body"
	// CodeMissingRequiredFields 表示请求缺少多个必填字段.
	CodeMissingRequiredFields = "missing_required_fields"
	// CodeMissingSessionID 表示请求没有提供会话 ID.
	CodeMissingSessionID = "missing_session_id"
	// CodeMissingUserID 表示请求没有提供用户 ID 或者用户 ID 无法解析.
	CodeMissingUserID = "missing_user_id"
	// CodeMissingDateParams 表示看板请求缺少开始日期或结束日期.
	CodeMissingDateParams = "missing_date_params"
	// CodeInvalidDateRange 表示看板查询日期范围不合法.
	CodeInvalidDateRange = "invalid_date_range"
	// CodeInvalidCandidateID 表示记忆候选 ID 不合法.
	CodeInvalidCandidateID = "invalid_candidate_id"
	// CodeInvalidMemoryID 表示正式记忆 ID 不合法.
	CodeInvalidMemoryID = "invalid_memory_id"
	// CodeMissingContent 表示请求没有提供记忆内容.
	CodeMissingContent = "missing_content"
	// CodeMessageTooLong 表示用户消息超过允许的最大长度.
	CodeMessageTooLong = "message_too_long"
	// CodeClientMessageIDTooLong 表示客户端消息 ID 超过允许的最大长度.
	CodeClientMessageIDTooLong = "client_message_id_too_long"
	// CodeUserNameTooLong 表示用户名超过允许的最大长度.
	CodeUserNameTooLong = "user_name_too_long"
	// CodeSensitiveMemory 表示记忆候选包含禁止保存的敏感信息.
	CodeSensitiveMemory = "sensitive_memory_prohibited"
)

// Error 是 handler 返回给客户端的统一应用错误.
// Error 方法在 constructors.go 中实现, 因此 *Error 满足 Go 标准 error 接口.
type Error struct {
	// Code 是稳定的机器可读业务错误码, 客户端用它决定处理方式.
	Code string
	// Message 是可以向用户展示的错误说明, 不能包含内部实现和敏感信息.
	Message string
	// StatusCode 是返回给客户端的 HTTP 状态码, 如 400、401、409、500.
	StatusCode int
	// Retryable 表示客户端是否可以重试同一个请求.
	Retryable bool
	// TraceID 是本次请求的链路 ID, 用于关联接口响应、日志和 Span.
	TraceID string
}
