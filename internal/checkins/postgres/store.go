package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"attendance-api/internal/checkins"
	"attendance-api/internal/db"
	"attendance-api/internal/db/dbgen"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type PostgresStore struct {
	queries *dbgen.Queries
}

func NewPostgresStore(queries *dbgen.Queries) (*PostgresStore, error) {
	if queries == nil {
		return nil, errors.New("sqlc queries are required")
	}
	return &PostgresStore{queries: queries}, nil
}

func (s *PostgresStore) LoadSession(ctx context.Context, institutionIDText, sessionIDText string) (checkins.Session, bool, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return checkins.Session{}, false, err
	}
	sessionID, err := db.ParseUUID(sessionIDText)
	if err != nil {
		return checkins.Session{}, false, err
	}
	row, err := s.queries.LoadCheckinSession(ctx, dbgen.LoadCheckinSessionParams{
		InstitutionID: institutionID, ID: sessionID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return checkins.Session{}, false, nil
	}
	if err != nil {
		return checkins.Session{}, false, fmt.Errorf("select session for check-in: %w", err)
	}
	return checkins.Session{
		ID: row.ID.String(), EndedAt: db.TimestampPointer(row.EndedAt),
		StartedAt:    row.StartedAt.Time.UTC(),
		Venue:        venueCentre(row.Latitude, row.Longitude, row.RadiusMeters),
		RotatingCode: row.RotatingCode,
		StaticCode:   row.StaticCode.String, CodeExpiresAt: db.TimestampPointer(row.CodeExpiresAt),
		LateAfter: time.Duration(row.LateAfterMin) * time.Minute, RosterPolicy: checkins.RosterPolicy(row.RosterPolicy),
	}, true, nil
}

// venueCentre builds the optional venue position a session snapshotted at
// start. A venue with no stored coordinates yields nil, and location then
// plays no part in the check-in decision.
func venueCentre(latitude, longitude pgtype.Float8, radius pgtype.Int4) *checkins.VenueCentre {
	if !latitude.Valid || !longitude.Valid || !radius.Valid {
		return nil
	}
	return &checkins.VenueCentre{
		Latitude: latitude.Float64, Longitude: longitude.Float64,
		RadiusMeters: float64(radius.Int32),
	}
}

func (s *PostgresStore) IsRegistered(ctx context.Context, institutionIDText, sessionIDText, studentIDText string) (bool, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return false, err
	}
	sessionID, err := db.ParseUUID(sessionIDText)
	if err != nil {
		return false, err
	}
	studentID, err := db.ParseUUID(studentIDText)
	if err != nil {
		return false, err
	}
	registered, err := s.queries.IsStudentRegistered(ctx, dbgen.IsStudentRegisteredParams{
		InstitutionID: institutionID, ID: sessionID, StudentRecordID: studentID,
	})
	if err != nil {
		return false, fmt.Errorf("select registration state: %w", err)
	}
	return registered, nil
}

func (s *PostgresStore) HasCheckin(ctx context.Context, institutionIDText, sessionIDText, studentIDText string) (bool, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return false, err
	}
	sessionID, err := db.ParseUUID(sessionIDText)
	if err != nil {
		return false, err
	}
	studentID, err := db.ParseUUID(studentIDText)
	if err != nil {
		return false, err
	}
	exists, err := s.queries.HasWebCheckin(ctx, dbgen.HasWebCheckinParams{
		InstitutionID: institutionID, SessionID: sessionID, StudentRecordID: studentID,
	})
	if err != nil {
		return false, fmt.Errorf("select existing check-in: %w", err)
	}
	return exists, nil
}

func (s *PostgresStore) DeviceUsedInSession(ctx context.Context, institutionIDText, sessionIDText, studentIDText string, deviceHash []byte) (bool, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return false, err
	}
	sessionID, err := db.ParseUUID(sessionIDText)
	if err != nil {
		return false, err
	}
	studentID, err := db.ParseUUID(studentIDText)
	if err != nil {
		return false, err
	}
	used, err := s.queries.DeviceUsedInSession(ctx, dbgen.DeviceUsedInSessionParams{
		InstitutionID: institutionID, SessionID: sessionID,
		StudentRecordID: studentID, DeviceHash: deviceHash,
	})
	if err != nil {
		return false, fmt.Errorf("select device binding for session: %w", err)
	}
	return used, nil
}

func (s *PostgresStore) InsertCheckin(ctx context.Context, record checkins.CheckinRecord) (bool, error) {
	institutionID, err := db.ParseUUID(record.InstitutionID)
	if err != nil {
		return false, err
	}
	sessionID, err := db.ParseUUID(record.SessionID)
	if err != nil {
		return false, err
	}
	studentID, err := db.ParseUUID(record.StudentID)
	if err != nil {
		return false, err
	}
	params := dbgen.InsertWebCheckinParams{
		InstitutionID: institutionID, SessionID: sessionID, StudentRecordID: studentID,
		DeviceHash: record.DeviceHash, OnRoster: record.OnRoster, Status: record.Status,
	}
	if record.Fix != nil {
		params.Latitude = pgtype.Float8{Float64: record.Fix.Latitude, Valid: true}
		params.Longitude = pgtype.Float8{Float64: record.Fix.Longitude, Valid: true}
		params.AccuracyMeters = pgtype.Float8{Float64: record.Fix.AccuracyMeters, Valid: true}
	}
	if record.DistanceMeters != nil {
		params.DistanceMeters = pgtype.Float8{Float64: *record.DistanceMeters, Valid: true}
	}
	_, err = s.queries.InsertWebCheckin(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("insert web check-in: %w", err)
	}
	return true, nil
}
