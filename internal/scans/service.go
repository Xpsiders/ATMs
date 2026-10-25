package scans

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

var (
	ErrNoActiveSession       = errors.New("no active session at device venue")
	ErrInvalidSlot           = errors.New("device slot is invalid")
	ErrInvalidConfidence     = errors.New("scan confidence must be between 0 and 1")
	ErrEnrolmentTaskNotFound = errors.New("enrolment task not found or already completed")
)

type Device struct {
	ID            string
	InstitutionID string
	VenueID       string
}

// DeviceStatus is the optional health telemetry a scanner reports. Any field may
// be nil when the firmware does not supply it.
type DeviceStatus struct {
	FirmwareVersion   *string
	BatteryPercent    *int
	PendingQueueDepth *int
	FreeStorageBytes  *int64
}

type ScanItem struct {
	SlotNumber      int
	Confidence      float64
	ClientScannedAt *time.Time
}

// DeviceTemplate is one roster entry a device loads into its sensor: the slot
// position to use, who it belongs to, and the base64-encoded template blob.
type DeviceTemplate struct {
	SlotNumber      int    `json:"slot_number"`
	StudentRecordID string `json:"student_record_id"`
	StudentRef      string `json:"student_ref"`
	Template        string `json:"template"`
}

// TemplateSync is the set of templates a device should hold for a session.
type TemplateSync struct {
	SessionID  string           `json:"session_id"`
	Slots      []DeviceTemplate `json:"slots"`
	ServerTime string           `json:"server_time"`
}

type ScanResult struct {
	SessionID string `json:"session_id,omitempty"`
	StudentID string `json:"student_id,omitempty"`
	Duplicate bool   `json:"duplicate"`
	ErrorCode string `json:"error_code,omitempty"`
}

type ScanStore interface {
	StudentForSlotNumber(context.Context, string, string, int) (string, bool, error)
	OpenSessionAtVenue(context.Context, string, string) (string, bool, error)
	SessionAtVenueTime(context.Context, string, string, time.Time) (string, bool, error)
	InsertFingerprintScan(context.Context, string, string, string, string, float64, *time.Time) (bool, error)
}

type TaskStore interface {
	LoadTasks(context.Context, string, string) (Tasks, error)
	CompleteEnrolment(context.Context, string, string, string, string, []byte, time.Time) error
}

type Service struct {
	store ScanStore
}

func NewService(store ScanStore) (*Service, error) {
	if store == nil {
		return nil, errors.New("scan store is required")
	}
	return &Service{store: store}, nil
}

func (s *Service) Process(ctx context.Context, device Device, item ScanItem) (ScanResult, error) {
	if item.SlotNumber < 1 {
		return ScanResult{ErrorCode: "invalid_slot"}, ErrInvalidSlot
	}
	if math.IsNaN(item.Confidence) || math.IsInf(item.Confidence, 0) ||
		item.Confidence < 0 || item.Confidence > 1 {
		return ScanResult{ErrorCode: "invalid_confidence"}, ErrInvalidConfidence
	}
	studentID, found, err := s.store.StudentForSlotNumber(ctx, device.InstitutionID, device.ID, item.SlotNumber)
	if err != nil {
		return ScanResult{}, fmt.Errorf("resolve device slot: %w", err)
	}
	if !found {
		return ScanResult{ErrorCode: "invalid_slot"}, ErrInvalidSlot
	}
	var sessionID string
	var sessionFound bool
	if item.ClientScannedAt != nil {
		sessionID, sessionFound, err = s.store.SessionAtVenueTime(ctx, device.InstitutionID, device.VenueID, item.ClientScannedAt.UTC())
	} else {
		sessionID, sessionFound, err = s.store.OpenSessionAtVenue(ctx, device.InstitutionID, device.VenueID)
	}
	if err != nil {
		return ScanResult{}, fmt.Errorf("find venue session for scan time: %w", err)
	}
	if !sessionFound {
		return ScanResult{ErrorCode: "no_active_session"}, ErrNoActiveSession
	}
	if item.ClientScannedAt != nil {
		value := item.ClientScannedAt.UTC()
		item.ClientScannedAt = &value
	}
	inserted, err := s.store.InsertFingerprintScan(ctx, device.InstitutionID, sessionID, studentID, device.ID,
		item.Confidence, item.ClientScannedAt)
	if err != nil {
		return ScanResult{}, fmt.Errorf("insert fingerprint scan: %w", err)
	}
	return ScanResult{SessionID: sessionID, StudentID: studentID, Duplicate: !inserted}, nil
}

type BatchItemResult struct {
	Index  int        `json:"index"`
	Result ScanResult `json:"result"`
}

func (s *Service) ProcessBatch(ctx context.Context, device Device, items []ScanItem) []BatchItemResult {
	results := make([]BatchItemResult, len(items))
	for index, item := range items {
		result, err := s.Process(ctx, device, item)
		if err != nil && result.ErrorCode == "" {
			result.ErrorCode = "scan_failed"
		}
		results[index] = BatchItemResult{Index: index, Result: result}
	}
	return results
}
