package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanJSON(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", `{"a":1}`, `{"a":1}`},
		{"leading_whitespace", "  \n  {\"a\":1}  ", `{"a":1}`},
		{"markdown_fence", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"markdown_fence_uppercase", "```JSON\n{\"a\":1}\n```", `{"a":1}`},
		{"markdown_fence_nolang", "```\n{\"a\":1}\n```", `{"a":1}`},
		{"prose_before", "Sure! Here it is: {\"a\":1}", `{"a":1}`},
		{"prose_after", `{"a":1} — let me know if you need anything`, `{"a":1}`},
		{"prose_both", "OK here's the JSON: {\"a\":1} — done!", `{"a":1}`},
		{"nested", `{"a":{"b":2}}`, `{"a":{"b":2}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, cleanJSON(c.in))
		})
	}
}

func TestParseResponse_SingleExpense(t *testing.T) {
	raw := `{"intent":"expenses","expenses":[{"amount":500000,"description":"Fuel","category_hint":"Transport","ledger_hint":"","spent_at":"now","confidence":0.95}],"query":null,"reason":""}`
	p, err := ParseResponse(raw)
	require.NoError(t, err)
	assert.Equal(t, "expenses", p.Intent)
	require.Len(t, p.Expenses, 1)
	require.NotNil(t, p.Expenses[0].Amount)
	assert.Equal(t, int64(500000), *p.Expenses[0].Amount)
	assert.Equal(t, "Fuel", p.Expenses[0].Description)
	assert.Equal(t, 0.95, p.Expenses[0].Confidence)
	assert.Nil(t, p.Query)
}

func TestParseResponse_BatchExpenses(t *testing.T) {
	raw := `{"intent":"expenses","expenses":[
		{"amount":1500,"description":"Lunch","category_hint":"Food","ledger_hint":"","spent_at":"now","confidence":0.95},
		{"amount":4000,"description":"Groceries","category_hint":"Food","ledger_hint":"","spent_at":"now","confidence":0.95}
	],"query":null,"reason":""}`
	p, err := ParseResponse(raw)
	require.NoError(t, err)
	assert.Equal(t, "expenses", p.Intent)
	require.Len(t, p.Expenses, 2)
	assert.Equal(t, int64(1500), *p.Expenses[0].Amount)
	assert.Equal(t, int64(4000), *p.Expenses[1].Amount)
}

func TestParseResponse_Query(t *testing.T) {
	raw := `{"intent":"query","expenses":[],"query":{"intent":"month","n":0,"ledger_hint":""},"reason":""}`
	p, err := ParseResponse(raw)
	require.NoError(t, err)
	assert.Equal(t, "query", p.Intent)
	assert.Empty(t, p.Expenses)
	require.NotNil(t, p.Query)
	assert.Equal(t, "month", p.Query.Intent)

	raw = `{"intent":"query","expenses":[],"query":{"intent":"last_n","n":10,"ledger_hint":""},"reason":""}`
	p, err = ParseResponse(raw)
	require.NoError(t, err)
	require.NotNil(t, p.Query)
	assert.Equal(t, "last_n", p.Query.Intent)
	assert.Equal(t, 10, p.Query.N)

	raw = `{"intent":"query","expenses":[],"query":{"intent":"ledger_summary","n":0,"ledger_hint":"Trip"},"reason":""}`
	p, err = ParseResponse(raw)
	require.NoError(t, err)
	require.NotNil(t, p.Query)
	assert.Equal(t, "ledger_summary", p.Query.Intent)
	assert.Equal(t, "Trip", p.Query.LedgerHint)
}

func TestParseResponse_Unclear(t *testing.T) {
	raw := `{"intent":"unclear","expenses":[],"query":null,"reason":"This doesn't look like an expense."}`
	p, err := ParseResponse(raw)
	require.NoError(t, err)
	assert.Equal(t, "unclear", p.Intent)
	assert.Empty(t, p.Expenses)
	assert.Nil(t, p.Query)
	assert.Equal(t, "This doesn't look like an expense.", p.Reason)
}

func TestParseResponse_NullAmountInItem(t *testing.T) {
	raw := `{"intent":"expenses","expenses":[{"amount":null,"description":"Something","category_hint":"Other","ledger_hint":"","spent_at":"now","confidence":0.4}],"query":null,"reason":""}`
	p, err := ParseResponse(raw)
	require.NoError(t, err)
	require.Len(t, p.Expenses, 1)
	assert.Nil(t, p.Expenses[0].Amount)
}

func TestParseResponse_FromMarkdownFence(t *testing.T) {
	raw := "```json\n{\"intent\":\"expenses\",\"expenses\":[{\"amount\":1250,\"description\":\"Coffee\",\"category_hint\":\"Food\",\"ledger_hint\":\"\",\"spent_at\":\"now\",\"confidence\":0.9}],\"query\":null,\"reason\":\"\"}\n```"
	p, err := ParseResponse(raw)
	require.NoError(t, err)
	require.Len(t, p.Expenses, 1)
	assert.Equal(t, "Coffee", p.Expenses[0].Description)
}

func TestParseResponse_FromTrailingProse(t *testing.T) {
	raw := `Here's your JSON: {"intent":"expenses","expenses":[{"amount":1000,"description":"Tea","category_hint":"Food","ledger_hint":"","spent_at":"now","confidence":0.8}],"query":null,"reason":""} — happy to help!`
	p, err := ParseResponse(raw)
	require.NoError(t, err)
	require.Len(t, p.Expenses, 1)
	assert.Equal(t, "Tea", p.Expenses[0].Description)
}

func TestParseResponse_FailsOnMalformed(t *testing.T) {
	raw := `{"intent":"expenses", oops}`
	_, err := ParseResponse(raw)
	require.Error(t, err)
	var pe *ParseError
	assert.ErrorAs(t, err, &pe)
	assert.NotEmpty(t, pe.Raw)
}

func TestParseResponse_FailsOnEmpty(t *testing.T) {
	_, err := ParseResponse("")
	require.Error(t, err)
}

func TestParseResponse_TruncatesRawInError(t *testing.T) {
	// 5 KB of printable garbage — the %q escape preserves length so we can
	// observe the 500-byte truncation in the formatted error message.
	garbage := make([]byte, 5000)
	for i := range garbage {
		garbage[i] = 'x'
	}
	long := "not-json " + string(garbage)
	_, err := ParseResponse(long)
	require.Error(t, err)
	assert.Less(t, len(err.Error()), 800, "ParseError must truncate large raw payloads")
}
