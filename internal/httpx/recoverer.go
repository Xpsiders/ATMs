package httpx

import (
	"log/slog"
	"net/http"
	"reflect"
)

type recoveryResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *recoveryResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *recoveryResponseWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *recoveryResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &recoveryResponseWriter{ResponseWriter: w}
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.ErrorContext(r.Context(), "HTTP handler panic recovered",
					"request_id", RequestIDFromContext(r.Context()),
					"method", r.Method,
					"path", r.URL.Path,
					"panic_type", reflect.TypeOf(recovered).String(),
				)
				if !recorder.wroteHeader {
					WriteError(recorder, http.StatusInternalServerError,
						"internal_error", "An unexpected error occurred", nil)
				}
			}
		}()
		next.ServeHTTP(recorder, r)
	})
}
