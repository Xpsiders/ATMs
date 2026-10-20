package httpx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

const maxLoggedErrorBody = 4096

type statusResponseWriter struct {
	http.ResponseWriter
	status    int
	errorBody []byte
}

func (w *statusResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.status >= http.StatusBadRequest && len(w.errorBody) < maxLoggedErrorBody {
		remaining := maxLoggedErrorBody - len(w.errorBody)
		captured := data
		if len(captured) > remaining {
			captured = captured[:remaining]
		}
		w.errorBody = append(w.errorBody, captured...)
	}
	return w.ResponseWriter.Write(data)
}

func (w *statusResponseWriter) errorCode() string {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(bytes.NewReader(w.errorBody)).Decode(&envelope); err != nil || envelope.Error.Code == "" {
		return "http_error"
	}
	return envelope.Error.Code
}

func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusResponseWriter{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		args := []any{
			"request_id", RequestIDFromContext(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration_ms", time.Since(started).Milliseconds(),
		}
		if status >= http.StatusBadRequest {
			args = append(args, "error_code", recorder.errorCode())
			slog.WarnContext(r.Context(), "HTTP request completed", args...)
			return
		}
		slog.InfoContext(r.Context(), "HTTP request completed", args...)
	})
}
