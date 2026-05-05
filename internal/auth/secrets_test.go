package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecretBox_RoundTrip(t *testing.T) {
	box, err := NewSecretBox([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)

	for _, plain := range []string{
		"sk-proj-abc",
		"a very long secret with spaces and 12345 numbers !@#$%",
		"unicode: ₦ € 日本語",
	} {
		sealed, err := box.Seal(plain)
		require.NoError(t, err)
		assert.NotEqual(t, plain, sealed)
		assert.NotContains(t, sealed, plain)

		opened, err := box.Open(sealed)
		require.NoError(t, err)
		assert.Equal(t, plain, opened)
	}
}

func TestSecretBox_EmptyPassesThrough(t *testing.T) {
	box, _ := NewSecretBox([]byte("0123456789abcdef0123456789abcdef"))
	out, err := box.Seal("")
	require.NoError(t, err)
	assert.Equal(t, "", out)
	out, err = box.Open("")
	require.NoError(t, err)
	assert.Equal(t, "", out)
}

func TestSecretBox_DifferentRootsCannotOpen(t *testing.T) {
	a, _ := NewSecretBox([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	b, _ := NewSecretBox([]byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"))
	sealed, err := a.Seal("hello")
	require.NoError(t, err)
	_, err = b.Open(sealed)
	require.Error(t, err)
}

func TestSecretBox_NonceIsUnique(t *testing.T) {
	box, _ := NewSecretBox([]byte("0123456789abcdef0123456789abcdef"))
	a, _ := box.Seal("hello")
	b, _ := box.Seal("hello")
	assert.NotEqual(t, a, b, "same plaintext must produce different ciphertexts")
}

func TestSecretBox_RejectsShortRoot(t *testing.T) {
	_, err := NewSecretBox([]byte("short"))
	require.Error(t, err)
}

func TestSecretBox_TamperRejected(t *testing.T) {
	box, _ := NewSecretBox([]byte("0123456789abcdef0123456789abcdef"))
	sealed, _ := box.Seal("payload")
	// Flip a byte in the middle of the ciphertext.
	bad := []byte(sealed)
	bad[len(bad)/2] = bad[len(bad)/2] ^ 0x01
	_, err := box.Open(string(bad))
	require.Error(t, err)
	assert.True(t,
		strings.Contains(err.Error(), "decrypt") || strings.Contains(err.Error(), "decode"),
		"got: %v", err)
}
