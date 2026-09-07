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

	RemovalError                   string
	RemovalSuccess                 bool
	AutoRemovalEnabled             bool
	AutoRemoveUnassociatedTorrents bool
	DryRun                         bool
}

func (server *Server) settingsData() settingsPageData {
	cfg := server.inv.Config()
	return settingsPageData{
		TMDBAPIKey:                     cfg.TMDB.APIKey,
		TMDBEnabled:                    cfg.TMDB.APIKey != "",
		AutoRemovalEnabled:             cfg.Removal.AutoEnabled,
		AutoRemoveUnassociatedTorrents: cfg.Removal.AutoRemoveUnassociatedTorrents,
		DryRun:                         cfg.Removal.DryRun,
	}
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

// setRemovalSettings saves the global automatic-removal switches. Automatic
// removal also still requires each service's own "Allow automatic removal"
// checkbox (see the service edit form) — this only controls whether the
// evaluation runs at all, whether it's allowed to touch torrents with no
// known owner, and whether any removal (manual or automatic) actually
// deletes anything or just simulates it. Checkboxes only submit when
// checked, so an absent field means false, the same as an HTML form always
// behaves.
func (server *Server) setRemovalSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	autoEnabled := r.FormValue("auto_enabled") == "on"
	autoRemoveUnassociated := r.FormValue("auto_remove_unassociated_torrents") == "on"
	dryRun := r.FormValue("dry_run") == "on"
	data := server.settingsData()
	if err := server.inv.SetRemovalSettings(autoEnabled, autoRemoveUnassociated, dryRun); err != nil {
		data.RemovalError = err.Error()
		_ = renderTemplate(w, server.settingsTpl, data)
		return
	}
	data = server.settingsData()
	data.RemovalSuccess = true
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
