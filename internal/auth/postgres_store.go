package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"attendance-api/internal/db"
	"attendance-api/internal/db/dbgen"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool    *pgxpool.Pool
	queries *dbgen.Queries
}

func NewPostgresStore(pool *pgxpool.Pool, queries *dbgen.Queries) (*PostgresStore, error) {
	if pool == nil || queries == nil {
		return nil, errors.New("database pool and sqlc queries are required")
	}
	return &PostgresStore{pool: pool, queries: queries}, nil
}

func (s *PostgresStore) FindLogin(ctx context.Context, institutionIDText, email string) (LoginRecord, bool, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return LoginRecord{}, false, err
	}
	row, err := s.queries.LookupLogin(ctx, dbgen.LookupLoginParams{InstitutionID: institutionID, Lower: email})
	if errors.Is(err, pgx.ErrNoRows) {
		return LoginRecord{}, false, nil
	}
	if err != nil {
		return LoginRecord{}, false, fmt.Errorf("query login user: %w", err)
	}
	identity := Identity{
		UserID: row.UserID.String(), InstitutionID: row.InstitutionID.String(),
		ForcePassword: row.ForcePasswordChange,
	}
	if row.StudentRecordID.Valid {
		identity.StudentID = row.StudentRecordID.String()
	}
	if row.DepartmentID.Valid {
		identity.DepartmentID = row.DepartmentID.String()
	}
	if row.Level.Valid {
		identity.Level = int(row.Level.Int32)
	}
	return LoginRecord{
		Identity: identity, PasswordHash: row.PasswordHash,
		EmailVerified: row.EmailVerifiedAt.Valid,
	}, true, nil
}

func (s *PostgresStore) FindPasswordHash(ctx context.Context, institutionIDText, userIDText string) (string, bool, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return "", false, err
	}
	userID, err := db.ParseUUID(userIDText)
	if err != nil {
		return "", false, err
	}
	hash, err := s.queries.GetPasswordHash(ctx, dbgen.GetPasswordHashParams{
		InstitutionID: institutionID, ID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("query password hash: %w", err)
	}
	return hash, true, nil
}

func (s *PostgresStore) UpdatePasswordHash(ctx context.Context, institutionIDText, userIDText, passwordHash string) (bool, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return false, err
	}
	userID, err := db.ParseUUID(userIDText)
	if err != nil {
		return false, err
	}
	rows, err := s.queries.SetPasswordHash(ctx, dbgen.SetPasswordHashParams{
		InstitutionID: institutionID, ID: userID, PasswordHash: passwordHash,
	})
	if err != nil {
		return false, fmt.Errorf("update user password: %w", err)
	}
	return rows == 1, nil
}

func (s *PostgresStore) Create(ctx context.Context, hash []byte, identity Identity, expiresAt time.Time) error {
	institutionID, err := db.ParseUUID(identity.InstitutionID)
	if err != nil {
		return err
	}
	userID, err := db.ParseUUID(identity.UserID)
	if err != nil {
		return err
	}
	if err := s.queries.CreateRefreshToken(ctx, dbgen.CreateRefreshTokenParams{
		InstitutionID: institutionID, UserID: userID, TokenHash: hash,
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt.UTC(), Valid: true},
	}); err != nil {
		return fmt.Errorf("insert refresh token hash: %w", err)
	}
	return nil
}

func (s *PostgresStore) Rotate(ctx context.Context, oldHash, newHash []byte, now, expiresAt time.Time) (Identity, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Identity{}, fmt.Errorf("begin refresh rotation: %w", err)
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	row, err := queries.LockRefreshToken(ctx, oldHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, ErrInvalidRefresh
	}
	if err != nil {
		return Identity{}, fmt.Errorf("lock refresh token: %w", err)
	}
	if row.RevokedAt.Valid || !now.Before(row.ExpiresAt.Time) {
		return Identity{}, ErrInvalidRefresh
	}
	user, err := queries.LoadRefreshIdentity(ctx, dbgen.LoadRefreshIdentityParams{
		InstitutionID: row.InstitutionID, ID: row.UserID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, ErrInvalidRefresh
	}
	if err != nil {
		return Identity{}, fmt.Errorf("load refresh token user: %w", err)
	}
	_, err = queries.RevokeRefreshToken(ctx, dbgen.RevokeRefreshTokenParams{
		InstitutionID: row.InstitutionID, RevokedAt: pgtype.Timestamptz{Time: now.UTC(), Valid: true},
		TokenHash: oldHash,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, ErrInvalidRefresh
	}
	if err != nil {
		return Identity{}, fmt.Errorf("revoke rotated refresh token: %w", err)
	}
	if err := queries.CreateRefreshToken(ctx, dbgen.CreateRefreshTokenParams{
		InstitutionID: row.InstitutionID, UserID: row.UserID, TokenHash: newHash,
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt.UTC(), Valid: true},
	}); err != nil {
		return Identity{}, fmt.Errorf("insert replacement refresh token: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Identity{}, fmt.Errorf("commit refresh rotation: %w", err)
	}
	return Identity{
		InstitutionID: user.InstitutionID.String(), UserID: user.UserID.String(),
		StudentID: optionalUUIDString(user.StudentRecordID), DepartmentID: optionalUUIDString(user.DepartmentID),
		Level: optionalInt(user.Level), ForcePassword: user.ForcePasswordChange,
	}, nil
}

func (s *PostgresStore) Revoke(ctx context.Context, hash []byte, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin refresh revocation: %w", err)
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	row, err := queries.LockRefreshToken(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidRefresh
	}
	if err != nil {
		return fmt.Errorf("lock refresh token for revocation: %w", err)
	}
	if row.RevokedAt.Valid || !now.Before(row.ExpiresAt.Time) {
		return ErrInvalidRefresh
	}
	_, err = queries.RevokeRefreshToken(ctx, dbgen.RevokeRefreshTokenParams{
		InstitutionID: row.InstitutionID, RevokedAt: pgtype.Timestamptz{Time: now.UTC(), Valid: true},
		TokenHash: hash,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidRefresh
	}
	if err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit refresh revocation: %w", err)
	}
	return nil
}

func (s *PostgresStore) FindStudentByMatric(ctx context.Context, institutionIDText, matricNo string) (StudentRecord, bool, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return StudentRecord{}, false, err
	}
	row, err := s.queries.FindStudentByMatric(ctx, dbgen.FindStudentByMatricParams{
		InstitutionID: institutionID, Lower: matricNo,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return StudentRecord{}, false, nil
	}
	if err != nil {
		return StudentRecord{}, false, fmt.Errorf("find student by matric number: %w", err)
	}
	return StudentRecord{
		ID: row.ID.String(), InstitutionID: row.InstitutionID.String(),
		DepartmentID: row.DepartmentID.String(), MatricNo: row.MatricNo,
		Email: row.Email, DisplayName: row.DisplayName, Level: int(row.Level),
		Claimed: row.ClaimedByUserID.Valid,
	}, true, nil
}

func (s *PostgresStore) CreateSignupRequest(ctx context.Context, institutionIDText, matricNo, email string) error {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return err
	}
	if err := s.queries.CreateSignupRequest(ctx, dbgen.CreateSignupRequestParams{
		InstitutionID: institutionID, MatricNo: matricNo, Email: email,
	}); err != nil {
		return fmt.Errorf("create unknown-student signup request: %w", err)
	}
	return nil
}

func (s *PostgresStore) CreateVerification(ctx context.Context, institutionIDText, studentIDText string, codeHash []byte, expiresAt time.Time) error {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return err
	}
	studentID, err := db.ParseUUID(studentIDText)
	if err != nil {
		return err
	}
	if err := s.queries.CreateEmailVerification(ctx, dbgen.CreateEmailVerificationParams{
		InstitutionID: institutionID, StudentRecordID: studentID, CodeHash: codeHash,
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt.UTC(), Valid: true},
	}); err != nil {
		return fmt.Errorf("create email verification: %w", err)
	}
	return nil
}

func (s *PostgresStore) ClaimStudent(ctx context.Context, institutionIDText, studentIDText string, codeHash []byte, now time.Time, passwordHash, displayName string) (ClaimedStudent, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return ClaimedStudent{}, err
	}
	studentID, err := db.ParseUUID(studentIDText)
	if err != nil {
		return ClaimedStudent{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ClaimedStudent{}, fmt.Errorf("begin student claim: %w", err)
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	email, err := queries.GetStudentClaimTarget(ctx, dbgen.GetStudentClaimTargetParams{
		InstitutionID: institutionID, ID: studentID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ClaimedStudent{}, ErrInvalidVerification
	}
	if err != nil {
		return ClaimedStudent{}, fmt.Errorf("load student claim target: %w", err)
	}
	_, err = queries.ConsumeEmailVerification(ctx, dbgen.ConsumeEmailVerificationParams{
		InstitutionID: institutionID, StudentRecordID: studentID, CodeHash: codeHash,
		ConsumedAt: pgtype.Timestamptz{Time: now.UTC(), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ClaimedStudent{}, ErrInvalidVerification
	}
	if err != nil {
		return ClaimedStudent{}, fmt.Errorf("consume email verification: %w", err)
	}
	userID, err := queries.CreateClaimedStudentUser(ctx, dbgen.CreateClaimedStudentUserParams{
		InstitutionID: institutionID, Email: email, PasswordHash: passwordHash,
		DisplayName: displayName, EmailVerifiedAt: pgtype.Timestamptz{Time: now.UTC(), Valid: true},
	})
	if err != nil {
		return ClaimedStudent{}, fmt.Errorf("create claimed student user: %w", err)
	}
	claimed, err := queries.ClaimStudentRecord(ctx, dbgen.ClaimStudentRecordParams{
		InstitutionID: institutionID, ID: studentID, ClaimedByUserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ClaimedStudent{}, ErrStudentAlreadyClaimed
	}
	if err != nil {
		return ClaimedStudent{}, fmt.Errorf("claim student record: %w", err)
	}
	if err := queries.CreateStudentRole(ctx, dbgen.CreateStudentRoleParams{
		InstitutionID: institutionID, UserID: userID, DepartmentID: claimed.DepartmentID,
	}); err != nil {
		return ClaimedStudent{}, fmt.Errorf("assign student role: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ClaimedStudent{}, fmt.Errorf("commit student claim: %w", err)
	}
	return ClaimedStudent{
		UserID: userID.String(), StudentID: studentID.String(),
		InstitutionID: institutionID.String(), DepartmentID: claimed.DepartmentID.String(),
		Level: int(claimed.Level),
	}, nil
}

func optionalUUIDString(value pgtype.UUID) string {
	if !value.Valid {
		return ""
	}
	return value.String()
}

func optionalInt(value pgtype.Int4) int {
	if !value.Valid {
		return 0
	}
	return int(value.Int32)
}
