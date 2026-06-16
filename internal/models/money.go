// Package models holds domain types and helpers that aren't database rows.
package models

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ParseAmount accepts a user-entered amount like "12.50" or "12,5" or "1234"
// and returns the value in minor units (cents). It rejects negative values and
// inputs with more than two decimal places.
func ParseAmount(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("amount required")
	}
	// Allow comma as decimal separator and strip thousands separators.
	s = strings.ReplaceAll(s, ",", ".")
	s = strings.TrimPrefix(s, "+")
	if strings.HasPrefix(s, "-") {
		return 0, errors.New("amount must be positive")
	}

	parts := strings.Split(s, ".")
	switch len(parts) {
	case 1:
		major, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid amount: %s", s)
		}
		if major > maxMajor {
			return 0, errors.New("amount too large")
		}
		return major * 100, nil
	case 2:
		major, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid amount: %s", s)
		}
		minorStr := parts[1]
		if len(minorStr) == 0 {
			minorStr = "0"
		}
		if len(minorStr) == 1 {
			minorStr += "0"
		}
		if len(minorStr) > 2 {
			return 0, errors.New("amounts may have at most two decimal places")
		}
		minor, err := strconv.ParseInt(minorStr, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid amount: %s", s)
		}
		if major > maxMajor {
			return 0, errors.New("amount too large")
		}
		return major*100 + minor, nil
	default:
		return 0, fmt.Errorf("invalid amount: %s", s)
	}
}

// FormatAmount renders cents as a major-unit decimal string with two places
// and thousands separators, e.g. 125000 → "1,250.00". It does not include a
// currency symbol.
func FormatAmount(cents int64) string {
	neg := ""
	if cents < 0 {
		neg = "-"
		cents = -cents
	}
	major := cents / 100
	minor := cents % 100
	return fmt.Sprintf("%s%s.%02d", neg, formatInt(major), minor)
}

func formatInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 || n < 0 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append(parts, s[len(s)-3:])
		s = s[:len(s)-3]
	}
	parts = append(parts, s)
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, ",")
}

// FormatMoney renders cents with a leading currency symbol, e.g. "$12.50".
func FormatMoney(cents int64, symbol string) string {
	return symbol + FormatAmount(cents)
}

// 1e15 cents = $10 trillion. More than enough for personal finance.
const maxMajor = 100_000_000_000_000
