package dbgen

import (
	"strings"
	"testing"
)

func TestVenueQueriesAreTenantScopedAndRoleGated(t *testing.T) {
	t.Parallel()
	for name, query := range map[string]string{
		"permission": canListVenues,
		"list":       listVenues,
		"count":      countVenues,
	} {
		if !strings.Contains(query, "institution_id = $1") {
			t.Errorf("%s query is not tenant-scoped: %s", name, query)
		}
	}
	for _, role := range []string{"platform_admin", "institution_admin", "faculty_admin", "department_admin", "lecturer"} {
		if !strings.Contains(canListVenues, "'"+role+"'") {
			t.Errorf("venue permission query does not allow %s", role)
		}
	}
	if strings.Contains(canListVenues, "'student'") {
		t.Fatal("student role must not be allowed to list venues")
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
