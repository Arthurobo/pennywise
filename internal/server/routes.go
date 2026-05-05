package server

import (
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Arthurobo/pennywise/internal/auth"
	"github.com/Arthurobo/pennywise/internal/handlers"
	staticpkg "github.com/Arthurobo/pennywise/internal/static"
)

// Mount returns the fully wired chi router.
func Mount(h *handlers.Handler) http.Handler {
	r := chi.NewRouter()

	r.Use(requestLogger)
	r.Use(recoverer)
	r.Use(secureHeaders)
	r.Use(methodOverride)
	r.Use(auth.AttachCSRF(h.CSRF))
	r.Use(auth.AttachSession(h.Sessions, h.Q))

	// Public, never gated by setup or auth.
	staticFS, _ := fs.Sub(staticpkg.FS, ".")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// First-run setup: only reachable until the owner is created.
	r.Group(func(r chi.Router) {
		r.Use(auth.RejectIfInitialized(h.IsInitializedFn()))
		r.Get("/setup", h.SetupForm)
		r.With(auth.VerifyCSRF(h.CSRF)).Post("/setup", h.Setup)
	})

	// Public, but only available once setup is done.
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireSetup(h.IsInitializedFn()))
		r.Get("/login", h.LoginForm)
		r.With(auth.VerifyCSRF(h.CSRF)).Post("/login", h.Login)
	})

	// Authenticated routes.
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireSetup(h.IsInitializedFn()))
		r.Use(auth.RequireAuth)
		r.Use(auth.VerifyCSRF(h.CSRF))

		r.Get("/", redirect("/dashboard"))
		r.Get("/dashboard", h.Dashboard)

		r.Post("/logout", h.Logout)

		// Expenses
		r.Get("/expenses", h.Expenses)
		r.Get("/expenses/new", h.NewExpenseForm)
		r.Post("/expenses", h.CreateExpense)
		r.Post("/expenses/bulk-delete", h.BulkDeleteExpenses)
		r.Post("/expenses/parse-receipt", h.ParseReceipt)
		r.Get("/expenses/trash", h.Trash)
		r.Post("/expenses/trash/empty", h.EmptyTrash)
		r.Post("/expenses/trash/{id}/restore", h.RestoreExpense)
		r.Post("/expenses/trash/{id}/delete", h.HardDeleteExpense)
		r.Get("/expenses/{id}/edit", h.EditExpenseForm)
		r.Post("/expenses/{id}", h.UpdateExpense)
		r.Delete("/expenses/{id}", h.DeleteExpense)

		// Ledgers
		r.Get("/ledgers", h.Ledgers)
		r.Get("/ledgers/new", h.NewLedgerForm)
		r.Post("/ledgers", h.CreateLedger)
		r.Get("/ledgers/{id}", h.LedgerDetail)
		r.Get("/ledgers/{id}/edit", h.EditLedgerForm)
		r.Post("/ledgers/{id}", h.UpdateLedger)
		r.Post("/ledgers/{id}/archive", h.ArchiveLedger)
		r.Delete("/ledgers/{id}", h.DeleteLedger)

		// Reports + export
		r.Get("/reports", h.Reports)
		r.Get("/export/csv", h.ExportCSV)

		// Settings
		r.Get("/settings", h.Settings)
		r.Post("/settings/profile", h.UpdateProfile)
		r.Post("/settings/password", h.UpdatePassword)
		r.Post("/settings/preferences", h.UpdatePreferences)
		r.Post("/settings/dashboard-url", h.UpdateDashboardURL)
		r.Post("/settings/trash-retention", h.UpdateTrashRetention)
		r.Get("/settings/categories", h.SettingsCategories)
		r.Post("/settings/categories", h.CreateCategory)
		r.Post("/settings/categories/{id}", h.UpdateCategory)
		r.Post("/settings/categories/{id}/archive", h.ArchiveCategory)

		// v2: LLM provider tab
		r.Get("/settings/llm", h.SettingsLLM)
		r.Get("/settings/llm/models", h.LLMModelOptions)
		r.Post("/settings/llm", h.SaveLLMConfig)
		r.Post("/settings/llm/test", h.TestLLMConnection)
		r.Post("/settings/llm/disable", h.DisableLLM)

		// v2: Telegram bot tab
		r.Get("/settings/telegram", h.SettingsTelegram)
		r.Get("/settings/telegram/status", h.PollPairingStatus)
		r.Post("/settings/telegram/bot", h.SaveTelegramBot)
		r.Post("/settings/telegram/pair", h.GenerateTelegramPairing)
		r.Post("/settings/telegram/enable", h.SetTelegramEnabled)
		r.Post("/settings/telegram/disable", h.SetTelegramEnabled)
		r.Post("/settings/telegram/disconnect", h.DisconnectTelegramChat)
		r.Post("/settings/telegram/remove", h.RemoveTelegramBot)

		// JSON chart data
		r.Get("/api/charts/daily-spending", h.DailySpendingJSON)
	})

	return r
}

func redirect(to string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, to, http.StatusSeeOther)
	}
}
