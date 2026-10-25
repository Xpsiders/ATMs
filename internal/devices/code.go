package devices

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
)

// pairingAlphabet is Crockford base32 (no I, L, O, U) so a code is easy to read
// and type. It has 32 symbols, which divides 256 evenly, so mapping a random
// byte with %32 is unbiased.
const pairingAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// pairingCodeLength is the number of significant characters in a pairing code.
const pairingCodeLength = 8

// GeneratePairingCode returns a short, human-flashable pairing code in the
// display form "XXXX-XXXX" together with the SHA-256 hash of its canonical form.
// Only the hash is stored; the plaintext is shown to the admin once.
func GeneratePairingCode(random io.Reader) (string, []byte, error) {
	if random == nil {
		random = rand.Reader
	}
	raw := make([]byte, pairingCodeLength)
	if _, err := io.ReadFull(random, raw); err != nil {
		return "", nil, fmt.Errorf("generate pairing code: %w", err)
	}
	canonical := make([]byte, pairingCodeLength)
	for i, b := range raw {
		canonical[i] = pairingAlphabet[int(b)%len(pairingAlphabet)]
	}
	display := string(canonical[:4]) + "-" + string(canonical[4:])
	hash := HashPairingCode(string(canonical))
	return display, hash, nil
}

// NormalizePairingCode reduces a code to its canonical form: uppercase, with any
// hyphens, spaces, or other separators removed. The firmware may send the code
// in any casing or grouping and still match.
func NormalizePairingCode(code string) string {
	var builder strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(code)) {
		if (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

// HashPairingCode returns the SHA-256 hash of a canonical pairing code.
func HashPairingCode(canonical string) []byte {
	hash := sha256.Sum256([]byte(canonical))
	return hash[:]
}
