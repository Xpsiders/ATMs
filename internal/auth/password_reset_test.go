package auth

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

type resetTestClock struct {
	now time.Time
}

func (c resetTestClock) Now() time.Time { return c.now }

type passwordResetFake struct {
	createdHash []byte
	createdCode string
	expiresAt   time.Time
	resetHash   []byte
	password    string
	resetErr    error
}

func (f *passwordResetFake) CreatePasswordReset(_ context.Context, _, _ string, hash []byte, code string, expiry time.Time) error {
	f.createdHash = append([]byte(nil), hash...)
	f.createdCode = code
	f.expiresAt = expiry
	return nil
}

func (f *passwordResetFake) CompletePasswordReset(_ context.Context, _, _ string, hash []byte, _ time.Time, password string) error {
	f.resetHash = append([]byte(nil), hash...)
	f.password = password
	return f.resetErr
}

func TestPasswordResetForgotAndResetUseInjectedClockAndRandomSource(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 11, 12, 0, time.UTC)
	repository := &passwordResetFake{}
	service, err := NewPasswordResetService(repository, resetTestClock{now: now}, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetRandomSource(bytes.NewReader([]byte{0, 1, 226, 64})); err != nil {
		t.Fatal(err)
	}
	if err := service.Forgot(context.Background(), "institution", " Student@Example.Test "); err != nil {
		t.Fatal(err)
	}
	if repository.createdCode != "123456" || !repository.expiresAt.Equal(now.Add(PasswordResetLifetime)) {
		t.Fatalf("created code/expiry = %q, %v", repository.createdCode, repository.expiresAt)
	}
	if err := service.Reset(context.Background(), "institution", "STUDENT@example.test", "123456", "Correct-Horse-Battery-12"); err != nil {
		t.Fatal(err)
	}
	if !passwordResetCodeMatches(repository.createdHash, repository.resetHash) {
		t.Fatal("reset submission did not produce the same normalized HMAC")
	}
	if repository.password == "" {
		t.Fatal("password hash was not passed to the repository")
	}
}

func TestPasswordResetAttemptCap(t *testing.T) {
	for _, test := range []struct {
		attempts int
		want     bool
	}{{0, true}, {4, true}, {5, false}, {-1, false}} {
		if got := canAttemptPasswordReset(test.attempts); got != test.want {
			t.Errorf("canAttemptPasswordReset(%d) = %v, want %v", test.attempts, got, test.want)
		}
	}
}

func TestPasswordResetReturnsInvalidCodeWithoutLeakingRepositoryDetails(t *testing.T) {
	repository := &passwordResetFake{resetErr: ErrInvalidPasswordReset}
	service, err := NewPasswordResetService(repository, resetTestClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	err = service.Reset(context.Background(), "institution", "user@example.test", "000000", "Correct-Horse-Battery-12")
	if !errors.Is(err, ErrInvalidPasswordReset) {
		t.Fatalf("reset error = %v", err)
	}
}
