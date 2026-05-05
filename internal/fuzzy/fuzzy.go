// Package fuzzy provides the small string-matching helpers Pennywise uses to
// reconcile LLM-suggested category and ledger names against the user's actual
// records. The strategy is deliberately simple:
//
//  1. Case-insensitive exact match wins.
//  2. Substring match (either direction) wins next.
//  3. Otherwise pick the closest item by Levenshtein distance, but only if
//     that distance is ≤ MaxDistance.
//
// Anything beyond that is "no match" and the caller falls back to a default
// (typically the seeded "Other" category).
package fuzzy

import "strings"

// MaxDistance is the Levenshtein distance threshold for accepting a fuzzy
// match. 3 forgives most casual typos without conflating distinct names.
const MaxDistance = 3

// Best returns the item from candidates that best matches needle, or "" if
// nothing reasonable matches.
//
// The string returned is the original candidate (with original casing).
func Best(needle string, candidates []string) string {
	needle = strings.TrimSpace(needle)
	if needle == "" || len(candidates) == 0 {
		return ""
	}
	lowerNeedle := strings.ToLower(needle)

	// Pass 1: exact case-insensitive match.
	for _, c := range candidates {
		if strings.EqualFold(c, needle) {
			return c
		}
	}
	// Pass 2: substring either direction.
	for _, c := range candidates {
		lc := strings.ToLower(c)
		if strings.Contains(lc, lowerNeedle) || strings.Contains(lowerNeedle, lc) {
			return c
		}
	}
	// Pass 3: closest by Levenshtein distance, capped at MaxDistance.
	best := ""
	bestDist := MaxDistance + 1
	for _, c := range candidates {
		d := Levenshtein(strings.ToLower(c), lowerNeedle)
		if d < bestDist {
			best = c
			bestDist = d
		}
	}
	if bestDist > MaxDistance {
		return ""
	}
	return best
}

// Levenshtein returns the edit distance between a and b. O(len(a)*len(b))
// time, O(min(len(a),len(b))) space.
func Levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	ar, br := []rune(a), []rune(b)
	la, lb := len(ar), len(br)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	// Always iterate the shorter dimension as the inner loop.
	if la > lb {
		ar, br = br, ar
		la, lb = lb, la
	}
	prev := make([]int, la+1)
	curr := make([]int, la+1)
	for i := 0; i <= la; i++ {
		prev[i] = i
	}
	for j := 1; j <= lb; j++ {
		curr[0] = j
		for i := 1; i <= la; i++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			del := prev[i] + 1
			ins := curr[i-1] + 1
			sub := prev[i-1] + cost
			curr[i] = min3(del, ins, sub)
		}
		prev, curr = curr, prev
	}
	return prev[la]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
