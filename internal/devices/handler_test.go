package devices

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"attendance-api/internal/auth"

	"github.com/go-chi/chi/v5"
)

func newRouter(t *testing.T, repo Repository) http.Handler {
	t.Helper()
	handler, err := NewHandler(newService(t, repo))
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Group(func(authed chi.Router) {
		authed.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := auth.WithPrincipal(r.Context(), auth.Principal{Identity: actor()})
				next.ServeHTTP(w, r.WithContext(ctx))
			})
		})
		handler.RegisterAdminRoutes(authed)
	})
	handler.RegisterPairingRoutes(router)
	return router
}

func post(t *testing.T, router http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestProvisionReturnsPairingCodeOnce(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{}
	router := newRouter(t, repo)
	response := post(t, router, "/devices", `{"venue_id":"`+testVenue+`","name":"Lab Scanner"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("provision response = %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"pairing_code"`) {
		t.Fatalf("provision body omitted pairing code: %s", response.Body.String())
	}
	// The stored hash must never appear in the response.
	if strings.Contains(response.Body.String(), "hash") {
		t.Fatalf("provision body leaked a hash field: %s", response.Body.String())
	}
}

func TestProvisionRejectsMissingFields(t *testing.T) {
	t.Parallel()
	router := newRouter(t, &fakeRepo{})
	response := post(t, router, "/devices", `{"name":"No Venue"}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("missing venue response = %d %s", response.Code, response.Body.String())
	}
}

func TestProvisionScopeDeniedMapsTo403(t *testing.T) {
	t.Parallel()
	router := newRouter(t, &fakeRepo{err: ErrDeviceScopeDenied})
	response := post(t, router, "/devices", `{"venue_id":"`+testVenue+`","name":"Lab Scanner"}`)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"device_scope_denied"`) {
		t.Fatalf("scope-denied response = %d %s", response.Code, response.Body.String())
	}
}

func TestEnqueueEnrolmentReturnsSlot(t *testing.T) {
	t.Parallel()
	router := newRouter(t, &fakeRepo{})
	response := post(t, router, "/devices/"+testDevice+"/enrolments", `{"student_record_id":"`+testStudent+`"}`)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"slot_number":7`) {
		t.Fatalf("enrolment response = %d %s", response.Code, response.Body.String())
	}
}

func TestEnqueueEnrolmentStudentNotFoundMapsTo404(t *testing.T) {
	t.Parallel()
	router := newRouter(t, &fakeRepo{err: ErrStudentNotFound})
	response := post(t, router, "/devices/"+testDevice+"/enrolments", `{"student_record_id":"`+testStudent+`"}`)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"student_not_found"`) {
		t.Fatalf("student-not-found response = %d %s", response.Code, response.Body.String())
	}
}

func TestPairReturnsDeviceKey(t *testing.T) {
	t.Parallel()
	router := newRouter(t, &fakeRepo{})
	response := post(t, router, "/device/pair", `{"pairing_code":"K7QF-2M9X"}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"device_key":"device-secret"`) {
		t.Fatalf("pair response = %d %s", response.Code, response.Body.String())
	}
}

func TestPairInvalidCodeMapsTo401(t *testing.T) {
	t.Parallel()
	router := newRouter(t, &fakeRepo{err: ErrPairingCodeInvalid})
	response := post(t, router, "/device/pair", `{"pairing_code":"K7QF-2M9X"}`)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"code":"invalid_pairing_code"`) {
		t.Fatalf("invalid pair response = %d %s", response.Code, response.Body.String())
	}
}

func TestRevokeReturnsNoContent(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{}
	router := newRouter(t, repo)
	request := httptest.NewRequest(http.MethodDelete, "/devices/"+testDevice, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || repo.revoked != testDevice {
		t.Fatalf("revoke response = %d, revoked = %q", response.Code, repo.revoked)
	}
}
