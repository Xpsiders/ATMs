package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"attendance-api/internal/auth"
	"attendance-api/internal/testutil"
)

type fakeLoginRepository struct {
	record  auth.LoginRecord
	found   bool
	profile auth.UserProfile
}

func (r fakeLoginRepository) FindLogin(context.Context, string, string) (auth.LoginRecord, bool, error) {
	return r.record, r.found, nil
}

func (r fakeLoginRepository) FindUserProfile(context.Context, string, string) (auth.UserProfile, bool, error) {
	if !r.found {
		return auth.UserProfile{}, false, nil
	}
	profile := r.profile
	if profile.ID == "" {
		profile = auth.UserProfile{
			ID: r.record.Identity.UserID, Name: "Test User", Email: "user@example.test",
			Status: "active", MustChangePassword: r.record.Identity.ForcePassword,
			Roles: []auth.UserRole{},
		}
	}
	return profile, true, nil
}

func (r fakeLoginRepository) FindPasswordHash(context.Context, string, string) (string, bool, error) {
	return r.record.PasswordHash, r.found, nil
}

func (r fakeLoginRepository) UpdatePasswordHash(context.Context, string, string, string) (bool, error) {
	return r.found, nil
}

func stringPointer(value string) *string { return &value }

func intPointer(value int) *int { return &value }

func TestLoginIssuesTokensAndHonorsEmailVerification(t *testing.T) {
	t.Parallel()
	passwordHash, err := auth.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	clock := testutil.NewFakeClock(time.Unix(1_700_000_000, 0))
	tokens, _ := newTestTokens(t, clock)
	repository := fakeLoginRepository{found: true, record: auth.LoginRecord{
		Identity: auth.Identity{
			InstitutionID: "i1", UserID: "u1", StudentID: "s1", DepartmentID: "d1",
			Level: 200, ForcePassword: true,
		},
		PasswordHash: passwordHash, EmailVerified: true,
	}, profile: auth.UserProfile{
		ID: "u1", Name: "Student One", Email: "student@example.test", Status: "active",
		MustChangePassword: true,
		Roles: []auth.UserRole{{
			Role: "student", ScopeType: "department", ScopeID: stringPointer("d1"),
			DepartmentID: "d1", Level: intPointer(200),
		}},
	}}
	service, err := auth.NewLoginService(repository, tokens)
	if err != nil {
		t.Fatal(err)
	}

	pair, err := service.Login(context.Background(), "i1", "student@example.test", "correct horse battery staple")
	if err != nil || pair.AccessToken == "" || pair.RefreshToken == "" || !pair.ForceChange {
		t.Fatalf("login pair = %+v, %v", pair, err)
	}
	if claims, err := tokens.ValidateAccess(pair.AccessToken); err != nil || claims.StudentID != "s1" {
		t.Fatalf("issued access token claims = %+v, %v", claims, err)
	}
}

func TestLoginRejectsBadCredentialsAndUnverifiedStudents(t *testing.T) {
	t.Parallel()
	hash, err := auth.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	clock := testutil.NewFakeClock(time.Unix(1_700_000_000, 0))
	tokens, _ := newTestTokens(t, clock)
	repository := fakeLoginRepository{found: true, record: auth.LoginRecord{
		Identity:     auth.Identity{InstitutionID: "i1", UserID: "u1", StudentID: "s1"},
		PasswordHash: hash, EmailVerified: false,
	}}
	service, err := auth.NewLoginService(repository, tokens)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Login(context.Background(), "i1", "student@example.test", "wrong-password"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("bad password error = %v", err)
	}
	if _, err := service.Login(context.Background(), "i1", "student@example.test", "correct horse battery staple"); !errors.Is(err, auth.ErrEmailNotVerified) {
		t.Fatalf("unverified account error = %v", err)
	}
	if _, err := service.Login(context.Background(), "i1", "unknown@example.test", "wrong-password"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("unknown user error = %v", err)
	}
}

func TestRefreshRotatesCookieTokenAndLogoutRevokes(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Unix(1_700_000_000, 0))
	tokens, _ := newTestTokens(t, clock)
	if err := tokens.SetRandomSource(&incrementingReader{}); err != nil {
		t.Fatal(err)
	}
	service, err := auth.NewLoginService(fakeLoginRepository{}, tokens)
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := tokens.IssueRefresh(context.Background(), auth.Identity{InstitutionID: "i1", UserID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	pair, err := service.Refresh(context.Background(), refresh)
	if err != nil || pair.AccessToken == "" || pair.RefreshToken == refresh {
		t.Fatalf("refresh = %+v, %v", pair, err)
	}
	if err := service.Logout(context.Background(), pair.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(context.Background(), pair.RefreshToken); !errors.Is(err, auth.ErrInvalidRefresh) {
		t.Fatalf("revoked refresh error = %v", err)
	}
}
