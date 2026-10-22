package reports_test

import (
	"context"
	"testing"

	"attendance-api/internal/reports"
)

func TestLiveSessionUsesCallerScopeAndAttendanceClassification(t *testing.T) {
	t.Parallel()
	store := &fakeReportStore{
		scope:      reports.Scope{CanView: true, DepartmentFilter: "department-a"},
		liveCounts: reports.LiveCounts{WebCheckins: 2, Scans: 1, Registered: 3},
		reportRows: []reports.ReportRow{
			{StudentRecordID: "student-a", OnRoster: true, HasWebCheckin: true, HasFingerprint: true, WebStatus: "late"},
			{StudentRecordID: "student-b", OnRoster: true},
		},
	}
	page, err := newReportService(t, store).LiveSession(context.Background(), "inst", "session", "lecturer",
		reports.ReportFilter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if store.countScope != "department-a:" || store.reportDepartment != "department-a" ||
		page.Counts.Registered != 3 || len(page.Items) != 1 || page.Items[0].Status != "late" ||
		!page.Items[0].HasWebCheckin || !page.Items[0].HasScan ||
		page.Items[0].Classification != reports.Verified || !page.HasMore {
		t.Fatalf("live page=%+v counts scope=%q report department=%q",
			page, store.countScope, store.reportDepartment)
	}
}
