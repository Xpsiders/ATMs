package rbac

type ScopeType string

const (
	ScopePlatform    ScopeType = "platform"
	ScopeInstitution ScopeType = "institution"
	ScopeFaculty     ScopeType = "faculty"
	ScopeDepartment  ScopeType = "department"
)

type Assignment struct {
	Role          string
	ScopeType     ScopeType
	InstitutionID string
	FacultyID     string
	DepartmentID  string
}

type Target struct {
	InstitutionID string
	FacultyID     string
	DepartmentID  string
	StudentID     string
}

func ResolveOfferingScope(institutionID, owningFacultyID, owningDepartmentID string) Target {
	return Target{
		InstitutionID: institutionID,
		FacultyID:     owningFacultyID,
		DepartmentID:  owningDepartmentID,
	}
}

func CanAccess(assignment Assignment, target Target, actorID, action string) bool {
	if assignment.Role == "student" {
		return action == "read" && actorID != "" && actorID == target.StudentID
	}
	switch assignment.ScopeType {
	case ScopePlatform:
		return assignment.Role == "platform_admin"
	case ScopeInstitution:
		return assignment.Role == "institution_admin" &&
			assignment.InstitutionID != "" && assignment.InstitutionID == target.InstitutionID
	case ScopeFaculty:
		return assignment.Role == "faculty_admin" &&
			assignment.InstitutionID != "" &&
			assignment.InstitutionID == target.InstitutionID &&
			assignment.FacultyID != "" && assignment.FacultyID == target.FacultyID
	case ScopeDepartment:
		return assignment.Role == "department_admin" &&
			assignment.InstitutionID != "" &&
			assignment.InstitutionID == target.InstitutionID &&
			assignment.DepartmentID != "" && assignment.DepartmentID == target.DepartmentID
	default:
		return false
	}
}
