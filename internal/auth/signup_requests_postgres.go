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

type signupQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *PostgresStore) ListSignupRequests(ctx context.Context, identity Identity, limit, offset int) ([]SignupRequest, error) {
	tenant, err := db.ParseUUID(identity.InstitutionID)
	if err != nil {
		return nil, fmt.Errorf("parse signup institution ID: %w", err)
	}
	actor, err := db.ParseUUID(identity.UserID)
	if err != nil {
		return nil, fmt.Errorf("parse signup reviewer ID: %w", err)
	}
	departments, err := manageableSignupDepartments(ctx, s.pool, tenant, actor, identity.InstitutionID)
	if err != nil {
		return nil, err
	}
	if len(departments) == 0 {
		return nil, ErrSignupRequestScopeDenied
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,matric_no,email,department_id::text,created_at
		FROM signup_requests WHERE institution_id=$1 AND status='pending' AND department_id=ANY($2::uuid[])
		ORDER BY created_at,id LIMIT $3 OFFSET $4`, tenant, departments, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query pending signup requests: %w", err)
	}
	defer rows.Close()
	items := make([]SignupRequest, 0, limit)
	for rows.Next() {
		var item SignupRequest
		if err := rows.Scan(&item.ID, &item.MatricNo, &item.Email, &item.DepartmentID, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan signup request: %w", err)
		}
		item.CreatedAt = item.CreatedAt.UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read signup requests: %w", err)
	}
	return items, nil
}

func (s *PostgresStore) ReviewSignupRequest(
	ctx context.Context,
	identity Identity,
	requestIDText, status, departmentIDText string,
	level int,
	programmeIDText, displayName string,
) error {
	tenant, err := db.ParseUUID(identity.InstitutionID)
	if err != nil {
		return fmt.Errorf("parse signup institution ID: %w", err)
	}
	actor, err := db.ParseUUID(identity.UserID)
	if err != nil {
		return fmt.Errorf("parse signup reviewer ID: %w", err)
	}
	requestID, err := db.ParseUUID(requestIDText)
	if err != nil {
		return ErrSignupRequestNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin signup review: %w", err)
	}
	defer tx.Rollback(ctx)
	var matricNo, email string
	var requestDepartment pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT matric_no,email,department_id FROM signup_requests
		WHERE institution_id=$1 AND id=$2 AND status='pending' FOR UPDATE`, tenant, requestID).
		Scan(&matricNo, &email, &requestDepartment)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSignupRequestNotFound
	}
	if err != nil {
		return fmt.Errorf("lock signup request: %w", err)
	}
	if !requestDepartment.Valid {
		return ErrSignupRequestScopeDenied
	}
	allowed, err := canManageSignupDepartment(ctx, tx, tenant, actor, identity.InstitutionID, requestDepartment.String())
	if err != nil {
		return err
	}
	if !allowed {
		return ErrSignupRequestScopeDenied
	}
	details := []byte(`{"status":"` + status + `"}`)
	if status == "approved" {
		departmentID, err := db.ParseUUID(departmentIDText)
		if err != nil || departmentID != requestDepartment {
			return ErrSignupRequestScopeDenied
		}
		programmeID, err := db.ParseUUID(programmeIDText)
		if err != nil {
			return ErrInvalidSignupReview
		}
		var programmeExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM programmes
			WHERE institution_id=$1 AND department_id=$2 AND id=$3)`, tenant, departmentID, programmeID).
			Scan(&programmeExists); err != nil {
			return fmt.Errorf("validate signup programme: %w", err)
		}
		if !programmeExists {
			return ErrInvalidSignupReview
		}
		_, err = tx.Exec(ctx, `INSERT INTO student_records(institution_id,department_id,programme_id,
			matric_no,email,display_name,level) VALUES($1,$2,$3,$4,lower($5),$6,$7)`,
			tenant, departmentID, programmeID, matricNo, email, displayName, level)
		if err != nil {
			var databaseError *pgconn.PgError
			if errors.As(err, &databaseError) && databaseError.Code == "23505" {
				return ErrSignupStudentExists
			}
			return fmt.Errorf("create approved student record: %w", err)
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE signup_requests SET status=$3,reviewed_by_user_id=$4,reviewed_at=now()
		WHERE institution_id=$1 AND id=$2 AND status='pending'`, tenant, requestID, status, actor)
	if err != nil {
		return fmt.Errorf("update signup request status: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrSignupRequestNotFound
	}
	action := "signup_request." + status
	if _, err := tx.Exec(ctx, `INSERT INTO audit_log(institution_id,actor_user_id,action,resource_type,resource_id,details)
		VALUES($1,$2,$3,'signup_request',$4,$5::jsonb)`, tenant, actor, action, requestID, string(details)); err != nil {
		return fmt.Errorf("audit signup request review: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit signup review: %w", err)
	}
	return nil
}

func manageableSignupDepartments(
	ctx context.Context,
	queryer signupQueryer,
	tenant, actor pgtype.UUID,
	institutionID string,
) ([]pgtype.UUID, error) {
	assignments, err := signupAssignments(ctx, queryer, tenant, actor)
	if err != nil {
		return nil, err
	}
	rows, err := queryer.Query(ctx, `SELECT id::text,faculty_id::text FROM departments WHERE institution_id=$1`, tenant)
	if err != nil {
		return nil, fmt.Errorf("load departments for signup scope: %w", err)
	}
	defer rows.Close()
	departments := make([]pgtype.UUID, 0)
	for rows.Next() {
		var departmentID, facultyID string
		if err := rows.Scan(&departmentID, &facultyID); err != nil {
			return nil, fmt.Errorf("scan department for signup scope: %w", err)
		}
		target := rbac.Target{InstitutionID: institutionID, DepartmentID: departmentID, FacultyID: facultyID}
		for _, assignment := range assignments {
			if rbac.CanAccess(assignment, target, "", "write") {
				parsed, err := db.ParseUUID(departmentID)
				if err != nil {
					return nil, fmt.Errorf("parse department for signup scope: %w", err)
				}
				departments = append(departments, parsed)
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read departments for signup scope: %w", err)
	}
	return departments, nil
}

func canManageSignupDepartment(
	ctx context.Context,
	queryer signupQueryer,
	tenant, actor pgtype.UUID,
	institutionID, departmentID string,
) (bool, error) {
	var facultyID string
	err := queryer.QueryRow(ctx, `SELECT faculty_id::text FROM departments
		WHERE institution_id=$1 AND id=$2`, tenant, departmentID).Scan(&facultyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load signup department: %w", err)
	}
	assignments, err := signupAssignments(ctx, queryer, tenant, actor)
	if err != nil {
		return false, err
	}
	target := rbac.Target{InstitutionID: institutionID, FacultyID: facultyID, DepartmentID: departmentID}
	for _, assignment := range assignments {
		if rbac.CanAccess(assignment, target, "", "write") {
			return true, nil
		}
	}
	return false, nil
}

func signupAssignments(ctx context.Context, queryer signupQueryer, tenant, actor pgtype.UUID) ([]rbac.Assignment, error) {
	rows, err := queryer.Query(ctx, `SELECT role,scope_type,institution_id,faculty_id,department_id
		FROM role_assignments WHERE institution_id=$1 AND user_id=$2`, tenant, actor)
	if err != nil {
		return nil, fmt.Errorf("load signup review permissions: %w", err)
	}
	defer rows.Close()
	assignments := make([]rbac.Assignment, 0)
	for rows.Next() {
		var role, scope string
		var institutionID pgtype.UUID
		var facultyID, departmentID pgtype.UUID
		if err := rows.Scan(&role, &scope, &institutionID, &facultyID, &departmentID); err != nil {
			return nil, fmt.Errorf("scan signup review permission: %w", err)
		}
		assignment := rbac.Assignment{
			Role: role, ScopeType: rbac.ScopeType(scope), InstitutionID: institutionID.String(),
		}
		if facultyID.Valid {
			assignment.FacultyID = facultyID.String()
		}
		if departmentID.Valid {
			assignment.DepartmentID = departmentID.String()
		}
		assignments = append(assignments, assignment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read signup review permissions: %w", err)
	}
	return assignments, nil
}

var _ SignupRequestRepository = (*PostgresStore)(nil)
