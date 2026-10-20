package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"attendance-api/internal/db"
	"attendance-api/internal/imports"
	"attendance-api/internal/rbac"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("database pool is required")
	}
	return &Store{pool: pool}, nil
}

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func (s *Store) ValidateStudents(ctx context.Context, institutionID, userID string, students []imports.StudentRow) ([]imports.RowError, error) {
	tenant, actor, err := parseActor(institutionID, userID)
	if err != nil {
		return nil, err
	}
	return validateStudents(ctx, s.pool, tenant, actor, institutionID, students)
}

func (s *Store) CommitStudents(ctx context.Context, institutionID, userID string, students []imports.StudentRow) error {
	tenant, actor, err := parseActor(institutionID, userID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin student import: %w", err)
	}
	defer tx.Rollback(ctx)
	validationErrors, err := validateStudents(ctx, tx, tenant, actor, institutionID, students)
	if err != nil {
		return err
	}
	if len(validationErrors) > 0 {
		return imports.ErrImportChanged
	}
	for _, student := range students {
		department, err := db.ParseUUID(student.DepartmentID)
		if err != nil {
			return imports.ErrImportPreviewHasErrors
		}
		_, err = tx.Exec(ctx, `INSERT INTO student_records(
			institution_id,department_id,matric_no,email,display_name,level
		) VALUES($1,$2,$3,$4,$5,$6)`,
			tenant, department, student.MatricNo, student.Email, student.DisplayName, student.Level)
		if err != nil {
			var databaseError *pgconn.PgError
			if errors.As(err, &databaseError) && databaseError.Code == "23505" &&
				databaseError.ConstraintName == "student_records_institution_id_matric_no_key" {
				return imports.ErrDuplicateMatric
			}
			return fmt.Errorf("insert student record on CSV line %d: %w", student.Line, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit student import transaction: %w", err)
	}
	return nil
}

func validateStudents(
	ctx context.Context,
	dbQuery queryer,
	tenant, actor pgtype.UUID,
	institutionID string,
	students []imports.StudentRow,
) ([]imports.RowError, error) {
	departmentSet := make(map[string]pgtype.UUID, len(students))
	matricNumbers := make([]string, 0, len(students))
	for _, student := range students {
		department, err := db.ParseUUID(student.DepartmentID)
		if err != nil {
			continue
		}
		departmentSet[student.DepartmentID] = department
		matricNumbers = append(matricNumbers, strings.ToLower(student.MatricNo))
	}
	departmentIDs := make([]pgtype.UUID, 0, len(departmentSet))
	for _, department := range departmentSet {
		departmentIDs = append(departmentIDs, department)
	}
	departments := make(map[string]string, len(departmentIDs))
	if len(departmentIDs) > 0 {
		rows, err := dbQuery.Query(ctx, `SELECT id::text, faculty_id::text
			FROM departments WHERE institution_id=$1 AND id=ANY($2::uuid[])`, tenant, departmentIDs)
		if err != nil {
			return nil, fmt.Errorf("load student import departments: %w", err)
		}
		for rows.Next() {
			var id, facultyID string
			if err := rows.Scan(&id, &facultyID); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan student import department: %w", err)
			}
			departments[id] = facultyID
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read student import departments: %w", err)
		}
		rows.Close()
	}
	assignments, err := loadAssignments(ctx, dbQuery, tenant, actor)
	if err != nil {
		return nil, err
	}
	existingMatric := make(map[string]struct{})
	if len(matricNumbers) > 0 {
		rows, err := dbQuery.Query(ctx, `SELECT lower(matric_no)
			FROM student_records WHERE institution_id=$1 AND lower(matric_no)=ANY($2::text[])`,
			tenant, matricNumbers)
		if err != nil {
			return nil, fmt.Errorf("check imported matric numbers: %w", err)
		}
		for rows.Next() {
			var matric string
			if err := rows.Scan(&matric); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan existing matric number: %w", err)
			}
			existingMatric[matric] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read existing matric numbers: %w", err)
		}
		rows.Close()
	}

	validationErrors := make([]imports.RowError, 0)
	for _, student := range students {
		facultyID, departmentFound := departments[student.DepartmentID]
		if !departmentFound {
			validationErrors = append(validationErrors, imports.RowError{
				Line: student.Line, Column: "department_id", Message: "department does not exist in this institution",
			})
			continue
		}
		target := rbac.Target{
			InstitutionID: institutionID,
			FacultyID:     facultyID,
			DepartmentID:  student.DepartmentID,
		}
		allowed := false
		for _, assignment := range assignments {
			if rbac.CanAccess(assignment, target, "", "write") {
				allowed = true
				break
			}
		}
		if !allowed {
			validationErrors = append(validationErrors, imports.RowError{
				Line: student.Line, Column: "department_id", Message: "department is outside your import scope",
			})
		}
		if _, exists := existingMatric[strings.ToLower(student.MatricNo)]; exists {
			validationErrors = append(validationErrors, imports.RowError{
				Line: student.Line, Column: "matric_no", Message: "matric number already exists in this institution",
			})
		}
	}
	return validationErrors, nil
}

func loadAssignments(ctx context.Context, dbQuery queryer, tenant, actor pgtype.UUID) ([]rbac.Assignment, error) {
	rows, err := dbQuery.Query(ctx, `SELECT role,scope_type,institution_id,faculty_id,department_id
		FROM role_assignments WHERE institution_id=$1 AND user_id=$2`, tenant, actor)
	if err != nil {
		return nil, fmt.Errorf("load student import permissions: %w", err)
	}
	defer rows.Close()
	assignments := make([]rbac.Assignment, 0)
	for rows.Next() {
		var role, scope string
		var institution, faculty, department pgtype.UUID
		if err := rows.Scan(&role, &scope, &institution, &faculty, &department); err != nil {
			return nil, fmt.Errorf("scan student import permission: %w", err)
		}
		assignment := rbac.Assignment{Role: role, ScopeType: rbac.ScopeType(scope), InstitutionID: institution.String()}
		if faculty.Valid {
			assignment.FacultyID = faculty.String()
		}
		if department.Valid {
			assignment.DepartmentID = department.String()
		}
		assignments = append(assignments, assignment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read student import permissions: %w", err)
	}
	return assignments, nil
}

func parseActor(institutionID, userID string) (pgtype.UUID, pgtype.UUID, error) {
	tenant, err := db.ParseUUID(institutionID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("parse import institution ID: %w", err)
	}
	actor, err := db.ParseUUID(userID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("parse import user ID: %w", err)
	}
	return tenant, actor, nil
}

var _ imports.StudentStore = (*Store)(nil)
