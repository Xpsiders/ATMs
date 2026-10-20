package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"attendance-api/internal/db"
	"attendance-api/internal/db/dbgen"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *PostgresStore) CreateInvitation(
	ctx context.Context,
	issuer Identity,
	invitation InvitationRequest,
	tokenHash []byte,
	expiresAt time.Time,
) (InvitationRecord, error) {
	institutionID, err := db.ParseUUID(issuer.InstitutionID)
	if err != nil {
		return InvitationRecord{}, fmt.Errorf("parse invitation institution ID: %w", err)
	}
	userID, err := db.ParseUUID(issuer.UserID)
	if err != nil {
		return InvitationRecord{}, fmt.Errorf("parse invitation issuer ID: %w", err)
	}
	facultyID, err := optionalInvitationUUID(invitation.FacultyID)
	if err != nil {
		return InvitationRecord{}, err
	}
	departmentID, err := optionalInvitationUUID(invitation.DepartmentID)
	if err != nil {
		return InvitationRecord{}, err
	}
	authorized, err := s.queries.CanInviteTarget(ctx, dbgen.CanInviteTargetParams{
		InstitutionID: institutionID,
		UserID:        userID,
		Column3:       invitation.Role,
		Column4:       invitation.ScopeType,
		Column5:       facultyID,
		Column6:       departmentID,
	})
	if err != nil {
		return InvitationRecord{}, fmt.Errorf("authorize invitation scope: %w", err)
	}
	if len(authorized) == 0 {
		return InvitationRecord{}, ErrInvitationScopeDenied
	}
	exists, err := s.queries.InvitationEmailExists(ctx, dbgen.InvitationEmailExistsParams{
		InstitutionID: institutionID, Lower: invitation.Email,
	})
	if err != nil {
		return InvitationRecord{}, fmt.Errorf("check invitation email: %w", err)
	}
	if exists {
		return InvitationRecord{}, ErrInvitationAccountExists
	}
	row, err := s.queries.CreateInvitation(ctx, dbgen.CreateInvitationParams{
		InstitutionID: institutionID,
		Lower:         invitation.Email,
		Role:          invitation.Role,
		ScopeType:     invitation.ScopeType,
		FacultyID:     facultyID,
		DepartmentID:  departmentID,
		TokenHash:     tokenHash,
		ExpiresAt:     pgtype.Timestamptz{Time: expiresAt.UTC(), Valid: true},
		UserID:        userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return InvitationRecord{}, ErrInvitationScopeDenied
	}
	if err != nil {
		return InvitationRecord{}, fmt.Errorf("create scoped invitation: %w", err)
	}
	return InvitationRecord{ID: row.ID.String(), ExpiresAt: row.ExpiresAt.Time.UTC()}, nil
}

func (s *PostgresStore) AcceptInvitation(
	ctx context.Context,
	institutionIDText string,
	tokenHash []byte,
	now time.Time,
	passwordHash, displayName string,
) (Identity, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Identity{}, fmt.Errorf("begin invitation acceptance: %w", err)
	}
	defer tx.Rollback(ctx)
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return Identity{}, fmt.Errorf("parse invitation institution ID: %w", err)
	}
	queries := s.queries.WithTx(tx)
	invitation, err := queries.LockInvitationForAcceptance(ctx, dbgen.LockInvitationForAcceptanceParams{
		InstitutionID: institutionID,
		TokenHash:     tokenHash,
		ExpiresAt:     pgtype.Timestamptz{Time: now.UTC(), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, ErrInvalidInvitation
	}
	if err != nil {
		return Identity{}, fmt.Errorf("lock invitation: %w", err)
	}
	userID, err := queries.CreateInvitedUser(ctx, dbgen.CreateInvitedUserParams{
		InstitutionID:   invitation.InstitutionID,
		Email:           invitation.Email,
		PasswordHash:    passwordHash,
		DisplayName:     displayName,
		EmailVerifiedAt: pgtype.Timestamptz{Time: now.UTC(), Valid: true},
	})
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			return Identity{}, ErrInvitationAccountExists
		}
		return Identity{}, fmt.Errorf("create invited user: %w", err)
	}
	if err := queries.CreateInvitedRole(ctx, dbgen.CreateInvitedRoleParams{
		InstitutionID: invitation.InstitutionID,
		UserID:        userID,
		Role:          invitation.Role,
		ScopeType:     invitation.ScopeType,
		FacultyID:     invitation.FacultyID,
		DepartmentID:  invitation.DepartmentID,
	}); err != nil {
		return Identity{}, fmt.Errorf("assign invited role: %w", err)
	}
	affected, err := queries.MarkInvitationAccepted(ctx, dbgen.MarkInvitationAcceptedParams{
		InstitutionID: invitation.InstitutionID,
		ID:            invitation.ID,
		AcceptedAt:    pgtype.Timestamptz{Time: now.UTC(), Valid: true},
	})
	if err != nil {
		return Identity{}, fmt.Errorf("mark invitation accepted: %w", err)
	}
	if affected != 1 {
		return Identity{}, ErrInvalidInvitation
	}
	if err := queries.CreateInvitationAudit(ctx, dbgen.CreateInvitationAuditParams{
		InstitutionID: invitation.InstitutionID,
		ActorUserID:   userID,
		Column3:       invitation.Role,
		Column4:       invitation.ScopeType,
		Column5:       invitation.FacultyID,
		Column6:       invitation.DepartmentID,
	}); err != nil {
		return Identity{}, fmt.Errorf("audit invited role assignment: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Identity{}, fmt.Errorf("commit invitation acceptance: %w", err)
	}
	return Identity{InstitutionID: invitation.InstitutionID.String(), UserID: userID.String()}, nil
}

func optionalInvitationUUID(value string) (pgtype.UUID, error) {
	if value == "" {
		return pgtype.UUID{}, nil
	}
	parsed, err := db.ParseUUID(value)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("parse invitation scope ID: %w", err)
	}
	return parsed, nil
}

var _ InvitationRepository = (*PostgresStore)(nil)
