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
	return newRouterWithActor(t, repo, actor())
}

func newRouterWithActor(t *testing.T, repo Repository, identity auth.Identity) http.Handler {
	t.Helper()
	handler, err := NewHandler(newService(t, repo))
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Group(func(authed chi.Router) {
		authed.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := auth.WithPrincipal(r.Context(), auth.Principal{Identity: identity})
				next.ServeHTTP(w, r.WithContext(ctx))
			})
		})
		handler.RegisterAdminRoutes(authed)
	})
	handler.RegisterPairingRoutes(router)
	return router
}

func request(t *testing.T, router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	router.ServeHTTP(recorder, req)
	return recorder
}

func post(t *testing.T, router http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return request(t, router, http.MethodPost, path, body)
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
	if response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), `"code":"device_scope_denied"`) ||
		!strings.Contains(response.Body.String(), ScannerManagementDeniedMessage) {
		t.Fatalf("scope-denied response = %d %s", response.Code, response.Body.String())
	}
}

func TestScannerManagementEndpointsAreInstitutionAdminOnly(t *testing.T) {
	t.Parallel()
	endpoints := []struct {
		method string
		path   string
		body   string
		want   int
	}{
		{http.MethodGet, "/devices", "", http.StatusOK},
		{http.MethodPost, "/devices", `{"venue_id":"` + testVenue + `","name":"Lab Scanner"}`, http.StatusCreated},
		{http.MethodPost, "/devices/" + testDevice + "/pairing-code", "", http.StatusCreated},
		{http.MethodPost, "/devices/" + testDevice + "/rotate-key", "", http.StatusCreated},
		{http.MethodDelete, "/devices/" + testDevice, "", http.StatusNoContent},
		{http.MethodPost, "/devices/" + testDevice + "/enrolments", `{"student_record_id":"` + testStudent + `"}`, http.StatusCreated},
	}
	roles := []string{"lecturer", "department_admin", "faculty_admin", "institution_admin", "platform_admin"}
	for _, endpoint := range endpoints {
		for _, role := range roles {
			t.Run(endpoint.method+" "+endpoint.path+" "+role, func(t *testing.T) {
				response := request(t, newRouterWithActor(t, &fakeRepo{}, roleActor(role)),
					endpoint.method, endpoint.path, endpoint.body)
				if role == "lecturer" || role == "department_admin" || role == "faculty_admin" {
					if response.Code != http.StatusForbidden ||
						!strings.Contains(response.Body.String(), ScannerManagementDeniedMessage) {
						t.Fatalf("status=%d body=%s", response.Code, response.Body)
					}
					return
				}
				if response.Code != endpoint.want {
					t.Fatalf("status=%d want=%d body=%s", response.Code, endpoint.want, response.Body)
				}
			})
		}
	}
}

func TestVenueScannerStatusIsReadOnlyAndAvailableToTeachingRoles(t *testing.T) {
	t.Parallel()
	for _, role := range []string{"lecturer", "department_admin", "faculty_admin", "institution_admin", "platform_admin"} {
		t.Run(role, func(t *testing.T) {
			response := request(t, newRouterWithActor(t, &fakeRepo{}, roleActor(role)),
				http.MethodGet, "/venues/"+testVenue+"/scanner-status", "")
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			body := response.Body.String()
			if !strings.Contains(body, `"status":"online"`) || !strings.Contains(body, `"last_seen_at"`) {
				t.Fatalf("missing scanner status fields: %s", body)
			}
			for _, secret := range []string{"device_key", "pairing_code", "key_hash", "actions"} {
				if strings.Contains(body, secret) {
					t.Fatalf("status response exposed %q: %s", secret, body)
				}
			}
		})
	}
}

func TestVenueScannerStatusRejectsUnknownRoleAndForeignTenantVenue(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{err: ErrVenueNotFound}
	response := request(t, newRouterWithActor(t, repo, auth.Identity{
		InstitutionID: testInstitution, UserID: "student-" + testUser,
	}), http.MethodGet, "/venues/"+testVenue+"/scanner-status", "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("unknown-role status=%d body=%s", response.Code, response.Body)
	}

	repo = &fakeRepo{err: ErrVenueNotFound}
	response = request(t, newRouter(t, repo), http.MethodGet,
		"/venues/99999999-9999-4999-8999-999999999999/scanner-status", "")
	if response.Code != http.StatusNotFound ||
		!strings.Contains(response.Body.String(), `"code":"venue_not_found"`) {
		t.Fatalf("foreign-tenant venue status=%d body=%s", response.Code, response.Body)
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

func TestEnqueueSeededStudentAccountIDReturnsEnrolment(t *testing.T) {
	t.Parallel()
	const seededStudentAccountID = "10000000-0000-4000-8000-000000000050"
	router := newRouter(t, &fakeRepo{})
	response := post(t, router, "/devices/"+testDevice+"/enrolments",
		`{"student_record_id":"`+seededStudentAccountID+`"}`)
	if response.Code != http.StatusCreated ||
		!strings.Contains(response.Body.String(), `"task_id":"task-1"`) ||
		!strings.Contains(response.Body.String(), `"slot_number":7`) {
		t.Fatalf("seeded student account enrolment = %d %s", response.Code, response.Body.String())
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

func TestEnqueueEnrolmentDeviceCapacityMapsToConflict(t *testing.T) {
	t.Parallel()
	router := newRouter(t, &fakeRepo{err: ErrDeviceSlotCapacity})
	response := post(t, router, "/devices/"+testDevice+"/enrolments", `{"student_record_id":"`+testStudent+`"}`)
	if response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), `"code":"device_capacity_reached"`) {
		t.Fatalf("device-capacity response = %d %s", response.Code, response.Body.String())
	}
}

func TestEnrolmentStudentLookupIsTenantScopedAndSupportsSeededAccountIDs(t *testing.T) {
	t.Parallel()
	normalized := strings.Join(strings.Fields(resolveEnrolmentStudentQuery), " ")
	for _, fragment := range []string{
		"FROM student_records",
		"WHERE institution_id=$1",
		"(id=$2 OR claimed_by_user_id=$2)",
		"ORDER BY (id=$2) DESC",
	} {
		if !strings.Contains(normalized, fragment) {
			t.Errorf("enrolment student query lacks %q: %s", fragment, normalized)
		}
	}
	slotQuery := strings.Join(strings.Fields(findDeviceStudentSlotQuery), " ")
	for _, fragment := range []string{
		"FROM device_slots",
		"WHERE institution_id=$1 AND device_id=$2 AND student_record_id=$3",
	} {
		if !strings.Contains(slotQuery, fragment) {
			t.Errorf("existing device-slot query lacks %q: %s", fragment, slotQuery)
		}
	}
}

func TestNextDeviceSlotNumberRespectsFirmwareRange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		maxSlot int
		want    int
		wantErr error
	}{
		{maxSlot: 0, want: 1},
		{maxSlot: 299, want: 300},
		{maxSlot: 300, wantErr: ErrDeviceSlotCapacity},
		{maxSlot: 301, wantErr: ErrDeviceSlotCapacity},
		{maxSlot: -1, wantErr: ErrDeviceSlotCapacity},
	}
	for _, test := range tests {
		got, err := nextDeviceSlotNumber(test.maxSlot)
		if test.wantErr != nil && err != test.wantErr {
			t.Errorf("nextDeviceSlotNumber(%d) error = %v, want %v", test.maxSlot, err, test.wantErr)
		}
		if test.wantErr == nil && err != nil {
			t.Errorf("nextDeviceSlotNumber(%d) error = %v", test.maxSlot, err)
		}
		if err == nil && got != test.want {
			t.Errorf("nextDeviceSlotNumber(%d) = %d, want %d", test.maxSlot, got, test.want)
		}
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
