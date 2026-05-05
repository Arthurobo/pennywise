package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// secretsHKDFInfo binds the derived key to its purpose. Changing this string
// invalidates every previously-encrypted secret on disk, so don't.
const secretsHKDFInfo = "pennywise-secrets-aes-gcm-v1"

// SecretBox encrypts and decrypts secrets at rest (LLM API keys, Telegram bot
// tokens) using AES-256-GCM. The key is derived from the long-lived session
// secret via HKDF-SHA256 so v1's CSRF/HMAC use and v2's encryption use don't
// share the same physical key, only the same root.
type SecretBox struct {
	aead cipher.AEAD
}

// NewSecretBox derives the encryption key from rootSecret and constructs the
// AEAD. rootSecret should be the bytes returned by config.Load (32 random
// bytes by default).
func NewSecretBox(rootSecret []byte) (*SecretBox, error) {
	if len(rootSecret) < 16 {
		return nil, errors.New("secret box: root secret must be ≥ 16 bytes")
	}
	key := make([]byte, 32)
	r := hkdf.New(sha256.New, rootSecret, nil, []byte(secretsHKDFInfo))
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("secret box: derive key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secret box: aes init: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secret box: gcm init: %w", err)
	}
	return &SecretBox{aead: aead}, nil
}

// Seal encrypts plaintext and returns base64(nonce || ciphertext || tag).
// Empty input maps to empty output so we can store a "no value" sentinel
// without distinguishing it from a genuinely empty secret.
func (s *SecretBox) Seal(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secret box: nonce: %w", err)
	}
	ct := s.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

// Open is the inverse of Seal. Empty input returns empty output.
func (s *SecretBox) Open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", fmt.Errorf("secret box: decode: %w", err)
	}
	if len(raw) < s.aead.NonceSize()+s.aead.Overhead() {
		return "", errors.New("secret box: ciphertext too short")
	}
	nonce, ct := raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():]
	pt, err := s.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("secret box: decrypt: %w", err)
	}
	return string(pt), nil
}
