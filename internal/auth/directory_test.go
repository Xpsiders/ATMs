package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"attendance-api/internal/rbac"

	"github.com/go-chi/chi/v5"
)

// directoryFake is an in-memory DirectoryRepository that honors the scope
// contract using the production userVisible/directoryScopeFromAssignments
// helpers, so handler tests exercise the real scoping logic.
type directoryFake struct {
	assignments []rbac.Assignment
	users       []DirectoryUser
	scopeErr    error
	lastScope   DirectoryScope
	lastFilter  DirectoryFilter
}

func (f *directoryFake) CallerDirectoryScope(_ context.Context, _ Identity) (DirectoryScope, error) {
	if f.scopeErr != nil {
		return DirectoryScope{}, f.scopeErr
	}
	return directoryScopeFromAssignments(f.assignments), nil
}

func (f *directoryFake) SearchUsers(_ context.Context, _ Identity, scope DirectoryScope, filter DirectoryFilter) ([]DirectoryUser, error) {
	f.lastScope, f.lastFilter = scope, filter
	matched := []DirectoryUser{}
	for _, user := range f.users {
		if !userVisible(scope, user) {
			continue
		}
		if filter.Role != "" && !directoryUserHasRole(user, filter.Role) {
			continue
		}
		if filter.Query != "" && !directoryUserMatchesQuery(user, filter.Query) {
			continue
		}
		if filter.DepartmentID != "" && !directoryUserInDepartment(user, filter.DepartmentID) {
			continue
		}
		matched = append(matched, user)
	}
	start := filter.Offset
	if start > len(matched) {
		start = len(matched)
	}
	end := start + filter.Limit
	if end > len(matched) {
		end = len(matched)
	}
	return matched[start:end], nil
}

func directoryUserHasRole(user DirectoryUser, role string) bool {
	for _, item := range user.Roles {
		if item.Role == role {
			return true
		}
	}
	return false
}

func directoryUserMatchesQuery(user DirectoryUser, query string) bool {
	query = strings.ToLower(query)
	return strings.Contains(strings.ToLower(user.DisplayName), query) ||
		strings.Contains(strings.ToLower(user.Email), query)
}

func directoryUserInDepartment(user DirectoryUser, departmentID string) bool {
	for _, item := range user.Roles {
		if item.ScopeType == "department" && item.ScopeID != nil && *item.ScopeID == departmentID {
			return true
		}
	}
	return false
}

func strptr(value string) *string { return &value }

func departmentRole(role, departmentID string) DirectoryRole {
	return DirectoryRole{Role: role, ScopeType: "department", ScopeID: strptr(departmentID)}
}

func directoryTestUsers() []DirectoryUser {
	return []DirectoryUser{
		{ID: "user-a", DisplayName: "Ada Lovelace", Email: "ada@example.edu",
			Roles: []DirectoryRole{departmentRole("lecturer", "dept-a")}},
		{ID: "user-b", DisplayName: "Alan Turing", Email: "alan@example.edu",
			Roles: []DirectoryRole{departmentRole("student", "dept-b")}},
		{ID: "user-c", DisplayName: "Grace Hopper", Email: "grace@example.edu",
			Roles: []DirectoryRole{{Role: "institution_admin", ScopeType: "institution", ScopeID: nil}}},
	}
}

func directoryRouter(t *testing.T, repo DirectoryRepository) *chi.Mux {
	t.Helper()
	service, err := NewDirectoryService(repo)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewDirectoryHandler(service)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	return router
}

func directoryRequest(target string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	return request.WithContext(WithPrincipal(request.Context(),
		Principal{Identity: Identity{InstitutionID: "inst", UserID: "admin"}}))
}

type directoryResponse struct {
	Items   []DirectoryUser `json:"items"`
	Limit   int             `json:"limit"`
	Offset  int             `json:"offset"`
	HasMore bool            `json:"has_more"`
}

func decodeDirectory(t *testing.T, response *httptest.ResponseRecorder) directoryResponse {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var body directoryResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestDirectoryScopeFromAssignments(t *testing.T) {
	t.Parallel()
	scope := directoryScopeFromAssignments([]rbac.Assignment{
		{Role: "institution_admin", ScopeType: rbac.ScopeInstitution, InstitutionID: "inst"},
	})
	if !scope.Institution {
		t.Fatalf("institution admin should grant institution-wide scope: %+v", scope)
	}
	scope = directoryScopeFromAssignments([]rbac.Assignment{
		{Role: "department_admin", ScopeType: rbac.ScopeDepartment, InstitutionID: "inst", DepartmentID: "dept-a"},
		{Role: "lecturer", ScopeType: rbac.ScopeDepartment, InstitutionID: "inst", DepartmentID: "dept-z"},
	})
	if scope.Institution || len(scope.DepartmentIDs) != 1 || scope.DepartmentIDs[0] != "dept-a" {
		t.Fatalf("department admin scope should be dept-a only, got %+v", scope)
	}
	if directoryScopeFromAssignments(nil).Empty() != true {
		t.Fatal("no assignments should yield an empty scope")
	}
}

func TestUserVisibleHonorsScope(t *testing.T) {
	t.Parallel()
	deptUser := DirectoryUser{Roles: []DirectoryRole{departmentRole("lecturer", "dept-a")}}
	otherDeptUser := DirectoryUser{Roles: []DirectoryRole{departmentRole("lecturer", "dept-b")}}
	instScope := DirectoryScope{Institution: true}
	if !userVisible(instScope, deptUser) || !userVisible(instScope, otherDeptUser) {
		t.Fatal("institution scope should see every user")
	}
	deptScope := DirectoryScope{DepartmentIDs: []string{"dept-a"}}
	if !userVisible(deptScope, deptUser) {
		t.Fatal("department scope should see its own department's user")
	}
	if userVisible(deptScope, otherDeptUser) {
		t.Fatal("department scope must not see other departments' users")
	}
}

func TestDirectoryDepartmentAdminScopedToDepartment(t *testing.T) {
	t.Parallel()
	repo := &directoryFake{
		assignments: []rbac.Assignment{
			{Role: "department_admin", ScopeType: rbac.ScopeDepartment, InstitutionID: "inst", DepartmentID: "dept-a"},
		},
		users: directoryTestUsers(),
	}
	router := directoryRouter(t, repo)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, directoryRequest("/users"))
	body := decodeDirectory(t, response)
	if len(body.Items) != 1 || body.Items[0].ID != "user-a" {
		t.Fatalf("department admin should see only dept-a users, got %+v", body.Items)
	}
	if repo.lastScope.Institution || len(repo.lastScope.DepartmentIDs) != 1 || repo.lastScope.DepartmentIDs[0] != "dept-a" {
		t.Fatalf("store should receive a department-restricted scope, got %+v", repo.lastScope)
	}
}

func TestDirectoryRoleFilterIsForwardedAndApplied(t *testing.T) {
	t.Parallel()
	repo := &directoryFake{
		assignments: []rbac.Assignment{
			{Role: "institution_admin", ScopeType: rbac.ScopeInstitution, InstitutionID: "inst"},
		},
		users: directoryTestUsers(),
	}
	router := directoryRouter(t, repo)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, directoryRequest("/users?role=lecturer"))
	body := decodeDirectory(t, response)
	if repo.lastFilter.Role != "lecturer" {
		t.Fatalf("role filter should be forwarded, got %q", repo.lastFilter.Role)
	}
	if len(body.Items) != 1 || body.Items[0].ID != "user-a" {
		t.Fatalf("role=lecturer should return only the lecturer, got %+v", body.Items)
	}
}

func TestDirectoryQueryFilterIsForwardedAndApplied(t *testing.T) {
	t.Parallel()
	repo := &directoryFake{
		assignments: []rbac.Assignment{
			{Role: "institution_admin", ScopeType: rbac.ScopeInstitution, InstitutionID: "inst"},
		},
		users: directoryTestUsers(),
	}
	router := directoryRouter(t, repo)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, directoryRequest("/users?q=ada"))
	body := decodeDirectory(t, response)
	if repo.lastFilter.Query != "ada" {
		t.Fatalf("q filter should be forwarded, got %q", repo.lastFilter.Query)
	}
	if len(body.Items) != 1 || body.Items[0].Email != "ada@example.edu" {
		t.Fatalf("q=ada should match only Ada Lovelace, got %+v", body.Items)
	}
}

func TestDirectoryInvalidRoleFilterRejected(t *testing.T) {
	t.Parallel()
	repo := &directoryFake{
		assignments: []rbac.Assignment{
			{Role: "institution_admin", ScopeType: rbac.ScopeInstitution, InstitutionID: "inst"},
		},
		users: directoryTestUsers(),
	}
	router := directoryRouter(t, repo)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, directoryRequest("/users?role=wizard"))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestDirectoryDeniesCallerWithoutAuthority(t *testing.T) {
	t.Parallel()
	repo := &directoryFake{
		assignments: []rbac.Assignment{
			{Role: "lecturer", ScopeType: rbac.ScopeDepartment, InstitutionID: "inst", DepartmentID: "dept-a"},
		},
		users: directoryTestUsers(),
	}
	router := directoryRouter(t, repo)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, directoryRequest("/users"))
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestDirectoryPaginationHasMore(t *testing.T) {
	t.Parallel()
	repo := &directoryFake{
		assignments: []rbac.Assignment{
			{Role: "institution_admin", ScopeType: rbac.ScopeInstitution, InstitutionID: "inst"},
		},
		users: directoryTestUsers(),
	}
	service, err := NewDirectoryService(repo)
	if err != nil {
		t.Fatal(err)
	}
	items, hasMore, err := service.Search(context.Background(),
		Identity{InstitutionID: "inst", UserID: "admin"}, DirectoryFilter{Limit: 2, Offset: 0})
	if err != nil || !hasMore || len(items) != 2 {
		t.Fatalf("Search() = %d items, hasMore=%v, err=%v", len(items), hasMore, err)
	}
	if repo.lastFilter.Limit != 3 {
		t.Fatalf("service should request limit+1 for has_more detection, got %d", repo.lastFilter.Limit)
	}
}
