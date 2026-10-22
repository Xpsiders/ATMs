package auth

import (
	"context"
	"errors"
	"fmt"

	"attendance-api/internal/db"
	"attendance-api/internal/rbac"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *PostgresStore) AssignRole(ctx context.Context, actor Identity, request RoleAssignmentRequest) error {
	tenant, err := db.ParseUUID(actor.InstitutionID)
	if err != nil {
		return fmt.Errorf("parse role assignment institution ID: %w", err)
	}
	actorID, err := db.ParseUUID(actor.UserID)
	if err != nil {
		return fmt.Errorf("parse role assignment actor ID: %w", err)
	}
	userID, err := db.ParseUUID(request.UserID)
	if err != nil {
		return ErrInvalidRoleAssignment
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin role assignment: %w", err)
	}
	defer tx.Rollback(ctx)
	var target rbac.Target
	target.InstitutionID = actor.InstitutionID
	var facultyID, departmentID pgtype.UUID
	switch request.ScopeType {
	case "faculty":
		facultyID, err = db.ParseUUID(request.FacultyID)
		if err != nil {
			return ErrInvalidRoleAssignment
		}
		target.FacultyID = request.FacultyID
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM faculties WHERE institution_id=$1 AND id=$2)`,
			tenant, facultyID).Scan(&exists); err != nil {
			return fmt.Errorf("validate role assignment faculty: %w", err)
		}
		if !exists {
			return ErrInvalidRoleAssignment
		}
	case "department":
		departmentID, err = db.ParseUUID(request.DepartmentID)
		if err != nil {
			return ErrInvalidRoleAssignment
		}
		if err := tx.QueryRow(ctx, `SELECT faculty_id::text FROM departments WHERE institution_id=$1 AND id=$2`,
			tenant, departmentID).Scan(&target.FacultyID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrInvalidRoleAssignment
			}
			return fmt.Errorf("validate role assignment department: %w", err)
		}
		target.DepartmentID = request.DepartmentID
	default:
		return ErrInvalidRoleAssignment
	}
	var userExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE institution_id=$1 AND id=$2 AND disabled_at IS NULL)`,
		tenant, userID).Scan(&userExists); err != nil {
		return fmt.Errorf("validate role assignment user: %w", err)
	}
	if !userExists {
		return ErrRoleAssignmentUserNotFound
	}
	assignments, err := signupAssignments(ctx, tx, tenant, actorID)
	if err != nil {
		return err
	}
	allowed := canAssignAdministratorRole(assignments, request.Role, target)
	if !allowed {
		return ErrRoleAssignmentDenied
	}
	_, err = tx.Exec(ctx, `INSERT INTO role_assignments(
		institution_id,user_id,role,scope_type,faculty_id,department_id
	) VALUES($1,$2,$3,$4,$5,$6)`, tenant, userID, request.Role, request.ScopeType, facultyID, departmentID)
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			return ErrRoleAssignmentExists
		}
		return fmt.Errorf("insert administrator role assignment: %w", err)
	}
	details := fmt.Sprintf(`{"role":%q,"scope_type":%q}`, request.Role, request.ScopeType)
	if _, err := tx.Exec(ctx, `INSERT INTO audit_log(institution_id,actor_user_id,action,resource_type,resource_id,details)
		VALUES($1,$2,'role_assignment.created','role_assignment',$3,$4::jsonb)`,
		tenant, actorID, request.UserID, details); err != nil {
		return fmt.Errorf("audit role assignment: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit role assignment: %w", err)
	}
	return nil
}

func canAssignAdministratorRole(assignments []rbac.Assignment, role string, target rbac.Target) bool {
	for _, assignment := range assignments {
		if assignment.Role == "institution_admin" && rbac.CanAccess(assignment, target, "", "write") {
			return true
		}
		if role == "department_admin" && assignment.Role == "faculty_admin" &&
			rbac.CanAccess(assignment, target, "", "write") {
			return true
		}
	}
	return false
}

var _ RoleAssignmentRepository = (*PostgresStore)(nil)
