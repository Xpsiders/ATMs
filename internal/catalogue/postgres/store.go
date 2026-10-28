package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"attendance-api/internal/catalogue"
	"attendance-api/internal/db"
	"attendance-api/internal/db/dbgen"
	"attendance-api/internal/rbac"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

type resourceConfig struct {
	table   string
	columns []string
}

type offeringSearchMetadata struct {
	mode      string
	level     int32
	lecturers []string
	status    string
}

var resources = map[catalogue.Resource]resourceConfig{
	catalogue.Institutions:     {"institutions", []string{"id", "name", "created_at"}},
	catalogue.Faculties:        {"faculties", []string{"id", "name", "created_at"}},
	catalogue.Departments:      {"departments", []string{"id", "faculty_id", "name", "code", "created_at"}},
	catalogue.Programmes:       {"programmes", []string{"id", "department_id", "name", "code", "created_at"}},
	catalogue.AcademicSessions: {"academic_sessions", []string{"id", "name", "starts_on", "ends_on", "current", "created_at"}},
	catalogue.Semesters:        {"semesters", []string{"id", "academic_session_id", "name", "starts_on", "ends_on", "created_at"}},
	catalogue.Courses:          {"courses", []string{"id", "department_id", "code", "title", "kind", "description", "credits", "created_at"}},
	catalogue.Offerings:        {"course_offerings", []string{"id", "course_id", "semester_id", "coordinator_user_id", "level", "registration_mode", "roster_policy", "min_attendance_pct", "late_counts_as_attended", "created_at"}},
}

func (s *Store) List(ctx context.Context, institutionID, userID, studentID string, resource catalogue.Resource, filters map[string]string) ([]map[string]any, error) {
	tenant, err := db.ParseUUID(institutionID)
	if err != nil {
		return nil, err
	}
	user, err := db.ParseUUID(userID)
	if err != nil {
		return nil, err
	}
	student, err := nullableUUID(studentID)
	if err != nil {
		return nil, err
	}
	config, ok := resources[resource]
	if !ok {
		return nil, catalogue.ErrInvalidRequest
	}
	limit, offset, err := pagination(filters)
	if err != nil {
		return nil, err
	}
	delete(filters, "limit")
	delete(filters, "offset")
	if resource == catalogue.Offerings && filters["q"] != "" {
		return s.searchOfferings(ctx, tenant, user, student, filters, limit, offset)
	}
	columns := make([]string, len(config.columns))
	for index, column := range config.columns {
		columns[index] = "t." + column
	}
	from := config.table + " AS t"
	if resource == catalogue.Offerings {
		columns = append(columns, "c.code AS course_code", "c.title AS course_title", "s.name AS semester_name")
		from += " JOIN courses AS c ON c.institution_id=t.institution_id AND c.id=t.course_id"
		from += " JOIN semesters AS s ON s.institution_id=t.institution_id AND s.id=t.semester_id"
	}
	tenantColumn := "t.institution_id"
	if resource == catalogue.Institutions {
		tenantColumn = "t.id"
	}
	query := "SELECT " + strings.Join(columns, ",") + " FROM " + from + " WHERE " + tenantColumn + "=$1"
	scope, err := catalogueReadScope(resource)
	if err != nil {
		return nil, err
	}
	args := []any{tenant, user}
	if strings.Contains(scope, "$3") {
		args = append(args, student)
	}
	query += " AND " + scope
	conditions := map[string]string{"id": "t.id"}
	switch resource {
	case catalogue.Faculties:
		conditions["q"] = "t.name ILIKE $FILTER"
	case catalogue.Departments:
		conditions["faculty_id"] = "t.faculty_id"
		conditions["q"] = "(t.name ILIKE $FILTER OR t.code ILIKE $FILTER)"
	case catalogue.Programmes:
		conditions["department_id"] = "t.department_id"
		conditions["q"] = "(t.name ILIKE $FILTER OR t.code ILIKE $FILTER)"
	case catalogue.AcademicSessions:
		conditions["current"] = "t.current"
		conditions["q"] = "t.name ILIKE $FILTER"
	case catalogue.Semesters:
		conditions["academic_session_id"] = "t.academic_session_id"
		conditions["q"] = "t.name ILIKE $FILTER"
	case catalogue.Courses:
		conditions["department_id"] = "t.department_id"
		conditions["faculty_id"] = "t.department_id IN (SELECT d.id FROM departments d WHERE d.institution_id=t.institution_id AND d.faculty_id=$FILTER)"
		conditions["kind"] = "t.kind"
		conditions["q"] = "(t.code ILIKE $FILTER OR t.title ILIKE $FILTER OR t.description ILIKE $FILTER)"
	case catalogue.Offerings:
		conditions["course_id"] = "t.course_id"
		conditions["semester_id"] = "t.semester_id"
		conditions["level"] = "t.level"
		conditions["registration_mode"] = "t.registration_mode"
		conditions["faculty_id"] = "c.department_id IN (SELECT d.id FROM departments d WHERE d.institution_id=t.institution_id AND d.faculty_id=$FILTER)"
		conditions["department_id"] = "c.department_id"
		conditions["q"] = "(c.code ILIKE $FILTER OR c.title ILIKE $FILTER)"
	}
	if resource == catalogue.Offerings {
		conditions["kind"] = "c.kind"
	}
	keys := make([]string, 0, len(filters))
	for key := range filters {
		if _, allowed := conditions[key]; allowed {
			keys = append(keys, key)
		} else {
			return nil, catalogue.ErrInvalidRequest
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := filters[key]
		args = append(args, value)
		placeholder := fmt.Sprintf("$%d", len(args))
		condition := conditions[key]
		condition = strings.ReplaceAll(condition, "$FILTER", placeholder)
		if key == "q" {
			value = "%" + value + "%"
			args[len(args)-1] = value
		}
		query += " AND " + condition
	}
	query += " ORDER BY " + listOrder(resource)
	args = append(args, limit, offset)
	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", resource, err)
	}
	defer rows.Close()
	items, err := collectRows(rows)
	if err != nil {
		return nil, err
	}
	if resource == catalogue.Offerings {
		if err := s.addOfferingAvailability(ctx, tenant, items); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (s *Store) ListVenues(ctx context.Context, institutionIDText, userIDText string, limit, offset int) ([]map[string]any, int64, error) {
	institutionID, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return nil, 0, err
	}
	userID, err := db.ParseUUID(userIDText)
	if err != nil {
		return nil, 0, err
	}
	allowed, err := s.queries.CanListVenues(ctx, dbgen.CanListVenuesParams{
		InstitutionID: institutionID, UserID: userID,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("authorize venue listing: %w", err)
	}
	if !allowed {
		return nil, 0, catalogue.ErrForbidden
	}
	scope := `v.institution_id=$1 AND (
		EXISTS(SELECT 1 FROM role_assignments ra WHERE ra.institution_id=$1 AND ra.user_id=$2
			AND ((ra.role='lecturer' AND v.active) OR ra.role='platform_admin'))
		OR (v.department_id IS NULL AND EXISTS(SELECT 1 FROM role_assignments ra
			WHERE ra.institution_id=$1 AND ra.user_id=$2 AND ra.role='institution_admin' AND ra.scope_type='institution'))
		OR EXISTS(SELECT 1 FROM departments d JOIN role_assignments ra ON ra.institution_id=d.institution_id
			WHERE d.institution_id=v.institution_id AND d.id=v.department_id AND ra.institution_id=$1 AND ra.user_id=$2
			AND ((ra.role='institution_admin' AND ra.scope_type='institution')
				OR (ra.role='faculty_admin' AND ra.scope_type='faculty' AND ra.faculty_id=d.faculty_id)
				OR (ra.role='department_admin' AND ra.scope_type='department' AND ra.department_id=d.id)))
	)`
	var total int64
	err = s.pool.QueryRow(ctx, "SELECT count(*) FROM venues v WHERE "+scope, institutionID, userID).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count scoped institution venues: %w", err)
	}
	rows, err := s.pool.Query(ctx, `SELECT v.id::text,v.name,v.latitude,v.longitude,v.radius_meters,v.department_id::text,v.active
		FROM venues v WHERE `+scope+` ORDER BY v.name,v.id LIMIT $3 OFFSET $4`,
		institutionID, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list scoped institution venues: %w", err)
	}
	defer rows.Close()
	items := make([]map[string]any, 0, limit)
	for rows.Next() {
		var id, name string
		// Nullable: a venue need not have been given a position.
		var latitude, longitude *float64
		var radius *int32
		var department pgtype.Text
		var active bool
		if err := rows.Scan(&id, &name, &latitude, &longitude, &radius, &department, &active); err != nil {
			return nil, 0, fmt.Errorf("scan institution venue: %w", err)
		}
		var departmentID any
		if department.Valid {
			departmentID = department.String
		}
		items = append(items, map[string]any{
			"id": id, "name": name, "latitude": latitude,
			"longitude": longitude, "radius_meters": radius, "department_id": departmentID, "active": active,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("read scoped institution venues: %w", err)
	}
	return items, total, nil
}

func (s *Store) searchOfferings(ctx context.Context, tenant, user, student pgtype.UUID, filters map[string]string, limit, offset int) ([]map[string]any, error) {
	department := pgtype.UUID{}
	if filters["department_id"] != "" {
		var err error
		department, err = db.ParseUUID(filters["department_id"])
		if err != nil {
			return nil, catalogue.ErrInvalidRequest
		}
	}
	semester := pgtype.UUID{}
	if filters["semester"] != "" {
		var err error
		semester, err = db.ParseUUID(filters["semester"])
		if err != nil {
			return nil, catalogue.ErrInvalidRequest
		}
	}
	level := pgtype.Int4{}
	if filters["level"] != "" {
		value, err := strconv.Atoi(filters["level"])
		if err != nil || value < 1 || value > 100 {
			return nil, catalogue.ErrInvalidRequest
		}
		level = pgtype.Int4{Int32: int32(value), Valid: true}
	}
	kind := pgtype.Text{}
	if filters["kind"] != "" {
		if filters["kind"] != "general" && filters["kind"] != "departmental" {
			return nil, catalogue.ErrInvalidRequest
		}
		kind = pgtype.Text{String: filters["kind"], Valid: true}
	}
	rows, err := s.queries.SearchOfferingsFullText(ctx, dbgen.SearchOfferingsFullTextParams{
		Q: filters["q"], InstitutionID: tenant, DepartmentID: department,
		Level: level, Kind: kind, Semester: semester,
		UserID: user, StudentID: student, PageSize: int32(limit), PageOffset: int32(offset),
	})
	if err != nil {
		return nil, fmt.Errorf("search offerings: %w", err)
	}
	items := make([]map[string]any, 0, len(rows))
	if len(rows) == 0 {
		return items, nil
	}
	offeringIDs := make([]pgtype.UUID, 0, len(rows))
	for _, row := range rows {
		offeringIDs = append(offeringIDs, row.OfferingID)
	}
	metadataRows, err := s.pool.Query(ctx, `
SELECT offering.id::text, offering.registration_mode, offering.level,
       coalesce(array_agg(DISTINCT lecturer.display_name ORDER BY lecturer.display_name)
         FILTER (WHERE lecturer.id IS NOT NULL), ARRAY[]::text[]),
       coalesce(registration.status, 'none')
FROM course_offerings AS offering
LEFT JOIN offering_lecturers AS assignment
  ON assignment.institution_id=offering.institution_id
 AND assignment.offering_id=offering.id
LEFT JOIN users AS lecturer
  ON lecturer.institution_id=assignment.institution_id
 AND lecturer.id=assignment.lecturer_user_id
LEFT JOIN course_registrations AS registration
  ON registration.institution_id=offering.institution_id
 AND registration.offering_id=offering.id
 AND registration.student_record_id=$3
WHERE offering.institution_id=$1
  AND offering.id=ANY($2::uuid[])
GROUP BY offering.id, registration.status`,
		tenant, offeringIDs, student)
	if err != nil {
		return nil, fmt.Errorf("load offering search metadata: %w", err)
	}
	metadata := make(map[string]offeringSearchMetadata, len(rows))
	for metadataRows.Next() {
		var id string
		var item offeringSearchMetadata
		if err := metadataRows.Scan(&id, &item.mode, &item.level, &item.lecturers, &item.status); err != nil {
			metadataRows.Close()
			return nil, fmt.Errorf("scan offering search metadata: %w", err)
		}
		metadata[id] = item
	}
	if err := metadataRows.Err(); err != nil {
		metadataRows.Close()
		return nil, fmt.Errorf("iterate offering search metadata: %w", err)
	}
	metadataRows.Close()
	for _, row := range rows {
		id := db.NormalizeUUIDValue(row.OfferingID)
		detail, ok := metadata[fmt.Sprint(id)]
		if !ok {
			return nil, fmt.Errorf("search metadata missing for offering %v", id)
		}
		items = append(items, mapSearchOffering(row, detail))
	}
	return items, nil
}

func mapSearchOffering(row dbgen.SearchOfferingsFullTextRow, detail offeringSearchMetadata) map[string]any {
	item := map[string]any{
		"offering_id": row.OfferingID, "course_id": row.CourseID,
		"code": row.Code, "title": row.Title, "course_code": row.Code, "course_title": row.Title,
		"kind": row.Kind, "department_id": row.DepartmentID, "department_name": row.DepartmentName,
		"semester_id": row.SemesterID, "semester": row.Semester, "rank": row.Rank,
		"registration_mode": detail.mode, "level": detail.level, "lecturers": detail.lecturers,
		"registration_status": detail.status, "is_registered": detail.status == "approved",
		"is_pending": detail.status == "pending",
	}
	for key, value := range item {
		item[key] = db.NormalizeUUIDValue(value)
	}
	return item
}

func pagination(filters map[string]string) (int, int, error) {
	limit, offset := 50, 0
	var err error
	if raw := filters["limit"]; raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			return 0, 0, catalogue.ErrInvalidRequest
		}
	}
	if raw := filters["offset"]; raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil {
			return 0, 0, catalogue.ErrInvalidRequest
		}
	}
	if limit < 1 || limit > 201 || offset < 0 {
		return 0, 0, catalogue.ErrInvalidRequest
	}
	return limit, offset, nil
}

func catalogueReadScope(resource catalogue.Resource) (string, error) {
	if resource == catalogue.AcademicSessions || resource == catalogue.Semesters || resource == catalogue.Institutions {
		return "$2::uuid IS NOT NULL", nil
	}

	departmentID := ""
	facultyOnly := false
	switch resource {
	case catalogue.Faculties:
		facultyOnly = true
	case catalogue.Departments:
		departmentID = "t.id"
	case catalogue.Programmes, catalogue.Courses:
		departmentID = "t.department_id"
	case catalogue.Offerings:
		departmentID = "c.department_id"
	default:
		return "", catalogue.ErrInvalidRequest
	}
	targetDepartment := "rd.id = " + departmentID
	studentTarget := "home.department_id = " + departmentID
	lecturerTarget := "owned.department_id = " + departmentID
	if facultyOnly {
		targetDepartment = "rd.faculty_id = t.id"
		studentTarget = "home_faculty.faculty_id = t.id"
		lecturerTarget = "owned_faculty.faculty_id = t.id"
	}

	adminAccess := `EXISTS (
		SELECT 1 FROM role_assignments AS ra
		JOIN departments AS rd ON rd.institution_id = ra.institution_id
		WHERE ra.institution_id = t.institution_id AND ra.user_id = $2
		  AND ` + targetDepartment + `
		  AND (
			(ra.role = 'department_admin' AND ra.scope_type = 'department' AND ra.department_id = rd.id)
			OR (ra.role = 'faculty_admin' AND ra.scope_type = 'faculty' AND ra.faculty_id = rd.faculty_id)
		  )
	) OR EXISTS (
		SELECT 1 FROM role_assignments AS broad_role
		WHERE broad_role.institution_id = t.institution_id AND broad_role.user_id = $2
		  AND ((broad_role.role = 'institution_admin' AND broad_role.scope_type = 'institution')
		    OR broad_role.role = 'platform_admin')
	)`
	studentAccess := `EXISTS (
		SELECT 1 FROM student_records AS home
		WHERE home.institution_id = t.institution_id AND home.id = $3
		  AND home.claimed_by_user_id = $2 AND (` + studentTarget + `
			OR (` + boolString(resource == catalogue.Courses) + ` AND EXISTS (
				SELECT 1 FROM course_offerings AS target_offering
				JOIN courses AS target_course
				  ON target_course.institution_id=target_offering.institution_id
				 AND target_course.id=target_offering.course_id
				JOIN offering_auto_targets AS target
				  ON target.institution_id=target_offering.institution_id
				 AND target.offering_id=target_offering.id
				WHERE target_offering.institution_id=t.institution_id
				  AND target_course.id=t.id AND target_course.kind='general'
				  AND target_offering.level=home.level
				  AND target.department_id=home.department_id AND target.level=home.level
			))
		  )
	)`
	lecturerAccess := `EXISTS (
		SELECT 1 FROM course_offerings AS assigned
		JOIN courses AS owned ON owned.institution_id = assigned.institution_id AND owned.id = assigned.course_id
		WHERE assigned.institution_id = t.institution_id AND ` + lecturerTarget + `
		  AND (assigned.coordinator_user_id = $2 OR EXISTS (
			SELECT 1 FROM offering_lecturers AS assigned_lecturer
			WHERE assigned_lecturer.institution_id = assigned.institution_id
			  AND assigned_lecturer.offering_id = assigned.id
			  AND assigned_lecturer.lecturer_user_id = $2
		  ))
	)`
	if facultyOnly {
		studentAccess = `EXISTS (
			SELECT 1 FROM student_records AS home
			JOIN departments AS home_faculty ON home_faculty.institution_id = home.institution_id
			  AND home_faculty.id = home.department_id
			WHERE home.institution_id = t.institution_id AND home.id = $3
			  AND home.claimed_by_user_id = $2 AND ` + studentTarget + `
		)`
		lecturerAccess = `EXISTS (
			SELECT 1 FROM course_offerings AS assigned
			JOIN courses AS owned ON owned.institution_id = assigned.institution_id AND owned.id = assigned.course_id
			JOIN departments AS owned_faculty ON owned_faculty.institution_id = owned.institution_id
			  AND owned_faculty.id = owned.department_id
			WHERE assigned.institution_id = t.institution_id AND ` + lecturerTarget + `
			  AND (assigned.coordinator_user_id = $2 OR EXISTS (
				SELECT 1 FROM offering_lecturers AS assigned_lecturer
				WHERE assigned_lecturer.institution_id = assigned.institution_id
				  AND assigned_lecturer.offering_id = assigned.id
				  AND assigned_lecturer.lecturer_user_id = $2
			  ))
		)`
	}

	if resource == catalogue.Offerings {
		return "(" + adminAccess + ") OR " + lecturerAccess + ` OR EXISTS (
			SELECT 1 FROM role_assignments AS scoped_admin
			JOIN departments AS admin_department
			  ON admin_department.institution_id = scoped_admin.institution_id
			 AND ((scoped_admin.role='department_admin' AND admin_department.id=scoped_admin.department_id)
			   OR (scoped_admin.role='faculty_admin' AND admin_department.faculty_id=scoped_admin.faculty_id))
			JOIN student_records AS registered_student
			  ON registered_student.institution_id = scoped_admin.institution_id
			 AND registered_student.department_id = admin_department.id
			JOIN course_registrations AS registration
			  ON registration.institution_id = registered_student.institution_id
			 AND registration.student_record_id = registered_student.id
			 AND registration.offering_id = t.id
			WHERE scoped_admin.institution_id = t.institution_id AND scoped_admin.user_id = $2
			  AND ((scoped_admin.role = 'department_admin' AND scoped_admin.scope_type = 'department')
			    OR (scoped_admin.role = 'faculty_admin' AND scoped_admin.scope_type = 'faculty'))
			  AND c.kind = 'general' AND registration.status IN ('approved', 'pending')
		) OR EXISTS (
			SELECT 1 FROM role_assignments AS scoped_admin
			JOIN departments AS admin_department
			  ON admin_department.institution_id = scoped_admin.institution_id
			 AND ((scoped_admin.role='department_admin' AND admin_department.id=scoped_admin.department_id)
			   OR (scoped_admin.role='faculty_admin' AND admin_department.faculty_id=scoped_admin.faculty_id))
			JOIN offering_auto_targets AS target
			  ON target.institution_id=t.institution_id
			 AND target.offering_id=t.id
			 AND target.department_id=admin_department.id
			 AND target.level=t.level
			WHERE scoped_admin.institution_id=t.institution_id
			  AND scoped_admin.user_id=$2 AND c.kind='general'
			  AND ((scoped_admin.role='department_admin' AND scoped_admin.scope_type='department')
			    OR (scoped_admin.role='faculty_admin' AND scoped_admin.scope_type='faculty'))
		) OR EXISTS (
			SELECT 1 FROM student_records AS home
			WHERE home.institution_id = t.institution_id AND home.id = $3
			  AND home.claimed_by_user_id = $2 AND t.level = home.level
			  AND (
				c.department_id = home.department_id
				OR (c.kind = 'general' AND EXISTS (
					SELECT 1 FROM offering_auto_targets AS target
					WHERE target.institution_id = t.institution_id
					  AND target.offering_id = t.id
					  AND target.department_id = home.department_id
					  AND target.level = home.level
				))
			  )
		)`, nil
	}
	return "(" + adminAccess + ") OR " + studentAccess + " OR " + lecturerAccess, nil
}

func boolString(value bool) string {
	if value {
		return "TRUE"
	}
	return "FALSE"
}

func listOrder(resource catalogue.Resource) string {
	switch resource {
	case catalogue.Offerings:
		return "c.code, c.title, t.level, s.starts_on"
	case catalogue.Courses:
		return "t.code"
	case catalogue.Departments, catalogue.Programmes:
		return "t.code"
	default:
		return "t.name"
	}
}

func (s *Store) Create(ctx context.Context, institutionID, actorID string, resource catalogue.Resource, fields map[string]any) (map[string]any, error) {
	config, ok := resources[resource]
	if !ok {
		return nil, catalogue.ErrInvalidRequest
	}
	if resource == catalogue.Institutions {
		return nil, catalogue.ErrForbidden
	}
	tenant, err := db.ParseUUID(institutionID)
	if err != nil {
		return nil, err
	}
	keys := sortedKeys(fields)
	columns := append([]string{"institution_id"}, keys...)
	args := []any{tenant}
	placeholders := []string{"$1"}
	for _, key := range keys {
		args = append(args, fields[key])
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	query := "INSERT INTO " + config.table + " (" + strings.Join(columns, ",") + ") VALUES (" +
		strings.Join(placeholders, ",") + ") RETURNING " + strings.Join(config.columns, ",")
	if academicResource(resource) {
		actor, err := db.ParseUUID(actorID)
		if err != nil {
			return nil, err
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return nil, fmt.Errorf("begin academic resource creation: %w", err)
		}
		defer tx.Rollback(ctx)
		result, err := scanRow(tx.QueryRow(ctx, query, args...), config.columns)
		if err != nil {
			return nil, fmt.Errorf("create %s: %w", resource, err)
		}
		if err := auditAcademicResource(ctx, tx, tenant, actor, resource, "created", result["id"], fields); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit academic resource creation: %w", err)
		}
		return result, nil
	}
	row := s.pool.QueryRow(ctx, query, args...)
	result, err := scanRow(row, config.columns)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", resource, err)
	}
	return result, nil
}

func (s *Store) Update(ctx context.Context, institutionID, actorID string, resource catalogue.Resource, id string, fields map[string]any) (map[string]any, error) {
	config, ok := resources[resource]
	if !ok {
		return nil, catalogue.ErrInvalidRequest
	}
	tenant, err := db.ParseUUID(institutionID)
	if err != nil {
		return nil, err
	}
	itemID, err := db.ParseUUID(id)
	if err != nil {
		return nil, catalogue.ErrNotFound
	}
	keys := sortedKeys(fields)
	sets := make([]string, 0, len(keys))
	args := []any{tenant, itemID}
	for _, key := range keys {
		args = append(args, fields[key])
		sets = append(sets, fmt.Sprintf("%s=$%d", key, len(args)))
	}
	where := "institution_id=$1 AND id=$2"
	if resource == catalogue.Institutions {
		where = "id=$1 AND id=$2"
	}
	query := "UPDATE " + config.table + " SET " + strings.Join(sets, ",") +
		" WHERE " + where + " RETURNING " + strings.Join(config.columns, ",")
	if academicResource(resource) {
		actor, err := db.ParseUUID(actorID)
		if err != nil {
			return nil, err
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return nil, fmt.Errorf("begin academic resource update: %w", err)
		}
		defer tx.Rollback(ctx)
		if resource == catalogue.AcademicSessions && fields["current"] == true {
			if _, err := tx.Exec(ctx, `UPDATE academic_sessions SET current=false
				WHERE institution_id=$1 AND current=true AND id<>$2`, tenant, itemID); err != nil {
				return nil, fmt.Errorf("clear previous current academic session: %w", err)
			}
		}
		result, err := scanRow(tx.QueryRow(ctx, query, args...), config.columns)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, catalogue.ErrNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("update %s: %w", resource, err)
		}
		if err := auditAcademicResource(ctx, tx, tenant, actor, resource, "updated", itemID, fields); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit academic resource update: %w", err)
		}
		return result, nil
	}
	if resource == catalogue.Offerings {
		actor, err := db.ParseUUID(actorID)
		if err != nil {
			return nil, err
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return nil, fmt.Errorf("begin offering update: %w", err)
		}
		defer tx.Rollback(ctx)
		result, err := scanRow(tx.QueryRow(ctx, query, args...), config.columns)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, catalogue.ErrNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("update offering: %w", err)
		}
		details, err := json.Marshal(fields)
		if err != nil {
			return nil, fmt.Errorf("encode offering update audit: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_log(institution_id, actor_user_id, action, resource_type, resource_id, details)
			VALUES ($1,$2,'offering.updated','course_offering',$3,jsonb_build_object('fields',$4::jsonb))`,
			tenant, actor, itemID, string(details)); err != nil {
			return nil, fmt.Errorf("audit offering update: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit offering update: %w", err)
		}
		return result, nil
	}
	result, err := scanRow(s.pool.QueryRow(ctx, query, args...), config.columns)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, catalogue.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update %s: %w", resource, err)
	}
	return result, nil
}

func (s *Store) Delete(ctx context.Context, institutionID, actorID string, resource catalogue.Resource, id string) error {
	config, ok := resources[resource]
	if !ok {
		return catalogue.ErrInvalidRequest
	}
	if resource == catalogue.Institutions {
		return catalogue.ErrForbidden
	}
	tenant, err := db.ParseUUID(institutionID)
	if err != nil {
		return err
	}
	itemID, err := db.ParseUUID(id)
	if err != nil {
		return catalogue.ErrNotFound
	}
	if resource == catalogue.Offerings {
		actor, err := db.ParseUUID(actorID)
		if err != nil {
			return err
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin offering deletion: %w", err)
		}
		defer tx.Rollback(ctx)
		tag, err := tx.Exec(ctx, "DELETE FROM "+config.table+" WHERE institution_id=$1 AND id=$2", tenant, itemID)
		if err != nil {
			return fmt.Errorf("delete offering: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return catalogue.ErrNotFound
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_log(institution_id, actor_user_id, action, resource_type, resource_id, details)
			VALUES ($1,$2,'offering.deleted','course_offering',$3,'{}'::jsonb)`, tenant, actor, itemID); err != nil {
			return fmt.Errorf("audit offering deletion: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit offering deletion: %w", err)
		}
		return nil
	}
	if academicResource(resource) {
		actor, err := db.ParseUUID(actorID)
		if err != nil {
			return err
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin academic resource deletion: %w", err)
		}
		defer tx.Rollback(ctx)
		tag, err := tx.Exec(ctx, "DELETE FROM "+config.table+" WHERE institution_id=$1 AND id=$2", tenant, itemID)
		if err != nil {
			return fmt.Errorf("delete %s: %w", resource, err)
		}
		if tag.RowsAffected() == 0 {
			return catalogue.ErrNotFound
		}
		if err := auditAcademicResource(ctx, tx, tenant, actor, resource, "deleted", itemID, nil); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit academic resource deletion: %w", err)
		}
		return nil
	}
	if resource == catalogue.Courses {
		actor, err := db.ParseUUID(actorID)
		if err != nil {
			return err
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin course deletion: %w", err)
		}
		defer tx.Rollback(ctx)
		tag, err := tx.Exec(ctx, "DELETE FROM courses WHERE institution_id=$1 AND id=$2", tenant, itemID)
		if err != nil {
			if isForeignKeyViolation(err) {
				return catalogue.ErrResourceInUse
			}
			return fmt.Errorf("delete course: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return catalogue.ErrNotFound
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_log(institution_id, actor_user_id, action, resource_type, resource_id, details)
			VALUES ($1,$2,'course.deleted','course',$3,'{}'::jsonb)`, tenant, actor, itemID); err != nil {
			return fmt.Errorf("audit course deletion: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit course deletion: %w", err)
		}
		return nil
	}
	tag, err := s.pool.Exec(ctx, "DELETE FROM "+config.table+" WHERE institution_id=$1 AND id=$2", tenant, itemID)
	if err != nil {
		if isForeignKeyViolation(err) {
			return catalogue.ErrResourceInUse
		}
		return fmt.Errorf("delete %s: %w", resource, err)
	}
	if tag.RowsAffected() == 0 {
		return catalogue.ErrNotFound
	}
	return nil
}

// isForeignKeyViolation reports whether err is a PostgreSQL foreign-key
// violation (SQLSTATE 23503), raised when a row is still referenced by others.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

func (s *Store) PublicDepartments(ctx context.Context, institutionIDText string) ([]map[string]any, error) {
	tenant, err := db.ParseUUID(institutionIDText)
	if err != nil {
		return nil, catalogue.ErrInvalidRequest
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text, name, code FROM departments
		WHERE institution_id=$1 ORDER BY name, id`, tenant)
	if err != nil {
		return nil, fmt.Errorf("list public departments: %w", err)
	}
	defer rows.Close()
	departments := []map[string]any{}
	for rows.Next() {
		var id, name, code string
		if err := rows.Scan(&id, &name, &code); err != nil {
			return nil, fmt.Errorf("scan public department: %w", err)
		}
		departments = append(departments, map[string]any{"id": id, "name": name, "code": code})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read public departments: %w", err)
	}
	return departments, nil
}

func (s *Store) CanManage(ctx context.Context, institutionID, userID string, resource catalogue.Resource, id string, fields map[string]any) (bool, error) {
	tenant, err := db.ParseUUID(institutionID)
	if err != nil {
		return false, err
	}
	user, err := db.ParseUUID(userID)
	if err != nil {
		return false, err
	}
	target := rbac.Target{InstitutionID: institutionID}
	lookupID := id
	if lookupID != "" {
		if err := s.resolveTarget(ctx, tenant, resource, lookupID, &target); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return false, nil
			}
			return false, err
		}
	} else if fields != nil {
		var parentID string
		var parentResource catalogue.Resource
		switch resource {
		case catalogue.Departments:
			parentID, parentResource = stringValue(fields["faculty_id"]), catalogue.Faculties
		case catalogue.Programmes, catalogue.Courses:
			parentID, parentResource = stringValue(fields["department_id"]), catalogue.Departments
		case catalogue.Offerings:
			parentID, parentResource = stringValue(fields["course_id"]), catalogue.Courses
		}
		if parentID != "" {
			if err := s.resolveTarget(ctx, tenant, parentResource, parentID, &target); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return false, nil
				}
				return false, err
			}
		}
	}
	rows, err := s.pool.Query(ctx, `SELECT role, scope_type, institution_id, faculty_id, department_id
		FROM role_assignments WHERE institution_id=$1 AND user_id=$2`, tenant, user)
	if err != nil {
		return false, fmt.Errorf("load management roles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var role, scope string
		var inst pgtype.UUID
		var faculty, department pgtype.UUID
		if err := rows.Scan(&role, &scope, &inst, &faculty, &department); err != nil {
			return false, err
		}
		assignment := rbac.Assignment{Role: role, ScopeType: rbac.ScopeType(scope), InstitutionID: inst.String()}
		if faculty.Valid {
			assignment.FacultyID = faculty.String()
		}
		if department.Valid {
			assignment.DepartmentID = department.String()
		}
		if rbac.CanAccess(assignment, target, "", "write") {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("read management roles: %w", err)
	}
	return false, nil
}

func (s *Store) CanReadOffering(ctx context.Context, institutionID, userID, offeringID string) (bool, error) {
	allowed, err := s.CanManage(ctx, institutionID, userID, catalogue.Offerings, offeringID, nil)
	if err != nil || allowed {
		return allowed, err
	}

	tenant, err := db.ParseUUID(institutionID)
	if err != nil {
		return false, err
	}
	offering, err := db.ParseUUID(offeringID)
	if err != nil {
		return false, catalogue.ErrNotFound
	}
	user, err := db.ParseUUID(userID)
	if err != nil {
		return false, err
	}
	var canRead bool
	err = s.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM course_offerings AS offering
		WHERE offering.institution_id=$1 AND offering.id=$2
		  AND (
			offering.coordinator_user_id=$3
			OR EXISTS (
				SELECT 1 FROM offering_lecturers AS lecturer
				WHERE lecturer.institution_id=offering.institution_id
				  AND lecturer.offering_id=offering.id
				  AND lecturer.lecturer_user_id=$3
			)
		  )
	)`, tenant, offering, user).Scan(&canRead)
	if err != nil {
		return false, fmt.Errorf("check offering read access: %w", err)
	}
	return canRead, nil
}

func academicResource(resource catalogue.Resource) bool {
	return resource == catalogue.AcademicSessions || resource == catalogue.Semesters
}

func (s *Store) resolveTarget(ctx context.Context, tenant pgtype.UUID, resource catalogue.Resource, id string, target *rbac.Target) error {
	switch resource {
	case catalogue.Faculties:
		return s.pool.QueryRow(ctx, "SELECT id::text FROM faculties WHERE institution_id=$1 AND id=$2", tenant, id).Scan(&target.FacultyID)
	case catalogue.Departments:
		return s.pool.QueryRow(ctx, `SELECT d.id::text, d.faculty_id::text FROM departments d
			WHERE d.institution_id=$1 AND d.id=$2`, tenant, id).Scan(&target.DepartmentID, &target.FacultyID)
	case catalogue.Programmes:
		return s.pool.QueryRow(ctx, `SELECT d.id::text, d.faculty_id::text FROM programmes p
			JOIN departments d ON d.institution_id=p.institution_id AND d.id=p.department_id
			WHERE p.institution_id=$1 AND p.id=$2`, tenant, id).Scan(&target.DepartmentID, &target.FacultyID)
	case catalogue.Courses:
		return s.pool.QueryRow(ctx, `SELECT d.id::text, d.faculty_id::text FROM courses c
			JOIN departments d ON d.institution_id=c.institution_id AND d.id=c.department_id
			WHERE c.institution_id=$1 AND c.id=$2`, tenant, id).Scan(&target.DepartmentID, &target.FacultyID)
	case catalogue.Offerings:
		return s.pool.QueryRow(ctx, `SELECT d.id::text, d.faculty_id::text FROM course_offerings o
			JOIN courses c ON c.institution_id=o.institution_id AND c.id=o.course_id
			JOIN departments d ON d.institution_id=c.institution_id AND d.id=c.department_id
			WHERE o.institution_id=$1 AND o.id=$2`, tenant, id).Scan(&target.DepartmentID, &target.FacultyID)
	}
	if strings.HasPrefix(id, "course:") {
		return s.pool.QueryRow(ctx, `SELECT d.id::text, d.faculty_id::text FROM courses c
			JOIN departments d ON d.institution_id=c.institution_id AND d.id=c.department_id
			WHERE c.institution_id=$1 AND c.id=$2`, tenant, strings.TrimPrefix(id, "course:")).Scan(&target.DepartmentID, &target.FacultyID)
	}
	switch resource {
	case catalogue.AcademicSessions, catalogue.Semesters, catalogue.Institutions:
		return nil
	case catalogue.Faculties, catalogue.Departments, catalogue.Programmes, catalogue.Courses, catalogue.Offerings:
		if target.FacultyID != "" || target.DepartmentID != "" {
			return nil
		}
	}
	return nil
}

func (s *Store) SetOfferingLecturers(ctx context.Context, institutionID, offeringID, _ string, lecturerIDs []string) error {
	tenant, err := db.ParseUUID(institutionID)
	if err != nil {
		return err
	}
	offering, err := db.ParseUUID(offeringID)
	if err != nil {
		return catalogue.ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "DELETE FROM offering_lecturers WHERE institution_id=$1 AND offering_id=$2", tenant, offering); err != nil {
		return fmt.Errorf("clear offering lecturers: %w", err)
	}
	for _, lecturerID := range lecturerIDs {
		lecturer, err := db.ParseUUID(lecturerID)
		if err != nil {
			return catalogue.ErrInvalidRequest
		}
		tag, err := tx.Exec(ctx, `INSERT INTO offering_lecturers(institution_id, offering_id, lecturer_user_id)
			SELECT $1,$2,u.id FROM users u WHERE u.institution_id=$1 AND u.id=$3
			AND EXISTS (SELECT 1 FROM role_assignments ra WHERE ra.institution_id=$1 AND ra.user_id=u.id AND ra.role='lecturer')`,
			tenant, offering, lecturer)
		if err != nil {
			return fmt.Errorf("assign offering lecturer: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return catalogue.ErrInvalidRequest
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) SetOfferingAutoTargets(ctx context.Context, institutionID, offeringID string, targets []catalogue.AutoTarget) error {
	tenant, err := db.ParseUUID(institutionID)
	if err != nil {
		return err
	}
	offering, err := db.ParseUUID(offeringID)
	if err != nil {
		return catalogue.ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "DELETE FROM offering_auto_targets WHERE institution_id=$1 AND offering_id=$2", tenant, offering); err != nil {
		return fmt.Errorf("clear offering auto targets: %w", err)
	}
	for _, target := range targets {
		department, err := db.ParseUUID(target.DepartmentID)
		if err != nil {
			return catalogue.ErrInvalidRequest
		}
		tag, err := tx.Exec(ctx, `INSERT INTO offering_auto_targets(institution_id, offering_id, department_id, level)
			SELECT $1,$2,d.id,$4 FROM departments d WHERE d.institution_id=$1 AND d.id=$3`,
			tenant, offering, department, target.Level)
		if err != nil {
			return fmt.Errorf("set offering auto target: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return catalogue.ErrInvalidRequest
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) OfferingLecturers(ctx context.Context, institutionID, offeringID string) ([]map[string]any, error) {
	tenant, err := db.ParseUUID(institutionID)
	if err != nil {
		return nil, err
	}
	offering, err := db.ParseUUID(offeringID)
	if err != nil {
		return nil, catalogue.ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `SELECT u.id, u.display_name, u.email
		FROM offering_lecturers ol
		JOIN users u ON u.institution_id=ol.institution_id AND u.id=ol.lecturer_user_id
		WHERE ol.institution_id=$1 AND ol.offering_id=$2 ORDER BY u.display_name, u.id`, tenant, offering)
	if err != nil {
		return nil, fmt.Errorf("list offering lecturers: %w", err)
	}
	defer rows.Close()
	return collectRows(rows)
}

func (s *Store) OfferingAutoTargets(ctx context.Context, institutionID, offeringID string) ([]catalogue.AutoTarget, error) {
	tenant, err := db.ParseUUID(institutionID)
	if err != nil {
		return nil, err
	}
	offering, err := db.ParseUUID(offeringID)
	if err != nil {
		return nil, catalogue.ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `SELECT department_id::text, level
		FROM offering_auto_targets WHERE institution_id=$1 AND offering_id=$2
		ORDER BY department_id, level`, tenant, offering)
	if err != nil {
		return nil, fmt.Errorf("list offering auto targets: %w", err)
	}
	defer rows.Close()
	targets := []catalogue.AutoTarget{}
	for rows.Next() {
		var target catalogue.AutoTarget
		if err := rows.Scan(&target.DepartmentID, &target.Level); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func sortedKeys(fields map[string]any) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func nullableUUID(value string) (pgtype.UUID, error) {
	if value == "" {
		return pgtype.UUID{}, nil
	}
	return db.ParseUUID(value)
}

func collectRows(rows pgx.Rows) ([]map[string]any, error) {
	results := []map[string]any{}
	fields := rows.FieldDescriptions()
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := make(map[string]any, len(fields))
		for index, field := range fields {
			row[string(field.Name)] = normalize(values[index])
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanRow(row rowScanner, columns []string) (map[string]any, error) {
	dest := make([]any, len(columns))
	ptrs := make([]any, len(columns))
	for index := range dest {
		ptrs[index] = &dest[index]
	}
	if err := row.Scan(ptrs...); err != nil {
		return nil, err
	}
	result := make(map[string]any, len(columns))
	for index, column := range columns {
		result[column] = normalize(dest[index])
	}
	return result, nil
}

func normalize(value any) any {
	value = db.NormalizeUUIDValue(value)
	switch value := value.(type) {
	case []byte:
		return string(value)
	default:
		return value
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
