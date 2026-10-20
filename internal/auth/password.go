package auth

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

var ErrWeakPassword = errors.New("password must contain between 12 and 72 bytes")

func HashPassword(password string) (string, error) {
	if len(password) < 12 || len(password) > 72 {
		return "", ErrWeakPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func VerifyPassword(encodedHash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(encodedHash), []byte(password)) == nil
}
