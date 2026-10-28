package devices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"attendance-api/internal/auth"
	"attendance-api/internal/db"
	"attendance-api/internal/rbac"
	"attendance-api/internal/scans"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore implements Repository with raw SQL, transactions, and audit
// rows, mirroring internal/catalogue/postgres/venue_management.go.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore validates dependencies and builds the store.
func NewPostgresStore(pool *pgxpool.Pool) (*PostgresStore, error) {
	if pool == nil {
		return nil, errors.New("database pool is required")
	}
	return &PostgresStore{pool: pool}, nil
}

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

const (
	deviceAssignmentsQuery = `SELECT role,scope_type,institution_id,faculty_id,department_id
		FROM role_assignments WHERE institution_id=$1 AND user_id=$2`
	deviceListQuery = `SELECT d.id::text, d.name, d.venue_id::text, v.name,
			v.department_id::text, d.key_hash IS NOT NULL, d.disabled_at IS NOT NULL,
			d.paired_at, d.last_seen_at, d.firmware_version, d.battery_percent,
			d.pending_queue_depth, d.status_reported_at,
			(SELECT min(c.expires_at) FROM device_pairing_codes c
			   WHERE c.institution_id=d.institution_id AND c.device_id=d.id
			     AND c.consumed_at IS NULL AND c.expires_at > $4)
		FROM devices d
		JOIN venues v ON v.institution_id=d.institution_id AND v.id=d.venue_id
		WHERE d.institution_id=$1
		ORDER BY d.name, d.id
		LIMIT $2 OFFSET $3`
	venueScannerStatusAuthorizationQuery = `SELECT EXISTS (
		SELECT 1 FROM role_assignments
		WHERE institution_id=$1 AND user_id=$2
		  AND (
			(role='platform_admin' AND scope_type='platform')
			OR (role='institution_admin' AND scope_type='institution')
			OR (role='faculty_admin' AND scope_type='faculty')
			OR (role='department_admin' AND scope_type='department')
			OR (role='lecturer' AND scope_type='department')
		  )
	)`
	venueScannerSnapshotQuery = `SELECT count(d.id), max(d.last_seen_at)
		FROM venues v
		LEFT JOIN devices d ON d.institution_id=v.institution_id AND d.venue_id=v.id
			AND d.disabled_at IS NULL
		WHERE v.institution_id=$1 AND v.id=$2
		GROUP BY v.id`
	resolveEnrolmentStudentQuery = `SELECT id
		FROM student_records
		WHERE institution_id=$1 AND (id=$2 OR claimed_by_user_id=$2)
		ORDER BY (id=$2) DESC
		LIMIT 1`
	findDeviceStudentSlotQuery = `SELECT id, slot_number
		FROM device_slots
		WHERE institution_id=$1 AND device_id=$2 AND student_record_id=$3`
)

const maxDeviceSlotNumber = 300

func (s *PostgresStore) Provision(ctx context.Context, actor auth.Identity, input ProvisionInput, code PairingCode, now time.Time) (Device, error) {
	tenant, actorID, err := parseActor(actor)
	if err != nil {
		return Device{}, err
	}
	venueID, err := db.ParseUUID(input.VenueID)
	if err != nil {
		return Device{}, ErrInvalidDeviceRequest
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Device{}, fmt.Errorf("begin device provisioning: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := s.requireScannerManagement(ctx, tx, tenant, actorID, actor.InstitutionID, input.VenueID); err != nil {
		return Device{}, err
	}
	var venueName string
	var department pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT name, department_id FROM venues
		WHERE institution_id=$1 AND id=$2`, tenant, venueID).Scan(&venueName, &department)
	if errors.Is(err, pgx.ErrNoRows) {
		return Device{}, ErrInvalidDeviceRequest
	}
	if err != nil {
		return Device{}, fmt.Errorf("resolve device venue: %w", err)
	}
	var id pgtype.UUID
	err = tx.QueryRow(ctx, `INSERT INTO devices(institution_id,venue_id,name)
		VALUES($1,$2,$3) RETURNING id`, tenant, venueID, input.Name).Scan(&id)
	if err != nil {
		return Device{}, fmt.Errorf("insert device: %w", err)
	}
	if err := insertPairingCode(ctx, tx, tenant, id, actorID, code); err != nil {
		return Device{}, err
	}
	if err := writeAudit(ctx, tx, tenant, actorID, "device.created", id.String(),
		map[string]any{"name": input.Name, "venue_id": input.VenueID}); err != nil {
		return Device{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Device{}, fmt.Errorf("commit device provisioning: %w", err)
	}
	expires := code.ExpiresAt
	return Device{
		ID: id.String(), Name: input.Name, VenueID: input.VenueID, VenueName: venueName,
		DepartmentID: nullableUUID(department), Status: StatusUnpaired,
		PairingCodeExpiresAt: &expires,
	}, nil
}

func (s *PostgresStore) List(ctx context.Context, actor auth.Identity, filter ListFilter) ([]Device, error) {
	tenant, actorID, err := parseActor(actor)
	if err != nil {
		return nil, err
	}
	if err := s.requireScannerManagement(ctx, s.pool, tenant, actorID, actor.InstitutionID, actor.InstitutionID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, deviceListQuery, tenant, filter.Limit, filter.Offset, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()
	devices := []Device{}
	for rows.Next() {
		var d Device
		var departmentID pgtype.Text
		var firmware pgtype.Text
		var battery, queueDepth pgtype.Int4
		var hasKey, disabled bool
		var pairedAt, lastSeenAt, statusReportedAt, pairingExpiresAt pgtype.Timestamptz
		if err := rows.Scan(&d.ID, &d.Name, &d.VenueID, &d.VenueName, &departmentID,
			&hasKey, &disabled, &pairedAt, &lastSeenAt, &firmware, &battery,
			&queueDepth, &statusReportedAt, &pairingExpiresAt); err != nil {
			return nil, fmt.Errorf("scan device row: %w", err)
		}
		d.Status = DeriveStatus(hasKey, disabled)
		if departmentID.Valid {
			value := departmentID.String
			d.DepartmentID = &value
		}
		if firmware.Valid {
			value := firmware.String
			d.FirmwareVersion = &value
		}
		d.BatteryPercent = nullableInt(battery)
		d.PendingQueueDepth = nullableInt(queueDepth)
		d.PairedAt = nullableTime(pairedAt)
		d.LastSeenAt = nullableTime(lastSeenAt)
		d.StatusReportedAt = nullableTime(statusReportedAt)
		d.PairingCodeExpiresAt = nullableTime(pairingExpiresAt)
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read device rows: %w", err)
	}
	return devices, nil
}

func (s *PostgresStore) VenueScannerSnapshot(ctx context.Context, actor auth.Identity, venueID string) (ScannerSnapshot, error) {
	tenant, actorID, err := parseActor(actor)
	if err != nil {
		return ScannerSnapshot{}, err
	}
	id, err := db.ParseUUID(venueID)
	if err != nil {
		return ScannerSnapshot{}, ErrVenueNotFound
	}
	var reader bool
	if err := s.pool.QueryRow(ctx, venueScannerStatusAuthorizationQuery, tenant, actorID).Scan(&reader); err != nil {
		return ScannerSnapshot{}, fmt.Errorf("authorize venue scanner status: %w", err)
	}
	if !reader {
		return ScannerSnapshot{}, ErrVenueStatusDenied
	}
	var count int64
	var lastSeen pgtype.Timestamptz
	if err := s.pool.QueryRow(ctx, venueScannerSnapshotQuery, tenant, id).Scan(&count, &lastSeen); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ScannerSnapshot{}, ErrVenueNotFound
		}
		return ScannerSnapshot{}, fmt.Errorf("load venue scanner status: %w", err)
	}
	snapshot := ScannerSnapshot{Exists: count > 0}
	if lastSeen.Valid {
		value := lastSeen.Time.UTC()
		snapshot.LastSeenAt = &value
	}
	return snapshot, nil
}

func (s *PostgresStore) IssuePairingCode(ctx context.Context, actor auth.Identity, deviceID string, code PairingCode, now time.Time, rotate bool) error {
	tenant, actorID, err := parseActor(actor)
	if err != nil {
		return err
	}
	id, err := db.ParseUUID(deviceID)
	if err != nil {
		return ErrDeviceNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin pairing code issuance: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := s.lockDeviceScope(ctx, tx, tenant, actorID, actor.InstitutionID, deviceID, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE device_pairing_codes SET consumed_at=$3
		WHERE institution_id=$1 AND device_id=$2 AND consumed_at IS NULL`, tenant, id, now); err != nil {
		return fmt.Errorf("consume outstanding pairing codes: %w", err)
	}
	action := "device.pairing_code_issued"
	if rotate {
		if _, err := tx.Exec(ctx, `UPDATE devices SET key_hash=NULL, paired_at=NULL
			WHERE institution_id=$1 AND id=$2`, tenant, id); err != nil {
			return fmt.Errorf("rotate device key: %w", err)
		}
		action = "device.key_rotated"
	}
	if err := insertPairingCode(ctx, tx, tenant, id, actorID, code); err != nil {
		return err
	}
	if err := writeAudit(ctx, tx, tenant, actorID, action, deviceID, map[string]any{}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit pairing code issuance: %w", err)
	}
	return nil
}

func (s *PostgresStore) Revoke(ctx context.Context, actor auth.Identity, deviceID string, now time.Time) error {
	tenant, actorID, err := parseActor(actor)
	if err != nil {
		return err
	}
	id, err := db.ParseUUID(deviceID)
	if err != nil {
		return ErrDeviceNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin device revocation: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := s.lockDeviceScope(ctx, tx, tenant, actorID, actor.InstitutionID, deviceID, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE devices SET disabled_at=$3
		WHERE institution_id=$1 AND id=$2 AND disabled_at IS NULL`, tenant, id, now); err != nil {
		return fmt.Errorf("revoke device: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE device_pairing_codes SET consumed_at=$3
		WHERE institution_id=$1 AND device_id=$2 AND consumed_at IS NULL`, tenant, id, now); err != nil {
		return fmt.Errorf("consume pairing codes on revocation: %w", err)
	}
	if err := writeAudit(ctx, tx, tenant, actorID, "device.revoked", deviceID, map[string]any{}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit device revocation: %w", err)
	}
	return nil
}

func (s *PostgresStore) EnqueueEnrolment(ctx context.Context, actor auth.Identity, deviceID, studentRecordID string, now time.Time) (Enrolment, error) {
	tenant, actorID, err := parseActor(actor)
	if err != nil {
		return Enrolment{}, err
	}
	id, err := db.ParseUUID(deviceID)
	if err != nil {
		return Enrolment{}, ErrDeviceNotFound
	}
	studentID, err := db.ParseUUID(studentRecordID)
	if err != nil {
		return Enrolment{}, ErrInvalidDeviceRequest
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Enrolment{}, fmt.Errorf("begin enrolment enqueue: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := s.lockDeviceScope(ctx, tx, tenant, actorID, actor.InstitutionID, deviceID, id); err != nil {
		return Enrolment{}, err
	}
	err = tx.QueryRow(ctx, resolveEnrolmentStudentQuery, tenant, studentID).Scan(&studentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Enrolment{}, ErrStudentNotFound
	}
	if err != nil {
		return Enrolment{}, fmt.Errorf("resolve enrolment student: %w", err)
	}
	var slotID pgtype.UUID
	var slotNumber int
	err = tx.QueryRow(ctx, findDeviceStudentSlotQuery, tenant, id, studentID).Scan(&slotID, &slotNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		var maxSlotNumber int
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(slot_number),0) FROM device_slots
			WHERE institution_id=$1 AND device_id=$2`, tenant, id).Scan(&maxSlotNumber); err != nil {
			return Enrolment{}, fmt.Errorf("resolve maximum device slot: %w", err)
		}
		slotNumber, err = nextDeviceSlotNumber(maxSlotNumber)
		if err != nil {
			return Enrolment{}, err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO device_slots(institution_id,device_id,student_record_id,slot_number)
			VALUES($1,$2,$3,$4) RETURNING id`, tenant, id, studentID, slotNumber).Scan(&slotID); err != nil {
			return Enrolment{}, fmt.Errorf("insert device slot: %w", err)
		}
	} else if err != nil {
		return Enrolment{}, fmt.Errorf("resolve existing device slot: %w", err)
	} else if slotNumber < 1 || slotNumber > maxDeviceSlotNumber {
		return Enrolment{}, ErrDeviceSlotCapacity
	}
	var taskID pgtype.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO enrolment_tasks(institution_id,device_id,student_record_id,slot_id,status)
		VALUES($1,$2,$3,$4,'pending') RETURNING id`, tenant, id, studentID, slotID).Scan(&taskID); err != nil {
		return Enrolment{}, fmt.Errorf("insert enrolment task: %w", err)
	}
	if err := writeAudit(ctx, tx, tenant, actorID, "device.enrolment_queued", deviceID,
		map[string]any{"student_record_id": studentID.String(), "slot_number": slotNumber}); err != nil {
		return Enrolment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Enrolment{}, fmt.Errorf("commit enrolment enqueue: %w", err)
	}
	return Enrolment{TaskID: taskID.String(), SlotNumber: slotNumber, Status: "pending"}, nil
}

func nextDeviceSlotNumber(maxSlotNumber int) (int, error) {
	if maxSlotNumber < 0 || maxSlotNumber >= maxDeviceSlotNumber {
		return 0, ErrDeviceSlotCapacity
	}
	return maxSlotNumber + 1, nil
}

func (s *PostgresStore) Pair(ctx context.Context, codeHash []byte, now time.Time) (Paired, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Paired{}, fmt.Errorf("begin device pairing: %w", err)
	}
	defer tx.Rollback(ctx)

	var codeID, institutionID, deviceID, venueID pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT c.id, c.institution_id, c.device_id, d.venue_id
		FROM device_pairing_codes c
		JOIN devices d ON d.institution_id=c.institution_id AND d.id=c.device_id AND d.disabled_at IS NULL
		WHERE c.code_hash=$1 AND c.consumed_at IS NULL AND c.expires_at > $2
		FOR UPDATE OF c`, codeHash, now).Scan(&codeID, &institutionID, &deviceID, &venueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Paired{}, ErrPairingCodeInvalid
	}
	if err != nil {
		return Paired{}, fmt.Errorf("resolve pairing code: %w", err)
	}
	plaintext, keyHash, err := scans.GenerateDeviceKey(nil)
	if err != nil {
		return Paired{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE devices SET key_hash=$3, paired_at=$4, disabled_at=NULL
		WHERE institution_id=$1 AND id=$2`, institutionID, deviceID, keyHash, now); err != nil {
		return Paired{}, fmt.Errorf("activate paired device: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE device_pairing_codes SET consumed_at=$2 WHERE id=$1`,
		codeID, now); err != nil {
		return Paired{}, fmt.Errorf("consume pairing code: %w", err)
	}
	// The pairing is device-initiated, so the audit row carries no user actor.
	if _, err := tx.Exec(ctx, `INSERT INTO audit_log(institution_id,action,resource_type,resource_id,details)
		VALUES($1,'device.paired','device',$2,'{}'::jsonb)`, institutionID, deviceID.String()); err != nil {
		return Paired{}, fmt.Errorf("audit device pairing: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Paired{}, fmt.Errorf("commit device pairing: %w", err)
	}
	return Paired{
		DeviceKey: plaintext, DeviceID: deviceID.String(),
		InstitutionID: institutionID.String(), VenueID: venueID.String(),
	}, nil
}

func (s *PostgresStore) lockDeviceScope(ctx context.Context, tx pgx.Tx, tenant, actorID pgtype.UUID, institutionID, targetID string, deviceID pgtype.UUID) error {
	if err := s.requireScannerManagement(ctx, tx, tenant, actorID, institutionID, targetID); err != nil {
		return err
	}
	var venueID pgtype.UUID
	err := tx.QueryRow(ctx, `SELECT d.venue_id
		FROM devices d
		WHERE d.institution_id=$1 AND d.id=$2 AND d.disabled_at IS NULL
		FOR UPDATE OF d`, tenant, deviceID).Scan(&venueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDeviceNotFound
	}
	if err != nil {
		return fmt.Errorf("lock device for management: %w", err)
	}
	return nil
}

func (s *PostgresStore) requireScannerManagement(ctx context.Context, q queryer, tenant, actorID pgtype.UUID, institutionID, targetID string) error {
	assignments, err := loadAssignments(ctx, q, tenant, actorID)
	if err != nil {
		return err
	}
	if scannerAdminAllows(assignments, institutionID) {
		return nil
	}
	roles := make([]string, 0, len(assignments))
	for _, assignment := range assignments {
		roles = append(roles, assignment.Role)
	}
	role := strings.Join(roles, ",")
	if role == "" {
		role = "none"
	}
	slog.WarnContext(ctx, "scanner management denied",
		"role", role, "target_id", targetID,
		"reason", ScannerManagementDeniedMessage)
	return ErrDeviceScopeDenied
}

func insertPairingCode(ctx context.Context, q queryer, tenant, deviceID, actorID pgtype.UUID, code PairingCode) error {
	if _, err := q.Exec(ctx, `INSERT INTO device_pairing_codes(institution_id,device_id,code_hash,expires_at,created_by_user_id)
		VALUES($1,$2,$3,$4,$5)`, tenant, deviceID, code.hash, pgtype.Timestamptz{Time: code.ExpiresAt, Valid: true}, actorID); err != nil {
		return fmt.Errorf("insert pairing code: %w", err)
	}
	return nil
}

func writeAudit(ctx context.Context, q queryer, tenant, actorID pgtype.UUID, action, resourceID string, details map[string]any) error {
	encoded, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode device audit details: %w", err)
	}
	if _, err := q.Exec(ctx, `INSERT INTO audit_log(institution_id,actor_user_id,action,resource_type,resource_id,details)
		VALUES($1,$2,$3,'device',$4,$5::jsonb)`, tenant, actorID, action, resourceID, string(encoded)); err != nil {
		return fmt.Errorf("write device audit: %w", err)
	}
	return nil
}

func loadAssignments(ctx context.Context, q queryer, tenant, actorID pgtype.UUID) ([]rbac.Assignment, error) {
	rows, err := q.Query(ctx, deviceAssignmentsQuery, tenant, actorID)
	if err != nil {
		return nil, fmt.Errorf("load device management roles: %w", err)
	}
	defer rows.Close()
	assignments := make([]rbac.Assignment, 0)
	for rows.Next() {
		var role, scope string
		var institution, faculty, department pgtype.UUID
		if err := rows.Scan(&role, &scope, &institution, &faculty, &department); err != nil {
			return nil, fmt.Errorf("scan device management role: %w", err)
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
		return nil, fmt.Errorf("read device management roles: %w", err)
	}
	return assignments, nil
}

func parseActor(actor auth.Identity) (pgtype.UUID, pgtype.UUID, error) {
	tenant, err := db.ParseUUID(actor.InstitutionID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("parse device institution ID: %w", err)
	}
	actorID, err := db.ParseUUID(actor.UserID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("parse device actor ID: %w", err)
	}
	return tenant, actorID, nil
}

func nullableUUID(value pgtype.UUID) *string {
	if !value.Valid {
		return nil
	}
	text := value.String()
	return &text
}

func nullableTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	utc := value.Time.UTC()
	return &utc
}

func nullableInt(value pgtype.Int4) *int {
	if !value.Valid {
		return nil
	}
	result := int(value.Int32)
	return &result
}

var _ Repository = (*PostgresStore)(nil)
