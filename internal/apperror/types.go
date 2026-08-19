// Package apperror defines the unified application error model.
//
// All HTTP handlers return errors using apperror.Error, which carries:
//   - Code: a stable machine-readable error code (e.g. "invalid_request").
//   - Message: a human-readable explanation (safe to show to clients).
//   - Retryable: whether the client should retry the same request.
//   - TraceID: the trace ID for cross-referencing logs and spans.
//
// Handlers call New() or Wrap() to create an Error, then writeHTTP() serializes it
// as a JSON response with the appropriate HTTP status code.
//
// Error code conventions:
//   - Use snake_case for code values.
//   - Code is stable across versions; Message may change.
//   - 4xx errors are generally not retryable; 5xx errors may be.
package apperror

// Error is the unified error type returned by handlers.
// It implements the error interface via the Error() method.
type Error struct {
	// Code is a stable, machine-readable error code.
	// Clients use this to decide how to handle the error.
	Code string
	// Message is a human-readable explanation of the error.
	// Safe to show to end users; must not contain internal details.
	Message string
	// StatusCode is the HTTP status code to return (e.g. 400, 401, 409, 500).
	StatusCode int
	// Retryable indicates whether the client should retry the same request.
	// Generally false for 4xx (except 429) and true for 5xx.
	Retryable bool
	// TraceID is the request trace ID, written into the response for debugging.
	TraceID string
}

// Error returns the Code string, making *Error satisfy the error interface.
// This allows apperror.Error to be used anywhere a standard error is expected.
func (e *Error) Error() string {
	return e.Code
}
