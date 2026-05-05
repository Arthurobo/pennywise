package fuzzy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "ab", 1},
		{"kitten", "sitting", 3},
		{"flaw", "lawn", 2},
		{"food", "Food", 1}, // case-sensitive at this layer
	}
	for _, c := range cases {
		assert.Equal(t, c.want, Levenshtein(c.a, c.b), "Lev(%q, %q)", c.a, c.b)
	}
}

func TestBest_ExactMatch(t *testing.T) {
	assert.Equal(t, "Food", Best("Food", []string{"Food", "Transport"}))
	assert.Equal(t, "Food", Best("food", []string{"Food", "Transport"}), "should be case-insensitive")
}

func TestBest_Substring(t *testing.T) {
	assert.Equal(t, "Travel Fund", Best("travel", []string{"Other", "Travel Fund"}))
	assert.Equal(t, "Travel Fund", Best("Travel", []string{"Travel Fund"}))
}

func TestBest_TypoWithinDistance(t *testing.T) {
	assert.Equal(t, "Transport", Best("transprt", []string{"Food", "Transport"}))
	assert.Equal(t, "Transport", Best("transpot", []string{"Food", "Transport"}))
}

func TestBest_NoMatch(t *testing.T) {
	assert.Equal(t, "", Best("zzzzzzz", []string{"Food", "Transport"}))
	assert.Equal(t, "", Best("", []string{"Food", "Transport"}))
	assert.Equal(t, "", Best("Food", nil))
}

func TestBest_PrefersSubstringOverDistance(t *testing.T) {
	// "trav" is a substring of "Travel Fund" but also Lev distance 3 from
	// "Food". Substring should win.
	assert.Equal(t, "Travel Fund", Best("trav", []string{"Food", "Travel Fund"}))
}
