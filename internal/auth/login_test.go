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
	record auth.LoginRecord
	found  bool
}

func (r fakeLoginRepository) FindLogin(context.Context, string, string) (auth.LoginRecord, bool, error) {
	return r.record, r.found, nil
}

func (r fakeLoginRepository) FindPasswordHash(context.Context, string, string) (string, bool, error) {
	return r.record.PasswordHash, r.found, nil
}

func (r fakeLoginRepository) UpdatePasswordHash(context.Context, string, string, string) (bool, error) {
	return r.found, nil
}

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
