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
	ID            string
	EndedAt       *time.Time
	StartedAt     time.Time
	Latitude      float64
	Longitude     float64
	RadiusMeters  float64
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
	InsertCheckin(context.Context, CheckinRecord) (bool, error)
}

type CheckinRecord struct {
	InstitutionID  string
	SessionID      string
	StudentID      string
	Latitude       float64
	Longitude      float64
	AccuracyMeters float64
	DistanceMeters float64
	DeviceHash     []byte
	OnRoster       bool
	Status         string
}

type Request struct {
	SessionID  string  `json:"session_id"`
	Code       string  `json:"code"`
	Latitude   float64 `json:"lat"`
	Longitude  float64 `json:"lng"`
	Accuracy   float64 `json:"accuracy"`
	DeviceHash string  `json:"device_hash"`
}

type CheckinResult struct {
	SessionID      string  `json:"session_id"`
	Status         string  `json:"status"`
	OnRoster       bool    `json:"on_roster"`
	AccuracyPoor   bool    `json:"accuracy_poor"`
	DistanceMeters float64 `json:"distance_meters"`
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
	if institutionID == "" || studentID == "" || request.SessionID == "" {
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
	registered, err := s.store.IsRegistered(ctx, institutionID, session.ID, studentID)
	if err != nil {
		return CheckinResult{}, "", fmt.Errorf("check student registration: %w", err)
	}
	validation, code := Validate(Input{
		SessionOpen:    true,
		CodeValid:      true,
		OnRoster:       registered,
		RosterPolicy:   session.RosterPolicy,
		Latitude:       request.Latitude,
		Longitude:      request.Longitude,
		VenueLatitude:  session.Latitude,
		VenueLongitude: session.Longitude,
		RadiusMeters:   session.RadiusMeters,
		AccuracyMeters: request.Accuracy,
		Now:            now,
		StartedAt:      session.StartedAt,
		LateAfter:      session.LateAfter,
	})
	if code != "" {
		return CheckinResult{}, code, nil
	}
	already, err := s.store.HasCheckin(ctx, institutionID, session.ID, studentID)
	if err != nil {
		return CheckinResult{}, "", fmt.Errorf("check existing attendance: %w", err)
	}
	if already {
		return CheckinResult{}, ErrAlreadyExists, nil
	}
	status := "present"
	if validation.Late {
		status = "late"
	}
	deviceHash := []byte(nil)
	if request.DeviceHash != "" {
		hash := sha256.Sum256([]byte(request.DeviceHash))
		deviceHash = hash[:]
	}
	inserted, err := s.store.InsertCheckin(ctx, CheckinRecord{
		InstitutionID: institutionID, SessionID: session.ID, StudentID: studentID,
		Latitude: request.Latitude, Longitude: request.Longitude,
		AccuracyMeters: request.Accuracy, DistanceMeters: validation.DistanceMeters,
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
	}, "", nil
}
