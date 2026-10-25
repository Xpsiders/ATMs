package auth

import (
	"context"
	"fmt"

	"attendance-api/internal/db"

	"github.com/jackc/pgx/v5/pgtype"
)

// CallerDirectoryScope loads the caller's administrator role assignments and
// derives the slice of the institution they may read. Faculty authority is
// expanded into the faculty's departments so department-scoped users remain
// visible to faculty administrators.
func (s *PostgresStore) CallerDirectoryScope(ctx context.Context, actor Identity) (DirectoryScope, error) {
	tenant, err := db.ParseUUID(actor.InstitutionID)
	if err != nil {
		return DirectoryScope{}, fmt.Errorf("parse directory institution ID: %w", err)
	}
	actorID, err := db.ParseUUID(actor.UserID)
	if err != nil {
		return DirectoryScope{}, fmt.Errorf("parse directory actor ID: %w", err)
	}
	assignments, err := signupAssignments(ctx, s.pool, tenant, actorID)
	if err != nil {
		return DirectoryScope{}, err
	}
	scope := directoryScopeFromAssignments(assignments)
	if scope.Institution || len(scope.FacultyIDs) == 0 {
		return scope, nil
	}
	faculties, err := parseDirectoryUUIDs(scope.FacultyIDs)
	if err != nil {
		return DirectoryScope{}, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text FROM departments
		WHERE institution_id=$1 AND faculty_id = ANY($2)`, tenant, faculties)
	if err != nil {
		return DirectoryScope{}, fmt.Errorf("expand directory faculty scope: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var departmentID string
		if err := rows.Scan(&departmentID); err != nil {
			return DirectoryScope{}, fmt.Errorf("scan directory department scope: %w", err)
		}
		scope.DepartmentIDs = appendUnique(scope.DepartmentIDs, departmentID)
	}
	if err := rows.Err(); err != nil {
		return DirectoryScope{}, fmt.Errorf("read directory department scope: %w", err)
	}
	return scope, nil
}

// SearchUsers returns the scoped, filtered, paginated directory page. The scope
// predicate restricts results to users holding at least one role assignment in
// a faculty or department the caller administers (or every user, for an
// institution administrator).
func (s *PostgresStore) SearchUsers(ctx context.Context, actor Identity, scope DirectoryScope, filter DirectoryFilter) ([]DirectoryUser, error) {
	tenant, err := db.ParseUUID(actor.InstitutionID)
	if err != nil {
		return nil, fmt.Errorf("parse directory institution ID: %w", err)
	}
	faculties, err := parseDirectoryUUIDs(scope.FacultyIDs)
	if err != nil {
		return nil, err
	}
	departments, err := parseDirectoryUUIDs(scope.DepartmentIDs)
	if err != nil {
		return nil, err
	}
	var departmentFilter pgtype.UUID
	if filter.DepartmentID != "" {
		departmentFilter, err = db.ParseUUID(filter.DepartmentID)
		if err != nil {
			return nil, fmt.Errorf("parse directory department filter: %w", err)
		}
	}
	rows, err := s.pool.Query(ctx, `SELECT u.id::text, u.display_name, u.email
		FROM users u
		WHERE u.institution_id=$1 AND u.disabled_at IS NULL
		  AND ($2 OR EXISTS (
		      SELECT 1 FROM role_assignments ra
		      WHERE ra.institution_id=u.institution_id AND ra.user_id=u.id
		        AND (ra.faculty_id = ANY($3) OR ra.department_id = ANY($4))))
		  AND ($5 = '' OR u.display_name ILIKE '%' || $5 || '%' OR u.email ILIKE '%' || $5 || '%')
		  AND ($6 = '' OR EXISTS (
		      SELECT 1 FROM role_assignments rr
		      WHERE rr.institution_id=u.institution_id AND rr.user_id=u.id AND rr.role=$6))
		  AND (NOT $7::boolean OR EXISTS (
		      SELECT 1 FROM role_assignments rd
		      WHERE rd.institution_id=u.institution_id AND rd.user_id=u.id AND rd.department_id=$8))
		ORDER BY u.display_name, u.id
		LIMIT $9 OFFSET $10`,
		tenant, scope.Institution, faculties, departments,
		filter.Query, filter.Role, departmentFilter.Valid, departmentFilter,
		filter.Limit, filter.Offset)
	if err != nil {
		return nil, fmt.Errorf("query directory users: %w", err)
	}
	defer rows.Close()
	users := []DirectoryUser{}
	ids := []pgtype.UUID{}
	position := map[string]int{}
	for rows.Next() {
		var id, name, email string
		if err := rows.Scan(&id, &name, &email); err != nil {
			return nil, fmt.Errorf("scan directory user: %w", err)
		}
		position[id] = len(users)
		users = append(users, DirectoryUser{ID: id, DisplayName: name, Email: email, Roles: []DirectoryRole{}})
		parsed, err := db.ParseUUID(id)
		if err != nil {
			return nil, fmt.Errorf("parse directory user ID: %w", err)
		}
		ids = append(ids, parsed)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read directory users: %w", err)
	}
	if len(users) == 0 {
		return users, nil
	}
	roleRows, err := s.pool.Query(ctx, `SELECT user_id::text, role, scope_type, faculty_id::text, department_id::text
		FROM role_assignments
		WHERE institution_id=$1 AND user_id = ANY($2)
		ORDER BY role, scope_type`, tenant, ids)
	if err != nil {
		return nil, fmt.Errorf("query directory roles: %w", err)
	}
	defer roleRows.Close()
	for roleRows.Next() {
		var userID, role, scopeType string
		var facultyID, departmentID pgtype.Text
		if err := roleRows.Scan(&userID, &role, &scopeType, &facultyID, &departmentID); err != nil {
			return nil, fmt.Errorf("scan directory role: %w", err)
		}
		index, ok := position[userID]
		if !ok {
			continue
		}
		entry := DirectoryRole{Role: role, ScopeType: scopeType}
		switch scopeType {
		case "faculty":
			if facultyID.Valid {
				value := facultyID.String
				entry.ScopeID = &value
			}
		case "department":
			if departmentID.Valid {
				value := departmentID.String
				entry.ScopeID = &value
			}
		}
		users[index].Roles = append(users[index].Roles, entry)
	}
	if err := roleRows.Err(); err != nil {
		return nil, fmt.Errorf("read directory roles: %w", err)
	}
	return users, nil
}

func parseDirectoryUUIDs(values []string) ([]pgtype.UUID, error) {
	parsed := make([]pgtype.UUID, 0, len(values))
	for _, value := range values {
		id, err := db.ParseUUID(value)
		if err != nil {
			return nil, fmt.Errorf("parse directory scope ID: %w", err)
		}
		parsed = append(parsed, id)
	}
	return parsed, nil
}

var _ DirectoryRepository = (*PostgresStore)(nil)
