package postgres

import (
	"os"
	"strings"
	"testing"
)

// The session report derives a web check-in's location trust from data already
// stored: the recorded distance against the session's own radius, widened by
// the same accuracy allowance the check-in itself applied. This guards that
// contract, since the lecturer-facing location_unverified reason depends on
// the radius being selected, the comparison being made, and the two sides
// agreeing on when a fix is implausible rather than merely imprecise.
func TestSessionReportQueryDerivesLocationTrustFromDistanceAndRadius(t *testing.T) {
	t.Parallel()
	source, err := os.ReadFile("../../../db/queries/identity.sql")
	if err != nil {
		t.Fatal(err)
	}
	query, ok := namedQuery(string(source), "SessionReportPage")
	if !ok {
		t.Fatal("SessionReportPage query is missing from db/queries/identity.sql")
	}
	for _, fragment := range []string{
		"ls.radius_meters",
		"wc.distance_meters > si.radius_meters + least(coalesce(wc.accuracy_meters, 0), 250)",
		"AS location_unverified",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("SessionReportPage is missing %q", fragment)
		}
	}
}

// namedQuery returns the body of a sqlc "-- name: <name>" block.
func namedQuery(source, name string) (string, bool) {
	marker := "-- name: " + name + " "
	start := strings.Index(source, marker)
	if start < 0 {
		return "", false
	}
	body := source[start+len(marker):]
	if end := strings.Index(body, "\n-- name: "); end >= 0 {
		body = body[:end]
	}
	return body, true
}
