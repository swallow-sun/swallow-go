// errors.go 定义 ASR Provider 的结构化错误，供 HTTP 层决定状态码和重试策略。
package asr

import (
	"fmt"
	"net/http"
	"strings"
)

// maxASRResponseBytes 限制所有远程 ASR 供应商的响应体，避免异常响应占满内存。
const maxASRResponseBytes = 2 * 1024 * 1024

// ProviderErrorKind 是不会暴露供应商内部细节的稳定错误分类。
type ProviderErrorKind string

const (
	ProviderErrorInvalidInput    ProviderErrorKind = "invalid_input"
	ProviderErrorAuthentication  ProviderErrorKind = "authentication"
	ProviderErrorRateLimited     ProviderErrorKind = "rate_limited"
	ProviderErrorUnavailable     ProviderErrorKind = "unavailable"
	ProviderErrorInvalidResponse ProviderErrorKind = "invalid_response"
)

// ProviderError 保存错误分类、上游 HTTP 状态和仅用于服务端日志的说明。
type ProviderError struct {
	Kind       ProviderErrorKind
	HTTPStatus int
	Message    string
	Cause      error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "ASR provider error"
	}
	if e.HTTPStatus > 0 {
		return fmt.Sprintf("%s (HTTP %d): %s", e.Kind, e.HTTPStatus, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func newProviderError(kind ProviderErrorKind, status int, message string, cause error) error {
	return &ProviderError{Kind: kind, HTTPStatus: status, Message: message, Cause: cause}
}

// providerHTTPErrorKind 将 OpenAI 兼容供应商的 HTTP 状态统一映射为稳定分类。
func providerHTTPErrorKind(status int) ProviderErrorKind {
	switch status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
		return ProviderErrorInvalidInput
	case http.StatusUnauthorized, http.StatusForbidden:
		return ProviderErrorAuthentication
	case http.StatusTooManyRequests:
		return ProviderErrorRateLimited
	default:
		return ProviderErrorUnavailable
	}
}

// compactErrorBody 保留有限长度的上游错误详情，仅写入服务端日志用于排查。
func compactErrorBody(body []byte) string {
	const maxErrorBytes = 4096
	if len(body) > maxErrorBytes {
		body = body[:maxErrorBytes]
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "empty response body"
	}
	return text
}
