package handlers

import "encoding/json"

// jsonMust marshals v, returning "null" if marshaling somehow fails.
// We use it for trusted internal payloads embedded into HTML.
func jsonMust(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}
