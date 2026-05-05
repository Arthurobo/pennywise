// Package auth implements password hashing, sessions, CSRF, and middleware.
package auth

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// MinPasswordLength is the minimum acceptable password length for new accounts.
const MinPasswordLength = 8

// bcryptCost balances login latency against brute-force resistance.
const bcryptCost = 12

// Hash returns the bcrypt hash of plaintext.
func Hash(plaintext string) (string, error) {
	if len(plaintext) < MinPasswordLength {
		return "", fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	}
	b, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(b), nil
}

// Verify reports whether plaintext matches the bcrypt hash.
func Verify(hash, plaintext string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext))
	return err == nil
}

// IsCostUpgradable returns true if the stored hash uses a lower cost than the current target.
func IsCostUpgradable(hash string) bool {
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil || errors.Is(err, bcrypt.ErrHashTooShort) {
		return false
	}
	return cost < bcryptCost
}
