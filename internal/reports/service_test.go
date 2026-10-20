package reports_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"attendance-api/internal/reports"
	"attendance-api/internal/testutil"
)

type fakeReportStore struct {
	scope         reports.Scope
	coverage      reports.ScannerCoverageInput
	reportRows    []reports.ReportRow
	rosterRows    []reports.RosterStudent
	overrides     map[string]error
	rosterFilter  string
	studentFilter string
}

func (f *fakeReportStore) SessionScope(context.Context, string, string, string) (reports.Scope, error) {
	return f.scope, nil
}

func (f *fakeReportStore) ScannerCoverageData(context.Context, string, string, time.Time, time.Duration) (reports.ScannerCoverageInput, error) {
	return f.coverage, nil
}

func (f *fakeReportStore) ReportRows(_ context.Context, _, _, _, studentFilter string, limit, offset int) ([]reports.ReportRow, error) {
	f.studentFilter = studentFilter
	if offset >= len(f.reportRows) {
		return []reports.ReportRow{}, nil
	}
	end := offset + limit
	if end > len(f.reportRows) {
		end = len(f.reportRows)
	}
	return f.reportRows[offset:end], nil
}

func (f *fakeReportStore) RosterRows(_ context.Context, _, _, department, studentFilter string, limit, offset int) ([]reports.RosterStudent, error) {
	f.rosterFilter = department
	f.studentFilter = studentFilter
	if offset >= len(f.rosterRows) {
		return []reports.RosterStudent{}, nil
	}
	end := offset + limit
	if end > len(f.rosterRows) {
		end = len(f.rosterRows)
	}
	return f.rosterRows[offset:end], nil
}

func TestStudentReportIsRestrictedToOwnRecord(t *testing.T) {
	t.Parallel()
	store := &fakeReportStore{
		scope:      reports.Scope{CanView: true, StudentFilter: "student-own"},
		reportRows: []reports.ReportRow{{StudentRecordID: "student-own", OnRoster: true}},
		rosterRows: []reports.RosterStudent{{StudentRecordID: "student-own"}},
	}
	service := newReportService(t, store)
	page, err := service.SessionReport(context.Background(), "inst", "session", "student", reports.ReportFilter{})
	if err != nil || len(page.Items) != 1 || store.studentFilter != "student-own" {
		t.Fatalf("student report=%+v filter=%q err=%v", page, store.studentFilter, err)
	}
	roster, err := service.RosterSheet(context.Background(), "inst", "session", "student", reports.ReportFilter{})
	if err != nil || len(roster.Items) != 1 || store.studentFilter != "student-own" {
		t.Fatalf("student roster=%+v filter=%q err=%v", roster, store.studentFilter, err)
	}
	if _, err := service.BulkOverride(context.Background(), "inst", "session", "student",
		reports.OverrideInput{StudentIDs: []string{"student-own"}, Status: "present", ReasonCode: "other"}); !errors.Is(err, reports.ErrReportForbidden) {
		t.Fatalf("student override error=%v", err)
	}
}

func (f *fakeReportStore) SaveOverride(_ context.Context, _, _, studentID, _ string, _, _, _, _ string, _ time.Time) error {
	if f.overrides == nil {
		f.overrides = map[string]error{}
	}
	return f.overrides[studentID]
}

func newReportService(t *testing.T, store *fakeReportStore) *reports.Service {
	t.Helper()
	service, err := reports.NewService(store, testutil.NewFakeClock(time.Unix(1_800_000_000, 0)), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestSessionReportUsesCoverageAndIncludesUnrosteredStudents(t *testing.T) {
	t.Parallel()
	checkedAt := time.Unix(1_799_999_900, 0).UTC()
	store := &fakeReportStore{
		scope: reports.Scope{CanView: true},
		coverage: reports.ScannerCoverageInput{
			SessionStart: time.Unix(1_799_999_800, 0).UTC(),
			AsOf:         time.Unix(1_800_000_000, 0).UTC(),
			OfflineAfter: time.Minute,
			Heartbeats:   [][]time.Time{{time.Unix(1_799_999_700, 0).UTC()}},
			Unavailable:  true,
		},
		reportRows: []reports.ReportRow{
			{StudentRecordID: "student-a", HasWebCheckin: true, ScannerAvailable: true, WebCheckinAt: &checkedAt},
			{StudentRecordID: "student-b", HasFingerprint: true, ScannerAvailable: true, OnRoster: false},
		},
	}
	page, err := newReportService(t, store).SessionReport(context.Background(), "inst", "session", "user", reports.ReportFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if page.ScannerCoverage.State != reports.CoverageNone || len(page.Items) != 2 ||
		page.Items[0].Classification != reports.WebOnlyUnverified || page.Items[0].Reason != "scanner_unavailable" ||
		page.Items[1].Classification != reports.PresentNoWebLog || page.Items[1].OnRoster {
		t.Fatalf("report page = %+v", page)
	}
}

func TestRosterSheetAppliesDepartmentScope(t *testing.T) {
	t.Parallel()
	store := &fakeReportStore{
		scope: reports.Scope{CanView: true, DepartmentFilter: "department-b"},
		rosterRows: []reports.RosterStudent{
			{StudentRecordID: "student-b", DepartmentID: "department-b"},
		},
	}
	page, err := newReportService(t, store).RosterSheet(context.Background(), "inst", "session", "user",
		reports.ReportFilter{DepartmentID: "department-b"})
	if err != nil {
		t.Fatal(err)
	}
	if store.rosterFilter != "department-b" || len(page.Items) != 1 || page.Items[0].DepartmentID != "department-b" {
		t.Fatalf("roster filter=%q page=%+v", store.rosterFilter, page)
	}
	if _, err := newReportService(t, store).RosterSheet(context.Background(), "inst", "session", "user",
		reports.ReportFilter{DepartmentID: "department-a"}); !errors.Is(err, reports.ErrInvalidScope) {
		t.Fatalf("out-of-scope request error = %v", err)
	}
}

func TestBulkOverrideReturnsPartialItemFailures(t *testing.T) {
	t.Parallel()
	store := &fakeReportStore{
		scope: reports.Scope{CanView: true, CanOverride: true},
		overrides: map[string]error{
			"student-b": reports.ErrInvalidScope,
		},
	}
	results, err := newReportService(t, store).BulkOverride(context.Background(), "inst", "session", "user",
		reports.OverrideInput{
			StudentIDs: []string{"student-a", "student-b", "student-a", ""},
			Status:     "present", ReasonCode: "power_outage",
		})
	if err != nil || len(results) != 4 || !results[0].Applied ||
		results[1].ErrorCode != "student_out_of_scope" ||
		results[2].ErrorCode != "duplicate_student_id" ||
		results[3].ErrorCode != "invalid_student_id" {
		t.Fatalf("bulk results = %+v, %v", results, err)
	}
}

func TestReportCSVIncludesClassificationAndEscapesFormulaCells(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	page := reports.ReportPage{Items: []reports.StudentAttendance{{
		StudentRecordID: "student-1", MatricNo: "=CMD()", DisplayName: "@formula",
		DepartmentID: "dept-1", Level: 200, OnRoster: true,
		Classification: reports.WebOnlyUnverified, Status: "present", Reason: "scanner_offline",
	}}}
	if err := reports.WriteCSV(&output, page); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte("classification")) ||
		!bytes.Contains(output.Bytes(), []byte("'=CMD()")) ||
		!bytes.Contains(output.Bytes(), []byte("'@formula")) {
		t.Fatalf("CSV missing classification or escaping: %s", output.String())
	}
}

func TestNewServiceRequiresValidDependencies(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Unix(1_800_000_000, 0))
	store := &fakeReportStore{}
	for _, test := range []struct {
		name    string
		store   reports.ReportStore
		clock   reports.Clock
		offline time.Duration
	}{
		{name: "missing store", clock: clock, offline: time.Minute},
		{name: "missing clock", store: store, offline: time.Minute},
		{name: "zero threshold", store: store, clock: clock},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := reports.NewService(test.store, test.clock, test.offline); err == nil {
				t.Fatal("expected invalid service dependencies to be rejected")
			}
		})
	}
}

func TestRosterCSVIncludesEscapedStudentFields(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	page := reports.RosterPage{Items: []reports.RosterStudent{{
		StudentRecordID: "student-1",
		MatricNo:        "MAT-1",
		DisplayName:     "=malicious",
		DepartmentID:    "dept-1",
		DepartmentName:  "Engineering",
		Level:           300,
	}}}
	if err := reports.WriteRosterCSV(&output, page); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte("student_record_id,matric_no,display_name")) ||
		!bytes.Contains(output.Bytes(), []byte("'=malicious")) {
		t.Fatalf("roster CSV output = %s", output.String())
	}
}

func TestSessionReportRejectsStudentDepartmentFilter(t *testing.T) {
	t.Parallel()
	store := &fakeReportStore{scope: reports.Scope{CanView: true, StudentFilter: "student-own"}}
	_, err := newReportService(t, store).SessionReport(context.Background(), "inst", "session", "student",
		reports.ReportFilter{DepartmentID: "another-department"})
	if !errors.Is(err, reports.ErrInvalidScope) {
		t.Fatalf("student requested department filter error = %v", err)
	}
}
