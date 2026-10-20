package auth_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"attendance-api/internal/auth"
	"attendance-api/internal/notify"
	"attendance-api/internal/testutil"
)

type memoryInvitationRepository struct {
	request      auth.InvitationRequest
	issuer       auth.Identity
	tokenHash    []byte
	record       auth.InvitationRecord
	createError  error
	acceptError  error
	accepted     bool
	passwordHash string
	displayName  string
}

func (r *memoryInvitationRepository) CreateInvitation(
	_ context.Context,
	issuer auth.Identity,
	request auth.InvitationRequest,
	tokenHash []byte,
	expiresAt time.Time,
) (auth.InvitationRecord, error) {
	r.issuer = issuer
	r.request = request
	r.tokenHash = append([]byte(nil), tokenHash...)
	if r.createError != nil {
		return auth.InvitationRecord{}, r.createError
	}
	r.record = auth.InvitationRecord{ID: "invite-1", ExpiresAt: expiresAt}
	return r.record, nil
}

func (r *memoryInvitationRepository) AcceptInvitation(
	_ context.Context,
	_ string,
	tokenHash []byte,
	now time.Time,
	passwordHash, displayName string,
) (auth.Identity, error) {
	if r.acceptError != nil {
		return auth.Identity{}, r.acceptError
	}
	if r.accepted || !bytes.Equal(tokenHash, r.tokenHash) || !now.Before(r.record.ExpiresAt) {
		return auth.Identity{}, auth.ErrInvalidInvitation
	}
	r.accepted = true
	r.passwordHash = passwordHash
	r.displayName = displayName
	return auth.Identity{
		InstitutionID: r.issuer.InstitutionID,
		UserID:        "invited-user",
	}, nil
}

func newInvitationService(t *testing.T, repo *memoryInvitationRepository, mailer notify.Mailer, clock *testutil.FakeClock) *auth.InvitationService {
	t.Helper()
	service, err := auth.NewInvitationService(repo, mailer, clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetRandomSource(bytes.NewReader(make([]byte, 32))); err != nil {
		t.Fatal(err)
	}
	return service
}

func TestInvitationCreateSendsHashedSingleUseToken(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	clock := testutil.NewFakeClock(now)
	repo := &memoryInvitationRepository{}
	mailer := &testutil.MemoryMailer{}
	service := newInvitationService(t, repo, mailer, clock)
	created, err := service.Create(context.Background(),
		auth.Identity{InstitutionID: "institution-1", UserID: "admin-1"},
		auth.InvitationRequest{
			Email: " Lecturer@Example.Test ", Role: "lecturer", ScopeType: "department",
			DepartmentID: "department-1",
		})
	if err != nil {
		t.Fatal(err)
	}
	messages := mailer.Sent()
	if len(messages) != 1 || messages[0].To != "lecturer@example.test" ||
		!strings.Contains(messages[0].Text, "Institution ID: institution-1") {
		t.Fatalf("invitation email = %+v", messages)
	}
	token := strings.SplitN(strings.SplitN(messages[0].Text, "account: ", 2)[1], ".", 2)[0]
	if len(token) != 43 {
		t.Fatalf("token length = %d, token=%q", len(token), token)
	}
	wantHash := sha256.Sum256([]byte(token))
	if !bytes.Equal(repo.tokenHash, wantHash[:]) {
		t.Fatal("repository received plaintext or incorrect invitation token hash")
	}
	if !created.ExpiresAt.Equal(now.Add(auth.InvitationLifetime)) {
		t.Fatalf("expiry = %v", created.ExpiresAt)
	}
	if repo.request.Email != "lecturer@example.test" || repo.issuer.UserID != "admin-1" {
		t.Fatalf("stored invitation = %+v issuer=%+v", repo.request, repo.issuer)
	}
}

func TestInvitationCreateRejectsInvalidRoleScopeBeforeStorage(t *testing.T) {
	t.Parallel()
	repo := &memoryInvitationRepository{}
	service := newInvitationService(t, repo, &testutil.MemoryMailer{}, testutil.NewFakeClock(time.Unix(1, 0)))
	_, err := service.Create(context.Background(), auth.Identity{
		InstitutionID: "institution-1", UserID: "admin-1",
	}, auth.InvitationRequest{
		Email: "staff@example.test", Role: "lecturer", ScopeType: "institution",
	})
	if !errors.Is(err, auth.ErrInvalidInvitationRequest) || repo.record.ID != "" {
		t.Fatalf("invalid invitation error=%v record=%+v", err, repo.record)
	}
}

func TestInvitationCreateRespectsRepositoryScopeDenial(t *testing.T) {
	t.Parallel()
	repo := &memoryInvitationRepository{createError: auth.ErrInvitationScopeDenied}
	mailer := &testutil.MemoryMailer{}
	service := newInvitationService(t, repo, mailer, testutil.NewFakeClock(time.Unix(1, 0)))
	_, err := service.Create(context.Background(), auth.Identity{
		InstitutionID: "institution-1", UserID: "admin-1",
	}, auth.InvitationRequest{
		Email: "staff@example.test", Role: "lecturer", ScopeType: "department",
		DepartmentID: "department-1",
	})
	if !errors.Is(err, auth.ErrInvitationScopeDenied) || len(mailer.Sent()) != 0 {
		t.Fatalf("scope denial error=%v sent=%d", err, len(mailer.Sent()))
	}
}

func TestInvitationAcceptanceExpiresAndConsumesTokenOnce(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	repo := &memoryInvitationRepository{}
	mailer := &testutil.MemoryMailer{}
	service := newInvitationService(t, repo, mailer, testutil.NewFakeClock(now))
	_, err := service.Create(context.Background(),
		auth.Identity{InstitutionID: "institution-1", UserID: "admin-1"},
		auth.InvitationRequest{Email: "staff@example.test", Role: "institution_admin", ScopeType: "institution"})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.SplitN(strings.SplitN(mailer.Sent()[0].Text, "account: ", 2)[1], ".", 2)[0]
	identity, err := service.Accept(context.Background(), "institution-1", token,
		"Invited Staff", "correct horse battery staple")
	if err != nil || identity.UserID != "invited-user" {
		t.Fatalf("accept = %+v, %v", identity, err)
	}
	if repo.displayName != "Invited Staff" || !auth.VerifyPassword(repo.passwordHash, "correct horse battery staple") {
		t.Fatal("acceptance did not persist account name and password hash")
	}
	if _, err := service.Accept(context.Background(), "institution-1", token,
		"Invited Staff", "correct horse battery staple"); !errors.Is(err, auth.ErrInvalidInvitation) {
		t.Fatalf("reused token error = %v", err)
	}

	expiredRepo := &memoryInvitationRepository{}
	expiredService := newInvitationService(t, expiredRepo, &testutil.MemoryMailer{}, testutil.NewFakeClock(now))
	_, err = expiredService.Create(context.Background(),
		auth.Identity{InstitutionID: "institution-1", UserID: "admin-1"},
		auth.InvitationRequest{Email: "expired@example.test", Role: "institution_admin", ScopeType: "institution"})
	if err != nil {
		t.Fatal(err)
	}
	expiredToken := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	if !bytes.Equal(expiredRepo.tokenHash, sha256Bytes(expiredToken)) {
		t.Fatal("unexpected deterministic token hash")
	}
	expiredRepo.record.ExpiresAt = now.Add(-time.Second)
	if _, err := expiredService.Accept(context.Background(), "institution-1", expiredToken,
		"Expired Staff", "correct horse battery staple"); !errors.Is(err, auth.ErrInvalidInvitation) {
		t.Fatalf("expired token error = %v", err)
	}
}

func sha256Bytes(value string) []byte {
	hash := sha256.Sum256([]byte(value))
	return hash[:]
}
