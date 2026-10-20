package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"attendance-api/internal/db"
	"attendance-api/internal/db/dbgen"
	"attendance-api/internal/sessions"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	queries *dbgen.Queries
	pool    *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool, queries *dbgen.Queries) (*PostgresStore, error) {
	if pool == nil || queries == nil {
		return nil, errors.New("database pool and sqlc queries are required")
	}
	return &PostgresStore{pool: pool, queries: queries}, nil
}

func (s *PostgresStore) Create(ctx context.Context, input sessions.StartInput, startedAt time.Time, staticCode string, expiresAt *time.Time) (sessions.Session, error) {
	institutionID, err := db.ParseUUID(input.InstitutionID)
	if err != nil {
		return sessions.Session{}, err
	}
	offeringID, err := db.ParseUUID(input.OfferingID)
	if err != nil {
		return sessions.Session{}, err
	}
	venueID, err := db.ParseUUID(input.VenueID)
	if err != nil {
		return sessions.Session{}, err
	}
	startedBy, err := db.ParseUUID(input.StartedBy)
	if err != nil {
		return sessions.Session{}, err
	}
	timetableID := pgtype.UUID{}
	if input.TimetableID != "" {
		timetableID, err = db.ParseUUID(input.TimetableID)
		if err != nil {
			return sessions.Session{}, err
		}
		matches, err := s.queries.IsTimetableForOfferingVenue(ctx, dbgen.IsTimetableForOfferingVenueParams{
			InstitutionID: institutionID, ID: timetableID, OfferingID: offeringID, VenueID: venueID,
		})
		if err != nil {
			return sessions.Session{}, fmt.Errorf("validate session timetable: %w", err)
		}
		if !matches {
			return sessions.Session{}, sessions.ErrInvalidSession
		}
	}
	staticValue := pgtype.Text{}
	expiryValue := pgtype.Timestamptz{}
	if !input.RotatingCode {
		if expiresAt == nil || staticCode == "" {
			return sessions.Session{}, errors.New("static session code and expiry are required")
		}
		staticValue = pgtype.Text{String: staticCode, Valid: true}
		expiryValue = pgtype.Timestamptz{Time: expiresAt.UTC(), Valid: true}
	}
	row, err := s.queries.CreateLectureSession(ctx, dbgen.CreateLectureSessionParams{
		InstitutionID: institutionID, OfferingID: offeringID, VenueID: venueID, TimetableID: timetableID,
		StartedByUserID: startedBy, StartedAt: pgtype.Timestamptz{Time: startedAt.UTC(), Valid: true},
		Latitude: input.Latitude, Longitude: input.Longitude, RadiusMeters: int32(input.RadiusMeters),
		RotatingCode: input.RotatingCode, StaticCode: staticValue, CodeExpiresAt: expiryValue,
		LateAfterMin: int32(input.LateAfter / time.Minute),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		open, openErr := s.queries.FindActiveSessionAtVenue(ctx, dbgen.FindActiveSessionAtVenueParams{
			InstitutionID: institutionID, VenueID: venueID,
		})
		if openErr != nil {
			return sessions.Session{}, fmt.Errorf("find conflicting venue session: %w", openErr)
		}
		return sessions.Session{}, &sessions.OpenVenueError{SessionID: open.ID.String()}
	}
	if err != nil {
		return sessions.Session{}, fmt.Errorf("insert lecture session: %w", err)
	}
	return sessions.Session{
		ID: row.ID.String(), InstitutionID: row.InstitutionID.String(), OfferingID: row.OfferingID.String(),
		VenueID: row.VenueID.String(), StartedAt: row.StartedAt.Time.UTC(), EndedAt: db.TimestampPointer(row.EndedAt),
		Latitude: row.Latitude, Longitude: row.Longitude, RadiusMeters: float64(row.RadiusMeters),
		RotatingCode: row.RotatingCode, StaticCode: row.StaticCode.String,
		CodeExpiresAt: db.TimestampPointer(row.CodeExpiresAt), LateAfter: time.Duration(row.LateAfterMin) * time.Minute,
		LateAfterMin: int64(row.LateAfterMin),
	}, nil
}

func (s *PostgresStore) Get(ctx context.Context, institutionIDText, sessionIDText string) (sessions.Session, bool, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return sessions.Session{}, false, err
	}
	sessionID, err := db.ParseUUID(sessionIDText)
	if err != nil {
		return sessions.Session{}, false, err
	}
	row, err := s.queries.GetLectureSession(ctx, dbgen.GetLectureSessionParams{
		InstitutionID: institutionID, ID: sessionID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Session{}, false, nil
	}
	if err != nil {
		return sessions.Session{}, false, fmt.Errorf("select lecture session: %w", err)
	}
	return sessionFromGetRow(row), true, nil
}

func (s *PostgresStore) End(ctx context.Context, institutionIDText, sessionIDText string, endedAt time.Time) (sessions.Session, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return sessions.Session{}, err
	}
	sessionID, err := db.ParseUUID(sessionIDText)
	if err != nil {
		return sessions.Session{}, err
	}
	row, err := s.queries.EndLectureSession(ctx, dbgen.EndLectureSessionParams{
		InstitutionID: institutionID, ID: sessionID,
		EndedAt: pgtype.Timestamptz{Time: endedAt.UTC(), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, found, getErr := s.Get(ctx, institutionIDText, sessionIDText)
		if getErr != nil {
			return sessions.Session{}, getErr
		}
		if !found {
			return sessions.Session{}, sessions.ErrSessionNotFound
		}
		if existing.EndedAt != nil {
			return sessions.Session{}, sessions.ErrSessionEnded
		}
		return sessions.Session{}, errors.New("session could not be ended")
	}
	if err != nil {
		return sessions.Session{}, fmt.Errorf("update lecture session: %w", err)
	}
	return sessions.Session{
		ID: row.ID.String(), InstitutionID: row.InstitutionID.String(), OfferingID: row.OfferingID.String(),
		VenueID: row.VenueID.String(), StartedAt: row.StartedAt.Time.UTC(), EndedAt: db.TimestampPointer(row.EndedAt),
		Latitude: row.Latitude, Longitude: row.Longitude, RadiusMeters: float64(row.RadiusMeters),
		RotatingCode: row.RotatingCode, StaticCode: row.StaticCode.String,
		CodeExpiresAt: db.TimestampPointer(row.CodeExpiresAt), LateAfter: time.Duration(row.LateAfterMin) * time.Minute,
		LateAfterMin: int64(row.LateAfterMin),
	}, nil
}

func (s *PostgresStore) ConvertToFixedCode(ctx context.Context, institutionIDText, sessionIDText, actorUserIDText, code string, changedAt time.Time) (sessions.Session, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return sessions.Session{}, err
	}
	sessionID, err := db.ParseUUID(sessionIDText)
	if err != nil {
		return sessions.Session{}, err
	}
	actorID, err := db.ParseUUID(actorUserIDText)
	if err != nil {
		return sessions.Session{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sessions.Session{}, fmt.Errorf("begin fixed-code conversion: %w", err)
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	row, err := queries.ConvertRotatingSessionToFixed(ctx, dbgen.ConvertRotatingSessionToFixedParams{
		InstitutionID: institutionID, ID: sessionID,
		StaticCode: pgtype.Text{String: code, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, found, getErr := s.Get(ctx, institutionIDText, sessionIDText)
		if getErr != nil {
			return sessions.Session{}, getErr
		}
		if !found {
			return sessions.Session{}, sessions.ErrSessionNotFound
		}
		if existing.EndedAt != nil {
			return sessions.Session{}, sessions.ErrSessionEnded
		}
		return sessions.Session{}, sessions.ErrCodeAlreadyFixed
	}
	if err != nil {
		return sessions.Session{}, fmt.Errorf("convert session code: %w", err)
	}
	if err := queries.InsertAuditLog(ctx, dbgen.InsertAuditLogParams{
		InstitutionID: institutionID, ActorUserID: actorID,
		Action: "session.code.fixed", ResourceType: "lecture_session",
		ResourceID: sessionID.String(), Details: []byte(`{"mode":"fixed"}`),
		CreatedAt: pgtype.Timestamptz{Time: changedAt.UTC(), Valid: true},
	}); err != nil {
		return sessions.Session{}, fmt.Errorf("audit fixed-code conversion: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sessions.Session{}, fmt.Errorf("commit fixed-code conversion: %w", err)
	}
	return sessions.Session{
		ID: row.ID.String(), InstitutionID: row.InstitutionID.String(), OfferingID: row.OfferingID.String(),
		VenueID: row.VenueID.String(), StartedAt: row.StartedAt.Time.UTC(), EndedAt: db.TimestampPointer(row.EndedAt),
		Latitude: row.Latitude, Longitude: row.Longitude, RadiusMeters: float64(row.RadiusMeters),
		RotatingCode: row.RotatingCode, StaticCode: row.StaticCode.String,
		CodeExpiresAt: db.TimestampPointer(row.CodeExpiresAt), LateAfter: time.Duration(row.LateAfterMin) * time.Minute,
		LateAfterMin: int64(row.LateAfterMin),
	}, nil
}

func (s *PostgresStore) MarkScannerUnavailable(ctx context.Context, institutionIDText, sessionIDText, actorUserIDText, note string, changedAt time.Time) error {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return err
	}
	sessionID, err := db.ParseUUID(sessionIDText)
	if err != nil {
		return err
	}
	actorID, err := db.ParseUUID(actorUserIDText)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin scanner status update: %w", err)
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	createdAt := pgtype.Timestamptz{Time: changedAt.UTC(), Valid: true}
	if err := queries.SetScannerUnavailable(ctx, dbgen.SetScannerUnavailableParams{
		InstitutionID: institutionID, SessionID: sessionID, Note: note,
		ChangedByUserID: actorID, CreatedAt: createdAt,
	}); err != nil {
		return fmt.Errorf("write scanner unavailable status: %w", err)
	}
	details, err := json.Marshal(map[string]string{"state": "unavailable", "note": note})
	if err != nil {
		return fmt.Errorf("encode scanner status audit details: %w", err)
	}
	if err := queries.InsertAuditLog(ctx, dbgen.InsertAuditLogParams{
		InstitutionID: institutionID, ActorUserID: actorID,
		Action: "session.scanner.unavailable", ResourceType: "lecture_session",
		ResourceID: sessionID.String(), Details: details, CreatedAt: createdAt,
	}); err != nil {
		return fmt.Errorf("audit scanner unavailable status: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit scanner status update: %w", err)
	}
	return nil
}

func (s *PostgresStore) VenueSettings(ctx context.Context, institutionIDText, venueIDText string) (sessions.VenueSettings, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return sessions.VenueSettings{}, err
	}
	venueID, err := db.ParseUUID(venueIDText)
	if err != nil {
		return sessions.VenueSettings{}, err
	}
	row, err := s.queries.GetVenueSettings(ctx, dbgen.GetVenueSettingsParams{
		InstitutionID: institutionID, ID: venueID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.VenueSettings{}, sessions.ErrVenueNotFound
	}
	if err != nil {
		return sessions.VenueSettings{}, fmt.Errorf("select venue settings: %w", err)
	}
	return sessions.VenueSettings{Latitude: row.Latitude, Longitude: row.Longitude, RadiusMeters: float64(row.RadiusMeters)}, nil
}

func (s *PostgresStore) CanManageOffering(ctx context.Context, institutionIDText, offeringIDText, userIDText string) (bool, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return false, err
	}
	offeringID, err := db.ParseUUID(offeringIDText)
	if err != nil {
		return false, err
	}
	userID, err := db.ParseUUID(userIDText)
	if err != nil {
		return false, err
	}
	allowed, err := s.queries.CanManageOffering(ctx, dbgen.CanManageOfferingParams{
		InstitutionID: institutionID, ID: offeringID, CoordinatorUserID: userID,
	})
	if err != nil {
		return false, fmt.Errorf("authorize offering management: %w", err)
	}
	return allowed, nil
}

func (s *PostgresStore) CanManageSession(ctx context.Context, institutionIDText, sessionIDText, userIDText string) (bool, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return false, err
	}
	sessionID, err := db.ParseUUID(sessionIDText)
	if err != nil {
		return false, err
	}
	userID, err := db.ParseUUID(userIDText)
	if err != nil {
		return false, err
	}
	allowed, err := s.queries.CanManageSession(ctx, dbgen.CanManageSessionParams{
		InstitutionID: institutionID, ID: sessionID, CoordinatorUserID: userID,
	})
	if err != nil {
		return false, fmt.Errorf("authorize session management: %w", err)
	}
	return allowed, nil
}

func sessionFromGetRow(row dbgen.GetLectureSessionRow) sessions.Session {
	return sessions.Session{
		ID: row.ID.String(), InstitutionID: row.InstitutionID.String(), OfferingID: row.OfferingID.String(),
		VenueID: row.VenueID.String(), StartedAt: row.StartedAt.Time.UTC(), EndedAt: db.TimestampPointer(row.EndedAt),
		Latitude: row.Latitude, Longitude: row.Longitude, RadiusMeters: float64(row.RadiusMeters),
		RotatingCode: row.RotatingCode, StaticCode: row.StaticCode.String,
		CodeExpiresAt: db.TimestampPointer(row.CodeExpiresAt), LateAfter: time.Duration(row.LateAfterMin) * time.Minute,
		LateAfterMin: int64(row.LateAfterMin),
	}
}
