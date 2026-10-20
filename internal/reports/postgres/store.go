package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"attendance-api/internal/db"
	"attendance-api/internal/db/dbgen"
	"attendance-api/internal/reports"

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
		return nil, errors.New("database pool and SQL queries are required")
	}
	return &Store{pool: pool, queries: queries}, nil
}

func (s *Store) SessionScope(ctx context.Context, institutionIDText, sessionIDText, userIDText string) (reports.Scope, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return reports.Scope{}, err
	}
	sessionID, err := db.ParseUUID(sessionIDText)
	if err != nil {
		return reports.Scope{}, err
	}
	userID, err := db.ParseUUID(userIDText)
	if err != nil {
		return reports.Scope{}, err
	}
	exists, err := s.queries.ReportSessionExists(ctx, dbgen.ReportSessionExistsParams{InstitutionID: institutionID, ID: sessionID})
	if err != nil {
		return reports.Scope{}, fmt.Errorf("check session existence: %w", err)
	}
	if !exists {
		return reports.Scope{}, reports.ErrSessionNotFound
	}
	row, err := s.queries.SessionRosterAccess(ctx, dbgen.SessionRosterAccessParams{
		InstitutionID: institutionID, ID: sessionID, UserID: userID,
	})
	if err != nil {
		return reports.Scope{}, fmt.Errorf("resolve session roster access: %w", err)
	}
	scope := reports.Scope{CanView: row.CanView}
	if row.DepartmentFilter.Valid {
		scope.DepartmentFilter = row.DepartmentFilter.String()
	}
	scope.StudentFilter = nullableText(row.StudentFilter)
	scope.CanOverride = row.CanOverride
	return scope, nil
}

func (s *Store) ScannerCoverageData(ctx context.Context, institutionIDText, sessionIDText string, asOf time.Time, offlineAfter time.Duration) (reports.ScannerCoverageInput, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return reports.ScannerCoverageInput{}, err
	}
	sessionID, err := db.ParseUUID(sessionIDText)
	if err != nil {
		return reports.ScannerCoverageInput{}, err
	}
	meta, err := s.queries.GetSessionCoverageMetadata(ctx, dbgen.GetSessionCoverageMetadataParams{
		InstitutionID: institutionID, ID: sessionID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return reports.ScannerCoverageInput{}, reports.ErrSessionNotFound
	}
	if err != nil {
		return reports.ScannerCoverageInput{}, fmt.Errorf("load session coverage metadata: %w", err)
	}
	input := reports.ScannerCoverageInput{
		SessionStart: meta.StartedAt.Time.UTC(),
		SessionEnd:   timestampOrZero(meta.EndedAt),
		AsOf:         asOf.UTC(),
		OfflineAfter: offlineAfter,
	}
	manualAt, err := s.queries.GetManualScannerUnavailableAt(ctx, dbgen.GetManualScannerUnavailableAtParams{
		InstitutionID: institutionID, SessionID: sessionID,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return reports.ScannerCoverageInput{}, fmt.Errorf("load manual scanner unavailable timestamp: %w", err)
	}
	if err == nil {
		unavailableAt := manualAt.Time.UTC()
		input.UnavailableAt = &unavailableAt
	}
	devices, err := s.queries.ListSessionCoverageDevices(ctx, dbgen.ListSessionCoverageDevicesParams{
		InstitutionID: institutionID, VenueID: meta.VenueID,
	})
	if err != nil {
		return reports.ScannerCoverageInput{}, fmt.Errorf("list session scanner devices: %w", err)
	}
	if len(devices) == 0 {
		return input, nil
	}
	lowerBound := meta.StartedAt.Time.Add(-offlineAfter)
	heartbeats, err := s.queries.ListSessionHeartbeats(ctx, dbgen.ListSessionHeartbeatsParams{
		InstitutionID: institutionID, VenueID: meta.VenueID,
		HeartbeatAt:   pgtype.Timestamptz{Time: lowerBound.UTC(), Valid: true},
		HeartbeatAt_2: pgtype.Timestamptz{Time: asOf.UTC(), Valid: true},
	})
	if err != nil {
		return reports.ScannerCoverageInput{}, fmt.Errorf("list session scanner heartbeats: %w", err)
	}
	grouped := make(map[string][]time.Time, len(devices))
	for _, device := range devices {
		grouped[device.String()] = nil
	}
	for _, heartbeat := range heartbeats {
		key := heartbeat.DeviceID.String()
		grouped[key] = append(grouped[key], heartbeat.HeartbeatAt.Time.UTC())
	}
	input.Heartbeats = make([][]time.Time, 0, len(grouped))
	for _, values := range grouped {
		input.Heartbeats = append(input.Heartbeats, values)
	}
	return input, nil
}

func (s *Store) ReportRows(ctx context.Context, institutionIDText, sessionIDText, departmentIDText, studentIDText string, limit, offset int) ([]reports.ReportRow, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return nil, err
	}
	sessionID, err := db.ParseUUID(sessionIDText)
	if err != nil {
		return nil, err
	}
	departmentID, err := nullableUUID(departmentIDText)
	if err != nil {
		return nil, err
	}
	studentID, err := nullableUUID(studentIDText)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.SessionReportPage(ctx, dbgen.SessionReportPageParams{
		InstitutionID: institutionID, SessionID: sessionID, Column3: departmentID,
		Limit: int32(limit), Offset: int32(offset), Column6: studentID,
	})
	if err != nil {
		return nil, fmt.Errorf("query session report page: %w", err)
	}
	items := make([]reports.ReportRow, 0, len(rows))
	for _, row := range rows {
		item := reports.ReportRow{
			StudentRecordID: row.StudentRecordID.String(), MatricNo: row.MatricNo,
			DisplayName: row.DisplayName, DepartmentID: row.DepartmentID.String(), Level: int(row.Level),
			OnRoster: row.OnRoster, HasWebCheckin: row.HasWebCheckin, HasFingerprint: row.HasFingerprint,
			ScannerAvailable: row.ScannerAvailable, WebCheckinAt: db.TimestampPointer(row.WebCheckinAt),
		}
		if row.OverrideStatus.Valid {
			item.OverrideStatus = row.OverrideStatus.String
		}
		if row.WebStatus.Valid {
			item.WebStatus = row.WebStatus.String
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) RosterRows(ctx context.Context, institutionIDText, sessionIDText, departmentIDText, studentIDText string, limit, offset int) ([]reports.RosterStudent, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return nil, err
	}
	sessionID, err := db.ParseUUID(sessionIDText)
	if err != nil {
		return nil, err
	}
	departmentID, err := nullableUUID(departmentIDText)
	if err != nil {
		return nil, err
	}
	studentID, err := nullableUUID(studentIDText)
	if err != nil {
		return nil, err
	}
	exists, err := s.queries.ReportSessionExists(ctx, dbgen.ReportSessionExistsParams{InstitutionID: institutionID, ID: sessionID})
	if err != nil {
		return nil, fmt.Errorf("check roster session existence: %w", err)
	}
	if !exists {
		return nil, reports.ErrSessionNotFound
	}
	rows, err := s.queries.SessionRosterPage(ctx, dbgen.SessionRosterPageParams{
		InstitutionID: institutionID, ID: sessionID, Column3: departmentID,
		Limit: int32(limit), Offset: int32(offset), Column6: studentID,
	})
	if err != nil {
		return nil, fmt.Errorf("query session roster page: %w", err)
	}
	items := make([]reports.RosterStudent, 0, len(rows))
	for _, row := range rows {
		items = append(items, reports.RosterStudent{
			StudentRecordID: row.StudentRecordID.String(), MatricNo: row.MatricNo,
			DisplayName: row.DisplayName, DepartmentID: row.DepartmentID.String(),
			DepartmentName: row.DepartmentName, Level: int(row.Level),
		})
	}
	return items, nil
}

func (s *Store) SaveOverride(ctx context.Context, institutionIDText, sessionIDText, studentIDText, actorIDText,
	status, reasonCode, note, departmentIDText string, changedAt time.Time) error {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return err
	}
	sessionID, err := db.ParseUUID(sessionIDText)
	if err != nil {
		return err
	}
	studentID, err := db.ParseUUID(studentIDText)
	if err != nil {
		return reports.ErrInvalidScope
	}
	actorID, err := db.ParseUUID(actorIDText)
	if err != nil {
		return err
	}
	departmentID, err := nullableUUID(departmentIDText)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin attendance override: %w", err)
	}
	defer tx.Rollback(ctx)
	queries := s.queries.WithTx(tx)
	ended, err := queries.IsSessionEnded(ctx, dbgen.IsSessionEndedParams{InstitutionID: institutionID, ID: sessionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return reports.ErrSessionNotFound
	}
	if err != nil {
		return fmt.Errorf("check session state for override: %w", err)
	}
	if !ended {
		return reports.ErrSessionOpen
	}
	inScope, err := queries.IsStudentInOverrideScope(ctx, dbgen.IsStudentInOverrideScopeParams{
		InstitutionID: institutionID, ID: studentID, Column3: departmentID,
	})
	if err != nil {
		return fmt.Errorf("check student override scope: %w", err)
	}
	if !inScope {
		return reports.ErrInvalidScope
	}
	timestamp := pgtype.Timestamptz{Time: changedAt.UTC(), Valid: true}
	if err := queries.UpsertAttendanceOverride(ctx, dbgen.UpsertAttendanceOverrideParams{
		InstitutionID: institutionID, SessionID: sessionID, StudentRecordID: studentID,
		Status: status, Reason: note, ReasonCode: reasonCode, Note: note,
		ChangedByUserID: actorID, CreatedAt: timestamp,
	}); err != nil {
		return fmt.Errorf("upsert attendance override: %w", err)
	}
	details, err := json.Marshal(map[string]string{
		"student_record_id": studentID.String(), "status": status, "reason_code": reasonCode, "note": note,
	})
	if err != nil {
		return fmt.Errorf("encode override audit details: %w", err)
	}
	if err := queries.InsertAuditLog(ctx, dbgen.InsertAuditLogParams{
		InstitutionID: institutionID, ActorUserID: actorID, Action: "attendance.override",
		ResourceType: "lecture_session", ResourceID: sessionID.String(), Details: details,
		CreatedAt: timestamp,
	}); err != nil {
		return fmt.Errorf("audit attendance override: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit attendance override: %w", err)
	}
	return nil
}

func nullableUUID(value string) (pgtype.UUID, error) {
	if value == "" {
		return pgtype.UUID{}, nil
	}
	return db.ParseUUID(value)
}

func timestampOrZero(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}

func nullableText(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case []byte:
		return string(value)
	default:
		return ""
	}
}

var _ reports.ReportStore = (*Store)(nil)
