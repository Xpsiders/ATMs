package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"attendance-api/internal/catalogue"
	"attendance-api/internal/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) CreateLecturerOffering(ctx context.Context, institutionIDText, userIDText string, input catalogue.OfferingInput) (map[string]any, error) {
	tenant, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return nil, err
	}
	user, err := db.ParseUUID(userIDText)
	if err != nil {
		return nil, err
	}
	course, err := db.ParseUUID(input.CourseID)
	if err != nil {
		return nil, catalogue.ErrInvalidRequest
	}
	semester, err := db.ParseUUID(input.SemesterID)
	if err != nil {
		return nil, catalogue.ErrInvalidRequest
	}
	lateCounts := input.LateCountsAsAttended == nil || *input.LateCountsAsAttended
	targetsJSON, err := json.Marshal(input.AutoTargets)
	if err != nil {
		return nil, fmt.Errorf("encode offering targets: %w", err)
	}
	slotsJSON, err := json.Marshal(input.Timetable)
	if err != nil {
		return nil, fmt.Errorf("encode offering timetable: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin offering creation: %w", err)
	}
	defer tx.Rollback(ctx)
	var department, faculty pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT d.id, d.faculty_id
		FROM courses c JOIN departments d ON d.institution_id=c.institution_id AND d.id=c.department_id
		WHERE c.institution_id=$1 AND c.id=$2`, tenant, course).Scan(&department, &faculty); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, catalogue.ErrNotFound
		}
		return nil, fmt.Errorf("load course ownership: %w", err)
	}
	var exists bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM semesters WHERE institution_id=$1 AND id=$2)", tenant, semester).Scan(&exists); err != nil {
		return nil, fmt.Errorf("validate offering semester: %w", err)
	}
	if !exists {
		return nil, catalogue.ErrNotFound
	}
	for _, target := range input.AutoTargets {
		targetDepartment, err := db.ParseUUID(target.DepartmentID)
		if err != nil {
			return nil, catalogue.ErrInvalidRequest
		}
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM departments WHERE institution_id=$1 AND id=$2)", tenant, targetDepartment).Scan(&exists); err != nil {
			return nil, fmt.Errorf("validate auto-registration department: %w", err)
		}
		if !exists {
			return nil, catalogue.ErrInvalidRequest
		}
	}
	for _, slot := range input.Timetable {
		venue, err := db.ParseUUID(slot.VenueID)
		if err != nil {
			return nil, catalogue.ErrInvalidRequest
		}
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM venues WHERE institution_id=$1 AND id=$2)", tenant, venue).Scan(&exists); err != nil {
			return nil, fmt.Errorf("validate timetable venue: %w", err)
		}
		if !exists {
			return nil, catalogue.ErrInvalidRequest
		}
	}
	var isLecturer, ownsCourse, isAdmin bool
	if err := tx.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM role_assignments WHERE institution_id=$1 AND user_id=$2 AND role='lecturer'),
		EXISTS(SELECT 1 FROM role_assignments WHERE institution_id=$1 AND user_id=$2 AND role='lecturer'
			AND scope_type='department' AND department_id=$3),
		EXISTS(SELECT 1 FROM role_assignments WHERE institution_id=$1 AND user_id=$2 AND (
			(role='department_admin' AND scope_type='department' AND department_id=$3)
			OR (role='faculty_admin' AND scope_type='faculty' AND faculty_id=$4)
			OR (role='institution_admin' AND scope_type='institution') OR role='platform_admin'))`,
		tenant, user, department, faculty).Scan(&isLecturer, &ownsCourse, &isAdmin); err != nil {
		return nil, fmt.Errorf("check offering management scope: %w", err)
	}
	// An administrator over the course's department may create the offering
	// directly, with no coordinator, and assign lecturers afterwards. An owning
	// lecturer also creates it directly, as its coordinator. A lecturer without
	// department ownership instead files a teaching request for admin review;
	// anyone else is forbidden.
	if isAdmin && !ownsCourse {
		offeringID, err := createOfferingTx(ctx, tx, tenant, user, pgtype.UUID{}, course, semester, input, lateCounts)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit administrator offering: %w", err)
		}
		return map[string]any{"status": "created", "id": offeringID.String(), "course_id": course.String(), "semester_id": semester.String()}, nil
	}
	if !isLecturer {
		return nil, catalogue.ErrForbidden
	}
	if !ownsCourse {
		var requestID string
		err := tx.QueryRow(ctx, `INSERT INTO teaching_requests (
			institution_id, lecturer_user_id, course_id, semester_id, level, registration_mode,
			roster_policy, min_attendance_pct, late_counts_as_attended, auto_targets, timetable
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11::jsonb) RETURNING id::text`,
			tenant, user, course, semester, input.Level, input.RegistrationMode, input.RosterPolicy,
			*input.MinAttendancePct, lateCounts, targetsJSON, slotsJSON).Scan(&requestID)
		if err != nil {
			return nil, fmt.Errorf("create pending teaching request: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_log(institution_id, actor_user_id, action, resource_type, resource_id, details)
			VALUES ($1,$2,'teaching_request.created','teaching_request',$3,jsonb_build_object('course_id',$4::uuid))`,
			tenant, user, requestID, course); err != nil {
			return nil, fmt.Errorf("audit teaching request: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit teaching request: %w", err)
		}
		return map[string]any{"status": "pending", "teaching_request_id": requestID}, nil
	}
	offeringID, err := createOfferingTx(ctx, tx, tenant, user, user, course, semester, input, lateCounts)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit lecturer offering: %w", err)
	}
	return map[string]any{"status": "created", "id": offeringID.String(), "course_id": course.String(), "semester_id": semester.String()}, nil
}

func (s *Store) ListTeachingRequests(ctx context.Context, institutionIDText, userIDText string, limit, offset int) ([]map[string]any, error) {
	tenant, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return nil, err
	}
	user, err := db.ParseUUID(userIDText)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT tr.id::text, tr.lecturer_user_id::text, lecturer.display_name,
		tr.course_id::text, c.code, c.title, tr.semester_id::text, tr.level,
		tr.registration_mode, tr.roster_policy, tr.min_attendance_pct, tr.auto_targets,
		tr.status, tr.created_at
		FROM teaching_requests tr
		JOIN users lecturer ON lecturer.institution_id=tr.institution_id AND lecturer.id=tr.lecturer_user_id
		JOIN courses c ON c.institution_id=tr.institution_id AND c.id=tr.course_id
		JOIN departments d ON d.institution_id=c.institution_id AND d.id=c.department_id
		WHERE tr.institution_id=$1 AND tr.status='pending' AND EXISTS (
			SELECT 1 FROM role_assignments ra WHERE ra.institution_id=tr.institution_id AND ra.user_id=$2
			AND ((ra.role='department_admin' AND ra.scope_type='department' AND ra.department_id=d.id)
			OR (ra.role='faculty_admin' AND ra.scope_type='faculty' AND ra.faculty_id=d.faculty_id)
			OR (ra.role='institution_admin' AND ra.scope_type='institution') OR ra.role='platform_admin'))
		ORDER BY tr.created_at, tr.id LIMIT $3 OFFSET $4`, tenant, user, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list teaching requests: %w", err)
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var item map[string]any
		var id, lecturerID, lecturerName, courseID, code, title, semesterID, mode, policy, status string
		var level int
		var minimum float64
		var targetsJSON []byte
		var createdAt time.Time
		if err := rows.Scan(&id, &lecturerID, &lecturerName, &courseID, &code, &title, &semesterID,
			&level, &mode, &policy, &minimum, &targetsJSON, &status, &createdAt); err != nil {
			return nil, fmt.Errorf("scan teaching request: %w", err)
		}
		var targets []catalogue.AutoTarget
		if err := json.Unmarshal(targetsJSON, &targets); err != nil {
			return nil, fmt.Errorf("decode teaching request targets: %w", err)
		}
		item = map[string]any{"id": id, "lecturer_id": lecturerID, "lecturer_name": lecturerName,
			"course_id": courseID, "course_code": code, "course_title": title, "semester_id": semesterID,
			"level": level, "registration_mode": mode, "roster_policy": policy,
			"min_attendance_pct": minimum, "auto_targets": targets, "status": status, "created_at": createdAt.UTC()}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate teaching requests: %w", err)
	}
	return items, nil
}

func (s *Store) ReviewTeachingRequest(ctx context.Context, institutionIDText, userIDText, requestIDText, status string) (map[string]any, error) {
	tenant, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return nil, err
	}
	user, err := db.ParseUUID(userIDText)
	if err != nil {
		return nil, err
	}
	requestID, err := db.ParseUUID(requestIDText)
	if err != nil {
		return nil, catalogue.ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin teaching request review: %w", err)
	}
	defer tx.Rollback(ctx)
	var lecturer, course, semester, department, faculty pgtype.UUID
	var level int
	var mode, policy string
	var minimum float64
	var lateCounts bool
	var targetsJSON, slotsJSON []byte
	err = tx.QueryRow(ctx, `SELECT tr.lecturer_user_id, tr.course_id, tr.semester_id, tr.level,
		tr.registration_mode, tr.roster_policy, tr.min_attendance_pct, tr.late_counts_as_attended,
		tr.auto_targets, tr.timetable, d.id, d.faculty_id
		FROM teaching_requests tr JOIN courses c ON c.institution_id=tr.institution_id AND c.id=tr.course_id
		JOIN departments d ON d.institution_id=c.institution_id AND d.id=c.department_id
		WHERE tr.institution_id=$1 AND tr.id=$2 AND tr.status='pending' FOR UPDATE OF tr`,
		tenant, requestID).Scan(&lecturer, &course, &semester, &level, &mode, &policy, &minimum,
		&lateCounts, &targetsJSON, &slotsJSON, &department, &faculty)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, catalogue.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load teaching request: %w", err)
	}
	var allowed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM role_assignments ra
		WHERE ra.institution_id=$1 AND ra.user_id=$2 AND (
		(ra.role='department_admin' AND ra.scope_type='department' AND ra.department_id=$3)
		OR (ra.role='faculty_admin' AND ra.scope_type='faculty' AND ra.faculty_id=$4)
		OR (ra.role='institution_admin' AND ra.scope_type='institution') OR ra.role='platform_admin'))`,
		tenant, user, department, faculty).Scan(&allowed); err != nil {
		return nil, fmt.Errorf("authorize teaching request: %w", err)
	}
	if !allowed {
		return nil, catalogue.ErrForbidden
	}
	var offering pgtype.UUID
	if status == "approved" {
		var targets []catalogue.AutoTarget
		var timetable []catalogue.TimetableInput
		if err := json.Unmarshal(targetsJSON, &targets); err != nil {
			return nil, fmt.Errorf("decode request targets: %w", err)
		}
		if err := json.Unmarshal(slotsJSON, &timetable); err != nil {
			return nil, fmt.Errorf("decode request timetable: %w", err)
		}
		input := catalogue.OfferingInput{
			Level: level, RegistrationMode: mode, RosterPolicy: policy,
			MinAttendancePct: &minimum, AutoTargets: targets, Timetable: timetable,
		}
		offering, err = createOfferingTx(ctx, tx, tenant, user, lecturer, course, semester, input, lateCounts)
		if err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE teaching_requests SET status=$3, reviewed_by_user_id=$4,
		reviewed_at=now(), offering_id=$5 WHERE institution_id=$1 AND id=$2`,
		tenant, requestID, status, user, offering); err != nil {
		return nil, fmt.Errorf("save teaching request decision: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_log(institution_id, actor_user_id, action, resource_type, resource_id, details)
		VALUES ($1,$2,'teaching_request.reviewed','teaching_request',$3,jsonb_build_object('status',$4::text,'offering_id',$5::uuid))`,
		tenant, user, requestID, status, offering); err != nil {
		return nil, fmt.Errorf("audit teaching request decision: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit teaching request review: %w", err)
	}
	result := map[string]any{"id": requestID.String(), "status": status}
	if offering.Valid {
		result["offering_id"] = offering.String()
	}
	return result, nil
}

func createOfferingTx(ctx context.Context, tx pgx.Tx, tenant, actor, lecturer, course, semester pgtype.UUID, input catalogue.OfferingInput, lateCounts bool) (pgtype.UUID, error) {
	var offering pgtype.UUID
	// lecturer is the offering coordinator and first assigned lecturer. When it is
	// not set (an administrator created the offering), the coordinator is left
	// NULL and no lecturer is forced on; lecturers are assigned separately.
	err := tx.QueryRow(ctx, `INSERT INTO course_offerings (
		institution_id, course_id, semester_id, coordinator_user_id, level, registration_mode,
		roster_policy, min_attendance_pct, late_counts_as_attended
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		tenant, course, semester, lecturer, input.Level, input.RegistrationMode, input.RosterPolicy,
		*input.MinAttendancePct, lateCounts).Scan(&offering)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("create course offering: %w", err)
	}
	if lecturer.Valid {
		if _, err := tx.Exec(ctx, `INSERT INTO offering_lecturers(institution_id, offering_id, lecturer_user_id)
			VALUES ($1,$2,$3)`, tenant, offering, lecturer); err != nil {
			return pgtype.UUID{}, fmt.Errorf("assign lecturer to offering: %w", err)
		}
	}
	for _, target := range input.AutoTargets {
		department, err := db.ParseUUID(target.DepartmentID)
		if err != nil {
			return pgtype.UUID{}, catalogue.ErrInvalidRequest
		}
		tag, err := tx.Exec(ctx, `INSERT INTO offering_auto_targets(institution_id, offering_id, department_id, level)
			SELECT $1,$2,id,$4 FROM departments WHERE institution_id=$1 AND id=$3`,
			tenant, offering, department, target.Level)
		if err != nil {
			return pgtype.UUID{}, fmt.Errorf("create offering auto target: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return pgtype.UUID{}, catalogue.ErrInvalidRequest
		}
	}
	for _, slot := range input.Timetable {
		venue, err := db.ParseUUID(slot.VenueID)
		if err != nil {
			return pgtype.UUID{}, catalogue.ErrInvalidRequest
		}
		if err := createTimetableSlotTx(ctx, tx, tenant, actor, offering, venue, slot); err != nil {
			return pgtype.UUID{}, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_log(institution_id, actor_user_id, action, resource_type, resource_id, details)
		VALUES ($1,$2,'offering.created','course_offering',$3,jsonb_build_object('course_id',$4::uuid,'semester_id',$5::uuid))`,
		tenant, actor, offering, course, semester); err != nil {
		return pgtype.UUID{}, fmt.Errorf("audit offering creation: %w", err)
	}
	return offering, nil
}

func createTimetableSlotTx(ctx context.Context, tx pgx.Tx, tenant, actor, offering, venue pgtype.UUID, slot catalogue.TimetableInput) error {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))",
		fmt.Sprintf("%s:%s:%d", tenant.String(), venue.String(), slot.Weekday)); err != nil {
		return fmt.Errorf("lock timetable venue: %w", err)
	}
	var overlaps bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM timetable
		WHERE institution_id=$1 AND venue_id=$2 AND weekday=$3 AND starts_at < $5::time AND $4::time < ends_at)`,
		tenant, venue, slot.Weekday, slot.StartsAt, slot.EndsAt).Scan(&overlaps); err != nil {
		return fmt.Errorf("check timetable overlap: %w", err)
	}
	if overlaps {
		return catalogue.ErrTimetableOverlap
	}
	var id pgtype.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO timetable(institution_id, offering_id, venue_id, weekday, starts_at, ends_at)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, tenant, offering, venue, slot.Weekday,
		slot.StartsAt, slot.EndsAt).Scan(&id); err != nil {
		return fmt.Errorf("create timetable slot: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_log(institution_id, actor_user_id, action, resource_type, resource_id, details)
		VALUES ($1,$2,'timetable.created','timetable',$3,jsonb_build_object('offering_id',$4::uuid,'venue_id',$5::uuid,'weekday',$6::int))`,
		tenant, actor, id, offering, venue, slot.Weekday); err != nil {
		return fmt.Errorf("audit timetable creation: %w", err)
	}
	return nil
}

var _ catalogue.OfferingWorkflowStore = (*Store)(nil)
