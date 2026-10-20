package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"attendance-api/internal/auth"
	"attendance-api/internal/testutil"
)

func TestUserAuthRequiresValidTokenAndHonorsForcedPasswordChange(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Unix(1_700_000_000, 0))
	manager, _ := newTestTokens(t, clock)
	handler := auth.UserAuth(manager)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok || principal.Identity.UserID != "user-1" {
			t.Error("authenticated principal missing")
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/resource", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d", unauthenticated.Code)
	}

	token, err := manager.IssueAccess(auth.Identity{
		InstitutionID: "institution-1", UserID: "user-1", StudentID: "student-1", ForcePassword: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	blockedRequest := httptest.NewRequest(http.MethodGet, "/resource", nil)
	blockedRequest.Header.Set("Authorization", "Bearer "+token)
	blocked := httptest.NewRecorder()
	handler.ServeHTTP(blocked, blockedRequest)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("forced-change route status = %d", blocked.Code)
	}

	allowedRequest := httptest.NewRequest(http.MethodPost, "/auth/password/change", nil)
	allowedRequest.Header.Set("Authorization", "Bearer "+token)
	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, allowedRequest)
	if allowed.Code != http.StatusNoContent {
		t.Fatalf("password-change route status = %d", allowed.Code)
	}
}
