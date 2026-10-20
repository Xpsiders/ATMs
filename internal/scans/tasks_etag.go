package scans

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

type Tasks struct {
	ActiveSession    *ActiveSession `json:"active_session"`
	PendingEnrolment *Enrolment     `json:"pending_enrolment"`
	ServerTime       string         `json:"server_time"`
}

type ActiveSession struct {
	ID      string `json:"id"`
	VenueID string `json:"venue_id"`
}

type Enrolment struct {
	ID         string `json:"id"`
	StudentRef string `json:"student_ref"`
	SlotID     string `json:"slot_id"`
}

func TasksETag(tasks Tasks) (string, error) {
	payload := struct {
		ActiveSession    *ActiveSession `json:"active_session"`
		PendingEnrolment *Enrolment     `json:"pending_enrolment"`
	}{tasks.ActiveSession, tasks.PendingEnrolment}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode device tasks for ETag: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return `"` + hex.EncodeToString(sum[:]) + `"`, nil
}
