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
)

type Store struct {
	queries *dbgen.Queries
}

func NewStore(queries *dbgen.Queries) (*Store, error) {
	if queries == nil {
		return nil, errors.New("SQL queries are required")
	}
	return &Store{queries: queries}, nil
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

func (s *Store) Review(ctx context.Context, institutionIDText, offeringIDText, studentIDText, status string, at time.Time) (string, error) {
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
	result, err := s.queries.SetCourseRegistrationStatus(ctx, dbgen.SetCourseRegistrationStatusParams{
		InstitutionID: institutionID, OfferingID: offeringID, StudentRecordID: studentID,
		Status: status, ApprovedAt: pgtype.Timestamptz{Time: at.UTC(), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", registration.ErrRegistrationNotFound
	}
	if err != nil {
		return "", fmt.Errorf("update course registration status: %w", err)
	}
	return result, nil
}

var _ registration.Store = (*Store)(nil)
