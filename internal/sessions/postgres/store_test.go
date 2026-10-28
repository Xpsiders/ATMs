package postgres

import (
	"encoding/json"
	"testing"
)

func TestFixedCodeAuditDetailsAreValidJSON(t *testing.T) {
	t.Parallel()

	details, err := fixedCodeAuditDetails()
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(details)) {
		t.Fatalf("fixed-code audit details are invalid JSON: %q", details)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(details), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["mode"] != "fixed" {
		t.Fatalf("fixed-code audit payload = %v, want mode=fixed", payload)
	}
}
