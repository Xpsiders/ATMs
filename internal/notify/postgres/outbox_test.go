package postgres

import (
	"strings"
	"testing"
)

func TestClaimOutboxQueryTypesStaleLockTimestamp(t *testing.T) {
	if !strings.Contains(claimOutboxQuery,
		"locked_at < $1::timestamptz - interval '5 minutes'") {
		t.Fatal("outbox stale-lock comparison must type its timestamp parameter")
	}
}
