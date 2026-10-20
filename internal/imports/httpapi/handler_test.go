package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"attendance-api/internal/auth"
	"attendance-api/internal/imports"
	importshttp "attendance-api/internal/imports/httpapi"

	"github.com/go-chi/chi/v5"
)

type fakeStore struct {
	validationErrors []imports.RowError
	commitCalls      int
	commitError      error
}

func (f *fakeStore) ValidateStudents(context.Context, string, string, []imports.StudentRow) ([]imports.RowError, error) {
	return f.validationErrors, nil
}

func (f *fakeStore) CommitStudents(context.Context, string, string, []imports.StudentRow) error {
	f.commitCalls++
	return f.commitError
}

func newRouter(t *testing.T, store *fakeStore) http.Handler {
	t.Helper()
	service, err := imports.NewService(store, imports.MaxStudentCSVBytes)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := importshttp.NewHandler(service)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	return router
}

func authorizedCSVRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "text/csv")
	return request.WithContext(auth.WithPrincipal(request.Context(), auth.Principal{
		Identity: auth.Identity{
			InstitutionID: "10000000-0000-4000-8000-000000000001",
			UserID:        "10000000-0000-4000-8000-000000000020",
		},
	}))
}

func TestPreviewReturnsRowLevelErrorsWithoutCommit(t *testing.T) {
	t.Parallel()
	store := &fakeStore{validationErrors: []imports.RowError{{
		Line: 2, Column: "department_id", Message: "department is outside your import scope",
	}}}
	response := httptest.NewRecorder()
	newRouter(t, store).ServeHTTP(response, authorizedCSVRequest(http.MethodPost,
		"/imports/students/preview",
		"matric_no,email,display_name,department_id,level\nMAT001,student@example.test,Student,10000000-0000-4000-8000-000000000003,200\n"))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"line":2`) ||
		!strings.Contains(response.Body.String(), "outside your import scope") || store.commitCalls != 0 {
		t.Fatalf("preview response=%d %s commits=%d", response.Code, response.Body.String(), store.commitCalls)
	}
}

func TestCommitRefusesInvalidPreviewAndCommitsCleanCSV(t *testing.T) {
	t.Parallel()
	body := "matric_no,email,display_name,department_id,level\nMAT001,student@example.test,Student,10000000-0000-4000-8000-000000000003,200\n"
	store := &fakeStore{validationErrors: []imports.RowError{{Line: 2, Column: "matric_no", Message: "already exists"}}}
	response := httptest.NewRecorder()
	router := newRouter(t, store)
	router.ServeHTTP(response, authorizedCSVRequest(http.MethodPost, "/imports/students", body))
	if response.Code != http.StatusUnprocessableEntity || store.commitCalls != 0 {
		t.Fatalf("invalid import response=%d %s commits=%d", response.Code, response.Body.String(), store.commitCalls)
	}
	store.validationErrors = nil
	response = httptest.NewRecorder()
	router.ServeHTTP(response, authorizedCSVRequest(http.MethodPost, "/imports/students", body))
	if response.Code != http.StatusCreated || store.commitCalls != 1 {
		t.Fatalf("valid import response=%d %s commits=%d", response.Code, response.Body.String(), store.commitCalls)
	}
}

func TestCommitReturnsConflictForConcurrentDuplicateMatric(t *testing.T) {
	t.Parallel()
	store := &fakeStore{commitError: imports.ErrDuplicateMatric}
	response := httptest.NewRecorder()
	newRouter(t, store).ServeHTTP(response, authorizedCSVRequest(http.MethodPost,
		"/imports/students",
		"matric_no,email,display_name,department_id,level\nMAT001,student@example.test,Student,10000000-0000-4000-8000-000000000003,200\n"))
	if response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), `"code":"matric_number_conflict"`) ||
		strings.Contains(response.Body.String(), "MAT001") {
		t.Fatalf("conflict response=%d %s", response.Code, response.Body.String())
	}
}

func TestCSVHandlerRequiresAuthenticationAndContentType(t *testing.T) {
	t.Parallel()
	router := newRouter(t, &fakeStore{})
	request := httptest.NewRequest(http.MethodPost, "/imports/students/preview", strings.NewReader("x"))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"code":"authentication_required"`) {
		t.Fatalf("unauthorized response=%d %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/imports/students/preview", strings.NewReader("x"))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(auth.WithPrincipal(request.Context(), auth.Principal{
		Identity: auth.Identity{InstitutionID: "institution", UserID: "user"},
	}))
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("content type response=%d %s", response.Code, response.Body.String())
	}
}
