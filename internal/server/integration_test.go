package server_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Arthurobo/pennywise/internal/auth"
	"github.com/Arthurobo/pennywise/internal/config"
	pwdb "github.com/Arthurobo/pennywise/internal/db"
	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
	"github.com/Arthurobo/pennywise/internal/handlers"
	"github.com/Arthurobo/pennywise/internal/server"
	"github.com/Arthurobo/pennywise/internal/templates"
)

// bootTestServer spins up a real handler stack against a fresh on-disk SQLite
// file. It returns an httptest.Server and a cleanup func.
func bootTestServer(t *testing.T) (*httptest.Server, *handlers.Handler) {
	t.Helper()

	dir := t.TempDir()
	cfg := config.Config{
		DataDir:       dir,
		Host:          "127.0.0.1",
		Port:          0,
		SessionSecret: []byte("0123456789abcdef0123456789abcdef"),
		LogLevel:      "warn",
		Env:           "production",
	}

	db, err := pwdb.Open(filepath.Join(dir, "pennywise.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	q := sqlcgen.New(db)
	csrf := auth.NewCSRF(cfg.SessionSecret)
	sm := auth.NewManager(q)
	sm.StartSweeper(context.Background())

	secrets, err := auth.NewSecretBox(cfg.SessionSecret)
	require.NoError(t, err)
	h := handlers.New(cfg, db, q, nil, sm, csrf, secrets, nil, "test", "test", "test")
	require.NoError(t, h.WarmInitFlag(context.Background()))

	rdr, err := templates.New(false, h.TemplateFuncs(), nil)
	require.NoError(t, err)
	h.Renderer = rdr

	ts := httptest.NewServer(server.Mount(h))
	t.Cleanup(ts.Close)
	return ts, h
}

// completeSetup runs the first-run setup flow against ts and returns a
// configured cookie jar that's authenticated.
func completeSetup(t *testing.T, ts *httptest.Server) *http.Client {
	t.Helper()
	jar, _ := newJar()
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// GET /setup to mint the CSRF cookie + token.
	resp, err := client.Get(ts.URL + "/setup")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	tok := extractCSRF(t, string(body))

	form := url.Values{}
	form.Set("_csrf", tok)
	form.Set("display_name", "Tester")
	form.Set("email", "tester@example.com")
	form.Set("password", "hunter2hunter2")
	form.Set("password_confirm", "hunter2hunter2")
	form.Set("currency_code", "USD")
	form.Set("currency_symbol", "$")
	form.Set("timezone", "UTC")

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/setup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	require.Equal(t, "/dashboard", resp.Header.Get("Location"))
	return client
}

func extractCSRF(t *testing.T, body string) string {
	t.Helper()
	const marker = `csrf-token" content="`
	idx := strings.Index(body, marker)
	require.NotEqual(t, -1, idx)
	rest := body[idx+len(marker):]
	end := strings.Index(rest, `"`)
	require.NotEqual(t, -1, end)
	tok := rest[:end]
	require.Len(t, tok, 64)
	return tok
}

func newJar() (http.CookieJar, error) {
	// Stdlib net/http/cookiejar requires a proper PSL list; we simulate one with
	// a no-op jar that accepts any cookie via http.CookieJar interface.
	return &simpleJar{cookies: map[string][]*http.Cookie{}}, nil
}

type simpleJar struct {
	cookies map[string][]*http.Cookie
}

func (j *simpleJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	merged := j.cookies[u.Host]
	keep := merged[:0]
	for _, existing := range merged {
		drop := false
		for _, c := range cookies {
			if existing.Name == c.Name {
				drop = true
				break
			}
		}
		if !drop {
			keep = append(keep, existing)
		}
	}
	for _, c := range cookies {
		if c.MaxAge < 0 {
			continue
		}
		keep = append(keep, c)
	}
	j.cookies[u.Host] = keep
}

func (j *simpleJar) Cookies(u *url.URL) []*http.Cookie {
	return j.cookies[u.Host]
}

func TestSetupAndLoginFlow(t *testing.T) {
	ts, _ := bootTestServer(t)
	client := completeSetup(t, ts)

	// Dashboard reachable
	resp, err := client.Get(ts.URL + "/dashboard")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "Hi, Tester.")

	// /setup is now 404
	resp2, err := client.Get(ts.URL + "/setup")
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode)
}

func TestCreateExpenseAndList(t *testing.T) {
	ts, _ := bootTestServer(t)
	client := completeSetup(t, ts)

	// Get CSRF token from /expenses/new
	resp, err := client.Get(ts.URL + "/expenses/new")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	tok := extractCSRF(t, string(body))

	form := url.Values{}
	form.Set("_csrf", tok)
	form.Set("amount", "12.50")
	form.Set("description", "Coffee")
	form.Set("spent_at", "2026-05-03")
	form.Set("category_id", "1")

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/expenses", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)

	// List should include "Coffee"
	resp, err = client.Get(ts.URL + "/expenses")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ = io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "Coffee")
	assert.Contains(t, string(body), "$12.50")
}

func TestCSRFRejectsMissingToken(t *testing.T) {
	ts, _ := bootTestServer(t)
	client := completeSetup(t, ts)

	form := url.Values{}
	form.Set("amount", "1.00")
	form.Set("description", "x")
	form.Set("spent_at", "2026-05-03")

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/expenses", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestCSVExportContainsRow(t *testing.T) {
	ts, _ := bootTestServer(t)
	client := completeSetup(t, ts)

	// Create one expense
	resp, _ := client.Get(ts.URL + "/expenses/new")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	tok := extractCSRF(t, string(body))
	form := url.Values{}
	form.Set("_csrf", tok)
	form.Set("amount", "5.00")
	form.Set("description", "Latte")
	form.Set("spent_at", "2026-05-03")
	form.Set("category_id", "1")
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/expenses", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, _ = client.Do(req)
	resp.Body.Close()

	// Export
	resp, err := client.Get(ts.URL + "/export/csv")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	csvBody, _ := io.ReadAll(resp.Body)
	s := string(csvBody)
	assert.Contains(t, s, "id,spent_at,amount,currency,spent_on")
	assert.Contains(t, s, "Latte")
	assert.Contains(t, s, "5.00")
	assert.Contains(t, s, "USD")
}
