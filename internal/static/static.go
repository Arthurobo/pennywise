// Package static embeds the CSS, JS, and image assets into the binary.
package static

import "embed"

//go:embed css/output.css js/htmx.min.js js/chart.min.js js/app.js
var FS embed.FS
