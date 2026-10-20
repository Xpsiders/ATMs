package sessions

import (
	"testing"
	"time"

	"attendance-api/internal/testutil"
)

func TestDeriveCodeDeterministicAndScoped(t *testing.T) {
	t.Parallel()
	secret := []byte("stable-test-secret")
	code := DeriveCode(secret, "session-a", 42)
	if len(code) != 6 || code != DeriveCode(secret, "session-a", 42) {
		t.Fatalf("code is not stable six-digit output: %q", code)
	}
	if code == DeriveCode(secret, "session-a", 43) {
		t.Fatal("code did not change between windows")
	}
	if code == DeriveCode(secret, "session-b", 42) {
		t.Fatal("code did not change between sessions")
	}
}

func TestRotatingCodeAcceptsCurrentAndPreviousOnly(t *testing.T) {
	t.Parallel()
	const window = time.Minute
	clock := testutil.NewFakeClock(time.Unix(120, 0))
	now := clock.Now()
	current := DeriveCode([]byte("secret"), "session", 2)
	previous := DeriveCode([]byte("secret"), "session", 1)
	older := DeriveCode([]byte("secret"), "session", 0)

	for _, tc := range []struct {
		name string
		code string
		want bool
	}{{"current", current, true}, {"previous", previous, true}, {"older", older, false}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := VerifyRotatingCode([]byte("secret"), "session", tc.code, now, window); got != tc.want {
				t.Fatalf("VerifyRotatingCode() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCurrentCodeUsesFakeClockAndReturnsRemainingSeconds(t *testing.T) {
	t.Parallel()
	clock := testutil.NewFakeClock(time.Unix(125, 0))
	code, remaining, err := CurrentCode([]byte("secret"), "session", clock.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if code != DeriveCode([]byte("secret"), "session", 2) || remaining != 55 {
		t.Fatalf("CurrentCode() = (%s, %d), want window 2 and 55s", code, remaining)
	}
	if _, _, err := CurrentCode([]byte("secret"), "session", clock.Now(), 0); err == nil {
		t.Fatal("CurrentCode accepted a zero-length window")
	}
	if _, _, err := CurrentCode([]byte("secret"), "session", clock.Now(), time.Millisecond); err == nil {
		t.Fatal("CurrentCode accepted a sub-second window")
	}
	if VerifyRotatingCode([]byte("secret"), "session", "bad", clock.Now(), time.Minute) {
		t.Fatal("malformed rotating code accepted")
	}
	if VerifyRotatingCode([]byte("secret"), "session", "123456", clock.Now(), 0) {
		t.Fatal("rotating code accepted an invalid window")
	}
}

func TestCurrentCodeUsesFloorForPreEpochTimes(t *testing.T) {
	t.Parallel()
	now := time.Unix(-1, 0)
	code, remaining, err := CurrentCode([]byte("secret"), "session", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if code != DeriveCode([]byte("secret"), "session", -1) || remaining != 1 {
		t.Fatalf("pre-epoch code window = (%s, %d), want index -1 and 1s", code, remaining)
	}
}

func TestStaticCodeExpiry(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	expires := now.Add(time.Minute)
	if !VerifyStaticCode("123456", "123456", expires, now) {
		t.Fatal("valid unexpired static code rejected")
	}
	if VerifyStaticCode("123456", "123456", expires, expires) {
		t.Fatal("code at its expiry time accepted")
	}
	if VerifyStaticCode("123456", "654321", expires, now) {
		t.Fatal("wrong static code accepted")
	}
	if VerifyStaticCode("", "", expires, now) {
		t.Fatal("empty static code accepted")
	}
	if VerifyStaticCode("123456", "123", expires, now) {
		t.Fatal("static code with a different length accepted")
	}
}
