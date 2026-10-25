package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"attendance-api/internal/auth"
	"attendance-api/internal/reports"
	reportshttp "attendance-api/internal/reports/httpapi"
	"attendance-api/internal/testutil"

	"github.com/go-chi/chi/v5"
)

type fakeStore struct{}

func (fakeStore) SessionScope(context.Context, string, string, string) (reports.Scope, error) {
	return reports.Scope{CanView: true, CanOverride: true}, nil
}
func (fakeStore) ScannerCoverageData(context.Context, string, string, time.Time, time.Duration) (reports.ScannerCoverageInput, error) {
	return reports.ScannerCoverageInput{SessionStart: time.Unix(100, 0), AsOf: time.Unix(200, 0)}, nil
}
func (fakeStore) ReportRows(context.Context, string, string, string, string, int, int) ([]reports.ReportRow, error) {
	return []reports.ReportRow{{StudentRecordID: "7d4e915a-4377-4c8d-b6a8-762c7beebaa1", MatricNo: "MAT-1", OnRoster: true}}, nil
}
func (fakeStore) RosterRows(context.Context, string, string, string, string, int, int) ([]reports.RosterStudent, error) {
	return []reports.RosterStudent{{StudentRecordID: "7d4e915a-4377-4c8d-b6a8-762c7beebaa1", MatricNo: "MAT-1"}}, nil
}
func (fakeStore) SaveOverride(context.Context, string, string, string, string, string, string, string, string, time.Time) error {
	return nil
}

func newRouter(t *testing.T) http.Handler {
	t.Helper()
	service, err := reports.NewService(fakeStore{}, testutil.NewFakeClock(time.Unix(200, 0)), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := reportshttp.NewHandler(service)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	return router
}

func TestSessionReportRequiresIdentityAndReturnsClassifiedShape(t *testing.T) {
	t.Parallel()
	router := newRouter(t)
	path := "/sessions/7d4e915a-4377-4c8d-b6a8-762c7beebaa1/report"
	unauthenticated := httptest.NewRecorder()
	router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, path, nil))
	if unauthenticated.Code != http.StatusUnauthorized || !strings.Contains(unauthenticated.Body.String(), `"code":"authentication_required"`) {
		t.Fatalf("unauthenticated response=%d %s", unauthenticated.Code, unauthenticated.Body.String())
	}
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request = request.WithContext(auth.WithPrincipal(request.Context(), auth.Principal{
		Identity: auth.Identity{InstitutionID: "c55f010f-f53c-4408-ae53-d23d04838916", UserID: "e8725090-009e-4f71-9cf9-847ce5a0256a"},
	}))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"scanner_coverage"`) ||
		!strings.Contains(response.Body.String(), `"classification":"absent"`) {
		t.Fatalf("report response=%d %s", response.Code, response.Body.String())
	}
}

func TestRosterSheetCSVFormat(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodGet,
		"/sessions/7d4e915a-4377-4c8d-b6a8-762c7beebaa1/roster-sheet?format=csv", nil)
	request = request.WithContext(auth.WithPrincipal(request.Context(), auth.Principal{
		Identity: auth.Identity{InstitutionID: "c55f010f-f53c-4408-ae53-d23d04838916", UserID: "e8725090-009e-4f71-9cf9-847ce5a0256a"},
	}))
	response := httptest.NewRecorder()
	newRouter(t).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/csv") ||
		!strings.Contains(response.Body.String(), "student_record_id,matric_no") {
		t.Fatalf("roster response=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func TestSessionReportPDFFormat(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodGet,
		"/sessions/7d4e915a-4377-4c8d-b6a8-762c7beebaa1/report?format=pdf", nil)
	request = request.WithContext(auth.WithPrincipal(request.Context(), auth.Principal{
		Identity: auth.Identity{InstitutionID: "c55f010f-f53c-4408-ae53-d23d04838916", UserID: "e8725090-009e-4f71-9cf9-847ce5a0256a"},
	}))
	response := httptest.NewRecorder()
	newRouter(t).ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/pdf" ||
		response.Header().Get("Content-Disposition") != `attachment; filename="session-report.pdf"` ||
		!strings.HasPrefix(response.Body.String(), "%PDF-1.4") ||
		!strings.Contains(response.Body.String(), "STUDENT ATTENDANCE REPORT") {
		body := response.Body.String()
		if len(body) > 40 {
			body = body[:40]
		}
		t.Fatalf("report PDF response=%d headers=%v body prefix=%q", response.Code,
			response.Header(), body)
	}
}

func TestRosterSheetPDFFormat(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodGet,
		"/sessions/7d4e915a-4377-4c8d-b6a8-762c7beebaa1/roster-sheet?format=pdf", nil)
	request = request.WithContext(auth.WithPrincipal(request.Context(), auth.Principal{
		Identity: auth.Identity{InstitutionID: "c55f010f-f53c-4408-ae53-d23d04838916", UserID: "e8725090-009e-4f71-9cf9-847ce5a0256a"},
	}))
	response := httptest.NewRecorder()
	newRouter(t).ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/pdf" ||
		response.Header().Get("Content-Disposition") != `attachment; filename="session-roster.pdf"` ||
		!strings.HasPrefix(response.Body.String(), "%PDF-1.4") ||
		!strings.Contains(response.Body.String(), "SESSION ATTENDANCE ROSTER") {
		body := response.Body.String()
		if len(body) > 40 {
			body = body[:40]
		}
		t.Fatalf("roster PDF response=%d headers=%v body prefix=%q", response.Code,
			response.Header(), body)
	}
}

func TestReportFormatRejectsUnsupportedFormat(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodGet,
		"/sessions/7d4e915a-4377-4c8d-b6a8-762c7beebaa1/report?format=docx", nil)
	request = request.WithContext(auth.WithPrincipal(request.Context(), auth.Principal{
		Identity: auth.Identity{InstitutionID: "c55f010f-f53c-4408-ae53-d23d04838916", UserID: "e8725090-009e-4f71-9cf9-847ce5a0256a"},
	}))
	response := httptest.NewRecorder()
	newRouter(t).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "format must be json, csv, or pdf") {
		t.Fatalf("unsupported report format response=%d %s", response.Code, response.Body.String())
	}
}
