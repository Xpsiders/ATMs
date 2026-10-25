package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"attendance-api/internal/db"
	"attendance-api/internal/notify"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

const claimOutboxQuery = `WITH expired AS (
	UPDATE email_outbox SET status='pending', locked_at=NULL
	WHERE status='sending' AND locked_at < $1::timestamptz - interval '5 minutes'
), candidates AS (
	SELECT id FROM email_outbox
	WHERE status='pending' AND available_at <= $1 AND attempts < $2
	ORDER BY available_at, created_at, id
	FOR UPDATE SKIP LOCKED LIMIT $3
)
UPDATE email_outbox e SET status='sending', locked_at=$1, attempts=e.attempts+1
FROM candidates c WHERE e.id=c.id
RETURNING e.id::text,e.recipient,e.subject,e.body,e.attempts`

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("database pool is required")
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Enqueue(ctx context.Context, institutionID string, message notify.Message) error {
	tenant, err := db.ParseUUID(institutionID)
	if err != nil {
		return fmt.Errorf("parse email institution ID: %w", err)
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO email_outbox(institution_id,recipient,subject,body)
		VALUES($1,$2,$3,$4)`, tenant, message.To, message.Subject, message.Text)
	if err != nil {
		return fmt.Errorf("insert email outbox message: %w", err)
	}
	return nil
}

func (s *Store) Claim(ctx context.Context, now time.Time, limit int) ([]notify.OutboxMessage, error) {
	if limit < 1 || limit > notify.OutboxBatchSize {
		return nil, errors.New("email outbox claim limit is invalid")
	}
	rows, err := s.pool.Query(ctx, claimOutboxQuery, now.UTC(), notify.OutboxMaxTries, limit)
	if err != nil {
		return nil, fmt.Errorf("claim email outbox rows: %w", err)
	}
	defer rows.Close()
	messages := make([]notify.OutboxMessage, 0, limit)
	for rows.Next() {
		var item notify.OutboxMessage
		if err := rows.Scan(&item.ID, &item.Message.To, &item.Message.Subject, &item.Message.Text, &item.Attempts); err != nil {
			return nil, fmt.Errorf("scan email outbox row: %w", err)
		}
		messages = append(messages, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read email outbox rows: %w", err)
	}
	return messages, nil
}

func (s *Store) MarkSent(ctx context.Context, id string, sentAt time.Time) error {
	messageID, err := db.ParseUUID(id)
	if err != nil {
		return fmt.Errorf("parse email outbox ID: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `UPDATE email_outbox SET status='sent', sent_at=$2, locked_at=NULL
		WHERE id=$1 AND status='sending'`, messageID, pgtype.Timestamptz{Time: sentAt.UTC(), Valid: true})
	if err != nil {
		return fmt.Errorf("mark email outbox sent: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) MarkFailed(ctx context.Context, id string, attempts int, retryAt time.Time) error {
	messageID, err := db.ParseUUID(id)
	if err != nil {
		return fmt.Errorf("parse email outbox ID: %w", err)
	}
	status := "pending"
	if attempts >= notify.OutboxMaxTries {
		status = "failed"
	}
	tag, err := s.pool.Exec(ctx, `UPDATE email_outbox SET status=$2, available_at=$3, locked_at=NULL
		WHERE id=$1 AND status='sending'`, messageID, status,
		pgtype.Timestamptz{Time: retryAt.UTC(), Valid: true})
	if err != nil {
		return fmt.Errorf("mark email outbox failed: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

var _ notify.OutboxStore = (*Store)(nil)
