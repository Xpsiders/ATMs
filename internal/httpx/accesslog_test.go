package httpx

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestLoggerIncludesMachineReadableErrorCodeWithoutRequestBody(t *testing.T) {
	previous := slog.Default()
	var output bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(previous)

	const requestSecret = "private-request-body"
	handler := RequestID(RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil {
			t.Fatal("request body missing")
		}
		WriteError(w, http.StatusBadRequest, "wrong_or_expired_code", "Code is invalid", nil)
	})))
	request := httptest.NewRequest(http.MethodPost, "/checkins", strings.NewReader(requestSecret))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	logLine := output.String()
	if !strings.Contains(logLine, `"msg":"HTTP request completed"`) ||
		!strings.Contains(logLine, `"error_code":"wrong_or_expired_code"`) ||
		!strings.Contains(logLine, `"status":400`) {
		t.Fatalf("completion log omitted status or error code: %s", logLine)
	}
	if strings.Contains(logLine, requestSecret) || strings.Contains(logLine, "Code is invalid") {
		t.Fatalf("completion log contains request or response body: %s", logLine)
	}
}

func TestRequestLoggerUsesGenericCodeForNonEnvelopeServerError(t *testing.T) {
	previous := slog.Default()
	var output bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(previous)

	handler := RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal details", http.StatusInternalServerError)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(output.String(), `"error_code":"http_error"`) {
		t.Fatalf("completion log omitted fallback error code: %s", output.String())
	}
	if strings.Contains(output.String(), "internal details") {
		t.Fatalf("completion log contains response body: %s", output.String())
	}
}
