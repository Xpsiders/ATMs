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
	session sessions.Session
}

func (s *handlerSessionStore) Create(_ context.Context, input sessions.StartInput, startedAt time.Time, staticCode string, expiresAt *time.Time) (sessions.Session, error) {
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
		Latitude: 6.5, Longitude: 3.3, RadiusMeters: 75,
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
	if result.Session.ID != testSessionID || result.Session.Latitude != 6.5 ||
		result.Session.Longitude != 3.3 || result.Session.RadiusMeters != 75 ||
		len(result.Code) != 6 || result.SecondsRemaining <= 0 {
		t.Fatalf("unexpected start response: %+v", result)
	}
	if strings.Contains(response.Body.String(), "static_code") || strings.Contains(response.Body.String(), "InstitutionID") {
		t.Fatalf("internal session fields leaked in response: %s", response.Body.String())
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
