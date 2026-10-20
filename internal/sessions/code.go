package sessions

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"strconv"
	"time"
)

func DeriveCode(secret []byte, sessionID string, windowIndex int64) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(sessionID + strconv.FormatInt(windowIndex, 10)))
	sum := mac.Sum(nil)
	value := (uint32(sum[0])<<24 | uint32(sum[1])<<16 | uint32(sum[2])<<8 | uint32(sum[3])) % 1_000_000
	return fmt.Sprintf("%06d", value)
}

func CurrentCode(secret []byte, sessionID string, now time.Time, window time.Duration) (code string, secondsRemaining int, err error) {
	if window <= 0 {
		return "", 0, fmt.Errorf("code window must be positive")
	}
	windowSeconds := int64(window / time.Second)
	if windowSeconds < 1 {
		return "", 0, fmt.Errorf("code window must be at least one second")
	}
	unix := now.UTC().Unix()
	windowIndex, secondsIntoWindow := windowPosition(unix, windowSeconds)
	remaining := windowSeconds - secondsIntoWindow
	return DeriveCode(secret, sessionID, windowIndex), int(remaining), nil
}

func VerifyRotatingCode(secret []byte, sessionID, supplied string, now time.Time, window time.Duration) bool {
	if window <= 0 || window/time.Second < 1 || len(supplied) != 6 {
		return false
	}
	windowSeconds := int64(window / time.Second)
	windowIndex, _ := windowPosition(now.UTC().Unix(), windowSeconds)
	current := DeriveCode(secret, sessionID, windowIndex)
	previous := DeriveCode(secret, sessionID, windowIndex-1)
	return subtle.ConstantTimeCompare([]byte(supplied), []byte(current)) == 1 ||
		subtle.ConstantTimeCompare([]byte(supplied), []byte(previous)) == 1
}

func windowPosition(unixSeconds, windowSeconds int64) (int64, int64) {
	index := unixSeconds / windowSeconds
	offset := unixSeconds % windowSeconds
	if offset < 0 {
		index--
		offset += windowSeconds
	}
	return index, offset
}

func VerifyStaticCode(stored, supplied string, expiresAt, now time.Time) bool {
	if !VerifyStaticCodeUntil(stored, supplied, &expiresAt, now) {
		return false
	}
	return true
}

func VerifyStaticCodeUntil(stored, supplied string, expiresAt *time.Time, now time.Time) bool {
	if stored == "" || len(stored) != len(supplied) ||
		(expiresAt != nil && !now.Before(*expiresAt)) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(supplied)) == 1
}
