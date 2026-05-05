package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
)

// CSRFCookieName carries an opaque per-browser identifier used to derive the CSRF token.
const CSRFCookieName = "pennywise_csrf"

// CSRFFormField is the form input name carrying the token.
const CSRFFormField = "_csrf"

// CSRFHeader is the header HTMX uses to carry the token.
const CSRFHeader = "X-CSRF-Token"

// CSRF derives tokens via HMAC over an opaque per-browser identifier so the
// server holds no per-request state. The identifier is stored in a non-HttpOnly
// cookie; the token is HMAC(secret, identifier). Validation recomputes the
// expected token and compares in constant time.
type CSRF struct {
	secret []byte
}

func NewCSRF(secret []byte) *CSRF { return &CSRF{secret: secret} }

// EnsureID returns the CSRF identifier for this request, issuing a new cookie if absent.
func (c *CSRF) EnsureID(w http.ResponseWriter, r *http.Request) (string, error) {
	if cookie, err := r.Cookie(CSRFCookieName); err == nil && len(cookie.Value) == 64 {
		return cookie.Value, nil
	}
	id, err := newCSRFID()
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: false, // readable by JS/HTMX
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   60 * 60 * 24 * 365,
	})
	return id, nil
}

// Token returns the canonical token for the given identifier.
func (c *CSRF) Token(id string) string {
	mac := hmac.New(sha256.New, c.secret)
	mac.Write([]byte(id))
	return hex.EncodeToString(mac.Sum(nil))
}

// Validate reports whether the submitted token matches the identifier in the cookie.
func (c *CSRF) Validate(r *http.Request) bool {
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil || len(cookie.Value) != 64 {
		return false
	}
	submitted := r.Header.Get(CSRFHeader)
	if submitted == "" {
		submitted = r.PostFormValue(CSRFFormField)
	}
	if submitted == "" {
		return false
	}
	expected := c.Token(cookie.Value)
	return hmac.Equal([]byte(expected), []byte(submitted))
}

func newCSRFID() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
