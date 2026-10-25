package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"attendance-api/internal/db"
	"attendance-api/internal/rbac"
)

var (
	// ErrDirectoryScopeDenied is returned when a caller holds no administrator
	// authority that would let them read any part of the user directory.
	ErrDirectoryScopeDenied = errors.New("caller cannot read the user directory")
	// ErrInvalidDirectoryQuery is returned for malformed directory query input.
	ErrInvalidDirectoryQuery = errors.New("user directory query is invalid")
)

// directoryRoleFilters is the set of role values a caller may filter on.
var directoryRoleFilters = map[string]struct{}{
	"student":           {},
	"lecturer":          {},
	"department_admin":  {},
	"faculty_admin":     {},
	"institution_admin": {},
}

// DirectoryRole is one scoped role a directory user holds. ScopeID is the
// faculty or department the role is scoped to, and is null for
// institution- or platform-level roles.
type DirectoryRole struct {
	Role      string  `json:"role"`
	ScopeType string  `json:"scope_type"`
	ScopeID   *string `json:"scope_id"`
}

// DirectoryUser is one entry in the scoped user directory.
type DirectoryUser struct {
	ID          string          `json:"id"`
	DisplayName string          `json:"display_name"`
	Email       string          `json:"email"`
	Roles       []DirectoryRole `json:"roles"`
}

// DirectoryFilter carries the optional search and pagination parameters.
type DirectoryFilter struct {
	Query        string
	Role         string
	DepartmentID string
	Limit        int
	Offset       int
}

// DirectoryScope describes the slice of an institution a caller may read.
// FacultyIDs and DepartmentIDs are the scopes the caller administers; a
// faculty administrator's faculties are expanded into their departments by
// CallerDirectoryScope so department-scoped users become visible.
type DirectoryScope struct {
	Institution   bool
	FacultyIDs    []string
	DepartmentIDs []string
}

// Empty reports whether the scope grants no directory visibility at all.
func (s DirectoryScope) Empty() bool {
	return !s.Institution && len(s.FacultyIDs) == 0 && len(s.DepartmentIDs) == 0
}

// DirectoryRepository resolves caller authority and returns scoped results.
type DirectoryRepository interface {
	CallerDirectoryScope(context.Context, Identity) (DirectoryScope, error)
	SearchUsers(context.Context, Identity, DirectoryScope, DirectoryFilter) ([]DirectoryUser, error)
}

// DirectoryService exposes a scoped, paginated user-directory search.
type DirectoryService struct {
	repository DirectoryRepository
}

// NewDirectoryService validates dependencies and builds the service.
func NewDirectoryService(repository DirectoryRepository) (*DirectoryService, error) {
	if repository == nil {
		return nil, errors.New("directory repository is required")
	}
	return &DirectoryService{repository: repository}, nil
}

// Search resolves the caller's directory scope, rejects callers with no
// authority, and returns the matching page plus a has-more flag.
func (s *DirectoryService) Search(ctx context.Context, actor Identity, filter DirectoryFilter) ([]DirectoryUser, bool, error) {
	if actor.InstitutionID == "" || actor.UserID == "" {
		return nil, false, ErrInvalidDirectoryQuery
	}
	if filter.Limit < 1 || filter.Limit > 200 || filter.Offset < 0 {
		return nil, false, ErrInvalidDirectoryQuery
	}
	filter.Query = strings.TrimSpace(filter.Query)
	filter.Role = strings.TrimSpace(filter.Role)
	if filter.Role != "" {
		if _, ok := directoryRoleFilters[filter.Role]; !ok {
			return nil, false, ErrInvalidDirectoryQuery
		}
	}
	filter.DepartmentID = strings.TrimSpace(filter.DepartmentID)
	if filter.DepartmentID != "" {
		if _, err := db.ParseUUID(filter.DepartmentID); err != nil {
			return nil, false, ErrInvalidDirectoryQuery
		}
	}
	scope, err := s.repository.CallerDirectoryScope(ctx, actor)
	if err != nil {
		return nil, false, fmt.Errorf("resolve directory scope: %w", err)
	}
	if scope.Empty() {
		return nil, false, ErrDirectoryScopeDenied
	}
	users, err := s.repository.SearchUsers(ctx, actor, scope, DirectoryFilter{
		Query: filter.Query, Role: filter.Role, DepartmentID: filter.DepartmentID,
		Limit: filter.Limit + 1, Offset: filter.Offset,
	})
	if err != nil {
		return nil, false, fmt.Errorf("search directory users: %w", err)
	}
	hasMore := len(users) > filter.Limit
	if hasMore {
		users = users[:filter.Limit]
	}
	return users, hasMore, nil
}

// directoryScopeFromAssignments derives the raw directory scope implied by a
// caller's administrator role assignments. Non-admin roles (lecturer, student)
// grant no directory visibility.
func directoryScopeFromAssignments(assignments []rbac.Assignment) DirectoryScope {
	scope := DirectoryScope{}
	for _, assignment := range assignments {
		switch assignment.Role {
		case "institution_admin":
			if assignment.ScopeType == rbac.ScopeInstitution && assignment.InstitutionID != "" {
				scope.Institution = true
			}
		case "faculty_admin":
			if assignment.ScopeType == rbac.ScopeFaculty && assignment.FacultyID != "" {
				scope.FacultyIDs = appendUnique(scope.FacultyIDs, assignment.FacultyID)
			}
		case "department_admin":
			if assignment.ScopeType == rbac.ScopeDepartment && assignment.DepartmentID != "" {
				scope.DepartmentIDs = appendUnique(scope.DepartmentIDs, assignment.DepartmentID)
			}
		}
	}
	return scope
}

// userVisible reports whether a directory user falls within the caller's scope.
// Faculty-level callers must have their faculties' departments expanded into
// DepartmentIDs beforehand, so a user is visible when any of their role scopes
// names a faculty or department the caller administers.
func userVisible(scope DirectoryScope, user DirectoryUser) bool {
	if scope.Institution {
		return true
	}
	faculties := toStringSet(scope.FacultyIDs)
	departments := toStringSet(scope.DepartmentIDs)
	for _, role := range user.Roles {
		if role.ScopeID == nil {
			continue
		}
		switch role.ScopeType {
		case "faculty":
			if _, ok := faculties[*role.ScopeID]; ok {
				return true
			}
		case "department":
			if _, ok := departments[*role.ScopeID]; ok {
				return true
			}
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func toStringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}
