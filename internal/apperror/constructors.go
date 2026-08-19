// constructors.go 放统一应用错误的构造和方法实现.
//
// 做的事情:
//  1. 实现 Error 方法, 让 *Error 满足 Go 标准 error 接口.
//  2. 提供常见 HTTP 错误构造函数, 统一业务错误码、HTTP 状态码和重试策略.
//  3. 避免 handler 每次手动组装 Error, 导致字段遗漏或行为不一致.

package apperror

import (
	"net/http"
)

// Error 返回稳定错误码，使 *Error 满足 Go 标准 error 接口。
func (e *Error) Error() string {
	return e.Code
}

// New 创建一条可以完整指定所有字段的应用错误.
// 只有现有快捷构造函数无法表达错误场景时才使用.
func New(code, message string, statusCode int, retryable bool, traceID string) *Error {
	return &Error{
		Code:       code,
		Message:    message,
		StatusCode: statusCode,
		Retryable:  retryable,
		TraceID:    traceID,
	}
}

// BadRequest 创建 HTTP 400 参数错误.
// 参数错误由客户端修正请求, 默认不可重试.
func BadRequest(code, message, traceID string) *Error {
	return &Error{
		Code:       code,
		Message:    message,
		StatusCode: http.StatusBadRequest,
		Retryable:  false,
		TraceID:    traceID,
	}
}

// Unauthorized 创建 HTTP 401 未认证错误.
func Unauthorized(traceID string) *Error {
	return &Error{
		Code:       CodeUnauthorized,
		Message:    "authentication required",
		StatusCode: http.StatusUnauthorized,
		Retryable:  false,
		TraceID:    traceID,
	}
}

// NotFound 创建 HTTP 404 资源不存在错误.
func NotFound(resource, traceID string) *Error {
	return &Error{
		Code:       CodeNotFound,
		Message:    resource + " not found",
		StatusCode: http.StatusNotFound,
		Retryable:  false,
		TraceID:    traceID,
	}
}

// Conflict 创建 HTTP 409 状态冲突错误, 如幂等键对应的请求内容不一致.
func Conflict(message, traceID string) *Error {
	return &Error{
		Code:       CodeConflict,
		Message:    message,
		StatusCode: http.StatusConflict,
		Retryable:  false,
		TraceID:    traceID,
	}
}

// Internal 创建 HTTP 500 内部错误.
// 内部错误详情只写日志, 返回给客户端的是通用说明, 默认允许重试.
func Internal(traceID string) *Error {
	return &Error{
		Code:       CodeInternal,
		Message:    "internal server error",
		StatusCode: http.StatusInternalServerError,
		Retryable:  true,
		TraceID:    traceID,
	}
}

// ServiceUnavailable 创建 HTTP 503 服务不可用错误, 如主人令牌没有配置.
func ServiceUnavailable(message, traceID string) *Error {
	return &Error{
		Code:       CodeServiceUnavailable,
		Message:    message,
		StatusCode: http.StatusServiceUnavailable,
		Retryable:  true,
		TraceID:    traceID,
	}
}
