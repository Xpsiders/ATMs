package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
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
	pool           *pgxpool.Pool
	invitationBase string
}

func (s *Store) SetInvitationBaseURL(baseURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("invitation base URL must be absolute")
	}
	s.invitationBase = strings.TrimRight(baseURL, "/")
	return nil
}

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("database pool is required")
	}
	return &Store{pool: pool}, nil
}

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
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

func (s *Store) ValidateStaff(ctx context.Context, institutionID, userID string, staff []imports.StaffRow) ([]imports.RowError, error) {
	tenant, actor, err := parseActor(institutionID, userID)
	if err != nil {
		return nil, err
	}
	return validateStaff(ctx, s.pool, tenant, actor, institutionID, staff)
}

func (s *Store) CommitStaff(ctx context.Context, institutionID, userID string, staff []imports.StaffRow) error {
	tenant, actor, err := parseActor(institutionID, userID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin staff import: %w", err)
	}
	defer tx.Rollback(ctx)
	validationErrors, err := validateStaff(ctx, tx, tenant, actor, institutionID, staff)
	if err != nil {
		return err
	}
	if len(validationErrors) > 0 {
		return imports.ErrImportChanged
	}
	for _, row := range staff {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(lower($1), 0))`, row.Email); err != nil {
			return fmt.Errorf("lock staff invitation email: %w", err)
		}
		var emailExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM users WHERE institution_id=$1 AND lower(email)=lower($2)
			UNION ALL SELECT 1 FROM invitations WHERE institution_id=$1 AND lower(email)=lower($2) AND accepted_at IS NULL
			UNION ALL SELECT 1 FROM staff_records WHERE institution_id=$1 AND lower(email)=lower($2)
		)`, tenant, row.Email).Scan(&emailExists); err != nil {
			return fmt.Errorf("recheck staff invitation email: %w", err)
		}
		if emailExists {
			return imports.ErrImportChanged
		}
		department, err := db.ParseUUID(row.DepartmentID)
		if err != nil {
			return imports.ErrImportPreviewHasErrors
		}
		var recordID pgtype.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO staff_records(institution_id,department_id,staff_no,email,display_name)
			VALUES($1,$2,$3,lower($4),$5) RETURNING id`, tenant, department, row.StaffNo, row.Email, row.DisplayName).Scan(&recordID); err != nil {
			var databaseError *pgconn.PgError
			if errors.As(err, &databaseError) && databaseError.Code == "23505" &&
				databaseError.ConstraintName == "staff_records_institution_id_staff_no_key" {
				return imports.ErrDuplicateStaff
			}
			return fmt.Errorf("insert staff record on CSV line %d: %w", row.Line, err)
		}
		var tokenBytes [32]byte
		if _, err := rand.Read(tokenBytes[:]); err != nil {
			return fmt.Errorf("generate staff invitation token: %w", err)
		}
		token := base64.RawURLEncoding.EncodeToString(tokenBytes[:])
		tokenHash := sha256.Sum256([]byte(token))
		var invitationID pgtype.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO invitations(
			institution_id,email,role,scope_type,department_id,token_hash,expires_at
		) VALUES($1,lower($2),'lecturer','department',$3,$4,now()+interval '7 days') RETURNING id`,
			tenant, row.Email, department, tokenHash[:]).Scan(&invitationID); err != nil {
			return fmt.Errorf("create lecturer invitation on CSV line %d: %w", row.Line, err)
		}
		link := s.invitationBase + "/accept-invitation?institution_id=" +
			url.QueryEscape(institutionID) + "&token=" + url.QueryEscape(token)
		if s.invitationBase == "" {
			link = "/accept-invitation?institution_id=" + url.QueryEscape(institutionID) +
				"&token=" + url.QueryEscape(token)
		}
		body, err := json.Marshal(map[string]string{"staff_no": row.StaffNo, "department_id": row.DepartmentID})
		if err != nil {
			return fmt.Errorf("encode staff import audit: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_log(institution_id,actor_user_id,action,resource_type,resource_id,details)
			VALUES($1,$2,'staff.imported','staff_record',$3,$4::jsonb)`, tenant, actor, recordID, body); err != nil {
			return fmt.Errorf("audit staff import: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_log(institution_id,actor_user_id,action,resource_type,resource_id,details)
			VALUES($1,$2,'invitation.created','invitation',$3,jsonb_build_object('role','lecturer','scope_type','department','department_id',$4::text))`,
			tenant, actor, invitationID, department); err != nil {
			return fmt.Errorf("audit staff invitation: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO email_outbox(institution_id,recipient,subject,body)
			VALUES($1,lower($2),'Campus Attendance staff invitation',$3)`, tenant, row.Email,
			fmt.Sprintf("Open %s to create your lecturer account.", link)); err != nil {
			return fmt.Errorf("enqueue lecturer invitation: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit staff import transaction: %w", err)
	}
	return nil
}

func validateStaff(
	ctx context.Context,
	dbQuery queryer,
	tenant, actor pgtype.UUID,
	institutionID string,
	staff []imports.StaffRow,
) ([]imports.RowError, error) {
	departmentIDs := make([]pgtype.UUID, 0, len(staff))
	for _, row := range staff {
		if department, err := db.ParseUUID(row.DepartmentID); err == nil {
			departmentIDs = append(departmentIDs, department)
		}
	}
	departments := make(map[string]string, len(departmentIDs))
	if len(departmentIDs) > 0 {
		rows, err := dbQuery.Query(ctx, `SELECT id::text,faculty_id::text FROM departments
			WHERE institution_id=$1 AND id=ANY($2::uuid[])`, tenant, departmentIDs)
		if err != nil {
			return nil, fmt.Errorf("load staff import departments: %w", err)
		}
		for rows.Next() {
			var id, facultyID string
			if err := rows.Scan(&id, &facultyID); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan staff import department: %w", err)
			}
			departments[id] = facultyID
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read staff import departments: %w", err)
		}
		rows.Close()
	}
	assignments, err := loadAssignments(ctx, dbQuery, tenant, actor)
	if err != nil {
		return nil, err
	}
	result := make([]imports.RowError, 0)
	for _, row := range staff {
		facultyID, found := departments[row.DepartmentID]
		if !found {
			result = append(result, imports.RowError{Line: row.Line, Column: "department_id", Message: "department does not exist in this institution"})
			continue
		}
		target := rbac.Target{InstitutionID: institutionID, FacultyID: facultyID, DepartmentID: row.DepartmentID}
		allowed := false
		for _, assignment := range assignments {
			if rbac.CanAccess(assignment, target, "", "write") {
				allowed = true
				break
			}
		}
		if !allowed {
			result = append(result, imports.RowError{Line: row.Line, Column: "department_id", Message: "department is outside your import scope"})
			continue
		}
		var staffExists, emailExists bool
		if err := dbQuery.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM staff_records
			WHERE institution_id=$1 AND lower(staff_no)=lower($2))`, tenant, row.StaffNo).Scan(&staffExists); err != nil {
			return nil, fmt.Errorf("check imported staff number: %w", err)
		}
		if staffExists {
			result = append(result, imports.RowError{Line: row.Line, Column: "staff_no", Message: "staff number already exists in this institution"})
		}
		if err := dbQuery.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM users WHERE institution_id=$1 AND lower(email)=lower($2)
			UNION ALL SELECT 1 FROM invitations WHERE institution_id=$1 AND lower(email)=lower($2) AND accepted_at IS NULL
			UNION ALL SELECT 1 FROM staff_records WHERE institution_id=$1 AND lower(email)=lower($2)
		)`, tenant, row.Email).Scan(&emailExists); err != nil {
			return nil, fmt.Errorf("check imported staff email: %w", err)
		}
		if emailExists {
			result = append(result, imports.RowError{Line: row.Line, Column: "email", Message: "email already has a staff record, account, or invitation"})
		}
	}
	return result, nil
}

var _ imports.StudentStore = (*Store)(nil)
var _ imports.StaffStore = (*Store)(nil)
