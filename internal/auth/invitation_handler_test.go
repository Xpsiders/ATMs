package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"attendance-api/internal/auth"
	"attendance-api/internal/testutil"

	"github.com/go-chi/chi/v5"
)

func TestInvitationHandlerCreateAndAccept(t *testing.T) {
	t.Parallel()
	const institutionID = "2f4e4ed0-490c-4cba-9343-62b0744d6ba7"
	clock := testutil.NewFakeClock(time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC))
	tokens, _ := newTestTokens(t, clock)
	if err := tokens.SetRandomSource(strings.NewReader(strings.Repeat("r", 32))); err != nil {
		t.Fatal(err)
	}
	repository := &memoryInvitationRepository{}
	mailer := &testutil.MemoryMailer{}
	service := newInvitationService(t, repository, mailer, clock)
	handler, err := auth.NewInvitationHandler(service, tokens, true, http.SameSiteStrictMode)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterPublicRoutes(router)
	handler.RegisterAuthenticatedRoutes(router)

	create := httptest.NewRequest(http.MethodPost, "/invitations", strings.NewReader(
		`{"email":"staff@example.test","role":"lecturer","scope_type":"department","department_id":"10000000-0000-4000-8000-000000000003"}`,
	))
	create = create.WithContext(auth.WithPrincipal(create.Context(), auth.Principal{
		Identity: auth.Identity{InstitutionID: institutionID, UserID: "inviter-1"},
	}))
	createResponse := httptest.NewRecorder()
	router.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusAccepted ||
		!strings.Contains(createResponse.Body.String(), `"status":"invitation_sent"`) {
		t.Fatalf("create response=%d %s", createResponse.Code, createResponse.Body.String())
	}
	token := strings.SplitN(strings.SplitN(mailer.Sent()[0].Text, "account: ", 2)[1], ".", 2)[0]

	accept := httptest.NewRequest(http.MethodPost, "/invitations/accept", strings.NewReader(
		`{"institution_id":"`+institutionID+`","token":"`+token+`","display_name":"Invited Staff","password":"correct horse battery staple"}`,
	))
	acceptResponse := httptest.NewRecorder()
	router.ServeHTTP(acceptResponse, accept)
	if acceptResponse.Code != http.StatusCreated {
		t.Fatalf("accept response=%d %s", acceptResponse.Code, acceptResponse.Body.String())
	}
	var response auth.TokenPair
	if err := json.Unmarshal(acceptResponse.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.AccessToken == "" || response.RefreshToken != "" {
		t.Fatalf("accept response tokens=%+v", response)
	}
	cookies := acceptResponse.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != auth.RefreshCookieName ||
		!cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("accept refresh cookie=%+v", cookies)
	}
}

func TestInvitationHandlerReturnsScopeDenial(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Unix(1_700_000_000, 0))
	tokens, _ := newTestTokens(t, clock)
	repository := &memoryInvitationRepository{createError: auth.ErrInvitationScopeDenied}
	service := newInvitationService(t, repository, &testutil.MemoryMailer{}, clock)
	handler, err := auth.NewInvitationHandler(service, tokens, false, http.SameSiteLaxMode)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterAuthenticatedRoutes(router)
	request := httptest.NewRequest(http.MethodPost, "/invitations", strings.NewReader(
		`{"email":"staff@example.test","role":"lecturer","scope_type":"department","department_id":"10000000-0000-4000-8000-000000000003"}`,
	))
	request = request.WithContext(auth.WithPrincipal(request.Context(), auth.Principal{
		Identity: auth.Identity{InstitutionID: "2f4e4ed0-490c-4cba-9343-62b0744d6ba7", UserID: "admin-1"},
	}))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), `"code":"invitation_scope_denied"`) {
		t.Fatalf("scope denial response=%d %s", response.Code, response.Body.String())
	}
}
