// constructors.go provides factory functions for common apperror.Error values.
//
// Each factory returns a pre-configured *Error with sensible defaults.
// Handlers can override specific fields (e.g. TraceID) after construction if needed.

package apperror

import (
	"net/http"
)

// New creates a new *Error with the given fields.
// Use this when you need full control over all fields.
func New(code, message string, statusCode int, retryable bool, traceID string) *Error {
	return &Error{
		Code:       code,
		Message:    message,
		StatusCode: statusCode,
		Retryable:  retryable,
		TraceID:    traceID,
	}
}

// BadRequest returns a 400 error with the given code and message.
// 4xx errors are not retryable.
func BadRequest(code, message, traceID string) *Error {
	return &Error{
		Code:       code,
		Message:    message,
		StatusCode: http.StatusBadRequest,
		Retryable:  false,
		TraceID:    traceID,
	}
}

// Unauthorized returns a 401 error.
func Unauthorized(traceID string) *Error {
	return &Error{
		Code:       "unauthorized",
		Message:    "authentication required",
		StatusCode: http.StatusUnauthorized,
		Retryable:  false,
		TraceID:    traceID,
	}
}

// NotFound returns a 404 error for the given resource.
func NotFound(resource, traceID string) *Error {
	return &Error{
		Code:       "not_found",
		Message:    resource + " not found",
		StatusCode: http.StatusNotFound,
		Retryable:  false,
		TraceID:    traceID,
	}
}

// Conflict returns a 409 error (e.g. idempotency conflict).
func Conflict(message, traceID string) *Error {
	return &Error{
		Code:       "conflict",
		Message:    message,
		StatusCode: http.StatusConflict,
		Retryable:  false,
		TraceID:    traceID,
	}
}

// Internal returns a 500 error with a generic message.
// Internal details are logged but not exposed to the client.
// 5xx errors are retryable.
func Internal(traceID string) *Error {
	return &Error{
		Code:       "internal_error",
		Message:    "internal server error",
		StatusCode: http.StatusInternalServerError,
		Retryable:  true,
		TraceID:    traceID,
	}
}

// ServiceUnavailable returns a 503 error (e.g. owner token not configured).
func ServiceUnavailable(message, traceID string) *Error {
	return &Error{
		Code:       "service_unavailable",
		Message:    message,
		StatusCode: http.StatusServiceUnavailable,
		Retryable:  true,
		TraceID:    traceID,
	}
}
