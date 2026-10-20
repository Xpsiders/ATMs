package auth_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"attendance-api/internal/auth"
	"attendance-api/internal/testutil"
)

type incrementingReader struct {
	next byte
}

func (r *incrementingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.next
		r.next++
	}
	return len(p), nil
}

func newTestTokens(t *testing.T, clock *testutil.FakeClock) (*auth.TokenManager, *testutil.MemoryRefreshStore) {
	t.Helper()
	store := testutil.NewMemoryRefreshStore()
	manager, err := auth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), clock, store)
	if err != nil {
		t.Fatal(err)
	}
	return manager, store
}

func TestAccessTokenIssueValidateExpiry(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	manager, _ := newTestTokens(t, clock)
	identity := auth.Identity{InstitutionID: "i1", UserID: "u1", StudentID: "s1", DepartmentID: "d1", Level: 3, ForcePassword: true}
	raw, err := manager.IssueAccess(identity)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := manager.ValidateAccess(raw)
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID != identity.UserID || claims.DepartmentID != identity.DepartmentID ||
		claims.Level != identity.Level || !claims.ForcePassword {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	clock.Advance(auth.AccessTokenLifetime)
	if _, err := manager.ValidateAccess(raw); err == nil {
		t.Fatal("expired token accepted")
	}
}

func TestAccessTokenRejectsBadSignatureAndShortSecret(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Unix(1_700_000_000, 0))
	manager, _ := newTestTokens(t, clock)
	raw, err := manager.IssueAccess(auth.Identity{InstitutionID: "i1", UserID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := auth.NewTokenManager([]byte("abcdef0123456789abcdef0123456789"), clock, testutil.NewMemoryRefreshStore())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.ValidateAccess(raw); err == nil {
		t.Fatal("token with invalid signature accepted")
	}
	if _, err := auth.NewTokenManager([]byte("short"), clock, testutil.NewMemoryRefreshStore()); err == nil {
		t.Fatal("short signing secret accepted")
	}
}

func TestRefreshRotationAndReuseRejection(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Unix(1_700_000_000, 0))
	manager, _ := newTestTokens(t, clock)
	if err := manager.SetRandomSource(&incrementingReader{}); err != nil {
		t.Fatal(err)
	}
	first, err := manager.IssueRefresh(context.Background(), auth.Identity{InstitutionID: "i1", UserID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	second, identity, err := manager.RotateRefresh(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	if identity.InstitutionID != "i1" || identity.UserID != "u1" {
		t.Fatalf("refresh returned wrong identity: %+v", identity)
	}
	if first == second {
		t.Fatal("refresh token did not rotate")
	}
	if _, _, err := manager.RotateRefresh(context.Background(), first); !errors.Is(err, auth.ErrInvalidRefresh) {
		t.Fatalf("reuse error = %v", err)
	}
	if _, _, err := manager.RotateRefresh(context.Background(), second); err != nil {
		t.Fatalf("replacement token could not be used: %v", err)
	}
}

func TestRefreshRotationIsAtomic(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Unix(1_700_000_000, 0))
	store := testutil.NewMemoryRefreshStore()
	manager, err := auth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), clock, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetRandomSource(&incrementingReader{}); err != nil {
		t.Fatal(err)
	}
	token, err := manager.IssueRefresh(context.Background(), auth.Identity{InstitutionID: "i1", UserID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := manager.RotateRefresh(context.Background(), token)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent rotations = %d, want 1", successes)
	}
}
