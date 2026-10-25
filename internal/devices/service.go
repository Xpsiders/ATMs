package devices

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"attendance-api/internal/auth"
	"attendance-api/internal/db"
	"attendance-api/internal/rbac"
)

var (
	// ErrDeviceScopeDenied is returned when the caller holds no administrator
	// authority over the device's venue department.
	ErrDeviceScopeDenied = errors.New("caller cannot manage this device")
	// ErrInvalidDeviceRequest is returned for malformed provisioning input.
	ErrInvalidDeviceRequest = errors.New("device request is invalid")
	// ErrDeviceNotFound is returned when the device does not exist in the caller's
	// institution (or has been revoked).
	ErrDeviceNotFound = errors.New("device not found")
	// ErrPairingCodeInvalid is returned when a pairing code is unknown, expired, or
	// already consumed. It is deliberately generic to avoid code enumeration.
	ErrPairingCodeInvalid = errors.New("pairing code is invalid or expired")
	// ErrStudentNotFound is returned when an enrolment target is not a student
	// record in the caller's institution.
	ErrStudentNotFound = errors.New("student record not found")
)

// pairingCodeTTL is how long a freshly issued pairing code remains redeemable.
// It is generous because the code is flashed into a firmware image that may sit
// on a bench for days before the scanner is powered on.
const pairingCodeTTL = 14 * 24 * time.Hour

// Clock supplies the current time.
type Clock interface {
	Now() time.Time
}

// Device is an administrator-visible scanner summary.
type Device struct {
	ID                   string     `json:"id"`
	Name                 string     `json:"name"`
	VenueID              string     `json:"venue_id"`
	VenueName            string     `json:"venue_name"`
	DepartmentID         *string    `json:"department_id"`
	Status               string     `json:"status"`
	LastSeenAt           *time.Time `json:"last_seen_at"`
	PairedAt             *time.Time `json:"paired_at"`
	FirmwareVersion      *string    `json:"firmware_version"`
	BatteryPercent       *int       `json:"battery_percent"`
	PendingQueueDepth    *int       `json:"pending_queue_depth"`
	StatusReportedAt     *time.Time `json:"status_reported_at"`
	PairingCodeExpiresAt *time.Time `json:"pairing_code_expires_at"`
}

// Device status values derived from key/disable state.
const (
	StatusUnpaired = "unpaired"
	StatusActive   = "active"
	StatusRevoked  = "revoked"
)

// ProvisionInput is the request to register a new scanner.
type ProvisionInput struct {
	VenueID string
	Name    string
}

// PairingCode is a one-time code shown to the admin exactly once. The hash is
// what the store persists; the plaintext Code never leaves this process in
// storage and is omitted from any stored representation.
type PairingCode struct {
	DeviceID  string    `json:"device_id"`
	Code      string    `json:"pairing_code"`
	ExpiresAt time.Time `json:"pairing_code_expires_at"`
	hash      []byte
}

// Provisioned is the result of creating a scanner.
type Provisioned struct {
	Device  Device      `json:"device"`
	Pairing PairingCode `json:"pairing"`
}

// Enrolment is the result of queuing a student for fingerprint enrolment.
type Enrolment struct {
	TaskID     string `json:"task_id"`
	SlotNumber int    `json:"slot_number"`
	Status     string `json:"status"`
}

// Paired is returned to a scanner after it redeems a pairing code.
type Paired struct {
	DeviceKey     string
	DeviceID      string
	InstitutionID string
	VenueID       string
}

// ListFilter carries pagination for the scanner directory.
type ListFilter struct {
	Limit  int
	Offset int
}

// Repository performs the scoped, audited persistence for device management.
// Every method except Pair enforces that the actor administers the device's
// venue department and returns ErrDeviceScopeDenied otherwise.
type Repository interface {
	Provision(ctx context.Context, actor auth.Identity, input ProvisionInput, code PairingCode, now time.Time) (Device, error)
	List(ctx context.Context, actor auth.Identity, filter ListFilter) ([]Device, error)
	IssuePairingCode(ctx context.Context, actor auth.Identity, deviceID string, code PairingCode, now time.Time, rotate bool) error
	Revoke(ctx context.Context, actor auth.Identity, deviceID string, now time.Time) error
	EnqueueEnrolment(ctx context.Context, actor auth.Identity, deviceID, studentRecordID string, now time.Time) (Enrolment, error)
	Pair(ctx context.Context, codeHash []byte, now time.Time) (Paired, error)
}

// Service validates input and delegates to the Repository.
type Service struct {
	repository Repository
	clock      Clock
}

// NewService validates dependencies and builds the service.
func NewService(repository Repository, clock Clock) (*Service, error) {
	if repository == nil || clock == nil {
		return nil, errors.New("device repository and clock are required")
	}
	return &Service{repository: repository, clock: clock}, nil
}

// Provision registers a scanner for a venue and issues its first pairing code.
func (s *Service) Provision(ctx context.Context, actor auth.Identity, input ProvisionInput) (Provisioned, error) {
	if actor.InstitutionID == "" || actor.UserID == "" {
		return Provisioned{}, ErrInvalidDeviceRequest
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 120 {
		return Provisioned{}, ErrInvalidDeviceRequest
	}
	if _, err := db.ParseUUID(input.VenueID); err != nil {
		return Provisioned{}, ErrInvalidDeviceRequest
	}
	code, err := s.newPairingCode("")
	if err != nil {
		return Provisioned{}, err
	}
	now := s.clock.Now().UTC()
	code.ExpiresAt = now.Add(pairingCodeTTL)
	device, err := s.repository.Provision(ctx, actor, input, code, now)
	if err != nil {
		return Provisioned{}, err
	}
	code.DeviceID = device.ID
	return Provisioned{Device: device, Pairing: code}, nil
}

// List returns the caller's scoped scanner page plus a has-more flag.
func (s *Service) List(ctx context.Context, actor auth.Identity, filter ListFilter) ([]Device, bool, error) {
	if actor.InstitutionID == "" || actor.UserID == "" {
		return nil, false, ErrInvalidDeviceRequest
	}
	if filter.Limit < 1 || filter.Limit > 200 || filter.Offset < 0 {
		return nil, false, ErrInvalidDeviceRequest
	}
	devices, err := s.repository.List(ctx, actor, ListFilter{Limit: filter.Limit + 1, Offset: filter.Offset})
	if err != nil {
		return nil, false, err
	}
	hasMore := len(devices) > filter.Limit
	if hasMore {
		devices = devices[:filter.Limit]
	}
	return devices, hasMore, nil
}

// RegeneratePairingCode issues a fresh pairing code for a device, replacing any
// outstanding code without touching an already-paired key.
func (s *Service) RegeneratePairingCode(ctx context.Context, actor auth.Identity, deviceID string) (PairingCode, error) {
	return s.issue(ctx, actor, deviceID, false)
}

// RotateKey invalidates a scanner's current key and issues a new pairing code so
// the device must re-pair to resume sending data.
func (s *Service) RotateKey(ctx context.Context, actor auth.Identity, deviceID string) (PairingCode, error) {
	return s.issue(ctx, actor, deviceID, true)
}

func (s *Service) issue(ctx context.Context, actor auth.Identity, deviceID string, rotate bool) (PairingCode, error) {
	if actor.InstitutionID == "" || actor.UserID == "" {
		return PairingCode{}, ErrInvalidDeviceRequest
	}
	if _, err := db.ParseUUID(deviceID); err != nil {
		return PairingCode{}, ErrDeviceNotFound
	}
	code, err := s.newPairingCode(deviceID)
	if err != nil {
		return PairingCode{}, err
	}
	now := s.clock.Now().UTC()
	code.ExpiresAt = now.Add(pairingCodeTTL)
	if err := s.repository.IssuePairingCode(ctx, actor, deviceID, code, now, rotate); err != nil {
		return PairingCode{}, err
	}
	return code, nil
}

// Revoke soft-disables a scanner so its key stops working immediately.
func (s *Service) Revoke(ctx context.Context, actor auth.Identity, deviceID string) error {
	if actor.InstitutionID == "" || actor.UserID == "" {
		return ErrInvalidDeviceRequest
	}
	if _, err := db.ParseUUID(deviceID); err != nil {
		return ErrDeviceNotFound
	}
	return s.repository.Revoke(ctx, actor, deviceID, s.clock.Now().UTC())
}

// EnqueueEnrolment queues a student for fingerprint enrolment on a scanner.
func (s *Service) EnqueueEnrolment(ctx context.Context, actor auth.Identity, deviceID, studentRecordID string) (Enrolment, error) {
	if actor.InstitutionID == "" || actor.UserID == "" {
		return Enrolment{}, ErrInvalidDeviceRequest
	}
	if _, err := db.ParseUUID(deviceID); err != nil {
		return Enrolment{}, ErrDeviceNotFound
	}
	if _, err := db.ParseUUID(studentRecordID); err != nil {
		return Enrolment{}, ErrInvalidDeviceRequest
	}
	return s.repository.EnqueueEnrolment(ctx, actor, deviceID, studentRecordID, s.clock.Now().UTC())
}

// Pair redeems a pairing code for a permanent device key.
func (s *Service) Pair(ctx context.Context, code string) (Paired, error) {
	canonical := NormalizePairingCode(code)
	if len(canonical) != pairingCodeLength {
		return Paired{}, ErrPairingCodeInvalid
	}
	return s.repository.Pair(ctx, HashPairingCode(canonical), s.clock.Now().UTC())
}

func (s *Service) newPairingCode(deviceID string) (PairingCode, error) {
	display, hash, err := GeneratePairingCode(nil)
	if err != nil {
		return PairingCode{}, fmt.Errorf("issue pairing code: %w", err)
	}
	return PairingCode{DeviceID: deviceID, Code: display, hash: hash}, nil
}

// scopeAllows reports whether any of the caller's role assignments grants write
// authority over the device's venue scope target. It is the single source of
// truth for device-management authorization and is shared by the Postgres store.
func scopeAllows(assignments []rbac.Assignment, target rbac.Target) bool {
	for _, assignment := range assignments {
		if rbac.CanAccess(assignment, target, "", "write") {
			return true
		}
	}
	return false
}

// DeriveStatus maps a device's key and disable state to a status string.
func DeriveStatus(hasKey, disabled bool) string {
	switch {
	case disabled:
		return StatusRevoked
	case hasKey:
		return StatusActive
	default:
		return StatusUnpaired
	}
}
