package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"attendance-api/internal/db"
	"attendance-api/internal/db/dbgen"
	"attendance-api/internal/reports"
	reportspostgres "attendance-api/internal/reports/postgres"
	"attendance-api/internal/studentportal"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool          *pgxpool.Pool
	coverageStore interface {
		ScannerCoverageData(context.Context, string, string, time.Time, time.Duration) (reports.ScannerCoverageInput, error)
	}
	offlineAfter time.Duration
}

func NewStore(pool *pgxpool.Pool, queries *dbgen.Queries, offlineAfter time.Duration) (*Store, error) {
	if pool == nil || queries == nil {
		return nil, errors.New("database pool and SQL queries are required")
	}
	if offlineAfter <= 0 {
		return nil, errors.New("device offline threshold must be positive")
	}
	coverageStore, err := reportspostgres.NewStore(pool, queries)
	if err != nil {
		return nil, fmt.Errorf("configure scanner coverage store: %w", err)
	}
	return &Store{pool: pool, coverageStore: coverageStore, offlineAfter: offlineAfter}, nil
}

func (s *Store) CourseRows(ctx context.Context, institutionText, userText, studentText, localDate string, now time.Time) ([]studentportal.CourseRow, error) {
	institution, user, student, err := parseScope(institutionText, userText, studentText)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
SELECT offering.id::text, course.code, course.title,
       ARRAY(
         SELECT DISTINCT lecturer.display_name
         FROM offering_lecturers AS assignment
         JOIN users AS lecturer
           ON lecturer.institution_id=assignment.institution_id
          AND lecturer.id=assignment.lecturer_user_id
         WHERE assignment.institution_id=offering.institution_id
           AND assignment.offering_id=offering.id
         ORDER BY lecturer.display_name
       ),
       offering.min_attendance_pct::double precision,
       offering.late_counts_as_attended,
       session.id::text,
       coalesce(override.status, web.status,
         CASE WHEN scan.session_id IS NOT NULL THEN 'present' ELSE 'absent' END)
FROM course_registrations AS registration
JOIN student_records AS student
  ON student.institution_id=registration.institution_id
 AND student.id=registration.student_record_id
 AND student.claimed_by_user_id=$2
JOIN course_offerings AS offering
  ON offering.institution_id=registration.institution_id
 AND offering.id=registration.offering_id
JOIN courses AS course
  ON course.institution_id=offering.institution_id
 AND course.id=offering.course_id
JOIN semesters AS semester
  ON semester.institution_id=offering.institution_id
 AND semester.id=offering.semester_id
JOIN academic_sessions AS academic
  ON academic.institution_id=semester.institution_id
 AND academic.id=semester.academic_session_id
LEFT JOIN lecture_sessions AS session
  ON session.institution_id=offering.institution_id
 AND session.offering_id=offering.id
 AND session.ended_at IS NOT NULL
 AND session.started_at <= $5
LEFT JOIN attendance_overrides AS override
  ON override.institution_id=session.institution_id
 AND override.session_id=session.id
 AND override.student_record_id=student.id
LEFT JOIN web_checkins AS web
  ON web.institution_id=session.institution_id
 AND web.session_id=session.id
 AND web.student_record_id=student.id
LEFT JOIN fingerprint_scans AS scan
  ON scan.institution_id=session.institution_id
 AND scan.session_id=session.id
 AND scan.student_record_id=student.id
WHERE registration.institution_id=$1
  AND registration.student_record_id=$3
  AND registration.status='approved'
  AND academic.current
  AND semester.starts_on <= $4::date
  AND semester.ends_on >= $4::date
ORDER BY course.code, offering.id, session.started_at, session.id`,
		institution, user, student, localDate, now)
	if err != nil {
		return nil, fmt.Errorf("query student course summaries: %w", err)
	}
	defer rows.Close()
	items := make([]studentportal.CourseRow, 0)
	for rows.Next() {
		var item studentportal.CourseRow
		var sessionID pgtype.Text
		if err := rows.Scan(&item.OfferingID, &item.CourseCode, &item.CourseTitle, &item.LecturerNames,
			&item.MinimumPercent, &item.LateCounts, &sessionID, &item.Status); err != nil {
			return nil, fmt.Errorf("scan student course summary: %w", err)
		}
		if sessionID.Valid {
			item.SessionID = sessionID.String
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate student course summaries: %w", err)
	}
	return items, nil
}

func (s *Store) TimetableRows(ctx context.Context, institutionText, userText, studentText, dateText string) ([]studentportal.TimetableRow, error) {
	institution, user, student, err := parseScope(institutionText, userText, studentText)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
SELECT offering.id::text, course.code, course.title, venue.id::text, venue.name,
       to_char(schedule.starts_at, 'HH24:MI:SS'),
       to_char(schedule.ends_at, 'HH24:MI:SS'),
       EXISTS (
         SELECT 1 FROM lecture_sessions AS live
         WHERE live.institution_id=offering.institution_id
           AND live.offering_id=offering.id
           AND live.ended_at IS NULL
           AND (live.timetable_id=schedule.id OR (live.timetable_id IS NULL AND live.venue_id=schedule.venue_id))
       )
FROM course_registrations AS registration
JOIN student_records AS student
  ON student.institution_id=registration.institution_id
 AND student.id=registration.student_record_id
 AND student.claimed_by_user_id=$2
JOIN course_offerings AS offering
  ON offering.institution_id=registration.institution_id
 AND offering.id=registration.offering_id
JOIN courses AS course
  ON course.institution_id=offering.institution_id
 AND course.id=offering.course_id
JOIN timetable AS schedule
  ON schedule.institution_id=offering.institution_id
 AND schedule.offering_id=offering.id
JOIN venues AS venue
  ON venue.institution_id=schedule.institution_id
 AND venue.id=schedule.venue_id
WHERE registration.institution_id=$1
  AND registration.student_record_id=$3
  AND registration.status='approved'
  AND schedule.weekday=extract(dow FROM $4::date)
ORDER BY schedule.starts_at, course.code, offering.id`,
		institution, user, student, dateText)
	if err != nil {
		return nil, fmt.Errorf("query student timetable: %w", err)
	}
	defer rows.Close()
	items := make([]studentportal.TimetableRow, 0)
	for rows.Next() {
		var item studentportal.TimetableRow
		if err := rows.Scan(&item.OfferingID, &item.CourseCode, &item.CourseTitle,
			&item.VenueID, &item.VenueName, &item.StartsAt, &item.EndsAt, &item.HasOpen); err != nil {
			return nil, fmt.Errorf("scan student timetable: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate student timetable: %w", err)
	}
	return items, nil
}

func (s *Store) AttendanceRows(ctx context.Context, institutionText, userText, studentText, offeringText string, asOf time.Time) ([]studentportal.AttendanceEvidence, bool, error) {
	institution, user, student, err := parseScope(institutionText, userText, studentText)
	if err != nil {
		return nil, false, err
	}
	offering, err := db.ParseUUID(offeringText)
	if err != nil {
		return nil, false, err
	}
	var approved bool
	err = s.pool.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM course_registrations AS registration
  JOIN student_records AS student
    ON student.institution_id=registration.institution_id
   AND student.id=registration.student_record_id
   AND student.claimed_by_user_id=$2
  WHERE registration.institution_id=$1
    AND registration.student_record_id=$3
    AND registration.offering_id=$4
    AND registration.status='approved'
)`, institution, user, student, offering).Scan(&approved)
	if err != nil {
		return nil, false, fmt.Errorf("check approved offering registration: %w", err)
	}
	if !approved {
		return nil, false, nil
	}
	rows, err := s.pool.Query(ctx, `
SELECT session.id::text, session.started_at,
       coalesce(override.status, web.status,
         CASE WHEN scan.session_id IS NOT NULL THEN 'present' ELSE 'absent' END),
       coalesce(override.reason, ''),
       coalesce(web.status, ''),
       web.session_id IS NOT NULL,
       scan.session_id IS NOT NULL
FROM lecture_sessions AS session
LEFT JOIN attendance_overrides AS override
  ON override.institution_id=session.institution_id
 AND override.session_id=session.id
 AND override.student_record_id=$3
LEFT JOIN web_checkins AS web
  ON web.institution_id=session.institution_id
 AND web.session_id=session.id
 AND web.student_record_id=$3
LEFT JOIN fingerprint_scans AS scan
  ON scan.institution_id=session.institution_id
 AND scan.session_id=session.id
 AND scan.student_record_id=$3
WHERE session.institution_id=$1
  AND session.offering_id=$4
  AND session.ended_at IS NOT NULL
  AND EXISTS (
    SELECT 1 FROM student_records AS owner
    WHERE owner.institution_id=$1 AND owner.id=$3 AND owner.claimed_by_user_id=$2
  )
ORDER BY session.started_at DESC, session.id`,
		institution, user, student, offering)
	if err != nil {
		return nil, false, fmt.Errorf("query student attendance: %w", err)
	}
	defer rows.Close()
	items := make([]studentportal.AttendanceEvidence, 0)
	for rows.Next() {
		var item studentportal.AttendanceEvidence
		if err := rows.Scan(&item.SessionID, &item.StartedAt, &item.OverrideStatus,
			&item.OverrideReason, &item.WebStatus, &item.HasWebCheckin, &item.HasFingerprint,
		); err != nil {
			return nil, false, fmt.Errorf("scan student attendance: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate student attendance: %w", err)
	}
	for index := range items {
		coverage, err := s.coverageStore.ScannerCoverageData(ctx, institutionText, items[index].SessionID, asOf, s.offlineAfter)
		if err != nil {
			return nil, false, fmt.Errorf("load scanner coverage for student attendance session %s: %w", items[index].SessionID, err)
		}
		items[index].ScannerAvailable = len(coverage.Heartbeats) > 0
		items[index].ScannerCoverage = reports.ComputeScannerCoverage(coverage)
	}
	return items, true, nil
}

func (s *Store) PendingRows(ctx context.Context, institutionText, userText, studentText string) ([]studentportal.PendingRegistration, error) {
	institution, user, student, err := parseScope(institutionText, userText, studentText)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
SELECT offering.id::text, course.code, course.title, registration.status, registration.created_at
FROM course_registrations AS registration
JOIN student_records AS student
  ON student.institution_id=registration.institution_id
 AND student.id=registration.student_record_id
 AND student.claimed_by_user_id=$2
JOIN course_offerings AS offering
  ON offering.institution_id=registration.institution_id
 AND offering.id=registration.offering_id
JOIN courses AS course
  ON course.institution_id=offering.institution_id
 AND course.id=offering.course_id
WHERE registration.institution_id=$1
  AND registration.student_record_id=$3
  AND registration.status='pending'
ORDER BY registration.created_at DESC, offering.id`, institution, user, student)
	if err != nil {
		return nil, fmt.Errorf("query pending student registrations: %w", err)
	}
	defer rows.Close()
	items := make([]studentportal.PendingRegistration, 0)
	for rows.Next() {
		var item studentportal.PendingRegistration
		if err := rows.Scan(&item.OfferingID, &item.CourseCode, &item.CourseTitle, &item.Status, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan pending student registration: %w", err)
		}
		item.CreatedAt = item.CreatedAt.UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending student registrations: %w", err)
	}
	return items, nil
}

func parseScope(institutionText, userText, studentText string) (pgtype.UUID, pgtype.UUID, pgtype.UUID, error) {
	institution, err := db.ParseUUID(institutionText)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, err
	}
	user, err := db.ParseUUID(userText)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, err
	}
	student, err := db.ParseUUID(studentText)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, err
	}
	return institution, user, student, nil
}

var _ studentportal.Store = (*Store)(nil)
