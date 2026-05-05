package telegram

import (
	"crypto/rand"
	"strings"
)

// PairingTTL is the time a generated pairing code stays valid.
const PairingTTL = 600 // seconds (10 minutes)

// PairingCodePrefix is the human-friendly prefix on every code.
const PairingCodePrefix = "PW-"

// pairingAlphabet is base-32-ish: uppercase letters + digits, minus
// visually-confusable I, O, 1, 0.
const pairingAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// GeneratePairingCode returns a code in the format PW-XXXXXX.
//
// Six characters from a 32-letter alphabet → ~1 billion possibilities. With a
// 10-minute TTL and no online enumeration vector, that's overkill for a
// single-tenant pairing. We use crypto/rand for hygiene anyway.
func GeneratePairingCode() (string, error) {
	const n = 6
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = pairingAlphabet[int(b)%len(pairingAlphabet)]
	}
	return PairingCodePrefix + string(out), nil
}

// NormalizePairingInput uppercases and prefix-strips whatever the user typed
// in the bot ("/start pw-abc123" or just "pw-abc123" or "PW-ABC123").
func NormalizePairingInput(s string) string {
	s = strings.TrimSpace(strings.ToUpper(s))
	if !strings.HasPrefix(s, PairingCodePrefix) {
		s = PairingCodePrefix + s
	}
	return s
}
