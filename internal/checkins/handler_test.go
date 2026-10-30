package checkins_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"attendance-api/internal/auth"
	"attendance-api/internal/checkins"
	checkinhttp "attendance-api/internal/checkins/httpapi"
	"attendance-api/internal/sessions"
	"attendance-api/internal/testutil"
)

func TestCheckinHandlerReturnsErrorEnvelopeAndSuccessShape(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Unix(120, 0))
	secret := []byte("0123456789abcdef0123456789abcdef")
	store := &fakeCheckinStore{
		found: true, registered: true, inserted: true,
		session: checkins.Session{
			ID: "2f4e4ed0-490c-4cba-9343-62b0744d6ba7", StartedAt: time.Unix(0, 0),
			Venue:        &checkins.VenueCentre{Latitude: 0, Longitude: 0, RadiusMeters: 100},
			RotatingCode: true, RosterPolicy: checkins.RosterRegisteredOnly,
		},
	}
	service, err := checkins.NewService(store, clock, secret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := checkinhttp.NewHandler(service)
	if err != nil {
		t.Fatal(err)
	}
	principal := auth.Principal{Identity: auth.Identity{
		InstitutionID: "institution-1", UserID: "user-1", StudentID: "student-1",
	}}
	requestBody := func(code string) []byte {
		data, marshalErr := json.Marshal(checkins.Request{
			SessionID: store.session.ID, Code: code, Latitude: floatPtr(0), Longitude: floatPtr(0),
			Accuracy: floatPtr(5), DeviceHash: "device-aaaa",
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return data
	}

	validCode := sessions.DeriveCode(secret, store.session.ID, 2)
	wrongCode := "0" + validCode[1:]
	if wrongCode == validCode {
		wrongCode = "1" + validCode[1:]
	}
	wrong := httptest.NewRequest(http.MethodPost, "/checkins", bytes.NewReader(requestBody(wrongCode)))
	wrong = wrong.WithContext(auth.WithPrincipal(wrong.Context(), principal))
	wrongResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongResponse, wrong)
	if wrongResponse.Code != http.StatusBadRequest || !strings.Contains(wrongResponse.Body.String(), `"code":"wrong_or_expired_code"`) {
		t.Fatalf("wrong-code response = %d %s", wrongResponse.Code, wrongResponse.Body.String())
	}

	valid := httptest.NewRequest(http.MethodPost, "/checkins", bytes.NewReader(requestBody(validCode)))
	valid = valid.WithContext(auth.WithPrincipal(valid.Context(), principal))
	validResponse := httptest.NewRecorder()
	handler.ServeHTTP(validResponse, valid)
	if validResponse.Code != http.StatusCreated || !strings.Contains(validResponse.Body.String(), `"session_id":"`+store.session.ID+`"`) {
		t.Fatalf("valid response = %d %s", validResponse.Code, validResponse.Body.String())
	}

	missingLocation := httptest.NewRequest(http.MethodPost, "/checkins", strings.NewReader(
		`{"session_id":"2f4e4ed0-490c-4cba-9343-62b0744d6ba7","code":"`+validCode+`","device_hash":"device-bbbb"}`))
	missingLocation = missingLocation.WithContext(auth.WithPrincipal(missingLocation.Context(), principal))
	missingLocationResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingLocationResponse, missingLocation)
	if missingLocationResponse.Code != http.StatusBadRequest ||
		!strings.Contains(missingLocationResponse.Body.String(), `"code":"location_required"`) {
		t.Fatalf("missing location response = %d %s", missingLocationResponse.Code, missingLocationResponse.Body.String())
	}

	unknown := httptest.NewRequest(http.MethodPost, "/checkins", strings.NewReader(`{"session_id":"2f4e4ed0-490c-4cba-9343-62b0744d6ba7","code":"123456","device_hash":"device-aaaa","unexpected":true}`))
	unknown = unknown.WithContext(auth.WithPrincipal(unknown.Context(), principal))
	unknownResponse := httptest.NewRecorder()
	handler.ServeHTTP(unknownResponse, unknown)
	if unknownResponse.Code != http.StatusBadRequest || !strings.Contains(unknownResponse.Body.String(), `"code":"invalid_json"`) {
		t.Fatalf("strict JSON response = %d %s", unknownResponse.Code, unknownResponse.Body.String())
	}
}

var _ checkins.Store = (*fakeCheckinStore)(nil)

func floatPtr(value float64) *float64 { return &value }
