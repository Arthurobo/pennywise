package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ParseError wraps a JSON unmarshal failure together with the (truncated) raw
// response so the caller can log enough context to diagnose without dumping
// kilobytes into the application log.
type ParseError struct {
	Raw string
	Err error
}

func (e *ParseError) Error() string {
	r := e.Raw
	if len(r) > 500 {
		r = r[:500] + "…[truncated]"
	}
	return fmt.Sprintf("llm parse: %v (raw: %q)", e.Err, r)
}

func (e *ParseError) Unwrap() error { return e.Err }

// Parse is the unified output of the single Pennywise LLM prompt.
//
// One LLM call now classifies a user message as one of:
//
//   - "expenses" — Expenses is populated (1 entry for a single expense, N for
//     a batch — multi-line OR natural-language like "yesterday I spent 5k on
//     fuel and 12k on lunch"). Query is nil.
//   - "query"    — Query is populated. Expenses is empty.
//   - "unclear"  — Reason carries a one-sentence explanation. Both empty.
//
// Single-and-batch unification lets the dispatcher take one path instead of
// three (regex fast-path, batch detection, LLM fallback). One call per
// message also means natural-language queries don't need a second
// classification round-trip.
type Parse struct {
	Intent   string        `json:"intent"`
	Expenses []ExpenseItem `json:"expenses"`
	Query    *QueryIntent  `json:"query,omitempty"`
	Reason   string        `json:"reason"`
}

// ExpenseItem is one parsed expense within a Parse.Expenses slice.
//
// Amount is *int64 so the model can legitimately return null when an item
// can't be quantified — we then skip that item with a "no amount" note
// rather than logging a zero-value row.
type ExpenseItem struct {
	Amount       *int64  `json:"amount"`
	Description  string  `json:"description"`
	CategoryHint string  `json:"category_hint"`
	LedgerHint   string  `json:"ledger_hint"`
	SpentAt      string  `json:"spent_at"`
	Confidence   float64 `json:"confidence"`
}

// QueryIntent is the structured form of a "how much…?" question.
type QueryIntent struct {
	Intent     string `json:"intent"` // today | week | month | year | last_n | ledger_summary | unclear
	N          int    `json:"n"`
	LedgerHint string `json:"ledger_hint"`
}

// ParseResponse is the defensive parser. It cleans the LLM's raw text (strips
// markdown fences, slices to the JSON object) before unmarshal so transient
// model misbehaviour doesn't break the dispatcher.
func ParseResponse(raw string) (Parse, error) {
	cleaned := cleanJSON(raw)
	if cleaned == "" {
		return Parse{}, &ParseError{Raw: raw, Err: errors.New("empty after cleanup")}
	}
	var out Parse
	if err := json.Unmarshal([]byte(cleaned), &out); err != nil {
		return Parse{}, &ParseError{Raw: raw, Err: err}
	}
	return out, nil
}

// cleanJSON normalizes LLM output for the unmarshal step.
//
//  1. Trim outer whitespace.
//  2. If the payload is wrapped in a ```fence``` block, take the inner body.
//  3. Slice from the first `{` to the last `}` to drop leading/trailing prose.
//
// Implemented with plain string operations — no regex — for clarity and
// trivial speed on the few-hundred-byte payloads we see.
func cleanJSON(raw string) string {
	s := strings.TrimSpace(raw)

	// Strip a markdown fence if present. Opening fence may carry a language
	// tag (```json, ```JSON, etc.) — we just consume to the end of the
	// opening line and then trim the trailing ```.
	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		} else {
			// Single-line ```{...}``` — strip the leading fence only.
			s = s[3:]
		}
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
		s = strings.TrimSpace(s)
	}

	// Drop anything before the first `{` and after the last `}`.
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		s = s[start : end+1]
	}
	return s
}
