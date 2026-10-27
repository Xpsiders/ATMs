package dbgen

import (
	"strings"
	"testing"
)

func TestAutoRegisterClaimedStudentIsTenantAndScopeBound(t *testing.T) {
	t.Parallel()
	for _, fragment := range []string{
		// Qualified with the table name so sqlc resolves them against the CTE
		// source rather than the INSERT target, which is ambiguous. The
		// placeholder numbers are deliberately not asserted: sqlc assigns them
		// from the named arguments, and the call site binds by field name.
		"WHERE student_records.institution_id = ",
		"AND student_records.id = ",
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
