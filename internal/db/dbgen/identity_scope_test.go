package dbgen

import (
	"strings"
	"testing"
)

func TestVenueQueriesAreTenantScopedAndRoleGated(t *testing.T) {
	t.Parallel()
	if !strings.Contains(canListVenues, "institution_id = $1") {
		t.Errorf("venue permission query is not tenant-scoped: %s", canListVenues)
	}
	for _, roleScope := range []struct{ role, scope string }{
		{"platform_admin", "platform"},
		{"institution_admin", "institution"},
		{"faculty_admin", "faculty"},
		{"department_admin", "department"},
		{"lecturer", "department"},
	} {
		condition := "(role = '" + roleScope.role + "' AND scope_type = '" + roleScope.scope + "')"
		if !strings.Contains(canListVenues, condition) {
			t.Errorf("venue permission query does not allow scoped role %s: %s", roleScope.role, canListVenues)
		}
	}
	if strings.Contains(canListVenues, "role = 'student'") {
		t.Fatal("student role must not be allowed to list venues")
	}
}

func TestCanManageOfferingAllowsOnlyCoordinatorOrAssignedLecturer(t *testing.T) {
	t.Parallel()
	for _, fragment := range []string{
		"o.institution_id = $1",
		"o.id = $2",
		"o.coordinator_user_id = $3",
		"ol.offering_id = o.id",
		"ol.lecturer_user_id = $3",
	} {
		if !strings.Contains(canManageOffering, fragment) {
			t.Errorf("offering start authorization query missing %q: %s", fragment, canManageOffering)
		}
	}
}

func TestStudentLiveClassesRequireOwnedApprovedOpenSessionsWithoutCodes(t *testing.T) {
	t.Parallel()
	for _, fragment := range []string{
		"ls.institution_id = $1",
		"student.claimed_by_user_id = $2",
		"registration.status = 'approved'",
		"ls.ended_at IS NULL",
		"FROM fingerprint_scans AS fs",
		"FROM web_checkins AS wc",
	} {
		if !strings.Contains(listStudentLiveClasses, fragment) {
			t.Errorf("live class query missing required restriction/state: %q", fragment)
		}
	}
	for _, forbidden := range []string{"static_code", "code_expires_at", "AS code"} {
		if strings.Contains(listStudentLiveClasses, forbidden) {
			t.Errorf("live class query exposes attendance code data %q", forbidden)
		}
	}
}
