package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"attendance-api/internal/auth"
	"attendance-api/internal/studentportal"
	studenthttp "attendance-api/internal/studentportal/httpapi"

	"github.com/go-chi/chi/v5"
)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC) }

type scopeStore struct {
	institution string
	user        string
	student     string
}

func (s *scopeStore) CourseRows(_ context.Context, institution, user, student, _ string, _ time.Time) ([]studentportal.CourseRow, error) {
	s.institution, s.user, s.student = institution, user, student
	return []studentportal.CourseRow{}, nil
}
func (s *scopeStore) TimetableRows(_ context.Context, institution, user, student, _ string) ([]studentportal.TimetableRow, error) {
	s.institution, s.user, s.student = institution, user, student
	return []studentportal.TimetableRow{}, nil
}
func (s *scopeStore) AttendanceRows(_ context.Context, institution, user, student, _ string, _ time.Time) ([]studentportal.AttendanceEvidence, bool, error) {
	s.institution, s.user, s.student = institution, user, student
	return []studentportal.AttendanceEvidence{}, true, nil
}
func (s *scopeStore) PendingRows(_ context.Context, institution, user, student string) ([]studentportal.PendingRegistration, error) {
	s.institution, s.user, s.student = institution, user, student
	return []studentportal.PendingRegistration{}, nil
}

func newRouter(t *testing.T, store *scopeStore) *chi.Mux {
	t.Helper()
	service, err := studentportal.NewService(store, fixedClock{})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := studenthttp.NewHandler(service)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	return router
}

func TestPendingRegistrationsUsesAuthenticatedStudentScope(t *testing.T) {
	t.Parallel()
	store := &scopeStore{}
	router := newRouter(t, store)
	request := httptest.NewRequest(http.MethodGet, "/me/registrations", nil)
	request = request.WithContext(auth.WithPrincipal(request.Context(), auth.Principal{Identity: auth.Identity{
		InstitutionID: "tenant-id", UserID: "caller-id", StudentID: "caller-record",
	}}))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "{\"items\":[]}\n" {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
	if store.institution != "tenant-id" || store.user != "caller-id" || store.student != "caller-record" {
		t.Fatalf("query scope=%q %q %q", store.institution, store.user, store.student)
	}
}

func TestStudentRoutesRejectNonStudentBeforeStoreAccess(t *testing.T) {
	t.Parallel()
	store := &scopeStore{}
	router := newRouter(t, store)
	request := httptest.NewRequest(http.MethodGet, "/me/courses", nil)
	request = request.WithContext(auth.WithPrincipal(request.Context(), auth.Principal{Identity: auth.Identity{
		InstitutionID: "tenant-id", UserID: "caller-id",
	}}))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || store.institution != "" {
		t.Fatalf("response=%d store scope=%q", response.Code, store.institution)
	}
}
