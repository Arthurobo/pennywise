package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashAndVerify(t *testing.T) {
	hash, err := Hash("hunter2hunter")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(hash, "$2a$") || strings.HasPrefix(hash, "$2b$") || strings.HasPrefix(hash, "$2y$"))
	assert.True(t, Verify(hash, "hunter2hunter"))
	assert.False(t, Verify(hash, "wrong"))
}

func TestHashRejectsShortPasswords(t *testing.T) {
	_, err := Hash("short")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "8")
}

func TestVerifyRejectsTamperedHash(t *testing.T) {
	hash, err := Hash("hunter2hunter")
	require.NoError(t, err)
	assert.False(t, Verify(hash[:len(hash)-2]+"XX", "hunter2hunter"))
}

func TestEqualConstantTime(t *testing.T) {
	assert.True(t, EqualConstantTime("abc", "abc"))
	assert.False(t, EqualConstantTime("abc", "abd"))
	assert.False(t, EqualConstantTime("abc", "abcd"))
}
