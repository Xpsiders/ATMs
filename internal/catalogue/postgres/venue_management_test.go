package postgres

import (
	"testing"

	"attendance-api/internal/rbac"
)

func TestVenueManagementAssignmentsAreScoped(t *testing.T) {
	target := rbac.Target{
		InstitutionID: "institution-1",
		FacultyID:     "faculty-1",
		DepartmentID:  "department-1",
	}
	tests := []struct {
		name        string
		assignments []rbac.Assignment
		want        bool
	}{
		{
			name: "own department",
			assignments: []rbac.Assignment{{Role: "department_admin", ScopeType: rbac.ScopeDepartment,
				InstitutionID: "institution-1", DepartmentID: "department-1"}},
			want: true,
		},
		{
			name: "own faculty",
			assignments: []rbac.Assignment{{Role: "faculty_admin", ScopeType: rbac.ScopeFaculty,
				InstitutionID: "institution-1", FacultyID: "faculty-1"}},
			want: true,
		},
		{
			name: "different department",
			assignments: []rbac.Assignment{{Role: "department_admin", ScopeType: rbac.ScopeDepartment,
				InstitutionID: "institution-1", DepartmentID: "department-2"}},
			want: false,
		},
		{
			name: "different institution",
			assignments: []rbac.Assignment{{Role: "institution_admin", ScopeType: rbac.ScopeInstitution,
				InstitutionID: "institution-2"}},
			want: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := canManageVenueAssignments(test.assignments, target); got != test.want {
				t.Fatalf("canManageVenueAssignments() = %v, want %v", got, test.want)
			}
		})
	}
}
