package scans

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestGenerateDeviceKey(t *testing.T) {
	t.Parallel()
	plaintext, hash, err := GenerateDeviceKey(bytes.NewReader(bytes.Repeat([]byte{0x5a}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(plaintext)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("device key is not 32-byte base64url: %q, %v", plaintext, err)
	}
	want := sha256.Sum256([]byte(plaintext))
	if !bytes.Equal(hash, want[:]) || !bytes.Equal(HashDeviceKey(plaintext), hash) {
		t.Fatal("device key hash does not match plaintext")
	}
	if _, _, err := GenerateDeviceKey(bytes.NewReader(nil)); err == nil {
		t.Fatal("short random input unexpectedly generated a key")
	}
}
