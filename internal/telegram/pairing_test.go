package telegram

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratePairingCode(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		c, err := GeneratePairingCode()
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(c, "PW-"))
		require.Len(t, c, 9, "PW- + 6 chars")
		assert.False(t, seen[c], "expect uniqueness across 100 generations: %s", c)
		seen[c] = true

		// Alphabet excludes 0/O/1/I.
		for _, r := range c[3:] {
			assert.False(t, r == '0' || r == 'O' || r == '1' || r == 'I',
				"alphabet must exclude visually-confusable chars: %s", c)
		}
	}
}

func TestNormalizePairingInput(t *testing.T) {
	cases := map[string]string{
		"PW-ABC123": "PW-ABC123",
		"pw-abc123": "PW-ABC123",
		"abc123":    "PW-ABC123",
		"  ABC123 ": "PW-ABC123",
	}
	for in, want := range cases {
		assert.Equal(t, want, NormalizePairingInput(in), "in=%q", in)
	}
}
