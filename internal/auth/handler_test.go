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

func TestLoginHandlerResponseAndRefreshCookie(t *testing.T) {
	t.Parallel()
	hash, err := auth.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	clock := testutil.NewFakeClock(time.Unix(1_700_000_000, 0))
	tokens, _ := newTestTokens(t, clock)
	repository := fakeLoginRepository{found: true, record: auth.LoginRecord{
		Identity:     auth.Identity{InstitutionID: "institution-1", UserID: "user-1"},
		PasswordHash: hash, EmailVerified: true,
	}, profile: auth.UserProfile{
		ID: "user-1", Name: "Demo User", Email: "user@example.test", Status: "active",
		Roles: []auth.UserRole{{Role: "institution_admin", ScopeType: "institution", ScopeID: stringPointer("institution-1")}},
	}}
	service, err := auth.NewLoginService(repository, tokens)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := auth.NewHandler(service, true, http.SameSiteStrictMode)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	body := `{"institution_id":"2f4e4ed0-490c-4cba-9343-62b0744d6ba7","email":"user@example.test","password":"correct horse battery staple"}`
	request := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", response.Code, response.Body.String())
	}
	var bodyJSON map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &bodyJSON); err != nil {
		t.Fatal(err)
	}
	if bodyJSON["access_token"] == "" || bodyJSON["refresh_token"] != nil ||
		bodyJSON["id"] != "user-1" || bodyJSON["name"] != "Demo User" ||
		bodyJSON["email"] != "user@example.test" || bodyJSON["status"] != "active" ||
		bodyJSON["must_change_password"] != false {
		t.Fatalf("login response exposes wrong tokens: %v", bodyJSON)
	}
	roles, ok := bodyJSON["roles"].([]any)
	if !ok || len(roles) != 1 || roles[0].(map[string]any)["scope_type"] != "institution" ||
		roles[0].(map[string]any)["scope_id"] != "institution-1" {
		t.Fatalf("login roles = %v", bodyJSON["roles"])
	}
	cookie := response.Result().Cookies()[0]
	if cookie.Name != auth.RefreshCookieName || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("refresh cookie flags = %+v", cookie)
	}

	refreshRequest := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	refreshRequest.AddCookie(cookie)
	refreshResponse := httptest.NewRecorder()
	router.ServeHTTP(refreshResponse, refreshRequest)
	if refreshResponse.Code != http.StatusOK || len(refreshResponse.Result().Cookies()) != 1 {
		t.Fatalf("refresh response = %d %s", refreshResponse.Code, refreshResponse.Body.String())
	}

	logoutRequest := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	logoutRequest.AddCookie(refreshResponse.Result().Cookies()[0])
	logoutResponse := httptest.NewRecorder()
	router.ServeHTTP(logoutResponse, logoutRequest)
	if logoutResponse.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d", logoutResponse.Code)
	}
}

func TestMeReturnsUserProfileWithStudentScopeAndLockedAttributes(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Unix(1_700_000_000, 0))
	tokens, _ := newTestTokens(t, clock)
	repository := fakeLoginRepository{found: true, profile: auth.UserProfile{
		ID: "user-1", Name: "Student One", Email: "student@example.test", Status: "active",
		MustChangePassword: true,
		Roles: []auth.UserRole{{
			Role: "student", ScopeType: "department", ScopeID: stringPointer("department-1"),
			DepartmentID: "department-1", Level: intPointer(200),
		}},
	}}
	service, err := auth.NewLoginService(repository, tokens)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := auth.NewHandler(service, false, http.SameSiteLaxMode)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	accessToken, err := tokens.IssueAccess(auth.Identity{
		InstitutionID: "institution-1", UserID: "user-1", ForcePassword: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/me", nil)
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("/me status=%d body=%s", response.Code, response.Body.String())
	}
	var bodyJSON map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &bodyJSON); err != nil {
		t.Fatal(err)
	}
	if bodyJSON["id"] != "user-1" || bodyJSON["name"] != "Student One" ||
		bodyJSON["email"] != "student@example.test" || bodyJSON["status"] != "active" ||
		bodyJSON["must_change_password"] != true {
		t.Fatalf("/me profile = %v", bodyJSON)
	}
	roles, ok := bodyJSON["roles"].([]any)
	if !ok || len(roles) != 1 {
		t.Fatalf("/me roles = %v", bodyJSON["roles"])
	}
	studentRole, ok := roles[0].(map[string]any)
	if !ok || studentRole["role"] != "student" ||
		studentRole["scope_type"] != "department" ||
		studentRole["scope_id"] != "department-1" ||
		studentRole["department_id"] != "department-1" ||
		studentRole["level"] != float64(200) {
		t.Fatalf("/me student role = %v", roles[0])
	}
}

func TestLoginHandlerRejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Unix(1_700_000_000, 0))
	tokens, _ := newTestTokens(t, clock)
	service, err := auth.NewLoginService(fakeLoginRepository{}, tokens)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := auth.NewHandler(service, false, http.SameSiteLaxMode)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	request := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"unexpected":true}`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_json"`) {
		t.Fatalf("malformed login = %d %s", response.Code, response.Body.String())
	}
}

func TestForcedPasswordChangeReturnsReplacementAccessToken(t *testing.T) {
	t.Parallel()
	hash, err := auth.HashPassword("temporary password")
	if err != nil {
		t.Fatal(err)
	}
	clock := testutil.NewFakeClock(time.Unix(1_700_000_000, 0))
	tokens, _ := newTestTokens(t, clock)
	service, err := auth.NewLoginService(fakeLoginRepository{found: true, record: auth.LoginRecord{
		Identity:     auth.Identity{InstitutionID: "i1", UserID: "u1", ForcePassword: true},
		PasswordHash: hash,
	}}, tokens)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := auth.NewHandler(service, false, http.SameSiteLaxMode)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	oldToken, err := tokens.IssueAccess(auth.Identity{InstitutionID: "i1", UserID: "u1", ForcePassword: true})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/auth/password/change",
		strings.NewReader(`{"current_password":"temporary password","new_password":"new permanent password"}`))
	request.Header.Set("Authorization", "Bearer "+oldToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("change password status = %d, body=%s", response.Code, response.Body.String())
	}
	var pair auth.TokenPair
	if err := json.Unmarshal(response.Body.Bytes(), &pair); err != nil {
		t.Fatal(err)
	}
	claims, err := tokens.ValidateAccess(pair.AccessToken)
	if err != nil || claims.ForcePassword {
		t.Fatalf("replacement access claims = %+v, %v", claims, err)
	}
}

func TestStudentClaimHandlerVerifiesAndIssuesRefreshCookie(t *testing.T) {
	t.Parallel()
	const institutionID = "2f4e4ed0-490c-4cba-9343-62b0744d6ba7"
	clock := testutil.NewFakeClock(time.Unix(1_700_000_000, 0))
	tokens, _ := newTestTokens(t, clock)
	repository := newClaimRepository()
	repository.record.InstitutionID = institutionID
	mailer := &testutil.MemoryMailer{}
	service, err := auth.NewClaimService(repository, mailer, clock,
		[]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetRandomSource(strings.NewReader("\x00\x00\x00\x07")); err != nil {
		t.Fatal(err)
	}
	handler, err := auth.NewClaimHandler(service, tokens, true, http.SameSiteStrictMode)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	start := httptest.NewRequest(http.MethodPost, "/auth/signup-claim", strings.NewReader(
		`{"institution_id":"`+institutionID+`","matric_no":"MAT001","email":"record@example.test"}`,
	))
	startResponse := httptest.NewRecorder()
	router.ServeHTTP(startResponse, start)
	if startResponse.Code != http.StatusAccepted ||
		!strings.Contains(startResponse.Body.String(), `"status":"request_received"`) {
		t.Fatalf("claim start = %d %s", startResponse.Code, startResponse.Body.String())
	}

	verify := httptest.NewRequest(http.MethodPost, "/auth/verify-email", strings.NewReader(
		`{"institution_id":"`+institutionID+`","matric_no":"MAT001","code":"000007","password":"correct horse battery staple"}`,
	))
	verifyResponse := httptest.NewRecorder()
	router.ServeHTTP(verifyResponse, verify)
	if verifyResponse.Code != http.StatusCreated {
		t.Fatalf("claim verify = %d %s", verifyResponse.Code, verifyResponse.Body.String())
	}
	cookies := verifyResponse.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != auth.RefreshCookieName ||
		!cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("claim refresh cookie = %+v", cookies)
	}
	var response auth.TokenPair
	if err := json.Unmarshal(verifyResponse.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	claims, err := tokens.ValidateAccess(response.AccessToken)
	if err != nil || claims.StudentID != "record-1" || claims.DepartmentID != "department-locked" || claims.Level != 200 {
		t.Fatalf("claimed access token claims = %+v, %v", claims, err)
	}
}

func TestStudentClaimHandlerUnknownMatricRequestsApproval(t *testing.T) {
	t.Parallel()
	const institutionID = "2f4e4ed0-490c-4cba-9343-62b0744d6ba7"
	clock := testutil.NewFakeClock(time.Unix(1_700_000_000, 0))
	tokens, _ := newTestTokens(t, clock)
	repository := newClaimRepository()
	repository.record.InstitutionID = institutionID
	service, err := auth.NewClaimService(repository, &testutil.MemoryMailer{}, clock,
		[]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := auth.NewClaimHandler(service, tokens, false, http.SameSiteLaxMode)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	request := httptest.NewRequest(http.MethodPost, "/auth/signup-claim", strings.NewReader(
		`{"institution_id":"`+institutionID+`","matric_no":"UNKNOWN","email":"unknown@example.test"}`,
	))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted ||
		!strings.Contains(response.Body.String(), `"status":"request_received"`) ||
		repository.requests != 1 {
		t.Fatalf("unknown claim response = %d %s, requests=%d", response.Code, response.Body.String(), repository.requests)
	}
}

func TestStudentClaimStartDoesNotRevealRecordEligibility(t *testing.T) {
	t.Parallel()
	const institutionID = "2f4e4ed0-490c-4cba-9343-62b0744d6ba7"
	clock := testutil.NewFakeClock(time.Unix(1_700_000_000, 0))
	tokens, _ := newTestTokens(t, clock)
	repository := newClaimRepository()
	repository.record.InstitutionID = institutionID
	service, err := auth.NewClaimService(repository, &testutil.MemoryMailer{}, clock,
		[]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := auth.NewClaimHandler(service, tokens, false, http.SameSiteLaxMode)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	start := func(matricNo, email string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/auth/signup-claim", strings.NewReader(
			`{"institution_id":"`+institutionID+`","matric_no":"`+matricNo+`","email":"`+email+`"}`,
		))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}

	matching := start("MAT001", "record@example.test")
	mismatch := start("MAT001", "wrong@example.test")
	repository.record.Claimed = true
	claimed := start("MAT001", "record@example.test")
	unknown := start("UNKNOWN", "unknown@example.test")

	for label, response := range map[string]*httptest.ResponseRecorder{
		"matching": matching, "email mismatch": mismatch, "already claimed": claimed, "unknown": unknown,
	} {
		if response.Code != http.StatusAccepted || response.Body.String() != matching.Body.String() {
			t.Errorf("%s response = %d %s; expected same acknowledgement as matching record",
				label, response.Code, response.Body.String())
		}
	}

	verify := func(matricNo, code string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/auth/verify-email", strings.NewReader(
			`{"institution_id":"`+institutionID+`","matric_no":"`+matricNo+`","code":"`+code+`","password":"correct horse battery staple"}`,
		))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	invalidCode := verify("MAT001", "000000")
	claimedRecord := verify("MAT001", "000000")
	if invalidCode.Code != http.StatusBadRequest || claimedRecord.Code != invalidCode.Code ||
		claimedRecord.Body.String() != invalidCode.Body.String() {
		t.Errorf("verification response reveals claimed state: invalid=%d %s claimed=%d %s",
			invalidCode.Code, invalidCode.Body.String(), claimedRecord.Code, claimedRecord.Body.String())
	}
}

var _ auth.LoginRepository = fakeLoginRepository{}
