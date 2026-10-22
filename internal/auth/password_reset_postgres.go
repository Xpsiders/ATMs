package auth

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"time"

	"attendance-api/internal/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *PostgresStore) CreatePasswordReset(
	ctx context.Context,
	institutionIDText, email string,
	codeHash []byte,
	code string,
	expiresAt time.Time,
) error {
	tenant, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return fmt.Errorf("parse password reset institution ID: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin password reset request: %w", err)
	}
	defer tx.Rollback(ctx)
	var userID pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM users
		WHERE institution_id=$1 AND lower(email)=lower($2) AND disabled_at IS NULL
		FOR UPDATE`, tenant, email).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return fmt.Errorf("find password reset account: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE password_resets SET consumed_at=now()
		WHERE institution_id=$1 AND user_id=$2 AND consumed_at IS NULL`, tenant, userID); err != nil {
		return fmt.Errorf("expire previous password reset codes: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO password_resets(institution_id,user_id,token_hash,expires_at)
		VALUES($1,$2,$3,$4)`, tenant, userID, codeHash,
		pgtype.Timestamptz{Time: expiresAt.UTC(), Valid: true}); err != nil {
		return fmt.Errorf("store password reset code: %w", err)
	}
	body := "Your Campus Attendance password reset code is " + code + ". It expires in 15 minutes."
	if _, err := tx.Exec(ctx, `INSERT INTO email_outbox(institution_id,recipient,subject,body)
		VALUES($1,lower($2),'Campus Attendance password reset',$3)`, tenant, email, body); err != nil {
		return fmt.Errorf("enqueue password reset email: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit password reset request: %w", err)
	}
	return nil
}

func (s *PostgresStore) CompletePasswordReset(
	ctx context.Context,
	institutionIDText, email string,
	codeHash []byte,
	now time.Time,
	passwordHash string,
) error {
	tenant, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return fmt.Errorf("parse password reset institution ID: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin password reset completion: %w", err)
	}
	defer tx.Rollback(ctx)
	var userID, resetID pgtype.UUID
	var storedHash []byte
	var attempts int32
	err = tx.QueryRow(ctx, `SELECT u.id,pr.id,pr.token_hash,pr.attempts
		FROM users u JOIN password_resets pr ON pr.institution_id=u.institution_id AND pr.user_id=u.id
		WHERE u.institution_id=$1 AND lower(u.email)=lower($2) AND u.disabled_at IS NULL
			AND pr.consumed_at IS NULL AND pr.expires_at>$3
		ORDER BY pr.created_at DESC,pr.id DESC LIMIT 1 FOR UPDATE OF u,pr`,
		tenant, email, pgtype.Timestamptz{Time: now.UTC(), Valid: true}).
		Scan(&userID, &resetID, &storedHash, &attempts)
	if errors.Is(err, pgx.ErrNoRows) || !canAttemptPasswordReset(int(attempts)) {
		return ErrInvalidPasswordReset
	}
	if err != nil {
		return fmt.Errorf("load password reset code: %w", err)
	}
	if !hmac.Equal(storedHash, codeHash) {
		if _, err := tx.Exec(ctx, `UPDATE password_resets SET attempts=attempts+1 WHERE id=$1`, resetID); err != nil {
			return fmt.Errorf("record password reset attempt: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit failed password reset attempt: %w", err)
		}
		return ErrInvalidPasswordReset
	}
	if _, err := tx.Exec(ctx, `UPDATE password_resets SET consumed_at=$2 WHERE id=$1`,
		resetID, pgtype.Timestamptz{Time: now.UTC(), Valid: true}); err != nil {
		return fmt.Errorf("consume password reset code: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET password_hash=$3,force_password_change=false
		WHERE institution_id=$1 AND id=$2`, tenant, userID, passwordHash); err != nil {
		return fmt.Errorf("update reset password: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at=$3
		WHERE institution_id=$1 AND user_id=$2 AND revoked_at IS NULL`,
		tenant, userID, pgtype.Timestamptz{Time: now.UTC(), Valid: true}); err != nil {
		return fmt.Errorf("revoke existing refresh tokens: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_log(institution_id,actor_user_id,action,resource_type,resource_id,details)
		VALUES($1,$2,'password.reset','user',$2::text,'{}'::jsonb)`, tenant, userID); err != nil {
		return fmt.Errorf("audit password reset: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit password reset: %w", err)
	}
	return nil
}

var _ PasswordResetRepository = (*PostgresStore)(nil)
