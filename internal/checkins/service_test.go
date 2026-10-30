package checkins_test

import (
	"context"
	"testing"
	"time"

	"attendance-api/internal/checkins"
	"attendance-api/internal/sessions"
	"attendance-api/internal/testutil"
)

type fakeCheckinStore struct {
	session    checkins.Session
	found      bool
	registered bool
	checked    bool
	deviceUsed bool
	inserted   bool
	record     checkins.CheckinRecord
	checkCalls int
}

func (s *fakeCheckinStore) LoadSession(_ context.Context, _, _ string) (checkins.Session, bool, error) {
	return s.session, s.found, nil
}

func (s *fakeCheckinStore) IsRegistered(context.Context, string, string, string) (bool, error) {
	return s.registered, nil
}

func (s *fakeCheckinStore) HasCheckin(context.Context, string, string, string) (bool, error) {
	s.checkCalls++
	return s.checked, nil
}

func (s *fakeCheckinStore) DeviceUsedInSession(context.Context, string, string, string, []byte) (bool, error) {
	return s.deviceUsed, nil
}

func (s *fakeCheckinStore) InsertCheckin(_ context.Context, record checkins.CheckinRecord) (bool, error) {
	s.record = record
	return s.inserted, nil
}

func newCheckinService(t *testing.T, store *fakeCheckinStore, clock *testutil.FakeClock) *checkins.Service {
	t.Helper()
	service, err := checkins.NewService(store, clock, []byte("0123456789abcdef0123456789abcdef"), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestCheckinSessionScopedCodeAndValidationOrder(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Unix(120, 0))
	secret := []byte("0123456789abcdef0123456789abcdef")
	store := &fakeCheckinStore{
		found: true,
		session: checkins.Session{
			ID: "session-1", StartedAt: time.Unix(0, 0),
			Venue:        &checkins.VenueCentre{Latitude: 0, Longitude: 0, RadiusMeters: 100},
			RotatingCode: true, RosterPolicy: checkins.RosterRegisteredOnly, LateAfter: 10 * time.Minute,
		},
		registered: true, inserted: true,
	}
	service := newCheckinService(t, store, clock)
	wrong := sessions.DeriveCode(secret, "different-session", 2)
	result, code, err := service.Checkin(context.Background(), "i1", "student-1",
		checkins.Request{SessionID: "session-1", Code: wrong, Accuracy: ptr(5), DeviceHash: "device-aaaa"})
	if err != nil || code != checkins.ErrWrongCode || result.SessionID != "" || store.checkCalls != 0 {
		t.Fatalf("session-specific code check = %+v, %q, %v", result, code, err)
	}

	codeValue := sessions.DeriveCode(secret, "session-1", 2)
	result, code, err = service.Checkin(context.Background(), "i1", "student-1",
		checkins.Request{SessionID: "session-1", Code: codeValue, Latitude: ptr(0), Longitude: ptr(0),
			Accuracy: ptr(101), DeviceHash: "device-aaaa"})
	if err != nil || code != "" || result.Status != "present" || !result.AccuracyPoor {
		t.Fatalf("valid check-in = %+v, %q, %v", result, code, err)
	}
	if !store.record.OnRoster || store.record.Fix == nil || store.record.Fix.AccuracyMeters != 101 {
		t.Fatalf("check-in record did not preserve validation evidence: %+v", store.record)
	}
}

func TestCheckinClosedNotOnRosterDeviceReuseAndRaceDuplicate(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Unix(120, 0))
	secret := []byte("0123456789abcdef0123456789abcdef")
	session := checkins.Session{
		ID: "session-1", StartedAt: time.Unix(0, 0),
		Venue:        &checkins.VenueCentre{Latitude: 0, Longitude: 0, RadiusMeters: 10},
		RotatingCode: true, RosterPolicy: checkins.RosterRegisteredOnly,
	}
	currentCode := sessions.DeriveCode(secret, session.ID, 2)
	store := &fakeCheckinStore{found: false, session: session, inserted: true}
	service := newCheckinService(t, store, clock)
	request := checkins.Request{SessionID: session.ID, Code: currentCode, Latitude: ptr(0),
		Longitude: ptr(0), Accuracy: ptr(5), DeviceHash: "device-aaaa"}

	if _, code, _ := service.Checkin(context.Background(), "i1", "s1", request); code != checkins.ErrSessionClosed {
		t.Fatalf("closed session code = %q", code)
	}
	store.found = true
	if _, code, _ := service.Checkin(context.Background(), "i1", "s1", request); code != checkins.ErrNotOnRoster {
		t.Fatalf("roster error = %q", code)
	}
	store.registered = true

	// A position far outside the radius is recorded and flagged, never refused.
	far := request
	far.Latitude, far.Longitude, far.Accuracy = ptr(0), ptr(.01), ptr(5)
	result, code, err := service.Checkin(context.Background(), "i1", "s1", far)
	if err != nil || code != "" || !result.LocationUnverified {
		t.Fatalf("distant fix should be recorded and flagged: %+v, %q, %v", result, code, err)
	}

	// A device already bound to another student in this session is refused.
	store.deviceUsed = true
	if _, code, _ := service.Checkin(context.Background(), "i1", "s1", request); code != checkins.ErrDeviceAlreadyUsed {
		t.Fatalf("device reuse error = %q", code)
	}
	store.deviceUsed = false

	// The device hash is the gate, so an absent one is rejected outright.
	noDevice := request
	noDevice.DeviceHash = ""
	if _, code, _ := service.Checkin(context.Background(), "i1", "s1", noDevice); code != checkins.ErrInvalidInput {
		t.Fatalf("missing device hash error = %q", code)
	}

	for _, missingFix := range []struct {
		name string
		edit func(*checkins.Request)
	}{
		{name: "latitude", edit: func(request *checkins.Request) { request.Latitude = nil }},
		{name: "longitude", edit: func(request *checkins.Request) { request.Longitude = nil }},
		{name: "accuracy", edit: func(request *checkins.Request) { request.Accuracy = nil }},
	} {
		t.Run("missing "+missingFix.name, func(t *testing.T) {
			incomplete := request
			missingFix.edit(&incomplete)
			if _, code, _ := service.Checkin(context.Background(), "i1", "s1", incomplete); code != checkins.ErrLocationRequired {
				t.Fatalf("missing %s error = %q", missingFix.name, code)
			}
		})
	}

	store.inserted = false
	if _, code, _ := service.Checkin(context.Background(), "i1", "s1", request); code != checkins.ErrAlreadyExists {
		t.Fatalf("race duplicate error = %q", code)
	}
}

func ptr(value float64) *float64 { return &value }
