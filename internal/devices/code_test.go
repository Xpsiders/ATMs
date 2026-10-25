package devices

import (
	"bytes"
	"strings"
	"testing"
)

func TestGeneratePairingCodeFormatAndHash(t *testing.T) {
	t.Parallel()
	display, hash, err := GeneratePairingCode(bytes.NewReader(make([]byte, 8)))
	if err != nil {
		t.Fatal(err)
	}
	if display != "0000-0000" {
		t.Fatalf("display = %q, want 0000-0000", display)
	}
	// The stored hash must be the hash of the canonical (hyphen-free) code so a
	// firmware that sends it in any grouping still matches.
	if !bytes.Equal(hash, HashPairingCode(NormalizePairingCode(display))) {
		t.Fatal("hash does not match canonical code hash")
	}
	if len(hash) != 32 {
		t.Fatalf("hash length = %d, want 32", len(hash))
	}
}

func TestGeneratePairingCodeUsesCrockfordAlphabet(t *testing.T) {
	t.Parallel()
	// Bytes 0..7 map to the first eight alphabet symbols.
	display, _, err := GeneratePairingCode(bytes.NewReader([]byte{0, 1, 2, 3, 4, 5, 6, 7}))
	if err != nil {
		t.Fatal(err)
	}
	if display != "0123-4567" {
		t.Fatalf("display = %q, want 0123-4567", display)
	}
}

func TestNormalizePairingCodeStripsSeparatorsAndUppercases(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"k7qf-2m9x", " K7QF 2M9X ", "k7qf2m9x", "K7QF--2M9X"} {
		if got := NormalizePairingCode(input); got != "K7QF2M9X" {
			t.Fatalf("NormalizePairingCode(%q) = %q, want K7QF2M9X", input, got)
		}
	}
}

func TestHashPairingCodeIsDeterministic(t *testing.T) {
	t.Parallel()
	if !bytes.Equal(HashPairingCode("ABCD1234"), HashPairingCode("ABCD1234")) {
		t.Fatal("hash is not deterministic")
	}
	if bytes.Equal(HashPairingCode("ABCD1234"), HashPairingCode("ABCD1235")) {
		t.Fatal("different codes produced the same hash")
	}
	if strings.Contains(string(HashPairingCode("ABCD1234")), "ABCD") {
		t.Fatal("hash leaked the plaintext code")
	}
}
