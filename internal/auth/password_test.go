package auth_test

import (
	"testing"

	"attendance-api/internal/auth"
)

func TestPasswordHashAndVerify(t *testing.T) {
	t.Parallel()
	hash, err := auth.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "correct horse battery staple" || !auth.VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("password hash did not verify")
	}
	if auth.VerifyPassword(hash, "incorrect password") {
		t.Fatal("incorrect password verified")
	}
	if _, err := auth.HashPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
}
