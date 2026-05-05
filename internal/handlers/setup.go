package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/Arthurobo/pennywise/internal/auth"
	"github.com/Arthurobo/pennywise/internal/models"
	"github.com/Arthurobo/pennywise/internal/setupseed"
)

// SetupForm GET /setup
func (h *Handler) SetupForm(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, r, "setup", map[string]any{
		"Currencies": models.Currencies,
		"Timezones":  models.CommonTimezones,
		"Currency":   "USD",
		"Symbol":     "$",
	})
}

// Setup POST /setup
func (h *Handler) Setup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, "invalid form")
		return
	}

	email := strings.ToLower(formField(r, "email"))
	displayName := formField(r, "display_name")
	password := r.PostFormValue("password")
	confirm := r.PostFormValue("password_confirm")
	currencyCode := formField(r, "currency_code")
	currencySymbol := formField(r, "currency_symbol")
	timezone := formField(r, "timezone")

	formErr := func(msg string) {
		h.renderPage(w, r, "setup", map[string]any{
			"Currencies":  models.Currencies,
			"Timezones":   models.CommonTimezones,
			"Error":       msg,
			"Email":       email,
			"DisplayName": displayName,
			"Currency":    currencyCode,
			"Symbol":      currencySymbol,
			"Timezone":    timezone,
		})
	}

	if _, err := mail.ParseAddress(email); err != nil {
		formErr("Please enter a valid email address.")
		return
	}
	if displayName == "" {
		formErr("Display name is required.")
		return
	}
	if len(password) < auth.MinPasswordLength {
		formErr("Password must be at least 8 characters.")
		return
	}
	if password != confirm {
		formErr("Passwords do not match.")
		return
	}
	if currencyCode == "" {
		formErr("Pick a currency.")
		return
	}
	if c, ok := models.LookupCurrency(currencyCode); ok && currencySymbol == "" {
		currencySymbol = c.Symbol
	}
	if currencySymbol == "" {
		formErr("Currency symbol is required.")
		return
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		formErr("Invalid timezone.")
		return
	}

	hashed, err := auth.Hash(password)
	if err != nil {
		formErr(err.Error())
		return
	}

	ctx := r.Context()
	if err := setupseed.SeedInitialOwner(ctx, h.DB, h.Q, setupseed.OwnerData{
		Email:          email,
		PasswordHash:   hashed,
		DisplayName:    displayName,
		CurrencyCode:   currencyCode,
		CurrencySymbol: currencySymbol,
		Timezone:       timezone,
		DashboardURL:   fmt.Sprintf("http://%s", h.Cfg.Addr()),
	}); err != nil {
		if errors.Is(err, setupseed.ErrAlreadyInitialized) {
			// Owner exists; skip the redirect-to-dashboard race-friendly
			// path the original handler took.
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
			return
		}
		serverError(w, err)
		return
	}
	h.initialized.Store(true)

	if _, err := h.Sessions.Create(ctx, w, r); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// IsInitializedFn returns the closure used by the setup-required middleware.
func (h *Handler) IsInitializedFn() func(context.Context) bool {
	return func(ctx context.Context) bool { return h.initialized.Load() }
}
