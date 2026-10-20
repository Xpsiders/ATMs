package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewMailerUsesLogMailerWhenKeyEmpty(t *testing.T) {
	var output strings.Builder
	logger := slog.New(slog.NewTextHandler(&output, nil))
	mailer, err := NewMailer("", "", logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := mailer.Send(context.Background(), Message{To: "student@example.test", Subject: "Code", Text: "123456"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "123456") {
		t.Fatal("development mail log does not contain the verification code")
	}
}

func TestResendMailerUsesAuthorizationAndHandlesFailures(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected request: %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	mailer := &ResendMailer{APIKey: "test-key", From: "noreply@example.test", Endpoint: server.URL}
	if err := mailer.Send(context.Background(), Message{To: "student@example.test", Subject: "Verify", Text: "code"}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := mailer.Send(ctx, Message{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request error = %v", err)
	}
	mailer.Endpoint = "://invalid"
	if err := mailer.Send(context.Background(), Message{}); err == nil {
		t.Fatal("invalid endpoint accepted")
	}
}

func TestNewMailerRequiresSenderWithKey(t *testing.T) {
	t.Parallel()
	if _, err := NewMailer("provider-key", "", nil); err == nil {
		t.Fatal("configured provider accepted missing sender")
	}
}
