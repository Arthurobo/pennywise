package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAmount(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"0", 0, false},
		{"12", 1200, false},
		{"12.5", 1250, false},
		{"12.50", 1250, false},
		{"12.05", 1205, false},
		{"12,50", 1250, false},  // comma decimal
		{"+12.50", 1250, false}, // explicit plus
		{"  12.50  ", 1250, false},
		{"-1", 0, true},
		{"abc", 0, true},
		{"12.345", 0, true}, // too many decimals
		{"", 0, true},
		{"1.", 100, false}, // trailing dot is OK (treated as .00)
		{"1.5.0", 0, true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := ParseAmount(c.in)
			if c.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestFormatAmount(t *testing.T) {
	cases := []struct {
		cents int64
		want  string
	}{
		{0, "0.00"},
		{5, "0.05"},
		{50, "0.50"},
		{1250, "12.50"},
		{100000, "1000.00"},
		{-1250, "-12.50"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, FormatAmount(c.cents))
	}
}

func TestFormatMoney(t *testing.T) {
	assert.Equal(t, "$12.50", FormatMoney(1250, "$"))
	assert.Equal(t, "₦1000.00", FormatMoney(100000, "₦"))
}

func TestRoundTrip(t *testing.T) {
	for _, s := range []string{"0.01", "1.99", "100.00", "12345.67"} {
		c, err := ParseAmount(s)
		require.NoError(t, err)
		assert.Equal(t, s, FormatAmount(c), "round-trip %s", s)
	}
}
