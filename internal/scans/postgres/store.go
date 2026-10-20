package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"attendance-api/internal/db"
	"attendance-api/internal/db/dbgen"
	"attendance-api/internal/scans"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Store struct {
	queries *dbgen.Queries
}

func NewStore(queries *dbgen.Queries) (*Store, error) {
	if queries == nil {
		return nil, errors.New("sqlc queries are required")
	}
	return &Store{queries: queries}, nil
}

func (s *Store) LoadDeviceByKey(ctx context.Context, keyHash []byte) (scans.Device, bool, error) {
	row, err := s.queries.FindDeviceByKeyHash(ctx, keyHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return scans.Device{}, false, nil
	}
	if err != nil {
		return scans.Device{}, false, fmt.Errorf("find device by key hash: %w", err)
	}
	return scans.Device{
		ID: row.ID.String(), InstitutionID: row.InstitutionID.String(), VenueID: row.VenueID.String(),
	}, true, nil
}

func (s *Store) TouchDeviceHeartbeat(ctx context.Context, device scans.Device, heartbeatAt time.Time) error {
	institutionID, err := db.ParseUUID(device.InstitutionID)
	if err != nil {
		return err
	}
	deviceID, err := db.ParseUUID(device.ID)
	if err != nil {
		return err
	}
	if err := s.queries.TouchDeviceHeartbeat(ctx, dbgen.TouchDeviceHeartbeatParams{
		InstitutionID: institutionID, ID: deviceID,
		HeartbeatAt: pgtype.Timestamptz{Time: heartbeatAt.UTC(), Valid: true},
	}); err != nil {
		return fmt.Errorf("touch device heartbeat: %w", err)
	}
	return nil
}

func (s *Store) StudentForSlot(ctx context.Context, institutionIDText, deviceIDText, slotIDText string) (string, bool, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return "", false, err
	}
	deviceID, err := db.ParseUUID(deviceIDText)
	if err != nil {
		return "", false, err
	}
	slotID, err := db.ParseUUID(slotIDText)
	if err != nil {
		return "", false, err
	}
	studentID, err := s.queries.FindDeviceSlotStudent(ctx, dbgen.FindDeviceSlotStudentParams{
		InstitutionID: institutionID, DeviceID: deviceID, ID: slotID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("find student for device slot: %w", err)
	}
	return studentID.String(), true, nil
}

func (s *Store) OpenSessionAtVenue(ctx context.Context, institutionIDText, venueIDText string) (string, bool, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return "", false, err
	}
	venueID, err := db.ParseUUID(venueIDText)
	if err != nil {
		return "", false, err
	}
	row, err := s.queries.FindActiveSessionAtVenue(ctx, dbgen.FindActiveSessionAtVenueParams{
		InstitutionID: institutionID, VenueID: venueID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("find open session at device venue: %w", err)
	}
	return row.ID.String(), true, nil
}

func (s *Store) SessionAtVenueTime(ctx context.Context, institutionIDText, venueIDText string, scannedAt time.Time) (string, bool, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return "", false, err
	}
	venueID, err := db.ParseUUID(venueIDText)
	if err != nil {
		return "", false, err
	}
	row, err := s.queries.FindSessionAtVenueTime(ctx, dbgen.FindSessionAtVenueTimeParams{
		InstitutionID: institutionID, VenueID: venueID,
		StartedAt: pgtype.Timestamptz{Time: scannedAt.UTC(), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("find venue session at client scan time: %w", err)
	}
	return row.String(), true, nil
}

func (s *Store) InsertFingerprintScan(ctx context.Context, institutionIDText, sessionIDText, studentIDText, deviceIDText string, confidence float64, clientScannedAt *time.Time) (bool, error) {
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
	deviceID, err := db.ParseUUID(deviceIDText)
	if err != nil {
		return false, err
	}
	scannedAt := pgtype.Timestamptz{}
	if clientScannedAt != nil {
		scannedAt = pgtype.Timestamptz{Time: clientScannedAt.UTC(), Valid: true}
	}
	var confidenceValue pgtype.Numeric
	if err := confidenceValue.Scan(strconv.FormatFloat(confidence, 'f', -1, 64)); err != nil {
		return false, fmt.Errorf("encode scan confidence: %w", err)
	}
	_, err = s.queries.InsertFingerprintScan(ctx, dbgen.InsertFingerprintScanParams{
		InstitutionID: institutionID, SessionID: sessionID, StudentRecordID: studentID,
		DeviceID: deviceID, Confidence: confidenceValue, ClientScannedAt: scannedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("insert fingerprint scan: %w", err)
	}
	return true, nil
}

func (s *Store) LoadTasks(ctx context.Context, institutionIDText, deviceIDText string) (scans.Tasks, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return scans.Tasks{}, err
	}
	deviceID, err := db.ParseUUID(deviceIDText)
	if err != nil {
		return scans.Tasks{}, err
	}
	row, err := s.queries.GetDeviceTasks(ctx, dbgen.GetDeviceTasksParams{
		InstitutionID: institutionID, ID: deviceID,
	})
	if err != nil {
		return scans.Tasks{}, fmt.Errorf("load device tasks: %w", err)
	}
	tasks := scans.Tasks{}
	if row.ActiveSessionID.Valid {
		tasks.ActiveSession = &scans.ActiveSession{
			ID: row.ActiveSessionID.String(), VenueID: row.ActiveVenueID.String(),
		}
	}
	if row.PendingEnrolmentID.Valid {
		tasks.PendingEnrolment = &scans.Enrolment{
			ID: row.PendingEnrolmentID.String(), StudentRef: row.PendingStudentRef.String,
			SlotID: row.PendingSlotID.String(),
		}
	}
	return tasks, nil
}

func (s *Store) CompleteEnrolment(ctx context.Context, institutionIDText, deviceIDText, taskIDText, status string, completedAt time.Time) error {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return err
	}
	deviceID, err := db.ParseUUID(deviceIDText)
	if err != nil {
		return err
	}
	taskID, err := db.ParseUUID(taskIDText)
	if err != nil {
		return err
	}
	rows, err := s.queries.CompleteEnrolmentTask(ctx, dbgen.CompleteEnrolmentTaskParams{
		InstitutionID: institutionID, DeviceID: deviceID, ID: taskID, Status: status,
		CompletedAt: pgtype.Timestamptz{Time: completedAt.UTC(), Valid: true},
	})
	if err != nil {
		return fmt.Errorf("complete device enrolment task: %w", err)
	}
	if rows == 0 {
		return scans.ErrEnrolmentTaskNotFound
	}
	return nil
}

var _ scans.ScanStore = (*Store)(nil)
var _ scans.TaskStore = (*Store)(nil)
