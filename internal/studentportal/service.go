package studentportal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"attendance-api/internal/reports"
)

var (
	ErrNotStudent         = errors.New("claimed student account required")
	ErrOfferingUnapproved = errors.New("offering is not approved for student")
	ErrInvalidDate        = errors.New("date must be YYYY-MM-DD")
)

type Clock interface {
	Now() time.Time
}

type Store interface {
	CourseRows(context.Context, string, string, string, string, time.Time) ([]CourseRow, error)
	TimetableRows(context.Context, string, string, string, string) ([]TimetableRow, error)
	AttendanceRows(context.Context, string, string, string, string, time.Time) ([]AttendanceEvidence, bool, error)
	PendingRows(context.Context, string, string, string) ([]PendingRegistration, error)
}

type CourseRow struct {
	OfferingID     string
	CourseCode     string
	CourseTitle    string
	LecturerNames  []string
	MinimumPercent float64
	LateCounts     bool
	SessionID      string
	Status         string
}

type CourseSummary struct {
	OfferingID               string   `json:"offering_id"`
	CourseCode               string   `json:"course_code"`
	CourseTitle              string   `json:"course_title"`
	LecturerNames            []string `json:"lecturer_names"`
	SessionsHeld             int      `json:"sessions_held"`
	SessionsAttended         int      `json:"sessions_attended"`
	AttendancePercentage     float64  `json:"attendance_percentage"`
	MinimumAttendancePercent float64  `json:"minimum_attendance_percentage"`
	BelowThreshold           bool     `json:"below_threshold"`
}

type TimetableRow struct {
	OfferingID  string
	CourseCode  string
	CourseTitle string
	VenueID     string
	VenueName   string
	StartsAt    string
	EndsAt      string
	HasOpen     bool
}

type TimetableEntry struct {
	OfferingID  string `json:"offering_id"`
	CourseCode  string `json:"course_code"`
	CourseTitle string `json:"course_title"`
	VenueID     string `json:"venue_id"`
	VenueName   string `json:"venue_name"`
	StartsAt    string `json:"starts_at"`
	EndsAt      string `json:"ends_at"`
	LiveNow     bool   `json:"live_now"`
}

type AttendanceEvidence struct {
	SessionID        string
	StartedAt        time.Time
	OverrideStatus   string
	OverrideReason   string
	WebStatus        string
	HasWebCheckin    bool
	HasFingerprint   bool
	ScannerAvailable bool
	ScannerCoverage  reports.ScannerCoverage
}

type AttendanceEntry struct {
	SessionID      string                 `json:"session_id"`
	Date           string                 `json:"date"`
	Status         string                 `json:"status"`
	Classification reports.Classification `json:"classification"`
	OverrideReason string                 `json:"override_reason,omitempty"`
}

type PendingRegistration struct {
	OfferingID  string    `json:"offering_id"`
	CourseCode  string    `json:"course_code"`
	CourseTitle string    `json:"course_title"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type Service struct {
	store Store
	clock Clock
}

func NewService(store Store, clock Clock) (*Service, error) {
	if store == nil || clock == nil {
		return nil, errors.New("student portal store and clock are required")
	}
	return &Service{store: store, clock: clock}, nil
}

func (s *Service) Courses(ctx context.Context, institutionID, userID, studentID string) ([]CourseSummary, error) {
	if studentID == "" {
		return nil, ErrNotStudent
	}
	now := s.clock.Now().UTC()
	loc, err := time.LoadLocation("Africa/Lagos")
	if err != nil {
		return nil, fmt.Errorf("load Africa/Lagos timezone: %w", err)
	}
	localDate := now.In(loc).Format("2006-01-02")
	rows, err := s.store.CourseRows(ctx, institutionID, userID, studentID, localDate, now)
	if err != nil {
		return nil, fmt.Errorf("load current semester courses: %w", err)
	}
	summaries := make([]CourseSummary, 0, len(rows))
	statusesByOffering := make(map[string][]reports.AttendanceStatus)
	lateCountsByOffering := make(map[string]bool)
	indexByOffering := make(map[string]int, len(rows))
	for _, row := range rows {
		index, exists := indexByOffering[row.OfferingID]
		if !exists {
			index = len(summaries)
			indexByOffering[row.OfferingID] = index
			lecturers := append([]string(nil), row.LecturerNames...)
			if lecturers == nil {
				lecturers = []string{}
			}
			summaries = append(summaries, CourseSummary{
				OfferingID: row.OfferingID, CourseCode: row.CourseCode, CourseTitle: row.CourseTitle,
				LecturerNames: lecturers, MinimumAttendancePercent: row.MinimumPercent,
			})
			lateCountsByOffering[row.OfferingID] = row.LateCounts
		}
		if row.SessionID != "" {
			item := &summaries[index]
			item.SessionsHeld++
			status := reports.AttendanceStatus(row.Status)
			statusesByOffering[row.OfferingID] = append(statusesByOffering[row.OfferingID], status)
		}
	}
	for offeringID, statuses := range statusesByOffering {
		item := &summaries[indexByOffering[offeringID]]
		lateCounts := lateCountsByOffering[offeringID]
		item.AttendancePercentage = reports.AttendancePercentage(statuses, lateCounts)
		item.BelowThreshold = reports.BelowThreshold(statuses, lateCounts, item.MinimumAttendancePercent)
		for _, status := range statuses {
			if status == reports.StatusPresent || (status == reports.StatusLate && lateCounts) {
				item.SessionsAttended++
			}
		}
	}
	return summaries, nil
}

func (s *Service) Timetable(ctx context.Context, institutionID, userID, studentID, dateText string) (string, []TimetableEntry, error) {
	if studentID == "" {
		return "", nil, ErrNotStudent
	}
	now := s.clock.Now().UTC()
	loc, err := time.LoadLocation("Africa/Lagos")
	if err != nil {
		return "", nil, fmt.Errorf("load Africa/Lagos timezone: %w", err)
	}
	if dateText == "" {
		dateText = now.In(loc).Format("2006-01-02")
	}
	date, err := time.ParseInLocation("2006-01-02", dateText, loc)
	if err != nil || date.Format("2006-01-02") != dateText {
		return "", nil, ErrInvalidDate
	}
	rows, err := s.store.TimetableRows(ctx, institutionID, userID, studentID, dateText)
	if err != nil {
		return "", nil, fmt.Errorf("load student timetable: %w", err)
	}
	localNow := now.In(loc)
	items := make([]TimetableEntry, 0, len(rows))
	for _, row := range rows {
		start, startErr := time.Parse("15:04:05", row.StartsAt)
		end, endErr := time.Parse("15:04:05", row.EndsAt)
		if startErr != nil || endErr != nil {
			return "", nil, errors.New("store returned an invalid timetable time")
		}
		live := row.HasOpen && localNow.Format("2006-01-02") == dateText &&
			!localNow.Before(time.Date(date.Year(), date.Month(), date.Day(), start.Hour(), start.Minute(), start.Second(), 0, loc)) &&
			localNow.Before(time.Date(date.Year(), date.Month(), date.Day(), end.Hour(), end.Minute(), end.Second(), 0, loc))
		items = append(items, TimetableEntry{
			OfferingID: row.OfferingID, CourseCode: row.CourseCode, CourseTitle: row.CourseTitle,
			VenueID: row.VenueID, VenueName: row.VenueName, StartsAt: row.StartsAt,
			EndsAt: row.EndsAt, LiveNow: live,
		})
	}
	return dateText, items, nil
}

func (s *Service) Attendance(ctx context.Context, institutionID, userID, studentID, offeringID string) ([]AttendanceEntry, error) {
	if studentID == "" {
		return nil, ErrNotStudent
	}
	rows, approved, err := s.store.AttendanceRows(ctx, institutionID, userID, studentID, offeringID, s.clock.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("load student attendance: %w", err)
	}
	if !approved {
		return nil, ErrOfferingUnapproved
	}
	items := make([]AttendanceEntry, 0, len(rows))
	for _, row := range rows {
		outcome := reports.Classify(reports.Record{
			OnRoster: true, HasWebCheckin: row.HasWebCheckin, HasFingerprint: row.HasFingerprint,
			ScannerAvailable: row.ScannerAvailable, OverrideStatus: row.OverrideStatus,
			WebStatus: row.WebStatus, ScannerCoverage: row.ScannerCoverage,
		})
		items = append(items, AttendanceEntry{
			SessionID: row.SessionID, Date: row.StartedAt.UTC().Format("2006-01-02"),
			Status: outcome.Status, Classification: outcome.Classification, OverrideReason: row.OverrideReason,
		})
	}
	return items, nil
}

func (s *Service) PendingRegistrations(ctx context.Context, institutionID, userID, studentID string) ([]PendingRegistration, error) {
	if studentID == "" {
		return nil, ErrNotStudent
	}
	items, err := s.store.PendingRows(ctx, institutionID, userID, studentID)
	if err != nil {
		return nil, fmt.Errorf("load pending student registrations: %w", err)
	}
	if items == nil {
		items = []PendingRegistration{}
	}
	return items, nil
}
