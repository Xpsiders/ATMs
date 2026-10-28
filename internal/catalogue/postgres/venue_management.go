package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"attendance-api/internal/catalogue"
	"attendance-api/internal/db"
	"attendance-api/internal/rbac"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type venueQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *Store) CreateVenue(ctx context.Context, institutionID, userID string, input catalogue.VenueInput) (map[string]any, error) {
	tenant, actor, err := parseVenueActor(institutionID, userID)
	if err != nil {
		return nil, err
	}
	departmentID, err := optionalVenueUUID(input.DepartmentID)
	if err != nil {
		return nil, catalogue.ErrInvalidRequest
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin venue creation: %w", err)
	}
	defer tx.Rollback(ctx)
	allowed, err := canManageVenueScope(ctx, tx, tenant, actor, institutionID, departmentID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, catalogue.ErrForbidden
	}
	// A venue without coordinates stores NULL for all three columns. Writing a
	// zero radius instead would violate the surviving CHECK (radius_meters > 0),
	// and a zero coordinate would be a real position off the coast of Ghana.
	var radius *int
	if input.Latitude != nil && input.RadiusMeters > 0 {
		radius = &input.RadiusMeters
	}
	var id pgtype.UUID
	err = tx.QueryRow(ctx, `INSERT INTO venues(institution_id,department_id,name,latitude,longitude,radius_meters)
		VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, tenant, departmentID, input.Name,
		input.Latitude, input.Longitude, radius).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("insert venue: %w", err)
	}
	fields, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("encode venue audit: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_log(institution_id,actor_user_id,action,resource_type,resource_id,details)
		VALUES($1,$2,'venue.created','venue',$3,$4::jsonb)`, tenant, actor, id, string(fields)); err != nil {
		return nil, fmt.Errorf("audit venue creation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit venue creation: %w", err)
	}
	return map[string]any{
		"id": id.String(), "name": input.Name, "latitude": input.Latitude,
		"longitude": input.Longitude, "radius_meters": radius,
		"department_id": nullableUUIDString(departmentID), "active": true,
	}, nil
}

func (s *Store) UpdateVenue(ctx context.Context, institutionID, userID, venueID string, update catalogue.VenueUpdate) (map[string]any, error) {
	tenant, actor, err := parseVenueActor(institutionID, userID)
	if err != nil {
		return nil, err
	}
	id, err := db.ParseUUID(venueID)
	if err != nil {
		return nil, catalogue.ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin venue update: %w", err)
	}
	defer tx.Rollback(ctx)
	var oldDepartment pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT department_id FROM venues WHERE institution_id=$1 AND id=$2 FOR UPDATE`,
		tenant, id).Scan(&oldDepartment)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, catalogue.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock venue: %w", err)
	}
	allowed, err := canManageVenueScope(ctx, tx, tenant, actor, institutionID, oldDepartment)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, catalogue.ErrForbidden
	}
	if update.DepartmentID != nil {
		newDepartment, err := db.ParseUUID(*update.DepartmentID)
		if err != nil {
			return nil, catalogue.ErrInvalidRequest
		}
		allowed, err := canManageVenueScope(ctx, tx, tenant, actor, institutionID, newDepartment)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, catalogue.ErrForbidden
		}
	}
	sets := make([]string, 0, 6)
	args := []any{tenant, id}
	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s=$%d", column, len(args)))
	}
	if update.Name != nil {
		add("name", *update.Name)
	}
	if update.Latitude != nil {
		add("latitude", *update.Latitude)
	}
	if update.Longitude != nil {
		add("longitude", *update.Longitude)
	}
	if update.RadiusMeters != nil {
		add("radius_meters", *update.RadiusMeters)
	}
	if update.DepartmentID != nil {
		department, err := db.ParseUUID(*update.DepartmentID)
		if err != nil {
			return nil, catalogue.ErrInvalidRequest
		}
		add("department_id", department)
	}
	if update.Active != nil {
		add("active", *update.Active)
	}
	var columns = []string{"id", "name", "latitude", "longitude", "radius_meters", "department_id", "active"}
	query := "UPDATE venues SET " + strings.Join(sets, ",") +
		" WHERE institution_id=$1 AND id=$2 RETURNING " + strings.Join(columns, ",")
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("update venue fields: %w", err)
	}
	if !rows.Next() {
		rows.Close()
		return nil, catalogue.ErrNotFound
	}
	var venueIDResult pgtype.UUID
	var name string
	// A venue may have no coordinates, so these three come back nullable.
	var latitude, longitude *float64
	var radius *int32
	var department pgtype.UUID
	var active bool
	if err := rows.Scan(&venueIDResult, &name, &latitude, &longitude, &radius, &department, &active); err != nil {
		rows.Close()
		return nil, fmt.Errorf("scan updated venue: %w", err)
	}
	rows.Close()
	fields, err := json.Marshal(update)
	if err != nil {
		return nil, fmt.Errorf("encode venue update audit: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_log(institution_id,actor_user_id,action,resource_type,resource_id,details)
		VALUES($1,$2,'venue.updated','venue',$3,jsonb_build_object('fields',$4::jsonb))`,
		tenant, actor, id, string(fields)); err != nil {
		return nil, fmt.Errorf("audit venue update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit venue update: %w", err)
	}
	return map[string]any{
		"id": venueIDResult.String(), "name": name, "latitude": latitude, "longitude": longitude,
		"radius_meters": radius, "department_id": nullableUUIDString(department), "active": active,
	}, nil
}

// CaptureVenueLocation writes a position captured inside the venue over its
// stored coordinates, deriving a radius when the venue has none set.
func (s *Store) CaptureVenueLocation(
	ctx context.Context,
	institutionID, userID, venueID string,
	location catalogue.VenueLocation,
) (map[string]any, error) {
	tenant, actor, err := parseVenueActor(institutionID, userID)
	if err != nil {
		return nil, err
	}
	id, err := db.ParseUUID(venueID)
	if err != nil {
		return nil, catalogue.ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin venue location capture: %w", err)
	}
	defer tx.Rollback(ctx)
	var department pgtype.UUID
	var currentRadius *int32
	err = tx.QueryRow(ctx, `SELECT department_id,radius_meters FROM venues
		WHERE institution_id=$1 AND id=$2 FOR UPDATE`, tenant, id).Scan(&department, &currentRadius)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, catalogue.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock venue for location capture: %w", err)
	}
	allowed, err := canManageVenueScope(ctx, tx, tenant, actor, institutionID, department)
	if err != nil {
		return nil, err
	}
	// A lecturer who teaches in this room may calibrate it even without
	// administrative scope over the department. They are the only person who
	// is ever actually standing in it, which is the whole premise of capturing
	// a position instead of typing one; an administrator never visits.
	if !allowed {
		allowed, err = lecturerTeachesAtVenue(ctx, tx, tenant, actor, id)
		if err != nil {
			return nil, err
		}
	}
	if !allowed {
		return nil, catalogue.ErrForbidden
	}
	radius := 0
	if currentRadius != nil {
		radius = int(*currentRadius)
	}
	switch {
	case location.RadiusMeters != nil:
		radius = *location.RadiusMeters
	case radius < 1:
		radius = catalogue.VenueRadiusFromAccuracy(location.AccuracyMeters)
	}
	var name string
	var active bool
	err = tx.QueryRow(ctx, `UPDATE venues SET latitude=$3,longitude=$4,radius_meters=$5
		WHERE institution_id=$1 AND id=$2 RETURNING name,active`,
		tenant, id, location.Latitude, location.Longitude, radius).Scan(&name, &active)
	if err != nil {
		return nil, fmt.Errorf("store captured venue location: %w", err)
	}
	fields, err := json.Marshal(location)
	if err != nil {
		return nil, fmt.Errorf("encode venue location audit: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_log(institution_id,actor_user_id,action,resource_type,resource_id,details)
		VALUES($1,$2,'venue.location_captured','venue',$3,$4::jsonb)`, tenant, actor, id, string(fields)); err != nil {
		return nil, fmt.Errorf("audit venue location capture: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit venue location capture: %w", err)
	}
	return map[string]any{
		"id": id.String(), "name": name, "latitude": location.Latitude,
		"longitude": location.Longitude, "radius_meters": radius,
		"accuracy_meters": location.AccuracyMeters,
		"department_id":   nullableUUIDString(department), "active": active,
	}, nil
}

func (s *Store) DeactivateVenue(ctx context.Context, institutionID, userID, venueID string) error {
	tenant, actor, err := parseVenueActor(institutionID, userID)
	if err != nil {
		return err
	}
	id, err := db.ParseUUID(venueID)
	if err != nil {
		return catalogue.ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin venue deactivation: %w", err)
	}
	defer tx.Rollback(ctx)
	var department pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT department_id FROM venues WHERE institution_id=$1 AND id=$2 FOR UPDATE`,
		tenant, id).Scan(&department)
	if errors.Is(err, pgx.ErrNoRows) {
		return catalogue.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock venue for deactivation: %w", err)
	}
	allowed, err := canManageVenueScope(ctx, tx, tenant, actor, institutionID, department)
	if err != nil {
		return err
	}
	if !allowed {
		return catalogue.ErrForbidden
	}
	tag, err := tx.Exec(ctx, `UPDATE venues SET active=false WHERE institution_id=$1 AND id=$2 AND active=true`, tenant, id)
	if err != nil {
		return fmt.Errorf("deactivate venue: %w", err)
	}
	if tag.RowsAffected() == 1 {
		if _, err := tx.Exec(ctx, `INSERT INTO audit_log(institution_id,actor_user_id,action,resource_type,resource_id,details)
			VALUES($1,$2,'venue.deactivated','venue',$3,'{}'::jsonb)`, tenant, actor, id); err != nil {
			return fmt.Errorf("audit venue deactivation: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit venue deactivation: %w", err)
	}
	return nil
}

// lecturerTeachesAtVenue reports whether the actor is an assigned lecturer for
// an offering timetabled at the venue. The device store asks the same question
// of the same two tables; see lecturerTeachesAtVenue in
// internal/devices/postgres.go.
func lecturerTeachesAtVenue(ctx context.Context, queryer venueQueryer, tenant, actor, venueID pgtype.UUID) (bool, error) {
	var teaches bool
	err := queryer.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM offering_lecturers AS ol
		JOIN timetable AS t ON t.institution_id=ol.institution_id AND t.offering_id=ol.offering_id
		WHERE ol.institution_id=$1 AND ol.lecturer_user_id=$2 AND t.venue_id=$3)`,
		tenant, actor, venueID).Scan(&teaches)
	if err != nil {
		return false, fmt.Errorf("resolve lecturer venue authority: %w", err)
	}
	return teaches, nil
}

func canManageVenueScope(
	ctx context.Context,
	queryer venueQueryer,
	tenant, actor pgtype.UUID,
	institutionID string,
	departmentID pgtype.UUID,
) (bool, error) {
	target := rbac.Target{InstitutionID: institutionID}
	if departmentID.Valid {
		err := queryer.QueryRow(ctx, `SELECT faculty_id::text FROM departments
			WHERE institution_id=$1 AND id=$2`, tenant, departmentID).Scan(&target.FacultyID)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, catalogue.ErrInvalidRequest
		}
		if err != nil {
			return false, fmt.Errorf("resolve venue department scope: %w", err)
		}
		target.DepartmentID = departmentID.String()
	}
	assignments, err := venueAssignments(ctx, queryer, tenant, actor)
	if err != nil {
		return false, err
	}
	return canManageVenueAssignments(assignments, target), nil
}

func canManageVenueAssignments(assignments []rbac.Assignment, target rbac.Target) bool {
	for _, assignment := range assignments {
		if rbac.CanAccess(assignment, target, "", "write") {
			return true
		}
	}
	return false
}

func venueAssignments(ctx context.Context, queryer venueQueryer, tenant, actor pgtype.UUID) ([]rbac.Assignment, error) {
	rows, err := queryer.Query(ctx, `SELECT role,scope_type,institution_id,faculty_id,department_id
		FROM role_assignments WHERE institution_id=$1 AND user_id=$2`, tenant, actor)
	if err != nil {
		return nil, fmt.Errorf("load venue management roles: %w", err)
	}
	defer rows.Close()
	assignments := make([]rbac.Assignment, 0)
	for rows.Next() {
		var role, scope string
		var institution, faculty, department pgtype.UUID
		if err := rows.Scan(&role, &scope, &institution, &faculty, &department); err != nil {
			return nil, fmt.Errorf("scan venue management role: %w", err)
		}
		assignment := rbac.Assignment{
			Role: role, ScopeType: rbac.ScopeType(scope), InstitutionID: institution.String(),
		}
		if faculty.Valid {
			assignment.FacultyID = faculty.String()
		}
		if department.Valid {
			assignment.DepartmentID = department.String()
		}
		assignments = append(assignments, assignment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read venue management roles: %w", err)
	}
	return assignments, nil
}

func parseVenueActor(institutionID, userID string) (pgtype.UUID, pgtype.UUID, error) {
	tenant, err := db.ParseUUID(institutionID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("parse venue institution ID: %w", err)
	}
	actor, err := db.ParseUUID(userID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("parse venue actor ID: %w", err)
	}
	return tenant, actor, nil
}

func optionalVenueUUID(value string) (pgtype.UUID, error) {
	if strings.TrimSpace(value) == "" {
		return pgtype.UUID{}, nil
	}
	parsed, err := db.ParseUUID(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed.Bytes, Valid: true}, nil
}

func nullableUUIDString(value pgtype.UUID) any {
	if !value.Valid {
		return nil
	}
	return value.String()
}

var (
	_ catalogue.VenueManagementStore = (*Store)(nil)
	_ catalogue.VenueLocationStore   = (*Store)(nil)
)
