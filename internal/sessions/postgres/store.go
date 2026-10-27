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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sessions.Session{}, fmt.Errorf("begin session start: %w", err)
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	row, err := queries.CreateLectureSession(ctx, dbgen.CreateLectureSessionParams{
		InstitutionID: institutionID, OfferingID: offeringID, VenueID: venueID, TimetableID: timetableID,
		StartedByUserID: startedBy, StartedAt: pgtype.Timestamptz{Time: startedAt.UTC(), Valid: true},
		Latitude: coordinateParam(input.Latitude), Longitude: coordinateParam(input.Longitude), RadiusMeters: radiusParam(input.RadiusMeters),
		RotatingCode: input.RotatingCode, StaticCode: staticValue, CodeExpiresAt: expiryValue,
		LateAfterMin: int32(input.LateAfter / time.Minute),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		var venueActive bool
		venueErr := s.pool.QueryRow(ctx, `SELECT active FROM venues
			WHERE institution_id=$1 AND id=$2`, institutionID, venueID).Scan(&venueActive)
		if venueErr != nil {
			return sessions.Session{}, fmt.Errorf("check session venue availability: %w", venueErr)
		}
		if !venueActive {
			return sessions.Session{}, sessions.ErrInvalidSession
		}
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
	// The fix that centred this session is good enough to be the room's
	// anchor, so it is written to the venue in the same transaction: either
	// the class starts and the room is calibrated, or neither happens.
	if input.CalibrateVenue && input.Latitude != nil && input.Longitude != nil {
		if err := calibrateVenue(ctx, tx, institutionID, venueID, startedBy,
			sessions.Centre{
				Latitude: *input.Latitude, Longitude: *input.Longitude,
				RadiusMeters: radiusOrDefault(input.RadiusMeters, input.AccuracyMeters),
			}, input.AccuracyMeters, "session_start", row.ID.String(), startedAt); err != nil {
			return sessions.Session{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return sessions.Session{}, fmt.Errorf("commit session start: %w", err)
	}
	return sessions.Session{
		ID: row.ID.String(), InstitutionID: row.InstitutionID.String(), OfferingID: row.OfferingID.String(),
		VenueID: row.VenueID.String(), StartedAt: row.StartedAt.Time.UTC(), EndedAt: db.TimestampPointer(row.EndedAt),
		Latitude: optCoordinate(row.Latitude), Longitude: optCoordinate(row.Longitude), RadiusMeters: optRadius(row.RadiusMeters),
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
		Latitude: optCoordinate(row.Latitude), Longitude: optCoordinate(row.Longitude), RadiusMeters: optRadius(row.RadiusMeters),
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
		Latitude: optCoordinate(row.Latitude), Longitude: optCoordinate(row.Longitude), RadiusMeters: optRadius(row.RadiusMeters),
		RotatingCode: row.RotatingCode, StaticCode: row.StaticCode.String,
		CodeExpiresAt: db.TimestampPointer(row.CodeExpiresAt), LateAfter: time.Duration(row.LateAfterMin) * time.Minute,
		LateAfterMin: int64(row.LateAfterMin),
	}, nil
}

// RecentreSession writes a mid-session fix from the lecturer's device over the
// session's geofence and the venue's stored centre, in one transaction.
//
// The venue is calibrated from the same fix because the lecturer standing in
// the room is the best evidence the system will ever get about where that room
// is; leaving the venue stale would make the next class repeat the correction.
func (s *PostgresStore) RecentreSession(
	ctx context.Context,
	institutionIDText, sessionIDText, actorUserIDText string,
	centre sessions.Centre,
	fix sessions.DeviceFix,
	changedAt time.Time,
) error {
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
		return fmt.Errorf("begin session recentre: %w", err)
	}
	defer tx.Rollback(ctx)
	// ended_at IS NULL is re-checked here rather than trusted from the earlier
	// read: a class can end between the two, and a closed session's geofence
	// is part of its attendance record.
	var venueID pgtype.UUID
	err = tx.QueryRow(ctx, `UPDATE lecture_sessions
		SET latitude=$3, longitude=$4, radius_meters=$5
		WHERE institution_id=$1 AND id=$2 AND ended_at IS NULL
		RETURNING venue_id`,
		institutionID, sessionID, centre.Latitude, centre.Longitude,
		int32(centre.RadiusMeters)).Scan(&venueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.ErrSessionEnded
	}
	if err != nil {
		return fmt.Errorf("recentre lecture session: %w", err)
	}
	if err := calibrateVenue(ctx, tx, institutionID, venueID, actorID, centre,
		fix.AccuracyMeters, "session_update", sessionID.String(), changedAt); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit session recentre: %w", err)
	}
	return nil
}

// calibrateVenue writes a captured centre onto the venue and audits it. An
// existing radius is kept: it was set for that room, and a device fix says
// nothing about how big the room is. Only a venue that has no radius takes the
// one derived from this fix.
func calibrateVenue(
	ctx context.Context,
	tx pgx.Tx,
	institutionID, venueID, actorID pgtype.UUID,
	centre sessions.Centre,
	accuracyMeters float64,
	source, sessionID string,
	changedAt time.Time,
) error {
	if _, err := tx.Exec(ctx, `UPDATE venues
		SET latitude=$3, longitude=$4, radius_meters=coalesce(radius_meters,$5)
		WHERE institution_id=$1 AND id=$2`,
		institutionID, venueID, centre.Latitude, centre.Longitude,
		int32(centre.RadiusMeters)); err != nil {
		return fmt.Errorf("calibrate venue from device fix: %w", err)
	}
	details, err := json.Marshal(map[string]any{
		"latitude": centre.Latitude, "longitude": centre.Longitude,
		"accuracy_meters": accuracyMeters, "source": source, "session_id": sessionID,
	})
	if err != nil {
		return fmt.Errorf("encode venue calibration audit details: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_log(institution_id,actor_user_id,action,resource_type,resource_id,details,created_at)
		VALUES($1,$2,'venue.location_captured','venue',$3,$4::jsonb,$5)`,
		institutionID, actorID, venueID.String(), details,
		pgtype.Timestamptz{Time: changedAt.UTC(), Valid: true}); err != nil {
		return fmt.Errorf("audit venue calibration: %w", err)
	}
	return nil
}

// radiusOrDefault picks the radius to store on a venue being calibrated: the
// one the session is running with when it has it, otherwise the rule for a
// fresh centre.
func radiusOrDefault(radiusMeters *float64, accuracyMeters float64) float64 {
	if radiusMeters != nil && *radiusMeters > 0 {
		return *radiusMeters
	}
	return sessions.RadiusForNewCentre(accuracyMeters)
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
	settings := sessions.VenueSettings{
		Present: row.Latitude.Valid && row.Longitude.Valid && row.RadiusMeters.Valid,
	}
	if settings.Present {
		settings.Latitude = row.Latitude.Float64
		settings.Longitude = row.Longitude.Float64
		settings.RadiusMeters = float64(row.RadiusMeters.Int32)
	}
	return settings, nil
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

func (s *PostgresStore) ListLiveClasses(ctx context.Context, institutionIDText, userIDText, studentIDText string) ([]sessions.LiveClass, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return nil, err
	}
	userID, err := db.ParseUUID(userIDText)
	if err != nil {
		return nil, err
	}
	studentID, err := db.ParseUUID(studentIDText)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListStudentLiveClasses(ctx, dbgen.ListStudentLiveClassesParams{
		InstitutionID: institutionID, ClaimedByUserID: userID, ID: studentID,
	})
	if err != nil {
		return nil, fmt.Errorf("query live classes: %w", err)
	}
	items := make([]sessions.LiveClass, 0, len(rows))
	for _, row := range rows {
		items = append(items, sessions.LiveClass{
			SessionID: row.SessionID.String(), OfferingID: row.OfferingID.String(),
			CourseCode: row.CourseCode, CourseTitle: row.CourseTitle,
			VenueID: row.VenueID.String(), VenueName: row.VenueName, LecturerName: row.LecturerName,
			StartedAt: row.StartedAt.Time.UTC(), LateAfterMin: int(row.LateAfterMin),
			RotatingCode: row.RotatingCode, AlreadyCheckedIn: row.AlreadyCheckedIn.Bool,
		})
	}
	return items, nil
}

func sessionFromGetRow(row dbgen.GetLectureSessionRow) sessions.Session {
	return sessions.Session{
		ID: row.ID.String(), InstitutionID: row.InstitutionID.String(), OfferingID: row.OfferingID.String(),
		VenueID: row.VenueID.String(), StartedAt: row.StartedAt.Time.UTC(), EndedAt: db.TimestampPointer(row.EndedAt),
		Latitude: optCoordinate(row.Latitude), Longitude: optCoordinate(row.Longitude), RadiusMeters: optRadius(row.RadiusMeters),
		RotatingCode: row.RotatingCode, StaticCode: row.StaticCode.String,
		CodeExpiresAt: db.TimestampPointer(row.CodeExpiresAt), LateAfter: time.Duration(row.LateAfterMin) * time.Minute,
		LateAfterMin: int64(row.LateAfterMin),
	}
}

// A session's geofence columns are nullable: a venue with no stored
// coordinates starts a session with no centre. The three are always written
// and read together, so each mapper guards on its own column's validity.

func optCoordinate(value pgtype.Float8) *float64 {
	if !value.Valid {
		return nil
	}
	coordinate := value.Float64
	return &coordinate
}

func optRadius(value pgtype.Int4) *float64 {
	if !value.Valid {
		return nil
	}
	radius := float64(value.Int32)
	return &radius
}

func coordinateParam(value *float64) pgtype.Float8 {
	if value == nil {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: *value, Valid: true}
}

func radiusParam(value *float64) pgtype.Int4 {
	if value == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*value), Valid: true}
}
