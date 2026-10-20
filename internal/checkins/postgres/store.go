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
		StartedAt: row.StartedAt.Time.UTC(), Latitude: row.Latitude, Longitude: row.Longitude,
		RadiusMeters: float64(row.RadiusMeters), RotatingCode: row.RotatingCode,
		StaticCode: row.StaticCode.String, CodeExpiresAt: db.TimestampPointer(row.CodeExpiresAt),
		LateAfter: time.Duration(row.LateAfterMin) * time.Minute, RosterPolicy: checkins.RosterPolicy(row.RosterPolicy),
	}, true, nil
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
	_, err = s.queries.InsertWebCheckin(ctx, dbgen.InsertWebCheckinParams{
		InstitutionID: institutionID, SessionID: sessionID, StudentRecordID: studentID,
		Latitude: record.Latitude, Longitude: record.Longitude,
		AccuracyMeters: record.AccuracyMeters, DistanceMeters: record.DistanceMeters,
		DeviceHash: record.DeviceHash, OnRoster: record.OnRoster, Status: record.Status,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("insert web check-in: %w", err)
	}
	return true, nil
}
