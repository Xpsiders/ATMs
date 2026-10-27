package sessions_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"attendance-api/internal/sessions"
	"attendance-api/internal/testutil"
)

type memorySessionStore struct {
	sessions           map[string]sessions.Session
	open               map[string]string
	nextID             string
	audits             int
	scannerUnavailable bool
	liveClasses        []sessions.LiveClass
	recentred          []sessions.Centre
}

func newMemorySessionStore() *memorySessionStore {
	return &memorySessionStore{sessions: make(map[string]sessions.Session), open: make(map[string]string), nextID: "session-1"}
}

func (s *memorySessionStore) Create(_ context.Context, input sessions.StartInput, startedAt time.Time, staticCode string, expiresAt *time.Time) (sessions.Session, error) {
	if openID := s.open[input.VenueID]; openID != "" {
		return sessions.Session{}, &sessions.OpenVenueError{SessionID: openID}
	}
	session := sessions.Session{
		ID: s.nextID, InstitutionID: input.InstitutionID, OfferingID: input.OfferingID,
		VenueID: input.VenueID, StartedAt: startedAt, Latitude: input.Latitude,
		Longitude: input.Longitude, RadiusMeters: input.RadiusMeters,
		RotatingCode: input.RotatingCode, StaticCode: staticCode, CodeExpiresAt: expiresAt,
		LateAfter: input.LateAfter,
	}
	s.sessions[session.ID] = session
	s.open[input.VenueID] = session.ID
	s.nextID = "session-2"
	return session, nil
}

func (s *memorySessionStore) Get(_ context.Context, institutionID, sessionID string) (sessions.Session, bool, error) {
	session, ok := s.sessions[sessionID]
	return session, ok && session.InstitutionID == institutionID, nil
}

func (s *memorySessionStore) End(_ context.Context, institutionID, sessionID string, endedAt time.Time) (sessions.Session, error) {
	session, ok := s.sessions[sessionID]
	if !ok || session.InstitutionID != institutionID {
		return sessions.Session{}, sessions.ErrSessionNotFound
	}
	if session.EndedAt != nil {
		return sessions.Session{}, sessions.ErrSessionEnded
	}
	session.EndedAt = &endedAt
	s.sessions[sessionID] = session
	delete(s.open, session.VenueID)
	return session, nil
}

func (s *memorySessionStore) ListLiveClasses(context.Context, string, string, string) ([]sessions.LiveClass, error) {
	return s.liveClasses, nil
}

func (s *memorySessionStore) ConvertToFixedCode(_ context.Context, institutionID, sessionID, _ string, code string, _ time.Time) (sessions.Session, error) {
	session, ok := s.sessions[sessionID]
	if !ok || session.InstitutionID != institutionID {
		return sessions.Session{}, sessions.ErrSessionNotFound
	}
	if session.EndedAt != nil {
		return sessions.Session{}, sessions.ErrSessionEnded
	}
	if !session.RotatingCode {
		return sessions.Session{}, sessions.ErrCodeAlreadyFixed
	}
	session.RotatingCode = false
	session.StaticCode = code
	session.CodeExpiresAt = nil
	s.sessions[sessionID] = session
	s.audits++
	return session, nil
}

func (s *memorySessionStore) MarkScannerUnavailable(_ context.Context, institutionID, sessionID, _ string, note string, _ time.Time) error {
	session, ok := s.sessions[sessionID]
	if !ok || session.InstitutionID != institutionID {
		return sessions.ErrSessionNotFound
	}
	if session.EndedAt != nil {
		return sessions.ErrSessionEnded
	}
	if note == "" {
		return errors.New("note required")
	}
	s.scannerUnavailable = true
	s.audits++
	return nil
}

func (s *memorySessionStore) RecentreSession(_ context.Context, institutionID, sessionID, actorUserID string,
	centre sessions.Centre, _ sessions.DeviceFix, _ time.Time,
) error {
	session, ok := s.sessions[sessionID]
	if !ok || session.InstitutionID != institutionID {
		return sessions.ErrSessionNotFound
	}
	if session.EndedAt != nil {
		return sessions.ErrSessionEnded
	}
	if actorUserID == "" {
		return errors.New("actor required")
	}
	latitude, longitude, radius := centre.Latitude, centre.Longitude, centre.RadiusMeters
	session.Latitude, session.Longitude, session.RadiusMeters = &latitude, &longitude, &radius
	s.sessions[sessionID] = session
	s.recentred = append(s.recentred, centre)
	s.audits++
	return nil
}

func newSessionService(t *testing.T, store *memorySessionStore, clock *testutil.FakeClock) *sessions.Service {
	t.Helper()
	service, err := sessions.NewService(store, clock, []byte("0123456789abcdef0123456789abcdef"),
		time.Minute, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestSessionStartCodeAndVenueConflict(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Unix(120, 0))
	store := newMemorySessionStore()
	service := newSessionService(t, store, clock)
	input := sessions.StartInput{
		InstitutionID: "i1", OfferingID: "o1", VenueID: "v1", StartedBy: "lecturer",
		Latitude: coord(1.2), Longitude: coord(3.4), RadiusMeters: coord(50), RotatingCode: true,
	}
	started, err := service.Start(context.Background(), input)
	if err != nil || len(started.Code) != 6 || started.Session.StartedAt.Location() != time.UTC {
		t.Fatalf("rotating start = %+v, %v", started, err)
	}
	_, err = service.Start(context.Background(), input)
	var venueErr *sessions.OpenVenueError
	if !errors.As(err, &venueErr) || venueErr.SessionID != started.Session.ID {
		t.Fatalf("venue conflict = %v", err)
	}
}

func TestLiveClassesUsesStoreAndReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	store := newMemorySessionStore()
	service := newSessionService(t, store, testutil.NewFakeClock(time.Unix(120, 0)))
	items, err := service.LiveClasses(context.Background(), "institution-1", "student-user", "student-record")
	if err != nil || items == nil || len(items) != 0 {
		t.Fatalf("empty live classes = %#v, %v; want non-nil empty slice", items, err)
	}
	store.liveClasses = []sessions.LiveClass{{
		SessionID: "session-1", OfferingID: "offering-1", CourseCode: "ECE301",
		CourseTitle: "Circuits and Systems", VenueID: "venue-1",
		VenueName: "Engineering Hall", LecturerName: "Lecturer A",
		StartedAt: time.Unix(120, 0).UTC(), LateAfterMin: 10,
		RotatingCode: true, AlreadyCheckedIn: false,
	}}
	items, err = service.LiveClasses(context.Background(), "institution-1", "student-user", "student-record")
	if err != nil || len(items) != 1 || items[0].CourseCode != "ECE301" ||
		items[0].VenueName != "Engineering Hall" || items[0].LecturerName != "Lecturer A" ||
		items[0].LateAfterMin != 10 || !items[0].RotatingCode || items[0].AlreadyCheckedIn {
		t.Fatalf("live classes = %#v, %v", items, err)
	}
}

func TestStaticSessionCodeExpiryAndEndLock(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Unix(1_700_000_000, 0))
	service := newSessionService(t, newMemorySessionStore(), clock)
	if err := service.SetRandomSource(&sequenceReader{}); err != nil {
		t.Fatal(err)
	}
	input := sessions.StartInput{
		InstitutionID: "i1", OfferingID: "o1", VenueID: "v1", StartedBy: "u1",
		Latitude: coord(1), Longitude: coord(2), RadiusMeters: coord(50),
	}
	started, err := service.Start(context.Background(), input)
	if err != nil || len(started.Code) != 6 {
		t.Fatalf("static start = %+v, %v", started, err)
	}
	code, err := service.Code(context.Background(), "i1", started.Session.ID)
	if err != nil || code.Code != started.Code {
		t.Fatalf("static code = %+v, %v", code, err)
	}
	clock.Advance(6 * time.Minute)
	if _, err := service.Code(context.Background(), "i1", started.Session.ID); !errors.Is(err, sessions.ErrCodeExpired) {
		t.Fatalf("expired static code error = %v", err)
	}
	if _, err := service.End(context.Background(), "i1", started.Session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Code(context.Background(), "i1", started.Session.ID); !errors.Is(err, sessions.ErrSessionEnded) {
		t.Fatalf("ended session code error = %v", err)
	}
	if _, err := service.Code(context.Background(), "i1", started.Session.ID); !errors.Is(err, sessions.ErrSessionEnded) {
		t.Fatalf("ended session returned code after expiry: %v", err)
	}
}

func TestRotatingSessionCanConvertToFixedCodeOnlyOnceAndAudits(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Unix(1_700_000_000, 0))
	store := newMemorySessionStore()
	service := newSessionService(t, store, clock)
	input := sessions.StartInput{
		InstitutionID: "i1", OfferingID: "o1", VenueID: "v1", StartedBy: "lecturer",
		Latitude: coord(1), Longitude: coord(2), RadiusMeters: coord(50), RotatingCode: true,
	}
	started, err := service.Start(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetRandomSource(&sequenceReader{}); err != nil {
		t.Fatal(err)
	}
	fixed, err := service.ConvertToFixedCode(context.Background(), "i1", started.Session.ID, "lecturer")
	if err != nil || fixed.Session.RotatingCode || len(fixed.Code) != 6 || fixed.Code != fixed.Session.StaticCode {
		t.Fatalf("fixed conversion = %+v, %v", fixed, err)
	}
	if fixed.Session.CodeExpiresAt != nil || store.audits != 1 {
		t.Fatalf("fixed code expiry/audit = %v/%d", fixed.Session.CodeExpiresAt, store.audits)
	}
	clock.Advance(24 * time.Hour)
	current, err := service.Code(context.Background(), "i1", started.Session.ID)
	if err != nil || current.Code != fixed.Code {
		t.Fatalf("fixed code did not persist until session end: %+v, %v", current, err)
	}
	if _, err := service.ConvertToFixedCode(context.Background(), "i1", started.Session.ID, "lecturer"); !errors.Is(err, sessions.ErrCodeAlreadyFixed) {
		t.Fatalf("second conversion error = %v", err)
	}
	if store.audits != 1 {
		t.Fatalf("second conversion created audit; count=%d", store.audits)
	}
}

func TestScannerUnavailableStatusIsAuditedAndRequiresOpenSession(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Unix(1_700_000_000, 0))
	store := newMemorySessionStore()
	service := newSessionService(t, store, clock)
	started, err := service.Start(context.Background(), sessions.StartInput{
		InstitutionID: "i1", OfferingID: "o1", VenueID: "v1", StartedBy: "lecturer",
		Latitude: coord(1), Longitude: coord(2), RadiusMeters: coord(50), RotatingCode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.MarkScannerUnavailable(context.Background(), "i1", started.Session.ID, "lecturer", "power loss"); err != nil {
		t.Fatal(err)
	}
	if !store.scannerUnavailable || store.audits != 1 {
		t.Fatalf("scanner status/audit = %v/%d", store.scannerUnavailable, store.audits)
	}
	if _, err := service.End(context.Background(), "i1", started.Session.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.MarkScannerUnavailable(context.Background(), "i1", started.Session.ID, "lecturer", "power loss"); !errors.Is(err, sessions.ErrSessionEnded) {
		t.Fatalf("closed-session update error = %v", err)
	}
}

type sequenceReader struct{ value byte }

func (r *sequenceReader) Read(data []byte) (int, error) {
	for index := range data {
		data[index] = r.value
		r.value++
	}
	return len(data), nil
}

func coord(value float64) *float64 { return &value }

// startLocatedSession opens a session for the location tests. A nil centre
// stands for a venue that has never been calibrated.
func startLocatedSession(t *testing.T, service *sessions.Service, centre *sessions.Centre) sessions.Started {
	t.Helper()
	input := sessions.StartInput{
		InstitutionID: "i1", OfferingID: "o1", VenueID: "v1", StartedBy: "lecturer",
		RotatingCode: true,
	}
	if centre != nil {
		input.Latitude, input.Longitude = coord(centre.Latitude), coord(centre.Longitude)
		input.RadiusMeters = coord(centre.RadiusMeters)
	}
	started, err := service.Start(context.Background(), input)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	return started
}

func TestUpdateLocationRefinesCentreAndKeepsRadius(t *testing.T) {
	t.Parallel()
	store := newMemorySessionStore()
	service := newSessionService(t, store, testutil.NewFakeClock(time.Unix(120, 0)))
	started := startLocatedSession(t, service, &sessions.Centre{
		Latitude: venueLatitude, Longitude: venueLongitude, RadiusMeters: 75,
	})
	result, err := service.UpdateLocation(context.Background(), "i1", started.Session.ID, "lecturer",
		deviceFix(venueLatitude, offsetLongitude(40), 8))
	if err != nil {
		t.Fatalf("update location: %v", err)
	}
	if result.Outcome != sessions.LocationRefined {
		t.Fatalf("outcome = %q, want refined", result.Outcome)
	}
	if len(store.recentred) != 1 {
		t.Fatalf("recentre writes = %d, want 1", len(store.recentred))
	}
	if store.recentred[0].RadiusMeters != 75 {
		t.Fatalf("radius = %v, want the session's own 75 m kept; a device fix says nothing about room size",
			store.recentred[0].RadiusMeters)
	}
	if result.Latitude == nil || *result.Longitude != offsetLongitude(40) {
		t.Fatalf("response geofence = %+v, want the new centre", result)
	}
	if result.MovedMeters < 30 || result.MovedMeters > 50 {
		t.Fatalf("moved = %.1f m, want roughly 40", result.MovedMeters)
	}
}

func TestUpdateLocationEstablishesFirstCentreForUncalibratedVenue(t *testing.T) {
	t.Parallel()
	store := newMemorySessionStore()
	service := newSessionService(t, store, testutil.NewFakeClock(time.Unix(120, 0)))
	started := startLocatedSession(t, service, nil)
	if started.LocationSource != sessions.LocationNone {
		t.Fatalf("location source = %q, want none", started.LocationSource)
	}
	result, err := service.UpdateLocation(context.Background(), "i1", started.Session.ID, "lecturer",
		deviceFix(venueLatitude, venueLongitude, 8))
	if err != nil {
		t.Fatalf("update location: %v", err)
	}
	if result.Outcome != sessions.LocationEstablished {
		t.Fatalf("outcome = %q, want established", result.Outcome)
	}
	if len(store.recentred) != 1 || store.recentred[0].RadiusMeters != sessions.RadiusForNewCentre(8) {
		t.Fatalf("recentre writes = %+v, want one with a derived radius", store.recentred)
	}
	if result.RadiusMeters == nil || *result.RadiusMeters != sessions.RadiusForNewCentre(8) {
		t.Fatalf("response radius = %+v, want the derived radius", result.RadiusMeters)
	}
}

func TestUpdateLocationLeavesGeofenceAloneOnDriftAndVagueFixes(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		centre  *sessions.Centre
		fix     sessions.DeviceFix
		outcome sessions.LocationOutcome
	}{
		"lecturer stepped out of the room": {
			centre:  &sessions.Centre{Latitude: venueLatitude, Longitude: venueLongitude, RadiusMeters: 75},
			fix:     deviceFix(venueLatitude, offsetLongitude(400), 8),
			outcome: sessions.LocationRejectedDistance,
		},
		"fix too vague to trust": {
			centre:  &sessions.Centre{Latitude: venueLatitude, Longitude: venueLongitude, RadiusMeters: 75},
			fix:     deviceFix(venueLatitude, offsetLongitude(20), sessions.MaxDeviceFixAccuracyMeters+1),
			outcome: sessions.LocationRejectedAccuracy,
		},
		"vague fix cannot establish a missing centre": {
			centre:  nil,
			fix:     deviceFix(venueLatitude, venueLongitude, sessions.MaxDeviceFixAccuracyMeters+1),
			outcome: sessions.LocationRejectedAccuracy,
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := newMemorySessionStore()
			service := newSessionService(t, store, testutil.NewFakeClock(time.Unix(120, 0)))
			started := startLocatedSession(t, service, testCase.centre)
			result, err := service.UpdateLocation(context.Background(), "i1", started.Session.ID,
				"lecturer", testCase.fix)
			if err != nil {
				t.Fatalf("update location: %v", err)
			}
			if result.Outcome != testCase.outcome {
				t.Fatalf("outcome = %q, want %q", result.Outcome, testCase.outcome)
			}
			if len(store.recentred) != 0 {
				t.Fatalf("recentre writes = %d, want none", len(store.recentred))
			}
			if testCase.centre == nil && result.Latitude != nil {
				t.Fatalf("response latitude = %v, want null while the venue has no centre", *result.Latitude)
			}
			if testCase.centre != nil && *result.Latitude != testCase.centre.Latitude {
				t.Fatalf("response latitude = %v, want the unchanged centre", *result.Latitude)
			}
		})
	}
}

func TestUpdateLocationRejectsClosedAndUnknownSessions(t *testing.T) {
	t.Parallel()
	store := newMemorySessionStore()
	clock := testutil.NewFakeClock(time.Unix(120, 0))
	service := newSessionService(t, store, clock)
	started := startLocatedSession(t, service, &sessions.Centre{
		Latitude: venueLatitude, Longitude: venueLongitude, RadiusMeters: 75,
	})
	fix := deviceFix(venueLatitude, offsetLongitude(40), 8)
	if _, err := service.UpdateLocation(context.Background(), "i1", "missing-session", "lecturer", fix); !errors.Is(err, sessions.ErrSessionNotFound) {
		t.Fatalf("unknown session = %v, want ErrSessionNotFound", err)
	}
	if _, err := service.End(context.Background(), "i1", started.Session.ID); err != nil {
		t.Fatalf("end session: %v", err)
	}
	if _, err := service.UpdateLocation(context.Background(), "i1", started.Session.ID, "lecturer", fix); !errors.Is(err, sessions.ErrSessionEnded) {
		t.Fatalf("closed session = %v, want ErrSessionEnded", err)
	}
}
