package httpx

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/httprate"
)

func RateLimitByIP(limit int, window time.Duration) func(http.Handler) http.Handler {
	return httprate.Limit(limit, window, httprate.WithKeyFuncs(httprate.KeyByIP),
		httprate.WithLimitHandler(func(w http.ResponseWriter, r *http.Request) {
			slog.WarnContext(r.Context(), "request rate limit exceeded", "remote_addr", r.RemoteAddr)
			WriteError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests", nil)
		}),
		httprate.WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
			slog.ErrorContext(r.Context(), "rate-limit middleware failed", "error", err)
			WriteError(w, http.StatusInternalServerError, "internal_error", "Unable to apply request rate limit", nil)
		}),
	)
}
