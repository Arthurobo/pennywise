// Package templates parses and executes the embedded HTML templates.
//
// Page composition: every page is registered as its own *template.Template that
// includes the base layout plus all partials. In production the templates are
// parsed once from embedded FS at startup; in development they are reparsed on
// each render so edits are visible without a rebuild.
package templates

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"path"
	"sort"
	"strings"
	"sync"
)

//go:embed layouts/*.html partials/*.html pages/*.html
var FS embed.FS

// Renderer holds the parsed template registry. Use New() to build it.
type Renderer struct {
	mu      sync.RWMutex
	pages   map[string]*template.Template
	funcs   template.FuncMap
	devMode bool
	source  fs.FS
}

// New parses every page template against the layout. devMode causes a fresh
// parse on every Render call (so disk edits are picked up immediately).
func New(devMode bool, funcs template.FuncMap, devSource fs.FS) (*Renderer, error) {
	r := &Renderer{
		funcs:   mergeFuncs(funcs),
		devMode: devMode,
	}
	if devMode && devSource != nil {
		r.source = devSource
	} else {
		r.source = FS
	}
	if !devMode {
		if err := r.parseAll(); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Render writes a page to w. data is exposed as `.` in templates.
func (r *Renderer) Render(w io.Writer, page string, data any) error {
	tpl, err := r.lookup(page)
	if err != nil {
		return err
	}
	return tpl.ExecuteTemplate(w, "base", data)
}

// RenderTemplate executes a specific named template within page (used for HTMX
// partials where the layout chrome would be a duplicate).
func (r *Renderer) RenderTemplate(w io.Writer, page, name string, data any) error {
	tpl, err := r.lookup(page)
	if err != nil {
		return err
	}
	return tpl.ExecuteTemplate(w, name, data)
}

func (r *Renderer) lookup(page string) (*template.Template, error) {
	if r.devMode {
		if err := r.parseAll(); err != nil {
			return nil, err
		}
	}
	r.mu.RLock()
	tpl, ok := r.pages[page]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("template %q not registered", page)
	}
	return tpl, nil
}

func (r *Renderer) parseAll() error {
	layouts, err := globFS(r.source, "layouts/*.html")
	if err != nil {
		return err
	}
	partials, err := globFS(r.source, "partials/*.html")
	if err != nil && !strings.Contains(err.Error(), "no matching") {
		return err
	}
	pages, err := globFS(r.source, "pages/*.html")
	if err != nil {
		return err
	}
	if len(pages) == 0 {
		return fmt.Errorf("no page templates found")
	}

	registry := make(map[string]*template.Template, len(pages))
	for _, p := range pages {
		name := strings.TrimSuffix(path.Base(p), ".html")
		set := template.New("base").Funcs(r.funcs)

		files := append([]string{}, layouts...)
		files = append(files, partials...)
		files = append(files, p)

		for _, f := range files {
			b, err := fs.ReadFile(r.source, f)
			if err != nil {
				return fmt.Errorf("read %s: %w", f, err)
			}
			if _, err := set.New(path.Base(f)).Parse(string(b)); err != nil {
				return fmt.Errorf("parse %s: %w", f, err)
			}
		}
		registry[name] = set
	}

	r.mu.Lock()
	r.pages = registry
	r.mu.Unlock()
	if r.devMode {
		slog.Debug("templates reparsed", "count", len(registry))
	}
	return nil
}

func globFS(f fs.FS, pattern string) ([]string, error) {
	matches, err := fs.Glob(f, pattern)
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

func mergeFuncs(extra template.FuncMap) template.FuncMap {
	out := template.FuncMap{}
	for k, v := range BuiltinFuncs {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}
