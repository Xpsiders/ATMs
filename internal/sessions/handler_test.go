package sessions_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"attendance-api/internal/auth"
	"attendance-api/internal/sessions"
	sessionhttp "attendance-api/internal/sessions/httpapi"
	"attendance-api/internal/testutil"

	"github.com/go-chi/chi/v5"
)

const testSessionID = "2f4e4ed0-490c-4cba-9343-62b0744d6ba7"

type handlerSessionStore struct {
	session     sessions.Session
	liveClasses []sessions.LiveClass
	// started keeps the input the handler resolved, so a test can assert what
	// the handler decided about the lecturer's fix and not only what it
	// returned.
	started   sessions.StartInput
	recentred []sessions.Centre
}

func (s *handlerSessionStore) Create(_ context.Context, input sessions.StartInput, startedAt time.Time, staticCode string, expiresAt *time.Time) (sessions.Session, error) {
	s.started = input
	s.session = sessions.Session{
		ID: testSessionID, InstitutionID: input.InstitutionID, OfferingID: input.OfferingID,
		VenueID: input.VenueID, StartedAt: startedAt, Latitude: input.Latitude,
		Longitude: input.Longitude, RadiusMeters: input.RadiusMeters,
		RotatingCode: input.RotatingCode, StaticCode: staticCode, CodeExpiresAt: expiresAt,
		LateAfter: input.LateAfter,
	}
	return s.session, nil
}

func (s *handlerSessionStore) Get(_ context.Context, institutionID, sessionID string) (sessions.Session, bool, error) {
	return s.session, s.session.ID == sessionID && s.session.InstitutionID == institutionID, nil
}

func (s *handlerSessionStore) End(_ context.Context, institutionID, sessionID string, endedAt time.Time) (sessions.Session, error) {
	if s.session.ID != sessionID || s.session.InstitutionID != institutionID {
		return sessions.Session{}, sessions.ErrSessionNotFound
	}
	s.session.EndedAt = &endedAt
	return s.session, nil
}

func (s *handlerSessionStore) ListLiveClasses(context.Context, string, string, string) ([]sessions.LiveClass, error) {
	return s.liveClasses, nil
}

func (s *handlerSessionStore) RecentreSession(_ context.Context, _, sessionID, _ string,
	centre sessions.Centre, _ sessions.DeviceFix, _ time.Time,
) error {
	if s.session.ID != sessionID {
		return sessions.ErrSessionNotFound
	}
	latitude, longitude, radius := centre.Latitude, centre.Longitude, centre.RadiusMeters
	s.session.Latitude, s.session.Longitude, s.session.RadiusMeters = &latitude, &longitude, &radius
	s.recentred = append(s.recentred, centre)
	return nil
}

type handlerAccessStore struct {
	allowed bool
	venue   sessions.VenueSettings
}

func (s *handlerAccessStore) CanManageOffering(context.Context, string, string, string) (bool, error) {
	return s.allowed, nil
}

func (s *handlerAccessStore) CanManageSession(context.Context, string, string, string) (bool, error) {
	return s.allowed, nil
}

func (s *handlerAccessStore) VenueSettings(context.Context, string, string) (sessions.VenueSettings, error) {
	return s.venue, nil
}

func TestSessionStartHandlerResponseAndVenueDefaults(t *testing.T) {
	t.Parallel()
	store := &handlerSessionStore{}
	access := &handlerAccessStore{allowed: true, venue: sessions.VenueSettings{
		Latitude: 6.5, Longitude: 3.3, RadiusMeters: 75, Present: true,
	}}
	service, err := sessions.NewService(store, testutil.NewFakeClock(time.Unix(120, 0)),
		[]byte("0123456789abcdef0123456789abcdef"), time.Minute, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := sessionhttp.NewHandler(service, access)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	request := httptest.NewRequest(http.MethodPost, "/sessions",
		strings.NewReader(`{"offering_id":"4c39d74e-7b7c-4cb0-8bd5-c8274905e3ce","venue_id":"48c5efb3-9651-478d-9d70-bb5be469f62f"}`))
	request = request.WithContext(auth.WithPrincipal(request.Context(), auth.Principal{Identity: auth.Identity{
		InstitutionID: "2f4e4ed0-490c-4cba-9343-62b0744d6ba7",
		UserID:        "8a3e2ab7-5f95-4d80-a64a-fbd1bf17d28b",
	}}))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("start status = %d, body %s", response.Code, response.Body.String())
	}
	var result sessions.Started
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Session.ID != testSessionID ||
		result.Session.Latitude == nil || *result.Session.Latitude != 6.5 ||
		result.Session.Longitude == nil || *result.Session.Longitude != 3.3 ||
		result.Session.RadiusMeters == nil || *result.Session.RadiusMeters != 75 ||
		len(result.Code) != 6 || result.SecondsRemaining <= 0 {
		t.Fatalf("unexpected start response: %+v", result)
	}
	if strings.Contains(response.Body.String(), "static_code") || strings.Contains(response.Body.String(), "InstitutionID") {
		t.Fatalf("internal session fields leaked in response: %s", response.Body.String())
	}
	principal := auth.Principal{Identity: auth.Identity{
		InstitutionID: "2f4e4ed0-490c-4cba-9343-62b0744d6ba7",
		UserID:        "8a3e2ab7-5f95-4d80-a64a-fbd1bf17d28b",
	}}
	codeRequest := httptest.NewRequest(http.MethodGet, "/sessions/"+testSessionID+"/code", nil)
	codeRequest = codeRequest.WithContext(auth.WithPrincipal(codeRequest.Context(), principal))
	codeResponse := httptest.NewRecorder()
	router.ServeHTTP(codeResponse, codeRequest)
	var fetched sessions.Started
	if codeResponse.Code != http.StatusOK || json.Unmarshal(codeResponse.Body.Bytes(), &fetched) != nil ||
		fetched.Code != result.Code {
		t.Fatalf("get session code = %d %s", codeResponse.Code, codeResponse.Body.String())
	}
	endRequest := httptest.NewRequest(http.MethodPost, "/sessions/"+testSessionID+"/end", nil)
	endRequest = endRequest.WithContext(auth.WithPrincipal(endRequest.Context(), principal))
	endResponse := httptest.NewRecorder()
	router.ServeHTTP(endResponse, endRequest)
	if endResponse.Code != http.StatusOK {
		t.Fatalf("end session = %d %s", endResponse.Code, endResponse.Body.String())
	}
	closedCodeRequest := httptest.NewRequest(http.MethodGet, "/sessions/"+testSessionID+"/code", nil)
	closedCodeRequest = closedCodeRequest.WithContext(auth.WithPrincipal(closedCodeRequest.Context(), principal))
	closedCodeResponse := httptest.NewRecorder()
	router.ServeHTTP(closedCodeResponse, closedCodeRequest)
	if closedCodeResponse.Code != http.StatusConflict ||
		!strings.Contains(closedCodeResponse.Body.String(), `"code":"session_closed"`) {
		t.Fatalf("closed session code = %d %s", closedCodeResponse.Code, closedCodeResponse.Body.String())
	}
}

func TestSessionStartHandlerDeniesUnassignedUser(t *testing.T) {
	t.Parallel()
	store := &handlerSessionStore{}
	service, err := sessions.NewService(store, testutil.NewFakeClock(time.Unix(120, 0)),
		[]byte("0123456789abcdef0123456789abcdef"), time.Minute, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := sessionhttp.NewHandler(service, &handlerAccessStore{allowed: false})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	request := httptest.NewRequest(http.MethodPost, "/sessions",
		strings.NewReader(`{"offering_id":"4c39d74e-7b7c-4cb0-8bd5-c8274905e3ce","venue_id":"48c5efb3-9651-478d-9d70-bb5be469f62f"}`))
	request = request.WithContext(auth.WithPrincipal(request.Context(), auth.Principal{Identity: auth.Identity{
		InstitutionID: "2f4e4ed0-490c-4cba-9343-62b0744d6ba7",
		UserID:        "8a3e2ab7-5f95-4d80-a64a-fbd1bf17d28b",
	}}))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"offering_access_denied"`) {
		t.Fatalf("denied start = %d, %s", response.Code, response.Body.String())
	}
}

func TestLiveClassesHandlerReturnsOnlyStudentSessionMetadata(t *testing.T) {
	t.Parallel()
	store := &handlerSessionStore{liveClasses: []sessions.LiveClass{{
		SessionID: testSessionID, OfferingID: "4c39d74e-7b7c-4cb0-8bd5-c8274905e3ce",
		CourseCode: "ECE301", CourseTitle: "Circuits and Systems",
		VenueID: "48c5efb3-9651-478d-9d70-bb5be469f62f", VenueName: "Engineering Hall",
		LecturerName: "Lecturer A", StartedAt: time.Unix(120, 0).UTC(),
		LateAfterMin: 10, RotatingCode: true, AlreadyCheckedIn: true,
	}}}
	service, err := sessions.NewService(store, testutil.NewFakeClock(time.Unix(120, 0)),
		[]byte("0123456789abcdef0123456789abcdef"), time.Minute, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := sessionhttp.NewHandler(service, &handlerAccessStore{allowed: true})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	request := httptest.NewRequest(http.MethodGet, "/me/live-classes", nil)
	request = request.WithContext(auth.WithPrincipal(request.Context(), auth.Principal{Identity: auth.Identity{
		InstitutionID: "2f4e4ed0-490c-4cba-9343-62b0744d6ba7",
		UserID:        "8a3e2ab7-5f95-4d80-a64a-fbd1bf17d28b",
		StudentID:     "88af2f1d-15de-4000-8a59-98bd2fb857d5",
	}}))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	for _, field := range []string{
		`"session_id":"` + testSessionID + `"`,
		`"offering_id":"4c39d74e-7b7c-4cb0-8bd5-c8274905e3ce"`,
		`"course_code":"ECE301"`, `"course_title":"Circuits and Systems"`,
		`"venue_id":"48c5efb3-9651-478d-9d70-bb5be469f62f"`,
		`"venue_name":"Engineering Hall"`, `"lecturer_name":"Lecturer A"`,
		`"late_after_min":10`, `"rotating_code":true`, `"already_checked_in":true`,
	} {
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), field) {
			t.Errorf("live classes status=%d missing %s: %s", response.Code, field, response.Body.String())
		}
	}
	if strings.Contains(response.Body.String(), `"code"`) {
		t.Fatalf("live classes response leaked an attendance code: %s", response.Body.String())
	}
}

func TestLiveClassesHandlerRejectsNonStudentIdentity(t *testing.T) {
	t.Parallel()
	store := &handlerSessionStore{}
	service, err := sessions.NewService(store, testutil.NewFakeClock(time.Unix(120, 0)),
		[]byte("0123456789abcdef0123456789abcdef"), time.Minute, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := sessionhttp.NewHandler(service, &handlerAccessStore{allowed: true})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	request := httptest.NewRequest(http.MethodGet, "/me/live-classes", nil)
	request = request.WithContext(auth.WithPrincipal(request.Context(), auth.Principal{Identity: auth.Identity{
		InstitutionID: "2f4e4ed0-490c-4cba-9343-62b0744d6ba7", UserID: "lecturer",
	}}))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"student_account_required"`) {
		t.Fatalf("non-student live classes request = %d %s", response.Code, response.Body.String())
	}
}

// locationTestRouter wires a session handler over the given venue settings.
func locationTestRouter(t *testing.T, store *handlerSessionStore, venue sessions.VenueSettings) chi.Router {
	t.Helper()
	service, err := sessions.NewService(store, testutil.NewFakeClock(time.Unix(120, 0)),
		[]byte("0123456789abcdef0123456789abcdef"), time.Minute, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := sessionhttp.NewHandler(service, &handlerAccessStore{allowed: true, venue: venue})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	return router
}

func locationTestPrincipal() auth.Principal {
	return auth.Principal{Identity: auth.Identity{
		InstitutionID: "2f4e4ed0-490c-4cba-9343-62b0744d6ba7",
		UserID:        "8a3e2ab7-5f95-4d80-a64a-fbd1bf17d28b",
	}}
}

func postJSON(t *testing.T, router chi.Router, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request = request.WithContext(auth.WithPrincipal(request.Context(), locationTestPrincipal()))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestSessionStartHandlerCalibratesVenueFromLecturerFix(t *testing.T) {
	t.Parallel()
	store := &handlerSessionStore{}
	// A venue whose stored centre is wrong by hundreds of metres, which is
	// what typed-in coordinates look like in practice.
	router := locationTestRouter(t, store, sessions.VenueSettings{
		Latitude: 6.6, Longitude: 3.4, RadiusMeters: 90, Present: true,
	})
	response := postJSON(t, router, "/sessions", `{"offering_id":"4c39d74e-7b7c-4cb0-8bd5-c8274905e3ce",
		"venue_id":"48c5efb3-9651-478d-9d70-bb5be469f62f",
		"latitude":6.5244,"longitude":3.3792,"accuracy_meters":12.5}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("start = %d %s", response.Code, response.Body.String())
	}
	var result sessions.Started
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.LocationSource != sessions.LocationFromDevice {
		t.Fatalf("location_source = %q, want device", result.LocationSource)
	}
	if !store.started.CalibrateVenue {
		t.Fatal("a trusted lecturer fix must calibrate the venue, or the next class repeats the correction")
	}
	if store.started.Latitude == nil || *store.started.Latitude != 6.5244 {
		t.Fatalf("session centre = %+v, want the lecturer's fix", store.started.Latitude)
	}
	if store.started.RadiusMeters == nil || *store.started.RadiusMeters != 90 {
		t.Fatalf("radius = %+v, want the venue's own 90 m", store.started.RadiusMeters)
	}
}

func TestSessionStartHandlerSucceedsWithLocationPermissionDenied(t *testing.T) {
	t.Parallel()
	store := &handlerSessionStore{}
	// The browser refused to supply a position and the venue has never been
	// calibrated. Attendance must still work: location is evidence, not a gate.
	router := locationTestRouter(t, store, sessions.VenueSettings{})
	response := postJSON(t, router, "/sessions", `{"offering_id":"4c39d74e-7b7c-4cb0-8bd5-c8274905e3ce",
		"venue_id":"48c5efb3-9651-478d-9d70-bb5be469f62f"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("start without a position = %d %s", response.Code, response.Body.String())
	}
	var result sessions.Started
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.LocationSource != sessions.LocationNone {
		t.Fatalf("location_source = %q, want none", result.LocationSource)
	}
	if result.Session.Latitude != nil || result.Session.RadiusMeters != nil {
		t.Fatalf("session geofence = %+v, want null", result.Session)
	}
	if store.started.CalibrateVenue {
		t.Fatal("no fix was supplied, so nothing may be written to the venue")
	}
}

func TestSessionStartHandlerIgnoresVagueFixInFavourOfVenue(t *testing.T) {
	t.Parallel()
	store := &handlerSessionStore{}
	router := locationTestRouter(t, store, sessions.VenueSettings{
		Latitude: 6.6, Longitude: 3.4, RadiusMeters: 90, Present: true,
	})
	response := postJSON(t, router, "/sessions", `{"offering_id":"4c39d74e-7b7c-4cb0-8bd5-c8274905e3ce",
		"venue_id":"48c5efb3-9651-478d-9d70-bb5be469f62f",
		"latitude":6.5244,"longitude":3.3792,"accuracy_meters":400}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("start = %d %s", response.Code, response.Body.String())
	}
	var result sessions.Started
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.LocationSource != sessions.LocationFromVenue {
		t.Fatalf("location_source = %q, want venue", result.LocationSource)
	}
	if store.started.CalibrateVenue || *store.started.Latitude != 6.6 {
		t.Fatalf("a 400 m fix must not centre the class or calibrate the room: %+v", store.started)
	}
}

func TestSessionLocationHandlerOutcomes(t *testing.T) {
	t.Parallel()
	store := &handlerSessionStore{}
	router := locationTestRouter(t, store, sessions.VenueSettings{
		Latitude: 6.5244, Longitude: 3.3792, RadiusMeters: 75, Present: true,
	})
	if code := postJSON(t, router, "/sessions", `{"offering_id":"4c39d74e-7b7c-4cb0-8bd5-c8274905e3ce",
		"venue_id":"48c5efb3-9651-478d-9d70-bb5be469f62f"}`).Code; code != http.StatusCreated {
		t.Fatalf("start = %d", code)
	}

	drift := postJSON(t, router, "/sessions/"+testSessionID+"/location",
		`{"latitude":6.5244,"longitude":3.3892,"accuracy_meters":10}`)
	if drift.Code != http.StatusOK {
		t.Fatalf("drift = %d %s", drift.Code, drift.Body.String())
	}
	var driftResult sessions.LocationResult
	if err := json.Unmarshal(drift.Body.Bytes(), &driftResult); err != nil {
		t.Fatal(err)
	}
	if driftResult.Outcome != sessions.LocationRejectedDistance {
		t.Fatalf("outcome = %q, want rejected_distance", driftResult.Outcome)
	}
	if len(store.recentred) != 0 {
		t.Fatal("a lecturer who walked out must not move the area for students still in the room")
	}
	if driftResult.Latitude == nil || *driftResult.Latitude != 6.5244 {
		t.Fatalf("response must carry the geofence still in force: %+v", driftResult)
	}

	refine := postJSON(t, router, "/sessions/"+testSessionID+"/location",
		`{"latitude":6.52477,"longitude":3.3792,"accuracy_meters":9}`)
	var refineResult sessions.LocationResult
	if refine.Code != http.StatusOK || json.Unmarshal(refine.Body.Bytes(), &refineResult) != nil {
		t.Fatalf("refine = %d %s", refine.Code, refine.Body.String())
	}
	if refineResult.Outcome != sessions.LocationRefined || len(store.recentred) != 1 {
		t.Fatalf("outcome = %q, writes = %d; want one refinement", refineResult.Outcome, len(store.recentred))
	}

	if bad := postJSON(t, router, "/sessions/"+testSessionID+"/location",
		`{"latitude":6.5244,"longitude":3.3792}`); bad.Code != http.StatusBadRequest {
		t.Fatalf("missing accuracy = %d %s; a mid-session fix without it cannot be judged",
			bad.Code, bad.Body.String())
	}

	endRequest := httptest.NewRequest(http.MethodPost, "/sessions/"+testSessionID+"/end", nil)
	endRequest = endRequest.WithContext(auth.WithPrincipal(endRequest.Context(), locationTestPrincipal()))
	endResponse := httptest.NewRecorder()
	router.ServeHTTP(endResponse, endRequest)
	if endResponse.Code != http.StatusOK {
		t.Fatalf("end = %d %s", endResponse.Code, endResponse.Body.String())
	}
	closed := postJSON(t, router, "/sessions/"+testSessionID+"/location",
		`{"latitude":6.5244,"longitude":3.3792,"accuracy_meters":9}`)
	if closed.Code != http.StatusConflict || !strings.Contains(closed.Body.String(), `"code":"session_closed"`) {
		t.Fatalf("closed session = %d %s", closed.Code, closed.Body.String())
	}
}
