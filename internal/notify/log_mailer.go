package notify

import (
	"context"
	"errors"
	"log/slog"
)

type LogMailer struct {
	Logger *slog.Logger
}

func (m LogMailer) Send(ctx context.Context, message Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	logger := m.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.InfoContext(ctx, "development email",
		"to", message.To,
		"subject", message.Subject,
		"text", message.Text,
	)
	return nil
}

func NewMailer(apiKey, from, environment string, logger *slog.Logger) (Mailer, error) {
	if apiKey == "" {
		if environment != "development" {
			return nil, errors.New("MAIL_API_KEY is required outside development")
		}
		return LogMailer{Logger: logger}, nil
	}
	if from == "" {
		return nil, errors.New("MAIL_FROM is required when MAIL_API_KEY is configured")
	}
	return &ResendMailer{APIKey: apiKey, From: from}, nil
}
