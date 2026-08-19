// errors.go provides helper functions for writing unified error responses.
//
// All handlers should use writeError() instead of c.JSON() with map[string]string.
// This ensures every error response has the same structure:
//   { "code": "...", "message": "...", "retryable": false, "trace_id": "..." }
//
// The function also logs the error at the appropriate level:
//   - 4xx (client errors) → Debug
//   - 5xx (server errors) → Error
package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/swallow-sun/swallow-go/internal/apperror"
	"github.com/swallow-sun/swallow-go/internal/trace"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// writeError writes an apperror.Error as a JSON response.
// The HTTP status code comes from e.StatusCode.
// Also logs the error: 4xx at Debug, 5xx at Error.
func writeError(c *app.RequestContext, e *apperror.Error) {
	// 5xx errors are server-side problems, log at Error level
	if e.StatusCode >= 500 {
		logger.Error("request failed",
			zap.String("code", e.Code),
			zap.Int("status", e.StatusCode),
			zap.String("trace_id", e.TraceID),
		)
	} else {
		// 4xx errors are client-side problems, log at Debug level
		logger.Debug("request rejected",
			zap.String("code", e.Code),
			zap.Int("status", e.StatusCode),
			zap.String("trace_id", e.TraceID),
		)
	}

	// Serialize the error as JSON with the HTTP status code from the error
	c.JSON(e.StatusCode, map[string]any{
		"code":      e.Code,
		"message":   e.Message,
		"retryable": e.Retryable,
		"trace_id":  e.TraceID,
	})
}

// writeErrorFromCtx is a convenience wrapper for handlers that have a context.
// It extracts the trace ID from the context before writing the error.
func writeErrorFromCtx(ctx context.Context, c *app.RequestContext, e *apperror.Error) {
	// If the error already has a trace ID, keep it
	// Otherwise, extract from context
	if e.TraceID == "" {
		e.TraceID = trace.FromContext(ctx)
	}
	writeError(c, e)
}
