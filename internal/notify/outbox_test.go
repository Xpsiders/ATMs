package notify

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeOutbox struct {
	messages  []OutboxMessage
	sent      []string
	failed    []string
	retryAt   time.Time
	enqueueTo string
}

func (f *fakeOutbox) Enqueue(_ context.Context, institutionID string, _ Message) error {
	f.enqueueTo = institutionID
	return nil
}

func (f *fakeOutbox) Claim(context.Context, time.Time, int) ([]OutboxMessage, error) {
	return f.messages, nil
}

func (f *fakeOutbox) MarkSent(_ context.Context, id string, _ time.Time) error {
	f.sent = append(f.sent, id)
	return nil
}

func (f *fakeOutbox) MarkFailed(_ context.Context, id string, _ int, retryAt time.Time) error {
	f.failed = append(f.failed, id)
	f.retryAt = retryAt
	return nil
}

type fakeMailer struct {
	err      error
	messages []Message
}

func (f *fakeMailer) Send(_ context.Context, message Message) error {
	f.messages = append(f.messages, message)
	return f.err
}

func TestQueuedMailerRequiresInstitutionContext(t *testing.T) {
	store := &fakeOutbox{}
	mailer, err := NewQueuedMailer(store)
	if err != nil {
		t.Fatal(err)
	}
	message := Message{To: "user@example.test", Subject: "Verify", Text: "123456"}
	if err := mailer.Send(context.Background(), message); err == nil {
		t.Fatal("queued email without institution was accepted")
	}
	ctx := ContextWithInstitution(context.Background(), "institution-1")
	if err := mailer.Send(ctx, message); err != nil {
		t.Fatal(err)
	}
	if store.enqueueTo != "institution-1" {
		t.Fatalf("queued for institution %q", store.enqueueTo)
	}
}

func TestWorkerMarksSuccessAndSchedulesRetry(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	store := &fakeOutbox{messages: []OutboxMessage{
		{ID: "sent", Message: Message{To: "a@example.test"}, Attempts: 1},
		{ID: "retry", Message: Message{To: "b@example.test"}, Attempts: 2},
	}}
	mailer := &fakeMailer{err: errors.New("provider unavailable")}
	worker, err := NewWorker(store, mailer, func() time.Time { return now }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.sent) != 0 || len(store.failed) != 2 {
		t.Fatalf("unexpected recorded delivery results: sent=%v failed=%v", store.sent, store.failed)
	}
	if !store.retryAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("retry at = %v, want %v", store.retryAt, now.Add(time.Minute))
	}

	mailer.err = nil
	store.messages = store.messages[:1]
	if err := worker.ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.sent) != 1 || store.sent[0] != "sent" {
		t.Fatalf("sent messages = %v", store.sent)
	}
}

func TestRetryDelayCapsAndUsesExponentialBackoff(t *testing.T) {
	if got := retryDelay(1); got != 30*time.Second {
		t.Fatalf("first retry delay = %s", got)
	}
	if got := retryDelay(4); got != 4*time.Minute {
		t.Fatalf("fourth retry delay = %s", got)
	}
	if got := retryDelay(20); got != time.Hour {
		t.Fatalf("capped retry delay = %s", got)
	}
}
