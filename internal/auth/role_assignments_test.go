package auth

import (
	"testing"

	"attendance-api/internal/rbac"
)

func TestCanAssignAdministratorRoleRespectsHierarchyAndScope(t *testing.T) {
	target := rbac.Target{
		InstitutionID: "institution-1",
		FacultyID:     "faculty-1",
		DepartmentID:  "department-1",
	}
	tests := []struct {
		name        string
		assignments []rbac.Assignment
		role        string
		want        bool
	}{
		{
			name:        "institution admin assigns faculty admin",
			assignments: []rbac.Assignment{{Role: "institution_admin", ScopeType: rbac.ScopeInstitution, InstitutionID: "institution-1"}},
			role:        "faculty_admin", want: true,
		},
		{
			name:        "faculty admin assigns department admin in own faculty",
			assignments: []rbac.Assignment{{Role: "faculty_admin", ScopeType: rbac.ScopeFaculty, InstitutionID: "institution-1", FacultyID: "faculty-1"}},
			role:        "department_admin", want: true,
		},
		{
			name:        "faculty admin cannot assign same-level admin",
			assignments: []rbac.Assignment{{Role: "faculty_admin", ScopeType: rbac.ScopeFaculty, InstitutionID: "institution-1", FacultyID: "faculty-1"}},
			role:        "faculty_admin", want: false,
		},
		{
			name:        "faculty admin cannot cross faculties",
			assignments: []rbac.Assignment{{Role: "faculty_admin", ScopeType: rbac.ScopeFaculty, InstitutionID: "institution-1", FacultyID: "faculty-2"}},
			role:        "department_admin", want: false,
		},
		{
			name:        "department admin cannot assign administrators",
			assignments: []rbac.Assignment{{Role: "department_admin", ScopeType: rbac.ScopeDepartment, InstitutionID: "institution-1", DepartmentID: "department-1"}},
			role:        "department_admin", want: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := canAssignAdministratorRole(test.assignments, test.role, target); got != test.want {
				t.Fatalf("canAssignAdministratorRole() = %v, want %v", got, test.want)
			}
		})
	}
}
