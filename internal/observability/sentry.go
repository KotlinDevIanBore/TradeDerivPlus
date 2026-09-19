// Package observability wires error reporting and metrics. It is the only
// place that knows about Sentry or Prometheus, so the rest of the app stays
// unaware of either.
package observability

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/KotlinDevIanBore/tradederivplus/internal/config"
)

// scrubbedFields are stripped from any event before it leaves the process,
// wherever they appear. This is a money app handling identity documents: an
// error report is not worth leaking a phone number or an ID number over.
var scrubbedFields = []string{
	"phone", "phone_number", "amount", "password", "current_password",
	"new_password", "email", "id_number", "full_name",
}

// scrubbedHeaders never reach Sentry at all.
var scrubbedHeaders = []string{"authorization", "cookie", "set-cookie", "x-api-key"}

// bodylessPrefixes have their request body dropped entirely rather than
// field-scrubbed. A KYC rejection reason or a change-password payload can
// carry identity data in a free-text field that no fixed list would catch, so
// the safe move is to send no body at all for these.
var bodylessPrefixes = []string{"/api/v1/auth", "/api/v1/wallet", "/api/v1/admin"}

// Init starts Sentry if a DSN is configured. With no DSN it is a no-op, which
// is how development runs by default.
func Init(cfg *config.Config, log *slog.Logger) (flush func(), err error) {
	if cfg.SentryDSN == "" {
		log.Info("sentry disabled (no DSN)")
		return func() {}, nil
	}

	err = sentry.Init(sentry.ClientOptions{
		Dsn:              cfg.SentryDSN,
		Environment:      cfg.SentryEnvironment,
		SampleRate:       cfg.SentrySampleRate,
		AttachStacktrace: true,
		// Everything that leaves the process passes through here.
		BeforeSend: scrub,
	})
	if err != nil {
		return nil, fmt.Errorf("sentry init: %w", err)
	}

	log.Info("sentry enabled", "environment", cfg.SentryEnvironment)
	return func() { sentry.Flush(2 * time.Second) }, nil
}

// scrub removes credentials and personal data from an event.
func scrub(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	if event.Request != nil {
		for _, h := range scrubbedHeaders {
			for key := range event.Request.Headers {
				if strings.EqualFold(key, h) {
					event.Request.Headers[key] = "[redacted]"
				}
			}
		}
		event.Request.Cookies = ""

		// Query strings can carry a token on the WebSocket handshake.
		if event.Request.QueryString != "" {
			event.Request.QueryString = scrubQuery(event.Request.QueryString)
		}

		for _, prefix := range bodylessPrefixes {
			if strings.HasPrefix(event.Request.URL, prefix) ||
				strings.Contains(event.Request.URL, prefix) {
				event.Request.Data = "[body dropped: sensitive route]"
				break
			}
		}
	}

	for key := range event.Extra {
		if isScrubbed(key) {
			event.Extra[key] = "[redacted]"
		}
	}
	for key := range event.Tags {
		if isScrubbed(key) {
			event.Tags[key] = "[redacted]"
		}
	}

	// The user object may carry an email or a phone; the opaque id is enough
	// to find the account and is not personal data on its own.
	if event.User.Email != "" || event.User.Username != "" || event.User.IPAddress != "" {
		event.User = sentry.User{ID: event.User.ID}
	}
	return event
}

func isScrubbed(key string) bool {
	lower := strings.ToLower(key)
	for _, f := range scrubbedFields {
		if strings.Contains(lower, f) {
			return true
		}
	}
	return false
}

func scrubQuery(q string) string {
	parts := strings.Split(q, "&")
	for i, p := range parts {
		name, _, found := strings.Cut(p, "=")
		if !found {
			continue
		}
		if isScrubbed(name) || strings.EqualFold(name, "token") {
			parts[i] = name + "=[redacted]"
		}
	}
	return strings.Join(parts, "&")
}

// CaptureBusiness reports an expected business-rule failure, tagged so it can
// be filtered out of the issue list. Without the tag, Sentry drowns in
// "insufficient balance" and the real bugs get lost.
func CaptureBusiness(err error, route string) {
	if err == nil {
		return
	}
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("kind", "business")
		scope.SetTag("route", route)
		scope.SetLevel(sentry.LevelInfo)
		sentry.CaptureException(err)
	})
}

// CaptureBug reports an unexpected failure: a real defect.
func CaptureBug(err error, route, userID string) {
	if err == nil {
		return
	}
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("kind", "bug")
		scope.SetTag("route", route)
		if userID != "" {
			scope.SetUser(sentry.User{ID: userID})
		}
		scope.SetLevel(sentry.LevelError)
		sentry.CaptureException(err)
	})
}

// CapturePanic reports a recovered panic with the request context attached.
func CapturePanic(recovered any, route, method, userID string) {
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("kind", "panic")
		scope.SetTag("route", route)
		scope.SetTag("method", method)
		if userID != "" {
			scope.SetUser(sentry.User{ID: userID})
		}
		scope.SetLevel(sentry.LevelFatal)
		sentry.CurrentHub().Recover(recovered)
	})
}
