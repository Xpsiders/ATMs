package postgres

import (
	"context"
	"fmt"

	"attendance-api/internal/db"
	"attendance-api/internal/reports"
)

func (s *Store) LiveCounts(ctx context.Context, institutionIDText, sessionIDText, departmentIDText, studentIDText string) (reports.LiveCounts, error) {
	tenant, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return reports.LiveCounts{}, err
	}
	sessionID, err := db.ParseUUID(sessionIDText)
	if err != nil {
		return reports.LiveCounts{}, err
	}
	departmentID, err := nullableUUID(departmentIDText)
	if err != nil {
		return reports.LiveCounts{}, err
	}
	studentID, err := nullableUUID(studentIDText)
	if err != nil {
		return reports.LiveCounts{}, err
	}
	var counts reports.LiveCounts
	err = s.pool.QueryRow(ctx, `SELECT
		(SELECT count(DISTINCT wc.student_record_id) FROM web_checkins wc
		 JOIN student_records sr ON sr.institution_id=wc.institution_id AND sr.id=wc.student_record_id
		 WHERE wc.institution_id=$1 AND wc.session_id=$2
		   AND ($3::uuid IS NULL OR sr.department_id=$3) AND ($4::uuid IS NULL OR sr.id=$4)),
		(SELECT count(DISTINCT fs.student_record_id) FROM fingerprint_scans fs
		 JOIN student_records sr ON sr.institution_id=fs.institution_id AND sr.id=fs.student_record_id
		 WHERE fs.institution_id=$1 AND fs.session_id=$2
		   AND ($3::uuid IS NULL OR sr.department_id=$3) AND ($4::uuid IS NULL OR sr.id=$4)),
		(SELECT count(DISTINCT cr.student_record_id) FROM course_registrations cr
		 JOIN lecture_sessions ls ON ls.institution_id=cr.institution_id AND ls.offering_id=cr.offering_id
		 JOIN student_records sr ON sr.institution_id=cr.institution_id AND sr.id=cr.student_record_id
		 WHERE cr.institution_id=$1 AND ls.id=$2 AND cr.status='approved'
		   AND ($3::uuid IS NULL OR sr.department_id=$3) AND ($4::uuid IS NULL OR sr.id=$4))`,
		tenant, sessionID, departmentID, studentID).Scan(&counts.WebCheckins, &counts.Scans, &counts.Registered)
	if err != nil {
		return reports.LiveCounts{}, fmt.Errorf("count live session attendance: %w", err)
	}
	return counts, nil
}

var _ reports.LiveStore = (*Store)(nil)
