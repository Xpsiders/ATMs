package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const (
	OutboxBatchSize = 20
	OutboxMaxTries  = 5
)

type OutboxMessage struct {
	ID       string
	Message  Message
	Attempts int
}

type OutboxStore interface {
	Enqueue(context.Context, string, Message) error
	Claim(context.Context, time.Time, int) ([]OutboxMessage, error)
	MarkSent(context.Context, string, time.Time) error
	MarkFailed(context.Context, string, int, time.Time) error
}

type QueuedMailer struct {
	store OutboxStore
}

func NewQueuedMailer(store OutboxStore) (*QueuedMailer, error) {
	if store == nil {
		return nil, errors.New("email outbox store is required")
	}
	return &QueuedMailer{store: store}, nil
}

func (m *QueuedMailer) Send(ctx context.Context, message Message) error {
	if message.To == "" || message.Subject == "" || message.Text == "" {
		return errors.New("email recipient, subject, and body are required")
	}
	institutionID, ok := InstitutionIDFromContext(ctx)
	if !ok {
		return errors.New("email institution is missing from context")
	}
	if err := m.store.Enqueue(ctx, institutionID, message); err != nil {
		return fmt.Errorf("enqueue email: %w", err)
	}
	return nil
}

type institutionContextKey struct{}

func ContextWithInstitution(ctx context.Context, institutionID string) context.Context {
	return context.WithValue(ctx, institutionContextKey{}, institutionID)
}

func InstitutionIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(institutionContextKey{}).(string)
	return id, ok && id != ""
}

type Worker struct {
	store  OutboxStore
	mailer Mailer
	now    func() time.Time
	logger *slog.Logger
}

func NewWorker(store OutboxStore, mailer Mailer, now func() time.Time, logger *slog.Logger) (*Worker, error) {
	if store == nil || mailer == nil || now == nil {
		return nil, errors.New("outbox store, mailer, and clock are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{store: store, mailer: mailer, now: now, logger: logger}, nil
}

func (w *Worker) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return errors.New("outbox worker interval must be positive")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := w.ProcessBatch(ctx); err != nil && !errors.Is(err, context.Canceled) {
			w.logger.ErrorContext(ctx, "process email outbox batch failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w *Worker) ProcessBatch(ctx context.Context) error {
	messages, err := w.store.Claim(ctx, w.now().UTC(), OutboxBatchSize)
	if err != nil {
		return fmt.Errorf("claim email outbox messages: %w", err)
	}
	var failures []error
	for _, message := range messages {
		if err := w.mailer.Send(ctx, message.Message); err != nil {
			retryAt := w.now().UTC().Add(retryDelay(message.Attempts))
			if markErr := w.store.MarkFailed(ctx, message.ID, message.Attempts, retryAt); markErr != nil {
				failures = append(failures, fmt.Errorf("mark failed email %s: %w", message.ID, markErr))
			}
			w.logger.WarnContext(ctx, "email delivery failed",
				"message_id", message.ID, "attempt", message.Attempts, "error", err)
			continue
		}
		if err := w.store.MarkSent(ctx, message.ID, w.now().UTC()); err != nil {
			failures = append(failures, fmt.Errorf("mark sent email %s: %w", message.ID, err))
		}
	}
	return errors.Join(failures...)
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := 30 * time.Second
	for i := 1; i < attempt && delay < time.Hour; i++ {
		delay *= 2
	}
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}
