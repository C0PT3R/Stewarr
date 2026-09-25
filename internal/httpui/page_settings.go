package httpui

import (
	"net/http"
	"strconv"

	"stewarr/internal/config"
	"stewarr/internal/services/tmdb"
)

type settingsPageData struct {
	Error   string
	Success bool

	TMDBError   string
	TMDBSuccess bool
	TMDBAPIKey  string
	TMDBEnabled bool

	IMDbError   string
	IMDbSuccess bool
	IMDbEnabled bool

	RemovalError                   string
	RemovalSuccess                 bool
	AutoMode                       string
	AutoRemoveUnassociatedTorrents bool
	DryRun                         bool
	TorrentCarePercent             float64
	AutoUnmonitor                  bool
	AutoExcludeFromImportLists     bool
	AutoRemoveIncompleteTorrents   bool
}

func (server *Server) settingsData() settingsPageData {
	cfg := server.inv.Config()
	autoMode := cfg.Removal.AutoMode
	if autoMode == "" {
		autoMode = config.RemovalAutoDisabled
	}
	return settingsPageData{
		TMDBAPIKey:                     cfg.TMDB.APIKey,
		TMDBEnabled:                    cfg.TMDB.APIKey != "",
		IMDbEnabled:                    !cfg.IMDb.Disabled,
		AutoMode:                       autoMode,
		AutoRemoveUnassociatedTorrents: cfg.Removal.AutoRemoveUnassociatedTorrents,
		DryRun:                         cfg.Removal.DryRun,
		TorrentCarePercent:             cfg.Removal.TorrentCarePercent,
		AutoUnmonitor:                  cfg.Removal.AutoUnmonitor,
		AutoExcludeFromImportLists:     cfg.Removal.AutoExcludeFromImportLists,
		AutoRemoveIncompleteTorrents:   cfg.Removal.AutoRemoveIncompleteTorrents,
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

// setIMDbEnabled toggles IMDb ratings enrichment — no key field, since
// the dataset is free and keyless, unlike TMDB.
func (server *Server) setIMDbEnabled(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	data := server.settingsData()
	if err := server.inv.SetIMDbEnabled(r.FormValue("imdb_enabled") == "1"); err != nil {
		data.IMDbError = err.Error()
		_ = renderTemplate(w, server.settingsTpl, data)
		return
	}
	data = server.settingsData()
	data.IMDbSuccess = true
	_ = renderTemplate(w, server.settingsTpl, data)
}

// setRemovalSettings saves the global automatic-removal switches. Automatic
// removal also still requires each service's own "Allow automatic removal"
// checkbox (see the service edit form) — auto_mode controls whether the
// evaluation task runs at all (Disabled), runs and applies its filters but
// never submits anything (Confirm), or runs and submits (Auto);
// auto_remove_unassociated_torrents/dry_run are unrelated checkboxes that
// only submit when checked, so an absent field means false.
func (server *Server) setRemovalSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	autoMode := r.FormValue("auto_mode")
	autoRemoveUnassociated := r.FormValue("auto_remove_unassociated_torrents") == "on"
	dryRun := r.FormValue("dry_run") == "on"
	autoUnmonitor := r.FormValue("auto_unmonitor") == "on"
	autoExcludeFromImportLists := r.FormValue("auto_exclude_from_import_lists") == "on"
	autoRemoveIncomplete := r.FormValue("auto_remove_incomplete_torrents") == "on"
	data := server.settingsData()
	// Unlike the checkboxes above (an absent field unambiguously means
	// off), an absent or empty torrent_care_percent has no such natural
	// default — treat it as "leave unchanged" the same way an unrelated
	// credential field is preserved on the service-edit form, rather than
	// silently resetting it every time an unrelated removal setting is saved.
	torrentCarePercent := data.TorrentCarePercent
	if raw := r.FormValue("torrent_care_percent"); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			data.RemovalError = "torrent_care_percent must be a number"
			_ = renderTemplate(w, server.settingsTpl, data)
			return
		}
		torrentCarePercent = parsed
	}
	if err := server.inv.SetRemovalSettings(autoMode, autoRemoveUnassociated, dryRun, torrentCarePercent, autoUnmonitor, autoExcludeFromImportLists, autoRemoveIncomplete); err != nil {
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
