package scans_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"attendance-api/internal/scans"
)

type fakeScanStore struct {
	studentFound bool
	sessionFound bool
	inserted     bool
	insertCalls  int
	timeLookups  int
}

func (f *fakeScanStore) StudentForSlotNumber(context.Context, string, string, int) (string, bool, error) {
	return "student-1", f.studentFound, nil
}

func (f *fakeScanStore) OpenSessionAtVenue(context.Context, string, string) (string, bool, error) {
	return "session-1", f.sessionFound, nil
}

func (f *fakeScanStore) SessionAtVenueTime(_ context.Context, _, _ string, scannedAt time.Time) (string, bool, error) {
	f.timeLookups++
	if scannedAt.IsZero() {
		return "", false, errors.New("scan timestamp is required")
	}
	return "historical-session", f.sessionFound, nil
}

func (f *fakeScanStore) InsertFingerprintScan(_ context.Context, _, _, _, _ string, _ float64, _ *time.Time) (bool, error) {
	f.insertCalls++
	return f.inserted, nil
}

func newScanService(t *testing.T, store *fakeScanStore) *scans.Service {
	t.Helper()
	service, err := scans.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestScanIdempotencyAndNoActiveSession(t *testing.T) {
	t.Parallel()
	device := scans.Device{ID: "device-1", InstitutionID: "institution-1", VenueID: "venue-1"}
	store := &fakeScanStore{studentFound: true, sessionFound: true, inserted: false}
	service := newScanService(t, store)
	result, err := service.Process(context.Background(), device, scans.ScanItem{SlotNumber: 1, Confidence: .98})
	if err != nil || !result.Duplicate || result.SessionID != "session-1" || store.insertCalls != 1 {
		t.Fatalf("duplicate scan = %+v, %v", result, err)
	}
	store.sessionFound = false
	result, err = service.Process(context.Background(), device, scans.ScanItem{SlotNumber: 1, Confidence: .98})
	if !errors.Is(err, scans.ErrNoActiveSession) || result.ErrorCode != "no_active_session" {
		t.Fatalf("no-session result = %+v, %v", result, err)
	}
}

func TestLateSyncedScanUsesSessionActiveAtClientScanTime(t *testing.T) {
	t.Parallel()
	store := &fakeScanStore{studentFound: true, sessionFound: true, inserted: true}
	service := newScanService(t, store)
	scannedAt := time.Date(2026, 10, 20, 10, 0, 0, 0, time.FixedZone("test", 3600))
	result, err := service.Process(context.Background(),
		scans.Device{ID: "device-1", InstitutionID: "institution-1", VenueID: "venue-1"},
		scans.ScanItem{SlotNumber: 1, Confidence: .98, ClientScannedAt: &scannedAt})
	if err != nil || result.SessionID != "historical-session" || store.timeLookups != 1 {
		t.Fatalf("late scan result = %+v, %v; lookups=%d", result, err, store.timeLookups)
	}
}

func TestBatchHandlesMixedValidAndInvalidScans(t *testing.T) {
	t.Parallel()
	store := &fakeScanStore{studentFound: true, sessionFound: true, inserted: true}
	service := newScanService(t, store)
	results := service.ProcessBatch(context.Background(), scans.Device{ID: "d", InstitutionID: "i", VenueID: "v"}, []scans.ScanItem{
		{SlotNumber: 1, Confidence: .9},
		{SlotNumber: 0, Confidence: .9},
		{SlotNumber: 2, Confidence: 2},
	})
	if len(results) != 3 || results[0].Result.SessionID != "session-1" ||
		results[1].Result.ErrorCode != "invalid_slot" ||
		results[2].Result.ErrorCode != "invalid_confidence" || store.insertCalls != 1 {
		t.Fatalf("mixed batch results = %+v", results)
	}
}
