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
			ID: "session-1", StartedAt: time.Unix(0, 0), Latitude: 0, Longitude: 0, RadiusMeters: 100,
			RotatingCode: true, RosterPolicy: checkins.RosterRegisteredOnly, LateAfter: 10 * time.Minute,
		},
		registered: true, inserted: true,
	}
	service := newCheckinService(t, store, clock)
	wrong := sessions.DeriveCode(secret, "different-session", 2)
	result, code, err := service.Checkin(context.Background(), "i1", "student-1",
		checkins.Request{SessionID: "session-1", Code: wrong, Accuracy: 5})
	if err != nil || code != checkins.ErrWrongCode || result.SessionID != "" || store.checkCalls != 0 {
		t.Fatalf("session-specific code check = %+v, %q, %v", result, code, err)
	}

	codeValue := sessions.DeriveCode(secret, "session-1", 2)
	result, code, err = service.Checkin(context.Background(), "i1", "student-1",
		checkins.Request{SessionID: "session-1", Code: codeValue, Accuracy: 101})
	if err != nil || code != "" || result.Status != "present" || !result.AccuracyPoor {
		t.Fatalf("valid check-in = %+v, %q, %v", result, code, err)
	}
	if !store.record.OnRoster || store.record.AccuracyMeters != 101 {
		t.Fatalf("check-in record did not preserve validation evidence: %+v", store.record)
	}
}

func TestCheckinClosedNotOnRosterOutsideRadiusAndRaceDuplicate(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Unix(120, 0))
	secret := []byte("0123456789abcdef0123456789abcdef")
	session := checkins.Session{
		ID: "session-1", StartedAt: time.Unix(0, 0), Latitude: 0, Longitude: 0,
		RadiusMeters: 10, RotatingCode: true, RosterPolicy: checkins.RosterRegisteredOnly,
	}
	currentCode := sessions.DeriveCode(secret, session.ID, 2)
	store := &fakeCheckinStore{found: false, session: session, inserted: true}
	service := newCheckinService(t, store, clock)
	request := checkins.Request{SessionID: session.ID, Code: currentCode}

	if _, code, _ := service.Checkin(context.Background(), "i1", "s1", request); code != checkins.ErrSessionClosed {
		t.Fatalf("closed session code = %q", code)
	}
	store.found = true
	if _, code, _ := service.Checkin(context.Background(), "i1", "s1", request); code != checkins.ErrNotOnRoster {
		t.Fatalf("roster error = %q", code)
	}
	store.registered = true
	request.Longitude = .01
	if _, code, _ := service.Checkin(context.Background(), "i1", "s1", request); code != checkins.ErrOutsideRadius {
		t.Fatalf("geofence error = %q", code)
	}
	request.Longitude = 0
	store.inserted = false
	if _, code, _ := service.Checkin(context.Background(), "i1", "s1", request); code != checkins.ErrAlreadyExists {
		t.Fatalf("race duplicate error = %q", code)
	}
}
