package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"attendance-api/internal/auth"
	"attendance-api/internal/registration"
	registrationhttp "attendance-api/internal/registration/httpapi"
	"attendance-api/internal/testutil"

	"github.com/go-chi/chi/v5"
)

const (
	testInstitutionID = "2f4e4ed0-490c-4cba-9343-62b0744d6ba7"
	testOfferingID    = "4c39d74e-7b7c-4cb0-8bd5-c8274905e3ce"
	testStudentID     = "88af2f1d-15de-4000-8a59-98bd2fb857d5"
)

type registrationStore struct {
	context registration.Context
	status  string
}

func (s *registrationStore) RegistrationContext(context.Context, string, string, string) (registration.Context, error) {
	return s.context, nil
}
func (s *registrationStore) Submit(_ context.Context, _, _, _, status string, _ time.Time) (string, error) {
	s.status = status
	return status, nil
}
func (*registrationStore) OfferingAccess(context.Context, string, string, string) (registration.Access, error) {
	return registration.Access{}, nil
}
func (*registrationStore) List(context.Context, string, string, string, string, int, int) ([]registration.Registration, error) {
	return nil, nil
}
func (*registrationStore) CanReview(context.Context, string, string, string, string) (bool, error) {
	return false, nil
}
func (*registrationStore) Review(context.Context, string, string, string, string, time.Time) (string, error) {
	return "", errors.New("unexpected review")
}

func TestSubmitRegistrationReturnsModeDecisionAndClosedError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mode   registration.Mode
		target bool
		want   string
		status int
	}{
		{name: "auto target", mode: registration.ModeAuto, target: true, want: "approved", status: http.StatusCreated},
		{name: "auto not targeted", mode: registration.ModeAuto, want: "pending", status: http.StatusCreated},
		{name: "open", mode: registration.ModeOpen, want: "approved", status: http.StatusCreated},
		{name: "approval", mode: registration.ModeApproval, want: "pending", status: http.StatusCreated},
		{name: "closed", mode: registration.ModeClosed, status: http.StatusConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := &registrationStore{context: registration.Context{
				Mode: test.mode, Policy: registration.RosterRegisteredOnly, AutoTarget: test.target,
			}}
			service, err := registration.NewService(store, testutil.NewFakeClock(time.Unix(1_800_000_000, 0)))
			if err != nil {
				t.Fatal(err)
			}
			handler, err := registrationhttp.NewHandler(service)
			if err != nil {
				t.Fatal(err)
			}
			router := chi.NewRouter()
			handler.RegisterRoutes(router)
			request := httptest.NewRequest(http.MethodPost, "/offerings/"+testOfferingID+"/registrations", nil)
			request = request.WithContext(auth.WithPrincipal(request.Context(), auth.Principal{Identity: auth.Identity{
				InstitutionID: testInstitutionID, UserID: "8a3e2ab7-5f95-4d80-a64a-fbd1bf17d28b",
				StudentID: testStudentID,
			}}))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.status == http.StatusConflict {
				if !strings.Contains(response.Body.String(), `"code":"registration_closed"`) ||
					!strings.Contains(response.Body.String(), "closed") {
					t.Fatalf("closed response=%s", response.Body.String())
				}
				return
			}
			var result registration.Registration
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Status != test.want || store.status != test.want {
				t.Fatalf("result=%+v stored status=%q", result, store.status)
			}
			if !strings.Contains(response.Body.String(), `"on_roster":`) {
				t.Fatalf("registration response omitted on_roster: %s", response.Body.String())
			}
		})
	}
}
