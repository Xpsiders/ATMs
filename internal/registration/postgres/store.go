package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"attendance-api/internal/db"
	"attendance-api/internal/db/dbgen"
	"attendance-api/internal/registration"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	queries *dbgen.Queries
	pool    *pgxpool.Pool
}

func NewStore(queries *dbgen.Queries, pools ...*pgxpool.Pool) (*Store, error) {
	if queries == nil {
		return nil, errors.New("SQL queries are required")
	}
	store := &Store{queries: queries}
	if len(pools) > 0 {
		store.pool = pools[0]
	}
	return store, nil
}

func (s *Store) RegistrationContext(ctx context.Context, institutionIDText, offeringIDText, studentIDText string) (registration.Context, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return registration.Context{}, err
	}
	offeringID, err := db.ParseUUID(offeringIDText)
	if err != nil {
		return registration.Context{}, err
	}
	studentID, err := db.ParseUUID(studentIDText)
	if err != nil {
		return registration.Context{}, registration.ErrOfferingNotFound
	}
	row, err := s.queries.LoadRegistrationContext(ctx, dbgen.LoadRegistrationContextParams{
		InstitutionID: institutionID, ID: offeringID, ID_2: studentID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return registration.Context{}, registration.ErrOfferingNotFound
	}
	if err != nil {
		return registration.Context{}, fmt.Errorf("load registration rules: %w", err)
	}
	return registration.Context{
		Mode: registration.Mode(row.RegistrationMode), Policy: registration.RosterPolicy(row.RosterPolicy),
		Registered: row.Registered, AutoTarget: row.AutoTarget,
	}, nil
}

func (s *Store) Submit(ctx context.Context, institutionIDText, offeringIDText, studentIDText, status string, at time.Time) (string, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return "", err
	}
	offeringID, err := db.ParseUUID(offeringIDText)
	if err != nil {
		return "", err
	}
	studentID, err := db.ParseUUID(studentIDText)
	if err != nil {
		return "", err
	}
	rowStatus, err := s.queries.CreateCourseRegistration(ctx, dbgen.CreateCourseRegistrationParams{
		InstitutionID: institutionID, OfferingID: offeringID, StudentRecordID: studentID,
		Status: status, Column5: pgtype.Timestamptz{Time: at.UTC(), Valid: true},
	})
	if err != nil {
		return "", fmt.Errorf("create course registration: %w", err)
	}
	return rowStatus, nil
}

func (s *Store) OfferingAccess(ctx context.Context, institutionIDText, offeringIDText, userIDText string) (registration.Access, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return registration.Access{}, err
	}
	offeringID, err := db.ParseUUID(offeringIDText)
	if err != nil {
		return registration.Access{}, err
	}
	userID, err := db.ParseUUID(userIDText)
	if err != nil {
		return registration.Access{}, err
	}
	row, err := s.queries.OfferingRegistrationAccess(ctx, dbgen.OfferingRegistrationAccessParams{
		InstitutionID: institutionID, ID: offeringID, LecturerUserID: userID,
	})
	if err != nil {
		return registration.Access{}, fmt.Errorf("resolve offering registration access: %w", err)
	}
	access := registration.Access{CanView: row.CanView}
	if row.DepartmentFilter.Valid {
		access.DepartmentFilter = row.DepartmentFilter.String()
	}
	return access, nil
}

func (s *Store) List(ctx context.Context, institutionIDText, offeringIDText, status, departmentIDText string, limit, offset int) ([]registration.Registration, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return nil, err
	}
	offeringID, err := db.ParseUUID(offeringIDText)
	if err != nil {
		return nil, err
	}
	statusFilter := pgtype.Text{}
	if status != "" {
		statusFilter = pgtype.Text{String: status, Valid: true}
	}
	departmentFilter := pgtype.UUID{}
	if departmentIDText != "" {
		departmentFilter, err = db.ParseUUID(departmentIDText)
		if err != nil {
			return nil, err
		}
	}
	rows, err := s.queries.ListOfferingRegistrations(ctx, dbgen.ListOfferingRegistrationsParams{
		InstitutionID: institutionID, OfferingID: offeringID,
		Status: statusFilter, DepartmentID: departmentFilter,
		PageSize: int32(limit), PageOffset: int32(offset),
	})
	if err != nil {
		return nil, fmt.Errorf("list offering registrations: %w", err)
	}
	items := make([]registration.Registration, 0, len(rows))
	for _, row := range rows {
		item := registration.Registration{
			StudentID: row.StudentRecordID.String(), MatricNo: row.MatricNo,
			DisplayName: row.DisplayName, DepartmentID: row.DepartmentID.String(),
			Level: int(row.Level), Status: row.Status, CreatedAt: row.CreatedAt.Time.UTC(),
		}
		if row.ApprovedAt.Valid {
			at := row.ApprovedAt.Time.UTC()
			item.ApprovedAt = &at
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) CanReview(ctx context.Context, institutionIDText, offeringIDText, studentIDText, userIDText string) (bool, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return false, err
	}
	offeringID, err := db.ParseUUID(offeringIDText)
	if err != nil {
		return false, err
	}
	studentID, err := db.ParseUUID(studentIDText)
	if err != nil {
		return false, nil
	}
	userID, err := db.ParseUUID(userIDText)
	if err != nil {
		return false, err
	}
	allowed, err := s.queries.CanManageStudentRegistration(ctx, dbgen.CanManageStudentRegistrationParams{
		InstitutionID: institutionID, ID: offeringID, ID_2: studentID, CoordinatorUserID: userID,
	})
	if err != nil {
		return false, fmt.Errorf("authorize registration review: %w", err)
	}
	return allowed, nil
}

func (s *Store) Review(ctx context.Context, institutionIDText, offeringIDText, studentIDText, userIDText, status string, at time.Time) (string, error) {
	if s.pool == nil {
		return "", errors.New("registration audit requires a database pool")
	}
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return "", err
	}
	offeringID, err := db.ParseUUID(offeringIDText)
	if err != nil {
		return "", err
	}
	studentID, err := db.ParseUUID(studentIDText)
	if err != nil {
		return "", registration.ErrRegistrationNotFound
	}
	userID, err := db.ParseUUID(userIDText)
	if err != nil {
		return "", err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin registration review: %w", err)
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	allowed, err := queries.CanManageStudentRegistration(ctx, dbgen.CanManageStudentRegistrationParams{
		InstitutionID: institutionID, ID: offeringID, ID_2: studentID, CoordinatorUserID: userID,
	})
	if err != nil {
		return "", fmt.Errorf("authorize registration review: %w", err)
	}
	if !allowed {
		return "", registration.ErrAccessDenied
	}
	result, err := queries.SetCourseRegistrationStatus(ctx, dbgen.SetCourseRegistrationStatusParams{
		InstitutionID: institutionID, OfferingID: offeringID, StudentRecordID: studentID,
		Status: status, ApprovedAt: pgtype.Timestamptz{Time: at.UTC(), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", registration.ErrRegistrationNotFound
	}
	if err != nil {
		return "", fmt.Errorf("update course registration status: %w", err)
	}
	details := []byte(fmt.Sprintf(`{"status":%q}`, status))
	if err := queries.InsertAuditLog(ctx, dbgen.InsertAuditLogParams{
		InstitutionID: institutionID, ActorUserID: userID, Action: "registration.reviewed",
		ResourceType: "course_registration", ResourceID: offeringID.String() + ":" + studentID.String(),
		Details: details, CreatedAt: pgtype.Timestamptz{Time: at.UTC(), Valid: true},
	}); err != nil {
		return "", fmt.Errorf("audit registration review: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit registration review: %w", err)
	}
	return result, nil
}

func (s *Store) PendingApprovals(ctx context.Context, institutionIDText, userIDText string, limit, offset int) ([]registration.ApprovalRequest, error) {
	tenant, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return nil, err
	}
	user, err := db.ParseUUID(userIDText)
	if err != nil {
		return nil, err
	}
	if s.pool == nil {
		return nil, errors.New("pending approval listing requires a database pool")
	}
	rows, err := s.pool.Query(ctx, `SELECT r.offering_id::text, c.code, c.title, r.student_record_id::text,
		sr.matric_no, sr.display_name, sr.department_id::text, sr.level, r.status, r.created_at
		FROM course_registrations r
		JOIN course_offerings o ON o.institution_id=r.institution_id AND o.id=r.offering_id
		JOIN courses c ON c.institution_id=o.institution_id AND c.id=o.course_id
		JOIN student_records sr ON sr.institution_id=r.institution_id AND sr.id=r.student_record_id
		JOIN departments d ON d.institution_id=c.institution_id AND d.id=c.department_id
		WHERE r.institution_id=$1 AND r.status='pending' AND EXISTS (
			SELECT 1 FROM offering_lecturers ol WHERE ol.institution_id=o.institution_id
			AND ol.offering_id=o.id AND ol.lecturer_user_id=$2
			UNION ALL
			SELECT 1 WHERE o.coordinator_user_id=$2
			UNION ALL
			SELECT 1 FROM role_assignments ra WHERE ra.institution_id=o.institution_id AND ra.user_id=$2
			AND ((ra.role='department_admin' AND ra.scope_type='department' AND ra.department_id=d.id)
			OR (ra.role='faculty_admin' AND ra.scope_type='faculty' AND ra.faculty_id=d.faculty_id)
			OR (ra.role='institution_admin' AND ra.scope_type='institution') OR ra.role='platform_admin'))
		ORDER BY r.created_at, r.offering_id, sr.matric_no LIMIT $3 OFFSET $4`,
		tenant, user, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query pending registration approvals: %w", err)
	}
	defer rows.Close()
	items := make([]registration.ApprovalRequest, 0)
	for rows.Next() {
		var item registration.ApprovalRequest
		if err := rows.Scan(&item.OfferingID, &item.CourseCode, &item.CourseTitle, &item.StudentID,
			&item.MatricNo, &item.DisplayName, &item.DepartmentID, &item.Level, &item.Status, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan pending registration approval: %w", err)
		}
		item.CreatedAt = item.CreatedAt.UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending registration approvals: %w", err)
	}
	return items, nil
}

var _ registration.Store = (*Store)(nil)
var _ registration.ApprovalStore = (*Store)(nil)
