package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/KotlinDevIanBore/tradederivplus/internal/observability"
)

// Logger emits one structured line per request. Errors attached with c.Error
// are surfaced here rather than in the response body.
func Logger(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		status := c.Writer.Status()
		attrs := []any{
			"method", c.Request.Method,
			"path", path,
			"status", status,
			"duration_ms", time.Since(start).Milliseconds(),
		}
		if len(c.Errors) > 0 {
			attrs = append(attrs, "err", c.Errors.String())
		}

		switch {
		case status >= 500:
			log.Error("request", attrs...)
		case status >= 400:
			log.Warn("request", attrs...)
		default:
			log.Info("request", attrs...)
		}
	}
}

// Recovery turns a panic into a 500 without killing the process, and reports
// it with the route and the acting user attached. The user is an opaque id,
// never a phone number: see the scrub list in internal/observability.
func Recovery(log *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, err any) {
		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}
		log.Error("panic recovered", "err", err, "route", route)

		userID := ""
		if id := UserID(c); id != uuid.Nil {
			userID = id.String()
		}
		observability.CapturePanic(err, route, c.Request.Method, userID)

		c.AbortWithStatusJSON(500, gin.H{
			"error": gin.H{"message": "Something went wrong on our side", "code": "internal"},
		})
	})
}
