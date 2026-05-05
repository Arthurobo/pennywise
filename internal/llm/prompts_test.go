package llm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemPrompt_RendersUserContext(t *testing.T) {
	out, err := SystemPrompt(PromptContext{
		CurrencySymbol: "₦",
		CurrencyCode:   "NGN",
		Timezone:       "Africa/Lagos",
		NowRFC3339:     "2026-05-03T12:00:00+01:00",
		Categories:     []string{"Food", "Transport"},
		Ledgers:        []string{"Trip"},
	})
	require.NoError(t, err)
	assert.Contains(t, out, "₦ (NGN)")
	assert.Contains(t, out, "Africa/Lagos")
	assert.Contains(t, out, "2026-05-03T12:00:00+01:00")
	assert.Contains(t, out, "- Food")
	assert.Contains(t, out, "- Transport")
	assert.Contains(t, out, "- Trip")
	assert.Contains(t, out, "100*5000", "should inject MinorMultiplier from currency code")
}

func TestSystemPrompt_NoSubUnitCurrency(t *testing.T) {
	out, err := SystemPrompt(PromptContext{
		CurrencySymbol: "¥",
		CurrencyCode:   "JPY",
		Timezone:       "Asia/Tokyo",
		NowRFC3339:     "2026-05-03T12:00:00+09:00",
	})
	require.NoError(t, err)
	assert.Contains(t, out, "1*5000",
		"JPY has no sub-unit so MinorMultiplier should be 1")
}

func TestSystemPrompt_ActiveLedgerRendered(t *testing.T) {
	out, _ := SystemPrompt(PromptContext{
		CurrencySymbol: "$", CurrencyCode: "USD",
		ActiveLedger: "Travel Fund",
	})
	assert.Contains(t, out, `Active ledger: "Travel Fund"`)

	out, _ = SystemPrompt(PromptContext{
		CurrencySymbol: "$", CurrencyCode: "USD",
	})
	assert.Contains(t, out, "Active ledger: none")
}

func TestSystemPrompt_AlwaysIncludesGuardrails(t *testing.T) {
	out, _ := SystemPrompt(PromptContext{
		CurrencySymbol: "$", CurrencyCode: "USD",
	})
	for _, must := range []string{
		"You MUST respond with a single JSON object",
		"Never include any text outside the JSON object",
		"Never wrap the JSON in markdown",
	} {
		assert.True(t, strings.Contains(out, must),
			"prompt must contain guardrail %q", must)
	}
}

func TestSystemPrompt_DescribesAllThreeIntents(t *testing.T) {
	// The unified prompt must describe every intent route the dispatcher
	// handles. If we drop one of these in a refactor, parsing will silently
	// fall back to "unclear" — this test catches that.
	out, _ := SystemPrompt(PromptContext{
		CurrencySymbol: "$", CurrencyCode: "USD",
	})
	for _, must := range []string{
		`"intent": "expenses" | "query" | "unclear"`,
		`"expenses": [`,
		`"query":`,
		`"reason":`,
	} {
		assert.True(t, strings.Contains(out, must),
			"prompt must describe schema fragment %q", must)
	}
}

func TestMinorMultiplierFor(t *testing.T) {
	cases := map[string]int{
		"USD": 100, "EUR": 100, "NGN": 100, "GBP": 100,
		"JPY": 1, "KRW": 1, "VND": 1, "XAF": 1, "RWF": 1,
		"jpy": 1, // case-insensitive
	}
	for code, want := range cases {
		assert.Equal(t, want, MinorMultiplierFor(code), "code=%s", code)
	}
}
