package dbgen

import (
	"strings"
	"testing"
)

func TestAutoRegisterClaimedStudentIsTenantAndScopeBound(t *testing.T) {
	t.Parallel()
	for _, fragment := range []string{
		"WHERE institution_id = $1",
		"AND id = $2",
		"offering.registration_mode = 'auto'",
		"offering.level = student.level",
		"course.department_id = student.department_id",
		"target.department_id = student.department_id",
		"target.level = student.level",
		"ON CONFLICT (institution_id, offering_id, student_record_id) DO UPDATE",
		"WHERE course_registrations.status <> 'approved'",
	} {
		if !strings.Contains(autoRegisterClaimedStudent, fragment) {
			t.Errorf("auto-registration query missing scope or behavior %q", fragment)
		}
	}
}
