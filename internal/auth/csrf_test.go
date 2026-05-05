package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCSRF_TokenDeterministicForSameID(t *testing.T) {
	c := NewCSRF([]byte("0123456789abcdef0123456789abcdef"))
	id := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	require.Len(t, id, 64)
	assert.Equal(t, c.Token(id), c.Token(id))
}

func TestCSRF_DifferentSecretsDifferentTokens(t *testing.T) {
	id := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	a := NewCSRF([]byte("a-secret-a-secret-a-secret-aaaa"))
	b := NewCSRF([]byte("b-secret-b-secret-b-secret-bbbb"))
	assert.NotEqual(t, a.Token(id), b.Token(id))
}

func TestCSRF_EnsureIDIssuesCookie(t *testing.T) {
	c := NewCSRF([]byte("0123456789abcdef0123456789abcdef"))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	id, err := c.EnsureID(w, r)
	require.NoError(t, err)
	require.Len(t, id, 64)
	cookies := w.Result().Cookies()
	require.NotEmpty(t, cookies)
	assert.Equal(t, CSRFCookieName, cookies[0].Name)
	assert.Equal(t, id, cookies[0].Value)
}

func TestCSRF_ValidateAcceptsCorrectToken(t *testing.T) {
	c := NewCSRF([]byte("0123456789abcdef0123456789abcdef"))
	id := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	tok := c.Token(id)

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: id})
	r.Header.Set(CSRFHeader, tok)
	assert.True(t, c.Validate(r))
}

func TestCSRF_ValidateRejectsWrongToken(t *testing.T) {
	c := NewCSRF([]byte("0123456789abcdef0123456789abcdef"))
	id := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: id})
	r.Header.Set(CSRFHeader, "00000000")
	assert.False(t, c.Validate(r))
}

func TestCSRF_ValidateRejectsMissingCookie(t *testing.T) {
	c := NewCSRF([]byte("0123456789abcdef0123456789abcdef"))
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set(CSRFHeader, "anything")
	assert.False(t, c.Validate(r))
}
