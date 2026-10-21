package studentportal_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"attendance-api/internal/reports"
	"attendance-api/internal/studentportal"
)

type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }

type fakeStore struct {
	courses    []studentportal.CourseRow
	timetable  []studentportal.TimetableRow
	attendance []studentportal.AttendanceEvidence
	approved   bool
	pending    []studentportal.PendingRegistration
}

func (s *fakeStore) CourseRows(context.Context, string, string, string, string, time.Time) ([]studentportal.CourseRow, error) {
	return s.courses, nil
}
func (s *fakeStore) TimetableRows(context.Context, string, string, string, string) ([]studentportal.TimetableRow, error) {
	return s.timetable, nil
}
func (s *fakeStore) AttendanceRows(context.Context, string, string, string, string, time.Time) ([]studentportal.AttendanceEvidence, bool, error) {
	return s.attendance, s.approved, nil
}
func (s *fakeStore) PendingRows(context.Context, string, string, string) ([]studentportal.PendingRegistration, error) {
	return s.pending, nil
}

func newService(t *testing.T, store *fakeStore) *studentportal.Service {
	t.Helper()
	service, err := studentportal.NewService(store, fakeClock{now: time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestCoursesUsesAttendanceMathAndOnlyReturnsStoreRows(t *testing.T) {
	t.Parallel()
	store := &fakeStore{courses: []studentportal.CourseRow{
		{OfferingID: "offering-a", CourseCode: "ECE301", CourseTitle: "Circuits", LecturerNames: []string{"Dr A"}, MinimumPercent: 75, LateCounts: true, SessionID: "s1", Status: "present"},
		{OfferingID: "offering-a", CourseCode: "ECE301", CourseTitle: "Circuits", LecturerNames: []string{"Dr A"}, MinimumPercent: 75, LateCounts: true, SessionID: "s2", Status: "late"},
		{OfferingID: "offering-a", CourseCode: "ECE301", CourseTitle: "Circuits", LecturerNames: []string{"Dr A"}, MinimumPercent: 75, LateCounts: true, SessionID: "s3", Status: "absent"},
		{OfferingID: "offering-b", CourseCode: "GST101", CourseTitle: "Study Skills", LecturerNames: []string{}, MinimumPercent: 50, LateCounts: false},
	}}
	items, err := newService(t, store).Courses(context.Background(), "tenant", "student-user", "own-record")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("course count=%d", len(items))
	}
	got := items[0]
	if got.OfferingID != "offering-a" || got.SessionsHeld != 3 || got.SessionsAttended != 2 ||
		got.AttendancePercentage != 200.0/3 || !got.BelowThreshold || len(got.LecturerNames) != 1 {
		t.Fatalf("attendance summary=%+v", got)
	}
	if items[1].SessionsHeld != 0 || items[1].AttendancePercentage != 0 || items[1].BelowThreshold {
		t.Fatalf("empty course summary=%+v", items[1])
	}
}

func TestTimetableUsesLagosDateAndOpenSessionWindow(t *testing.T) {
	t.Parallel()
	store := &fakeStore{timetable: []studentportal.TimetableRow{{
		OfferingID: "offering", CourseCode: "ECE301", CourseTitle: "Circuits",
		VenueID: "venue", VenueName: "Hall", StartsAt: "10:00:00", EndsAt: "12:00:00", HasOpen: true,
	}}}
	date, items, err := newService(t, store).Timetable(context.Background(), "tenant", "user", "student", "")
	if err != nil {
		t.Fatal(err)
	}
	if date != "2026-10-21" || len(items) != 1 || !items[0].LiveNow {
		t.Fatalf("timetable date=%q entries=%+v", date, items)
	}
	if _, _, err := newService(t, store).Timetable(context.Background(), "tenant", "user", "student", "2026-02-30"); !errors.Is(err, studentportal.ErrInvalidDate) {
		t.Fatalf("invalid date error=%v", err)
	}
}

func TestAttendanceOverridesWinAndUnverifiedWebCheckinIsClassified(t *testing.T) {
	t.Parallel()
	store := &fakeStore{approved: true, attendance: []studentportal.AttendanceEvidence{
		{
			SessionID: "overridden", StartedAt: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC),
			OverrideStatus: "excused", OverrideReason: "Medical", WebStatus: "late", HasWebCheckin: true,
		},
		{
			SessionID: "web-only", StartedAt: time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC),
			WebStatus: "late", HasWebCheckin: true, ScannerAvailable: true,
			ScannerCoverage: reports.ScannerCoverage{State: reports.CoverageNone},
		},
	}}
	items, err := newService(t, store).Attendance(context.Background(), "tenant", "user", "student", "offering")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Status != "excused" ||
		items[0].Classification != reports.Classification("excused") || items[0].OverrideReason != "Medical" ||
		items[1].Status != "late" || items[1].Classification != reports.WebOnlyUnverified {
		t.Fatalf("attendance history=%+v", items)
	}
	store.approved = false
	if _, err := newService(t, store).Attendance(context.Background(), "tenant", "user", "student", "other"); !errors.Is(err, studentportal.ErrOfferingUnapproved) {
		t.Fatalf("unapproved offering error=%v", err)
	}
}

func TestStudentServicesRejectMissingClaimedRecord(t *testing.T) {
	t.Parallel()
	service := newService(t, &fakeStore{})
	if _, err := service.Courses(context.Background(), "tenant", "user", ""); !errors.Is(err, studentportal.ErrNotStudent) {
		t.Fatalf("courses error=%v", err)
	}
	if _, err := service.PendingRegistrations(context.Background(), "tenant", "user", ""); !errors.Is(err, studentportal.ErrNotStudent) {
		t.Fatalf("pending registrations error=%v", err)
	}
}
