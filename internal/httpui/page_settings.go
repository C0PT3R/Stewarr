package httpui

import (
	"net/http"

	"connarr/internal/integrations/tmdb"
)

type settingsPageData struct {
	Error   string
	Success bool

	TMDBError   string
	TMDBSuccess bool
	TMDBAPIKey  string
	TMDBEnabled bool
}

func (server *Server) settingsData() settingsPageData {
	cfg := server.inv.Config()
	return settingsPageData{TMDBAPIKey: cfg.TMDB.APIKey, TMDBEnabled: cfg.TMDB.APIKey != ""}
}

func (server *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_ = renderTemplate(w, server.settingsTpl, server.settingsData())
}

// changePassword rotates the admin account's password and, since a
// credential rotation should actually revoke access, signs out every other
// session before re-establishing one for the browser that made the change.
func (server *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	cfg := server.inv.Config()
	data := server.settingsData()
	if !cfg.VerifyPassword(r.FormValue("current_password")) {
		data.Error = "Current password is incorrect."
		_ = renderTemplate(w, server.settingsTpl, data)
		return
	}
	newPassword := r.FormValue("new_password")
	if newPassword != r.FormValue("confirm") {
		data.Error = "New passwords do not match."
		_ = renderTemplate(w, server.settingsTpl, data)
		return
	}
	if err := server.inv.SetCredentials(cfg.Auth.Username, newPassword); err != nil {
		data.Error = err.Error()
		_ = renderTemplate(w, server.settingsTpl, data)
		return
	}
	if server.inv.Store() != nil {
		_ = server.inv.Store().DeleteAllSessions()
		_ = server.startSession(w, r)
	}
	data = server.settingsData()
	data.Success = true
	_ = renderTemplate(w, server.settingsTpl, data)
}

// setTMDBAPIKey saves (or, with an empty value, clears) the TMDB
// enrichment API key. There is no separate on/off switch — an empty key
// is how TMDB enrichment is disabled.
func (server *Server) setTMDBAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	data := server.settingsData()
	if err := server.inv.SetTMDBAPIKey(r.FormValue("tmdb_api_key")); err != nil {
		data.TMDBError = err.Error()
		_ = renderTemplate(w, server.settingsTpl, data)
		return
	}
	data = server.settingsData()
	data.TMDBSuccess = true
	_ = renderTemplate(w, server.settingsTpl, data)
}

// testTMDBAPIKey checks a candidate API key against TMDB directly — no
// config mutation, no persistence — the same "test before you save" step
// the add-service wizard already offers for the other services.
func (server *Server) testTMDBAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	apiKey := r.FormValue("tmdb_api_key")
	if apiKey == "" {
		http.Error(w, "an API key is required to test", http.StatusBadRequest)
		return
	}
	if err := tmdb.New(apiKey).WithContext(r.Context()).Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
