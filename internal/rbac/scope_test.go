package rbac

import "testing"

func TestScopeResolvedFromOfferingOwningDepartment(t *testing.T) {
	t.Parallel()
	target := ResolveOfferingScope("inst-1", "faculty-1", "dept-owner")
	ownerAdmin := Assignment{Role: "department_admin", ScopeType: ScopeDepartment, InstitutionID: "inst-1", DepartmentID: "dept-owner"}
	otherAdmin := Assignment{Role: "department_admin", ScopeType: ScopeDepartment, InstitutionID: "inst-1", DepartmentID: "dept-student"}
	if !CanAccess(ownerAdmin, target, "", "read") {
		t.Fatal("owning department admin denied")
	}
	if CanAccess(otherAdmin, target, "", "read") {
		t.Fatal("admin of a different department was granted offering scope")
	}
}

func TestGeneralCourseHomeDepartmentIsNotGrantedFullOfferingScope(t *testing.T) {
	t.Parallel()
	target := ResolveOfferingScope("inst-1", "faculty-owner", "dept-owner")
	homeDepartmentAdmin := Assignment{
		Role: "department_admin", ScopeType: ScopeDepartment,
		InstitutionID: "inst-1", DepartmentID: "dept-home",
	}
	if CanAccess(homeDepartmentAdmin, target, "", "read") {
		t.Fatal("home-department admin was granted the full offering scope")
	}
	if CanAccess(homeDepartmentAdmin, target, "", "write") {
		t.Fatal("home-department admin was granted write access to an offering they do not own")
	}
}

func TestFacultyAndInstitutionScope(t *testing.T) {
	t.Parallel()
	target := Target{InstitutionID: "i1", FacultyID: "f1", DepartmentID: "d1"}
	tests := []struct {
		name       string
		assignment Assignment
		want       bool
	}{
		{"faculty member", Assignment{Role: "faculty_admin", ScopeType: ScopeFaculty, InstitutionID: "i1", FacultyID: "f1"}, true},
		{"different faculty", Assignment{Role: "faculty_admin", ScopeType: ScopeFaculty, InstitutionID: "i1", FacultyID: "f2"}, false},
		{"different institution", Assignment{Role: "faculty_admin", ScopeType: ScopeFaculty, InstitutionID: "i2", FacultyID: "f1"}, false},
		{"missing institution on target", Assignment{Role: "faculty_admin", ScopeType: ScopeFaculty, FacultyID: "f1"}, false},
		{"institution admin", Assignment{Role: "institution_admin", ScopeType: ScopeInstitution, InstitutionID: "i1"}, true},
		{"institution mismatch", Assignment{Role: "institution_admin", ScopeType: ScopeInstitution, InstitutionID: "i2"}, false},
		{"platform admin", Assignment{Role: "platform_admin", ScopeType: ScopePlatform}, true},
		{"platform role with wrong scope", Assignment{Role: "platform_admin", ScopeType: ScopeDepartment}, false},
		{"lecturer capability does not grant department scope", Assignment{Role: "lecturer", ScopeType: ScopeDepartment, InstitutionID: "i1", DepartmentID: "d1"}, false},
		{"missing tenant identity", Assignment{Role: "department_admin", ScopeType: ScopeDepartment, DepartmentID: "d1"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CanAccess(tc.assignment, target, "", "write"); got != tc.want {
				t.Fatalf("CanAccess() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStudentRestrictedToOwnRead(t *testing.T) {
	t.Parallel()
	student := Assignment{Role: "student"}
	target := Target{StudentID: "student-1"}
	if !CanAccess(student, target, "student-1", "read") {
		t.Fatal("student cannot read own record")
	}
	if CanAccess(student, target, "student-2", "read") || CanAccess(student, target, "student-1", "write") {
		t.Fatal("student received access outside own read scope")
	}
	if CanAccess(Assignment{Role: "auditor", ScopeType: ScopePlatform}, target, "", "read") {
		t.Fatal("unknown role received platform access")
	}
	if CanAccess(Assignment{Role: "department_admin", ScopeType: "unknown"}, target, "", "read") {
		t.Fatal("unknown scope received access")
	}
}
