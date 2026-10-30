package checkins

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"attendance-api/internal/sessions"
)

type Session struct {
	ID        string
	EndedAt   *time.Time
	StartedAt time.Time
	// Venue is the session's snapshot of the venue centre, nil when the venue
	// had no stored coordinates when the session started.
	Venue         *VenueCentre
	RotatingCode  bool
	StaticCode    string
	CodeExpiresAt *time.Time
	LateAfter     time.Duration
	RosterPolicy  RosterPolicy
}

type Store interface {
	LoadSession(context.Context, string, string) (Session, bool, error)
	IsRegistered(context.Context, string, string, string) (bool, error)
	HasCheckin(context.Context, string, string, string) (bool, error)
	// DeviceUsedInSession reports whether this device hash has already
	// recorded a different student in this session.
	DeviceUsedInSession(context.Context, string, string, string, []byte) (bool, error)
	InsertCheckin(context.Context, CheckinRecord) (bool, error)
}

type CheckinRecord struct {
	InstitutionID string
	SessionID     string
	StudentID     string
	// Fix is nil only for records created under earlier behavior. DistanceMeters
	// is nil when the venue has no centre to measure against.
	Fix            *Fix
	DistanceMeters *float64
	DeviceHash     []byte
	OnRoster       bool
	Status         string
}

type Request struct {
	SessionID  string   `json:"session_id"`
	Code       string   `json:"code"`
	Latitude   *float64 `json:"lat"`
	Longitude  *float64 `json:"lng"`
	Accuracy   *float64 `json:"accuracy"`
	DeviceHash string   `json:"device_hash"`
}

// fix builds a position when both coordinates are present. The service rejects
// requests missing any of the coordinates or accuracy before using this value.
func (r Request) fix() *Fix {
	if r.Latitude == nil || r.Longitude == nil {
		return nil
	}
	accuracy := 0.0
	if r.Accuracy != nil {
		accuracy = *r.Accuracy
	}
	return &Fix{Latitude: *r.Latitude, Longitude: *r.Longitude, AccuracyMeters: accuracy}
}

type CheckinResult struct {
	SessionID    string `json:"session_id"`
	Status       string `json:"status"`
	OnRoster     bool   `json:"on_roster"`
	AccuracyPoor bool   `json:"accuracy_poor"`
	// DistanceMeters is null when the venue has no centre to measure against.
	DistanceMeters *float64 `json:"distance_meters"`
	// LocationUnverified tells the student their attendance was recorded on a
	// fix their phone's own error cannot explain. A fingerprint scan still
	// confirms attendance regardless of this location signal.
	LocationUnverified bool `json:"location_unverified"`
}

type Clock interface {
	Now() time.Time
}

type Service struct {
	store      Store
	clock      Clock
	codeSecret []byte
	codeWindow time.Duration
}

func NewService(store Store, clock Clock, codeSecret []byte, codeWindow time.Duration) (*Service, error) {
	if store == nil || clock == nil {
		return nil, errors.New("check-in store and clock are required")
	}
	if len(codeSecret) < 32 {
		return nil, errors.New("code HMAC secret must contain at least 32 bytes")
	}
	if codeWindow < time.Second {
		return nil, errors.New("code window must be at least one second")
	}
	return &Service{
		store: store, clock: clock, codeSecret: append([]byte(nil), codeSecret...), codeWindow: codeWindow,
	}, nil
}

func (s *Service) Checkin(ctx context.Context, institutionID, studentID string, request Request) (CheckinResult, ErrorCode, error) {
	// The device hash is the anti-proxy control now that location cannot
	// refuse a check-in, so an absent one cannot be treated as an opt-out.
	if institutionID == "" || studentID == "" || request.SessionID == "" ||
		request.DeviceHash == "" {
		return CheckinResult{}, ErrInvalidInput, nil
	}
	session, found, err := s.store.LoadSession(ctx, institutionID, request.SessionID)
	if err != nil {
		return CheckinResult{}, "", fmt.Errorf("load check-in session: %w", err)
	}
	if !found || session.EndedAt != nil {
		return CheckinResult{}, ErrSessionClosed, nil
	}
	now := s.clock.Now().UTC()
	codeValid := false
	if session.RotatingCode {
		codeValid = sessions.VerifyRotatingCode(s.codeSecret, session.ID, request.Code, now, s.codeWindow)
	} else {
		codeValid = sessions.VerifyStaticCodeUntil(session.StaticCode, request.Code, session.CodeExpiresAt, now)
	}
	if !codeValid {
		return CheckinResult{}, ErrWrongCode, nil
	}
	if request.Latitude == nil || request.Longitude == nil || request.Accuracy == nil {
		return CheckinResult{}, ErrLocationRequired, nil
	}
	registered, err := s.store.IsRegistered(ctx, institutionID, session.ID, studentID)
	if err != nil {
		return CheckinResult{}, "", fmt.Errorf("check student registration: %w", err)
	}
	already, err := s.store.HasCheckin(ctx, institutionID, session.ID, studentID)
	if err != nil {
		return CheckinResult{}, "", fmt.Errorf("check existing attendance: %w", err)
	}
	// Device binding replaces the geofence as the control on one student
	// checking in for another, so it is resolved before the decision rather
	// than after it.
	hash := sha256.Sum256([]byte(request.DeviceHash))
	deviceHash := hash[:]
	deviceUsed, err := s.store.DeviceUsedInSession(ctx, institutionID, session.ID, studentID, deviceHash)
	if err != nil {
		return CheckinResult{}, "", fmt.Errorf("check device binding: %w", err)
	}
	validation, code := Validate(Input{
		SessionOpen:    true,
		CodeValid:      true,
		OnRoster:       registered,
		RosterPolicy:   session.RosterPolicy,
		Fix:            request.fix(),
		Venue:          session.Venue,
		AlreadyChecked: already,
		DeviceUsed:     deviceUsed,
		Now:            now,
		StartedAt:      session.StartedAt,
		LateAfter:      session.LateAfter,
	})
	if code != "" {
		return CheckinResult{}, code, nil
	}
	status := "present"
	if validation.Late {
		status = "late"
	}
	inserted, err := s.store.InsertCheckin(ctx, CheckinRecord{
		InstitutionID: institutionID, SessionID: session.ID, StudentID: studentID,
		Fix: request.fix(), DistanceMeters: validation.DistanceMeters,
		DeviceHash: deviceHash, OnRoster: validation.OnRoster, Status: status,
	})
	if err != nil {
		return CheckinResult{}, "", fmt.Errorf("record web check-in: %w", err)
	}
	if !inserted {
		return CheckinResult{}, ErrAlreadyExists, nil
	}
	return CheckinResult{
		SessionID: session.ID, Status: status, OnRoster: validation.OnRoster,
		AccuracyPoor: validation.AccuracyPoor, DistanceMeters: validation.DistanceMeters,
		LocationUnverified: validation.LocationUnverified,
	}, "", nil
}
