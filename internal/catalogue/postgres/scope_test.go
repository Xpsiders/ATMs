package postgres

import (
	"strings"
	"testing"

	"attendance-api/internal/catalogue"
)

func TestCatalogueReadScopeUsesOwningDepartment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		resource catalogue.Resource
		owner    string
	}{
		{catalogue.Departments, "rd.id = t.id"},
		{catalogue.Programmes, "rd.id = t.department_id"},
		{catalogue.Courses, "rd.id = t.department_id"},
		{catalogue.Offerings, "rd.id = c.department_id"},
	}
	for _, test := range tests {
		t.Run(string(test.resource), func(t *testing.T) {
			t.Parallel()
			scope, err := catalogueReadScope(test.resource)
			if err != nil {
				t.Fatal(err)
			}
			for _, fragment := range []string{
				test.owner,
				"ra.department_id = rd.id",
				"ra.faculty_id = rd.faculty_id",
				"broad_role.role = 'institution_admin'",
				"broad_role.role = 'platform_admin'",
				"ra.user_id = $2",
			} {
				if !strings.Contains(scope, fragment) {
					t.Errorf("read scope for %s missing %q: %s", test.resource, fragment, scope)
				}
			}
			if scope == "TRUE" {
				t.Fatalf("resource %s unexpectedly has an unscoped read predicate", test.resource)
			}
		})
	}
}

func TestOfferingReadScopeIncludesGeneralCourseVisibilityRules(t *testing.T) {
	t.Parallel()
	scope, err := catalogueReadScope(catalogue.Offerings)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"assigned.coordinator_user_id = $2",
		"assigned_lecturer.lecturer_user_id = $2",
		"c.kind = 'general'",
		"registration.status IN ('approved', 'pending')",
		"target.department_id = home.department_id",
		"target.level = home.level",
		"home.claimed_by_user_id = $2",
	} {
		if !strings.Contains(scope, fragment) {
			t.Errorf("offering scope missing %q: %s", fragment, scope)
		}
	}
}

func TestSharedCalendarRequiresAuthenticatedTenantIdentity(t *testing.T) {
	t.Parallel()
	for _, resource := range []catalogue.Resource{catalogue.AcademicSessions, catalogue.Semesters, catalogue.Institutions} {
		scope, err := catalogueReadScope(resource)
		if err != nil {
			t.Fatal(err)
		}
		if scope != "$2::uuid IS NOT NULL" {
			t.Errorf("scope for shared resource %s = %q, want an authenticated-actor predicate", resource, scope)
		}
	}
	if _, err := catalogueReadScope("unknown"); err == nil {
		t.Fatal("unknown resource scope was accepted")
	}
}
