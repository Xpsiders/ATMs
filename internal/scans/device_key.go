package scans

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

func GenerateDeviceKey(random io.Reader) (string, []byte, error) {
	if random == nil {
		random = rand.Reader
	}
	var key [32]byte
	if _, err := io.ReadFull(random, key[:]); err != nil {
		return "", nil, fmt.Errorf("generate device key: %w", err)
	}
	plaintext := base64.RawURLEncoding.EncodeToString(key[:])
	hash := sha256.Sum256([]byte(plaintext))
	return plaintext, hash[:], nil
}

func HashDeviceKey(plaintext string) []byte {
	hash := sha256.Sum256([]byte(plaintext))
	return hash[:]
}
