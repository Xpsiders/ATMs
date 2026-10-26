package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"attendance-api/internal/scans"
	scanhttp "attendance-api/internal/scans/httpapi"

	"github.com/go-chi/chi/v5"
)

const (
	testDeviceKey = "device-secret"
	testDeviceID  = "2f4e4ed0-490c-4cba-9343-62b0744d6ba7"
)

type fakeDeviceStore struct {
	tasks             scans.Tasks
	deviceFound       bool
	slotFound         bool
	sessionFound      bool
	inserted          bool
	completed         string
	completedTemplate []byte
	status            *scans.DeviceStatus
	syncSession       string
	templateSync      scans.TemplateSync
}

func (s *fakeDeviceStore) LoadDeviceByKey(context.Context, []byte) (scans.Device, bool, error) {
	return scans.Device{ID: testDeviceID, InstitutionID: "8a3e2ab7-5f95-4d80-a64a-fbd1bf17d28b", VenueID: "48c5efb3-9651-478d-9d70-bb5be469f62f"}, s.deviceFound, nil
}

func (s *fakeDeviceStore) TouchDeviceHeartbeat(context.Context, scans.Device, time.Time) error {
	return nil
}

func (s *fakeDeviceStore) StudentForSlotNumber(context.Context, string, string, int) (string, bool, error) {
	return "student-1", s.slotFound, nil
}

func (s *fakeDeviceStore) OpenSessionAtVenue(context.Context, string, string) (string, bool, error) {
	return "session-1", s.sessionFound, nil
}

func (s *fakeDeviceStore) SessionAtVenueTime(context.Context, string, string, time.Time) (string, bool, error) {
	return "session-1", s.sessionFound, nil
}

func (s *fakeDeviceStore) InsertFingerprintScan(context.Context, string, string, string, string, float64, *time.Time) (bool, error) {
	return s.inserted, nil
}

func (s *fakeDeviceStore) LoadTasks(context.Context, string, string) (scans.Tasks, error) {
	return s.tasks, nil
}

func (s *fakeDeviceStore) CompleteEnrolment(_ context.Context, _, _, taskID, status string, template []byte, _ time.Time) error {
	s.completed = taskID + ":" + status
	s.completedTemplate = template
	return nil
}

func (s *fakeDeviceStore) RecordDeviceStatus(_ context.Context, _ scans.Device, status scans.DeviceStatus, _ time.Time) error {
	s.status = &status
	return nil
}

func (s *fakeDeviceStore) SyncDeviceTemplates(_ context.Context, _ scans.Device, sessionID string) (scans.TemplateSync, error) {
	s.syncSession = sessionID
	return s.templateSync, nil
}

var _ scanhttp.DeviceStore = (*fakeDeviceStore)(nil)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func newDeviceRouter(t *testing.T, store *fakeDeviceStore) http.Handler {
	t.Helper()
	service, err := scans.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := scanhttp.NewHandler(service, store, fixedClock{now: time.Unix(1_800_000_000, 0)})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Group(func(routes chi.Router) {
		routes.Use(scanhttp.DeviceAuth(store, fixedClock{now: time.Unix(1_800_000_000, 0)}))
		handler.RegisterRoutes(routes)
	})
	return router
}

func TestDeviceTasksReturnsServerTimeAndSupportsETag(t *testing.T) {
	t.Parallel()
	store := &fakeDeviceStore{
		deviceFound: true,
		tasks:       scans.Tasks{ActiveSession: &scans.ActiveSession{ID: "session-1", VenueID: "venue-1"}},
	}
	router := newDeviceRouter(t, store)
	firstRequest := httptest.NewRequest(http.MethodGet, "/device/tasks", nil).WithContext(context.Background())
	firstRequest.Header.Set("X-Device-Key", testDeviceKey)
	first := httptest.NewRecorder()
	router.ServeHTTP(first, firstRequest)
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"server_time":"2027-01-15T08:00:00Z"`) {
		t.Fatalf("tasks response = %d %s", first.Code, first.Body.String())
	}
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("tasks response omitted ETag")
	}
	request := httptest.NewRequest(http.MethodGet, "/device/tasks", nil)
	request.Header.Set("X-Device-Key", testDeviceKey)
	request.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	router.ServeHTTP(second, request)
	if second.Code != http.StatusNotModified || second.Body.Len() != 0 {
		t.Fatalf("conditional tasks response = %d %q", second.Code, second.Body.String())
	}
	weakRequest := httptest.NewRequest(http.MethodGet, "/device/tasks", nil)
	weakRequest.Header.Set("X-Device-Key", testDeviceKey)
	weakRequest.Header.Set("If-None-Match", `W/`+etag+`, "another-etag"`)
	weakResponse := httptest.NewRecorder()
	router.ServeHTTP(weakResponse, weakRequest)
	if weakResponse.Code != http.StatusNotModified {
		t.Fatalf("weak conditional tasks response = %d", weakResponse.Code)
	}
}

func TestDeviceScanNoSessionAndBatchPartialResults(t *testing.T) {
	t.Parallel()
	store := &fakeDeviceStore{deviceFound: true, slotFound: true, sessionFound: false}
	router := newDeviceRouter(t, store)
	request := httptest.NewRequest(http.MethodPost, "/device/scans",
		strings.NewReader(`{"slot_number":1,"confidence":0.9}`))
	request.Header.Set("X-Device-Key", testDeviceKey)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"no_active_session"`) {
		t.Fatalf("no-session response = %d %s", response.Code, response.Body.String())
	}

	store.sessionFound = true
	store.inserted = true
	batch := httptest.NewRequest(http.MethodPost, "/device/scans/batch", strings.NewReader(
		`{"scans":[{"slot_number":1,"confidence":0.9},{"slot_number":0,"confidence":0.9}]}`,
	))
	batch.Header.Set("X-Device-Key", testDeviceKey)
	batchResponse := httptest.NewRecorder()
	router.ServeHTTP(batchResponse, batch)
	if batchResponse.Code != http.StatusOK ||
		!strings.Contains(batchResponse.Body.String(), `"session_id":"session-1"`) ||
		!strings.Contains(batchResponse.Body.String(), `"error_code":"invalid_slot"`) {
		t.Fatalf("batch response = %d %s", batchResponse.Code, batchResponse.Body.String())
	}
}

func TestDeviceAuthAndEnrolmentCompletion(t *testing.T) {
	t.Parallel()
	store := &fakeDeviceStore{deviceFound: true}
	router := newDeviceRouter(t, store)
	unauthenticated := httptest.NewRecorder()
	router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/device/tasks", nil))
	if unauthenticated.Code != http.StatusUnauthorized ||
		!strings.Contains(unauthenticated.Body.String(), `"code":"device_auth_required"`) {
		t.Fatalf("missing device key response = %d %s", unauthenticated.Code, unauthenticated.Body.String())
	}

	taskID := "d4f5b2a6-798e-4b2c-bf03-3941827d1ce8"
	request := httptest.NewRequest(http.MethodPost, "/device/enrolments/"+taskID+"/complete", strings.NewReader(`{"status":"done"}`))
	request.Header.Set("X-Device-Key", testDeviceKey)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || store.completed != taskID+":done" {
		t.Fatalf("enrolment completion = %d, %s", response.Code, store.completed)
	}
}

func TestDeviceStatusRecordsTelemetryAndRejectsBadBattery(t *testing.T) {
	t.Parallel()
	store := &fakeDeviceStore{deviceFound: true}
	router := newDeviceRouter(t, store)

	request := httptest.NewRequest(http.MethodPost, "/device/status",
		strings.NewReader(`{"firmware_version":"1.4.2","battery_percent":88,"pending_queue_depth":3}`))
	request.Header.Set("X-Device-Key", testDeviceKey)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status response = %d %s", response.Code, response.Body.String())
	}
	if store.status == nil || store.status.FirmwareVersion == nil || *store.status.FirmwareVersion != "1.4.2" ||
		store.status.BatteryPercent == nil || *store.status.BatteryPercent != 88 {
		t.Fatalf("recorded status = %+v", store.status)
	}

	store.status = nil
	bad := httptest.NewRequest(http.MethodPost, "/device/status",
		strings.NewReader(`{"battery_percent":150}`))
	bad.Header.Set("X-Device-Key", testDeviceKey)
	badResponse := httptest.NewRecorder()
	router.ServeHTTP(badResponse, bad)
	if badResponse.Code != http.StatusBadRequest || store.status != nil {
		t.Fatalf("bad battery response = %d, recorded = %+v", badResponse.Code, store.status)
	}
}

func TestDeviceTemplateSyncReturnsRoster(t *testing.T) {
	t.Parallel()
	store := &fakeDeviceStore{deviceFound: true, templateSync: scans.TemplateSync{
		SessionID: "session-9",
		Slots: []scans.DeviceTemplate{
			{SlotNumber: 1, StudentRecordID: "stud-1", StudentRef: "MAT-001", Template: "QUJD"},
		},
	}}
	router := newDeviceRouter(t, store)
	request := httptest.NewRequest(http.MethodPost, "/device/templates/sync?session_id=1f1e4ed0-490c-4cba-9343-62b0744d6ba7", nil)
	request.Header.Set("X-Device-Key", testDeviceKey)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"slot_number":1`) ||
		!strings.Contains(response.Body.String(), `"template":"QUJD"`) ||
		!strings.Contains(response.Body.String(), `"server_time":`) {
		t.Fatalf("template sync response = %d %s", response.Code, response.Body.String())
	}
	if store.syncSession != "1f1e4ed0-490c-4cba-9343-62b0744d6ba7" {
		t.Fatalf("sync session forwarded = %q", store.syncSession)
	}
}

func TestEnrolmentCompleteStoresDecodedTemplate(t *testing.T) {
	t.Parallel()
	store := &fakeDeviceStore{deviceFound: true}
	router := newDeviceRouter(t, store)
	taskID := "d4f5b2a6-798e-4b2c-bf03-3941827d1ce8"
	request := httptest.NewRequest(http.MethodPost, "/device/enrolments/"+taskID+"/complete",
		strings.NewReader(`{"status":"done","template":"QUJD"}`))
	request.Header.Set("X-Device-Key", testDeviceKey)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || string(store.completedTemplate) != "ABC" {
		t.Fatalf("enrolment template = %d %q", response.Code, store.completedTemplate)
	}
}

func TestInvalidDeviceKeyRejected(t *testing.T) {
	t.Parallel()
	store := &fakeDeviceStore{deviceFound: false}
	router := newDeviceRouter(t, store)
	request := httptest.NewRequest(http.MethodGet, "/device/tasks", nil)
	request.Header.Set("X-Device-Key", testDeviceKey)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"code":"invalid_device_key"`) {
		t.Fatalf("invalid device key = %d %s", response.Code, response.Body.String())
	}
}
