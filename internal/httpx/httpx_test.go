package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type pingFunc func(context.Context) error

func (f pingFunc) Ping(ctx context.Context) error { return f(ctx) }

func TestWriteErrorEnvelope(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteError(recorder, http.StatusBadRequest, "invalid", "bad input", nil)
	if got := recorder.Code; got != http.StatusBadRequest {
		t.Fatalf("status = %d", got)
	}
	want := `{"error":{"code":"invalid","message":"bad input","details":{}}}`
	if strings.TrimSpace(recorder.Body.String()) != want {
		t.Fatalf("body = %s, want %s", recorder.Body.String(), want)
	}
}

func TestDecodeJSONRejectsUnknownAndTrailingFields(t *testing.T) {
	for _, body := range []string{`{"known":1,"unknown":2}`, `{"known":1} {"known":2}`} {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		recorder := httptest.NewRecorder()
		var payload struct {
			Known int `json:"known"`
		}
		if err := DecodeJSON(recorder, req, &payload); err == nil {
			t.Errorf("DecodeJSON(%s) error = nil", body)
		}
	}
}

func TestRequestIDAndCORS(t *testing.T) {
	handler := RequestID(CORS([]string{"https://web.example"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if RequestIDFromContext(r.Context()) == "" {
			t.Error("request ID missing from context")
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://web.example")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Header().Get("Access-Control-Allow-Origin") != "https://web.example" {
		t.Fatal("allowed origin not returned")
	}
	if recorder.Header().Get("X-Request-ID") == "" {
		t.Fatal("request ID header missing")
	}
}

func TestCORSAllowlistAndWildcard(t *testing.T) {
	cors := CORS([]string{"https://campuscheck.vercel.app", "https://*.vercel.app"})
	handler := cors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	check := func(origin string, wantAllowed bool) {
		t.Helper()
		req := httptest.NewRequest(http.MethodOptions, "/me", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		allowed := rec.Header().Get("Access-Control-Allow-Origin") == origin &&
			rec.Header().Get("Access-Control-Allow-Credentials") == "true"
		if allowed != wantAllowed {
			t.Fatalf("origin %q allowed=%v want %v (status %d)", origin, allowed, wantAllowed, rec.Code)
		}
		if !wantAllowed && rec.Code != http.StatusForbidden {
			t.Fatalf("disallowed preflight for %q = %d, want 403", origin, rec.Code)
		}
	}
	check("https://campuscheck.vercel.app", true)            // exact
	check("https://campuscheck-git-main-x.vercel.app", true) // preview via wildcard
	check("https://evil.example.com", false)                 // not allowlisted
	check("https://vercel.app", false)                       // wildcard requires a subdomain label
}

func TestDecodeJSONRejectsOversizedBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat(" ", MaxRequestBody+1)))
	recorder := httptest.NewRecorder()
	var payload struct{}
	if err := DecodeJSON(recorder, req, &payload); err == nil {
		t.Fatal("expected oversized body error")
	}
}

func TestReadyHandlerReportsDatabaseFailure(t *testing.T) {
	handler := NewRouterWithPinger(pingFunc(func(_ context.Context) error { return errors.New("offline") }), []string{"https://web.example"})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestRecovererReturnsGenericErrorEnvelopeAndKeepsRequestID(t *testing.T) {
	t.Parallel()
	handler := RequestID(RequestLogger(Recoverer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("sensitive panic details")
	}))))
	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if strings.Contains(recorder.Body.String(), "sensitive panic details") {
		t.Fatalf("response disclosed panic value: %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"internal_error"`) {
		t.Fatalf("response is missing error envelope: %s", recorder.Body.String())
	}
	if recorder.Header().Get("X-Request-ID") == "" {
		t.Fatal("recovered response omitted request ID")
	}
}

func TestRecovererDoesNotAppendEnvelopeAfterResponseCommit(t *testing.T) {
	t.Parallel()
	handler := Recoverer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("response started"))
		panic("panic after commit")
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "response started" {
		t.Fatalf("committed response changed: status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

func TestRateLimitMiddlewareReturns429(t *testing.T) {
	t.Parallel()
	handler := RateLimitByIP(1, time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for request := 0; request < 2; request++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "192.0.2.10:1234"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		want := http.StatusNoContent
		if request == 1 {
			want = http.StatusTooManyRequests
		}
		if recorder.Code != want {
			t.Fatalf("request %d status = %d, want %d", request, recorder.Code, want)
		}
	}
}
