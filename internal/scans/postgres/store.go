package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"time"

	"attendance-api/internal/db"
	"attendance-api/internal/db/dbgen"
	"attendance-api/internal/scans"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool    *pgxpool.Pool
	queries *dbgen.Queries
}

func NewStore(pool *pgxpool.Pool, queries *dbgen.Queries) (*Store, error) {
	if pool == nil || queries == nil {
		return nil, errors.New("database pool and sqlc queries are required")
	}
	return &Store{pool: pool, queries: queries}, nil
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

func (s *Store) RecordDeviceStatus(ctx context.Context, device scans.Device, status scans.DeviceStatus, reportedAt time.Time) error {
	institutionID, err := db.ParseUUID(device.InstitutionID)
	if err != nil {
		return err
	}
	deviceID, err := db.ParseUUID(device.ID)
	if err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `UPDATE devices SET
		firmware_version = COALESCE($3::text, firmware_version),
		battery_percent = COALESCE($4::smallint, battery_percent),
		pending_queue_depth = COALESCE($5::integer, pending_queue_depth),
		free_storage_bytes = COALESCE($6::bigint, free_storage_bytes),
		status_reported_at = $7
		WHERE institution_id=$1 AND id=$2 AND disabled_at IS NULL`,
		institutionID, deviceID, status.FirmwareVersion, status.BatteryPercent,
		status.PendingQueueDepth, status.FreeStorageBytes,
		pgtype.Timestamptz{Time: reportedAt.UTC(), Valid: true}); err != nil {
		return fmt.Errorf("record device status: %w", err)
	}
	return nil
}

func (s *Store) StudentForSlotNumber(ctx context.Context, institutionIDText, deviceIDText string, slotNumber int) (string, bool, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return "", false, err
	}
	deviceID, err := db.ParseUUID(deviceIDText)
	if err != nil {
		return "", false, err
	}
	var studentID pgtype.UUID
	err = s.pool.QueryRow(ctx, `SELECT student_record_id FROM device_slots
		WHERE institution_id=$1 AND device_id=$2 AND slot_number=$3`,
		institutionID, deviceID, slotNumber).Scan(&studentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("find student for device slot number: %w", err)
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
		if row.PendingSlotID.Valid {
			var slotNumber int
			err := s.pool.QueryRow(ctx, `SELECT slot_number FROM device_slots
				WHERE institution_id=$1 AND id=$2`, institutionID, row.PendingSlotID).Scan(&slotNumber)
			if err == nil {
				tasks.PendingEnrolment.SlotNumber = &slotNumber
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return scans.Tasks{}, fmt.Errorf("load pending enrolment slot number: %w", err)
			}
		}
	}
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(string_agg(id::text, ',' ORDER BY id), '')
		FROM enrolment_tasks
		WHERE institution_id=$1 AND device_id=$2 AND status='pending'`,
		institutionID, deviceID).Scan(&tasks.EnrolmentQueueRevision); err != nil {
		return scans.Tasks{}, fmt.Errorf("load pending enrolment queue revision: %w", err)
	}
	return tasks, nil
}

func (s *Store) CompleteEnrolment(ctx context.Context, institutionIDText, deviceIDText, taskIDText, status string, template []byte, completedAt time.Time) error {
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin enrolment completion: %w", err)
	}
	defer tx.Rollback(ctx)
	var studentID pgtype.UUID
	err = tx.QueryRow(ctx, `UPDATE enrolment_tasks SET status=$4, completed_at=$5
		WHERE institution_id=$1 AND device_id=$2 AND id=$3 AND status='pending'
		RETURNING student_record_id`, institutionID, deviceID, taskID, status,
		pgtype.Timestamptz{Time: completedAt.UTC(), Valid: true}).Scan(&studentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return scans.ErrEnrolmentTaskNotFound
	}
	if err != nil {
		return fmt.Errorf("complete device enrolment task: %w", err)
	}
	// A captured template is stored centrally so any scanner serving a session the
	// student is registered for can load it (multi-venue recognition).
	if status == "done" && len(template) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO student_fingerprints
			(institution_id, student_record_id, template, enrolled_by_device_id, updated_at)
			VALUES ($1,$2,$3,$4,now())
			ON CONFLICT (institution_id, student_record_id)
			DO UPDATE SET template=EXCLUDED.template, enrolled_by_device_id=EXCLUDED.enrolled_by_device_id, updated_at=now()`,
			institutionID, studentID, template, deviceID); err != nil {
			return fmt.Errorf("store fingerprint template: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit enrolment completion: %w", err)
	}
	return nil
}

// SyncDeviceTemplates provisions the device's slots to the roster of the given
// (or currently open) session at its venue and returns each roster student's
// slot number and central template, so the device can load them into its sensor.
func (s *Store) SyncDeviceTemplates(ctx context.Context, device scans.Device, sessionIDText string) (scans.TemplateSync, error) {
	institutionID, err := db.ParseUUID(device.InstitutionID)
	if err != nil {
		return scans.TemplateSync{}, err
	}
	deviceID, err := db.ParseUUID(device.ID)
	if err != nil {
		return scans.TemplateSync{}, err
	}
	venueID, err := db.ParseUUID(device.VenueID)
	if err != nil {
		return scans.TemplateSync{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return scans.TemplateSync{}, fmt.Errorf("begin template sync: %w", err)
	}
	defer tx.Rollback(ctx)

	var sessionID, offeringID pgtype.UUID
	if sessionIDText != "" {
		requested, err := db.ParseUUID(sessionIDText)
		if err != nil {
			return scans.TemplateSync{}, err
		}
		err = tx.QueryRow(ctx, `SELECT id, offering_id FROM lecture_sessions
			WHERE institution_id=$1 AND id=$2 AND venue_id=$3`,
			institutionID, requested, venueID).Scan(&sessionID, &offeringID)
	} else {
		err = tx.QueryRow(ctx, `SELECT id, offering_id FROM lecture_sessions
			WHERE institution_id=$1 AND venue_id=$2 AND ended_at IS NULL
			ORDER BY started_at DESC LIMIT 1`, institutionID, venueID).Scan(&sessionID, &offeringID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return scans.TemplateSync{}, scans.ErrNoActiveSession
	}
	if err != nil {
		return scans.TemplateSync{}, fmt.Errorf("resolve template sync session: %w", err)
	}

	existing := map[string]int{}
	maxSlot := 0
	slotRows, err := tx.Query(ctx, `SELECT student_record_id::text, slot_number FROM device_slots
		WHERE institution_id=$1 AND device_id=$2`, institutionID, deviceID)
	if err != nil {
		return scans.TemplateSync{}, fmt.Errorf("load device slots: %w", err)
	}
	for slotRows.Next() {
		var student string
		var number int
		if err := slotRows.Scan(&student, &number); err != nil {
			slotRows.Close()
			return scans.TemplateSync{}, fmt.Errorf("scan device slot: %w", err)
		}
		existing[student] = number
		if number > maxSlot {
			maxSlot = number
		}
	}
	slotRows.Close()
	if err := slotRows.Err(); err != nil {
		return scans.TemplateSync{}, fmt.Errorf("read device slots: %w", err)
	}

	rosterRows, err := tx.Query(ctx, `SELECT cr.student_record_id::text, sr.matric_no, sf.template
		FROM course_registrations cr
		JOIN student_records sr ON sr.institution_id=cr.institution_id AND sr.id=cr.student_record_id
		JOIN student_fingerprints sf ON sf.institution_id=cr.institution_id AND sf.student_record_id=cr.student_record_id
		WHERE cr.institution_id=$1 AND cr.offering_id=$2 AND cr.status='approved'
		ORDER BY sr.matric_no`, institutionID, offeringID)
	if err != nil {
		return scans.TemplateSync{}, fmt.Errorf("load session roster templates: %w", err)
	}
	type rosterEntry struct {
		studentID string
		matric    string
		template  []byte
	}
	roster := []rosterEntry{}
	for rosterRows.Next() {
		var entry rosterEntry
		if err := rosterRows.Scan(&entry.studentID, &entry.matric, &entry.template); err != nil {
			rosterRows.Close()
			return scans.TemplateSync{}, fmt.Errorf("scan roster template: %w", err)
		}
		roster = append(roster, entry)
	}
	rosterRows.Close()
	if err := rosterRows.Err(); err != nil {
		return scans.TemplateSync{}, fmt.Errorf("read roster templates: %w", err)
	}

	slots := make([]scans.DeviceTemplate, 0, len(roster))
	for _, entry := range roster {
		slotNumber, ok := existing[entry.studentID]
		if !ok {
			maxSlot++
			slotNumber = maxSlot
			studentID, err := db.ParseUUID(entry.studentID)
			if err != nil {
				return scans.TemplateSync{}, err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO device_slots(institution_id, device_id, student_record_id, slot_number)
				VALUES ($1,$2,$3,$4)
				ON CONFLICT (institution_id, device_id, student_record_id) DO NOTHING`,
				institutionID, deviceID, studentID, slotNumber); err != nil {
				return scans.TemplateSync{}, fmt.Errorf("provision device slot: %w", err)
			}
			existing[entry.studentID] = slotNumber
		}
		slots = append(slots, scans.DeviceTemplate{
			SlotNumber: slotNumber, StudentRecordID: entry.studentID, StudentRef: entry.matric,
			Template: base64.StdEncoding.EncodeToString(entry.template),
		})
	}
	if err := tx.Commit(ctx); err != nil {
		return scans.TemplateSync{}, fmt.Errorf("commit template sync: %w", err)
	}
	return scans.TemplateSync{SessionID: sessionID.String(), Slots: slots}, nil
}

var _ scans.ScanStore = (*Store)(nil)
var _ scans.TaskStore = (*Store)(nil)
