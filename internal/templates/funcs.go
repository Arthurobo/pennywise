package templates

import (
	"fmt"
	"html/template"
	"strings"
	"time"
)

// BuiltinFuncs are template helpers always available regardless of caller.
// Caller-supplied funcs (e.g. money formatting bound to the owner's currency)
// are merged on top.
var BuiltinFuncs = template.FuncMap{
	"upper": strings.ToUpper,
	"lower": strings.ToLower,
	"title": func(s string) string { return strings.Title(s) }, //nolint:staticcheck // intentional
	"add":   func(a, b int) int { return a + b },
	"sub":   func(a, b int) int { return a - b },
	"now":   func() int64 { return time.Now().Unix() },

	// dict assembles ad-hoc maps for partial calls:
	//   {{ template "x" (dict "A" 1 "B" 2) }}
	"dict": func(pairs ...any) (map[string]any, error) {
		if len(pairs)%2 != 0 {
			return nil, fmt.Errorf("dict requires an even number of arguments")
		}
		m := make(map[string]any, len(pairs)/2)
		for i := 0; i < len(pairs); i += 2 {
			k, ok := pairs[i].(string)
			if !ok {
				return nil, fmt.Errorf("dict keys must be strings (arg %d)", i)
			}
			m[k] = pairs[i+1]
		}
		return m, nil
	},
	// default returns fallback if v is the zero value of its type.
	"default": func(v any, fallback any) any {
		switch x := v.(type) {
		case nil:
			return fallback
		case string:
			if x == "" {
				return fallback
			}
		case int:
			if x == 0 {
				return fallback
			}
		case int64:
			if x == 0 {
				return fallback
			}
		}
		return v
	},
	"yes": func(b bool, yes string) string {
		if b {
			return yes
		}
		return ""
	},
	"either": func(b bool, yes, no string) string {
		if b {
			return yes
		}
		return no
	},
	"truncate": func(s string, n int) string {
		if len(s) <= n {
			return s
		}
		return s[:n] + "…"
	},
	"percent": func(num, denom int64) string {
		if denom == 0 {
			return "0"
		}
		return fmt.Sprintf("%d", (num*100)/denom)
	},
}
