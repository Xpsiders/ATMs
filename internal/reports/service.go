package reports

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrReportForbidden = errors.New("session report access denied")
	ErrInvalidScope    = errors.New("requested department is outside report scope")
	ErrSessionOpen     = errors.New("attendance overrides require a closed session")
	ErrInvalidOverride = errors.New("override fields are invalid")
)

type Clock interface {
	Now() time.Time
}

type ReportFilter struct {
	DepartmentID string
	Limit        int
	Offset       int
}

type StudentAttendance struct {
	StudentRecordID string         `json:"student_record_id"`
	MatricNo        string         `json:"matric_no"`
	DisplayName     string         `json:"display_name"`
	DepartmentID    string         `json:"department_id"`
	Level           int            `json:"level"`
	OnRoster        bool           `json:"on_roster"`
	Classification  Classification `json:"classification"`
	Status          string         `json:"status"`
	Reason          string         `json:"reason,omitempty"`
}

type ReportPage struct {
	SessionID       string              `json:"session_id"`
	ScannerCoverage ScannerCoverage     `json:"scanner_coverage"`
	Items           []StudentAttendance `json:"items"`
	Limit           int                 `json:"limit"`
	Offset          int                 `json:"offset"`
	HasMore         bool                `json:"has_more"`
}

type RosterStudent struct {
	StudentRecordID string `json:"student_record_id"`
	MatricNo        string `json:"matric_no"`
	DisplayName     string `json:"display_name"`
	DepartmentID    string `json:"department_id"`
	DepartmentName  string `json:"department_name"`
	Level           int    `json:"level"`
}

type RosterPage struct {
	SessionID string          `json:"session_id"`
	Items     []RosterStudent `json:"items"`
	Limit     int             `json:"limit"`
	Offset    int             `json:"offset"`
	HasMore   bool            `json:"has_more"`
}

type LiveCounts struct {
	WebCheckins int `json:"web_checkins"`
	Scans       int `json:"scans"`
	Registered  int `json:"registered"`
}

type LiveStudent struct {
	StudentID      string         `json:"student_id"`
	MatricNo       string         `json:"matric_no"`
	DisplayName    string         `json:"display_name"`
	Status         string         `json:"status"`
	Classification Classification `json:"classification"`
	OnRoster       bool           `json:"on_roster"`
	HasWebCheckin  bool           `json:"has_web_checkin"`
	HasScan        bool           `json:"has_scan"`
}

type LivePage struct {
	SessionID string        `json:"session_id"`
	Counts    LiveCounts    `json:"counts"`
	Items     []LiveStudent `json:"items"`
	Limit     int           `json:"limit"`
	Offset    int           `json:"offset"`
	HasMore   bool          `json:"has_more"`
}

type OverrideInput struct {
	StudentIDs []string `json:"student_ids"`
	Status     string   `json:"status"`
	ReasonCode string   `json:"reason_code"`
	Note       string   `json:"note"`
}

type OverrideResult struct {
	StudentID string `json:"student_id"`
	Applied   bool   `json:"applied"`
	ErrorCode string `json:"error_code,omitempty"`
}

type Scope struct {
	CanView          bool
	DepartmentFilter string
	StudentFilter    string
	CanOverride      bool
}

type ReportStore interface {
	SessionScope(context.Context, string, string, string) (Scope, error)
	ScannerCoverageData(context.Context, string, string, time.Time, time.Duration) (ScannerCoverageInput, error)
	ReportRows(context.Context, string, string, string, string, int, int) ([]ReportRow, error)
	RosterRows(context.Context, string, string, string, string, int, int) ([]RosterStudent, error)
	SaveOverride(context.Context, string, string, string, string, string, string, string, string, time.Time) error
}

type LiveStore interface {
	LiveCounts(context.Context, string, string, string, string) (LiveCounts, error)
}

type ReportRow struct {
	StudentRecordID  string
	MatricNo         string
	DisplayName      string
	DepartmentID     string
	Level            int
	OnRoster         bool
	HasWebCheckin    bool
	HasFingerprint   bool
	ScannerAvailable bool
	OverrideStatus   string
	WebStatus        string
	WebCheckinAt     *time.Time
}

type Service struct {
	store        ReportStore
	clock        Clock
	offlineAfter time.Duration
}

func NewService(store ReportStore, clock Clock, offlineAfter time.Duration) (*Service, error) {
	if store == nil || clock == nil {
		return nil, errors.New("report store and clock are required")
	}
	if offlineAfter <= 0 {
		return nil, errors.New("device offline threshold must be positive")
	}
	return &Service{store: store, clock: clock, offlineAfter: offlineAfter}, nil
}

func (s *Service) SessionReport(ctx context.Context, institutionID, sessionID, userID string, filter ReportFilter) (ReportPage, error) {
	filter, err := normalizeFilter(filter)
	if err != nil {
		return ReportPage{}, err
	}
	scope, err := s.authorize(ctx, institutionID, sessionID, userID, filter.DepartmentID)
	if err != nil {
		return ReportPage{}, err
	}
	department := scope.DepartmentFilter
	coverageInput, err := s.store.ScannerCoverageData(ctx, institutionID, sessionID, s.clock.Now().UTC(), s.offlineAfter)
	if errors.Is(err, ErrSessionNotFound) {
		return ReportPage{}, err
	}
	if err != nil {
		return ReportPage{}, fmt.Errorf("load session scanner coverage: %w", err)
	}
	coverage := ComputeScannerCoverage(coverageInput)
	rows, err := s.store.ReportRows(ctx, institutionID, sessionID, department, scope.StudentFilter, filter.Limit+1, filter.Offset)
	if err != nil {
		return ReportPage{}, fmt.Errorf("load session report: %w", err)
	}
	hasMore := len(rows) > filter.Limit
	if hasMore {
		rows = rows[:filter.Limit]
	}
	items := make([]StudentAttendance, 0, len(rows))
	for _, row := range rows {
		outcome := Classify(Record{
			OnRoster: row.OnRoster, HasWebCheckin: row.HasWebCheckin, HasFingerprint: row.HasFingerprint,
			ScannerAvailable: row.ScannerAvailable, OverrideStatus: row.OverrideStatus,
			WebStatus: row.WebStatus, WebCheckinAt: row.WebCheckinAt, ScannerCoverage: coverage,
		})
		items = append(items, StudentAttendance{
			StudentRecordID: row.StudentRecordID, MatricNo: row.MatricNo,
			DisplayName: row.DisplayName, DepartmentID: row.DepartmentID, Level: row.Level,
			OnRoster: row.OnRoster, Classification: outcome.Classification,
			Status: outcome.Status, Reason: outcome.Reason,
		})
	}
	return ReportPage{SessionID: sessionID, ScannerCoverage: coverage, Items: items,
		Limit: filter.Limit, Offset: filter.Offset, HasMore: hasMore}, nil
}

func (s *Service) RosterSheet(ctx context.Context, institutionID, sessionID, userID string, filter ReportFilter) (RosterPage, error) {
	filter, err := normalizeFilter(filter)
	if err != nil {
		return RosterPage{}, err
	}
	scope, err := s.authorize(ctx, institutionID, sessionID, userID, filter.DepartmentID)
	if err != nil {
		return RosterPage{}, err
	}
	rows, err := s.store.RosterRows(ctx, institutionID, sessionID, scope.DepartmentFilter, scope.StudentFilter, filter.Limit+1, filter.Offset)
	if errors.Is(err, ErrSessionNotFound) {
		return RosterPage{}, err
	}
	if err != nil {
		return RosterPage{}, fmt.Errorf("load session roster: %w", err)
	}
	hasMore := len(rows) > filter.Limit
	if hasMore {
		rows = rows[:filter.Limit]
	}
	return RosterPage{SessionID: sessionID, Items: rows, Limit: filter.Limit,
		Offset: filter.Offset, HasMore: hasMore}, nil
}

func (s *Service) BulkOverride(ctx context.Context, institutionID, sessionID, userID string, input OverrideInput) ([]OverrideResult, error) {
	if len(input.StudentIDs) == 0 || len(input.StudentIDs) > 500 ||
		!validOverrideStatus(input.Status) || !validReasonCode(input.ReasonCode) ||
		len(input.Note) > 1000 {
		return nil, ErrInvalidOverride
	}
	scope, err := s.authorize(ctx, institutionID, sessionID, userID, "")
	if err != nil {
		return nil, err
	}
	if !scope.CanOverride {
		return nil, ErrReportForbidden
	}
	now := s.clock.Now().UTC()
	results := make([]OverrideResult, len(input.StudentIDs))
	seen := make(map[string]struct{}, len(input.StudentIDs))
	for index, studentID := range input.StudentIDs {
		result := OverrideResult{StudentID: studentID}
		results[index] = result
		if studentID == "" {
			results[index].ErrorCode = "invalid_student_id"
			continue
		}
		if _, duplicate := seen[studentID]; duplicate {
			results[index].ErrorCode = "duplicate_student_id"
			continue
		}
		seen[studentID] = struct{}{}
		err := s.store.SaveOverride(ctx, institutionID, sessionID, studentID, userID,
			input.Status, input.ReasonCode, input.Note, scope.DepartmentFilter, now)
		switch {
		case errors.Is(err, ErrSessionNotFound):
			results[index].ErrorCode = "session_not_found"
		case errors.Is(err, ErrSessionOpen):
			results[index].ErrorCode = "session_open"
		case errors.Is(err, ErrInvalidScope):
			results[index].ErrorCode = "student_out_of_scope"
		case err != nil:
			return results, fmt.Errorf("save override for student %q: %w", studentID, err)
		default:
			results[index].Applied = true
		}
	}
	return results, nil
}

func WriteCSV(writer io.Writer, page ReportPage) error {
	csvWriter := csv.NewWriter(writer)
	if err := csvWriter.Write([]string{"student_record_id", "matric_no", "display_name", "department_id", "level", "on_roster", "classification", "status", "reason", "scanner_coverage_state", "scanner_offline_intervals"}); err != nil {
		return err
	}
	intervals, err := json.Marshal(page.ScannerCoverage.OfflineIntervals)
	if err != nil {
		return fmt.Errorf("encode scanner outage intervals for CSV: %w", err)
	}
	for _, item := range page.Items {
		row := []string{item.StudentRecordID, item.MatricNo, item.DisplayName, item.DepartmentID,
			strconv.Itoa(item.Level), strconv.FormatBool(item.OnRoster), string(item.Classification), item.Status,
			item.Reason, string(page.ScannerCoverage.State), string(intervals)}
		for index := range row {
			row[index] = safeCSVCell(row[index])
		}
		if err := csvWriter.Write(row); err != nil {
			return err
		}
	}
	csvWriter.Flush()
	return csvWriter.Error()
}

func WriteRosterCSV(writer io.Writer, page RosterPage) error {
	csvWriter := csv.NewWriter(writer)
	if err := csvWriter.Write([]string{"student_record_id", "matric_no", "display_name", "department_id", "department_name", "level"}); err != nil {
		return err
	}
	for _, item := range page.Items {
		row := []string{item.StudentRecordID, item.MatricNo, item.DisplayName, item.DepartmentID,
			item.DepartmentName, strconv.Itoa(item.Level)}
		for index := range row {
			row[index] = safeCSVCell(row[index])
		}
		if err := csvWriter.Write(row); err != nil {
			return err
		}
	}
	csvWriter.Flush()
	return csvWriter.Error()
}

func safeCSVCell(value string) string {
	if len(value) > 0 && strings.ContainsRune("=+-@\t\r", rune(value[0])) {
		return "'" + value
	}
	return value
}

func normalizeFilter(filter ReportFilter) (ReportFilter, error) {
	if filter.Limit == 0 {
		filter.Limit = 50
	}
	if filter.Limit < 1 || filter.Limit > 200 || filter.Offset < 0 {
		return ReportFilter{}, errors.New("limit must be 1..200 and offset must be non-negative")
	}
	return filter, nil
}

func (s *Service) authorize(ctx context.Context, institutionID, sessionID, userID, requestedDepartment string) (Scope, error) {
	scope, err := s.store.SessionScope(ctx, institutionID, sessionID, userID)
	if errors.Is(err, ErrSessionNotFound) {
		return Scope{}, ErrSessionNotFound
	}
	if err != nil {
		return Scope{}, fmt.Errorf("resolve session report scope: %w", err)
	}
	if !scope.CanView {
		return Scope{}, ErrReportForbidden
	}
	if scope.StudentFilter != "" && requestedDepartment != "" {
		return Scope{}, ErrInvalidScope
	}
	department, allowed := ResolveRosterDepartment(scope.DepartmentFilter == "", scope.DepartmentFilter, requestedDepartment)
	if !allowed {
		return Scope{}, ErrInvalidScope
	}
	scope.DepartmentFilter = department
	return scope, nil
}

func validOverrideStatus(status string) bool {
	switch status {
	case "present", "absent", "late", "excused":
		return true
	default:
		return false
	}
}

func validReasonCode(code string) bool {
	switch code {
	case "power_outage", "device_failure", "official_duty", "medical", "other":
		return true
	default:
		return false
	}
}

func (s *Service) LiveSession(ctx context.Context, institutionID, sessionID, userID string, filter ReportFilter) (LivePage, error) {
	filter, err := normalizeFilter(filter)
	if err != nil {
		return LivePage{}, err
	}
	scope, err := s.authorize(ctx, institutionID, sessionID, userID, filter.DepartmentID)
	if err != nil {
		return LivePage{}, err
	}
	store, ok := s.store.(LiveStore)
	if !ok {
		return LivePage{}, errors.New("live session counts are not supported by this store")
	}
	counts, err := store.LiveCounts(ctx, institutionID, sessionID, scope.DepartmentFilter, scope.StudentFilter)
	if err != nil {
		return LivePage{}, fmt.Errorf("load live session counts: %w", err)
	}
	input, err := s.store.ScannerCoverageData(ctx, institutionID, sessionID, s.clock.Now().UTC(), s.offlineAfter)
	if err != nil {
		return LivePage{}, fmt.Errorf("load live session scanner coverage: %w", err)
	}
	coverage := ComputeScannerCoverage(input)
	rows, err := s.store.ReportRows(ctx, institutionID, sessionID, scope.DepartmentFilter,
		scope.StudentFilter, filter.Limit+1, filter.Offset)
	if err != nil {
		return LivePage{}, fmt.Errorf("load live session roster: %w", err)
	}
	hasMore := len(rows) > filter.Limit
	if hasMore {
		rows = rows[:filter.Limit]
	}
	items := make([]LiveStudent, 0, len(rows))
	for _, row := range rows {
		outcome := Classify(Record{
			OnRoster: row.OnRoster, HasWebCheckin: row.HasWebCheckin, HasFingerprint: row.HasFingerprint,
			ScannerAvailable: row.ScannerAvailable, OverrideStatus: row.OverrideStatus,
			WebStatus: row.WebStatus, WebCheckinAt: row.WebCheckinAt, ScannerCoverage: coverage,
		})
		items = append(items, LiveStudent{
			StudentID: row.StudentRecordID, MatricNo: row.MatricNo, DisplayName: row.DisplayName,
			Status: outcome.Status, Classification: outcome.Classification, OnRoster: row.OnRoster,
			HasWebCheckin: row.HasWebCheckin, HasScan: row.HasFingerprint,
		})
	}
	return LivePage{SessionID: sessionID, Counts: counts, Items: items, Limit: filter.Limit,
		Offset: filter.Offset, HasMore: hasMore}, nil
}
