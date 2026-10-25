package dbgen

import (
	"strings"
	"testing"
)

func TestSessionRosterAccessReturnsNonNullStudentFilter(t *testing.T) {
	if !strings.Contains(sessionRosterAccess, "END::text, '') AS student_filter") {
		t.Fatal("session roster access must return an empty string when no student filter applies")
	}
}

func TestSessionRosterAccessResolvesOfferingFromSession(t *testing.T) {
	for _, expected := range []string{
		"ls.id = $2 AND ol.lecturer_user_id = $3",
		"ls.id = $2 AND o.coordinator_user_id = $3",
		"o.id = ls.offering_id",
	} {
		if !strings.Contains(sessionRosterAccess, expected) {
			t.Errorf("session roster access query is missing %q", expected)
		}
	}
	if strings.Contains(sessionRosterAccess, "o.id = $2 AND ol.lecturer_user_id") ||
		strings.Contains(sessionRosterAccess, "o.id = $2 AND o.coordinator_user_id") {
		t.Fatal("session roster access must not compare the session ID with an offering ID")
	}
}
