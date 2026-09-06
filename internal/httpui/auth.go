package httpui

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"time"
)

const (
	sessionCookieName = "connarr_session"
	// sessionDuration is deliberately long: Connarr is a single-user app
	// meant to stay signed in on the devices you actually use, not a
	// multi-tenant service where a short session limits blast radius.
	sessionDuration = 30 * 24 * time.Hour
)

func newSessionToken() (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

// requestIsHTTPS reports whether the browser's connection to Connarr (not
// necessarily this process's own listener) was HTTPS, so the session
// cookie's Secure flag reflects reality whether Connarr terminates TLS
// itself or sits behind a reverse proxy that does.
func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// startSession creates and persists a new session, then sets its cookie on
// the response. Used after both first-run setup and a normal login.
func (server *Server) startSession(w http.ResponseWriter, r *http.Request) error {
	token, err := newSessionToken()
	if err != nil {
		return err
	}
	expires := time.Now().Add(sessionDuration)
	if err := server.inv.Store().CreateSession(token, expires); err != nil {
		return err
	}
	setSessionCookie(w, r, token, expires)
	return nil
}

// authGate enforces Connarr's single-admin login in front of every route.
// It fails open when no durable store is available (Store() nil) because
// sessions cannot be persisted at all without one — every real deployment
// opens a store before constructing Server (see cmd/connarr/main.go), so
// this only ever applies to handler-level tests built without one.
func (server *Server) authGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if server.inv == nil || server.inv.Store() == nil {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") || r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		cfg := server.inv.Config()
		if cfg.Auth.Username == "" {
			if r.URL.Path == "/setup" {
				next.ServeHTTP(w, r)
				return
			}
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		if r.URL.Path == "/login" {
			next.ServeHTTP(w, r)
			return
		}
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			if valid, _ := server.inv.Store().SessionValid(cookie.Value); valid {
				next.ServeHTTP(w, r)
				return
			}
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
}

type setupPageData struct {
	Username string
	Error    string
}

// setupPage handles Connarr's one-time "create the admin account" screen.
// Reachable without a session only while no account exists yet — once one
// does, authGate stops exempting this path and a signed-in visit here just
// bounces home instead of allowing a second account to overwrite the first.
func (server *Server) setupPage(w http.ResponseWriter, r *http.Request) {
	cfg := server.inv.Config()
	if cfg.Auth.Username != "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodGet {
		_ = renderTemplate(w, server.setupTpl, setupPageData{})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	if password != r.FormValue("confirm") {
		_ = renderTemplate(w, server.setupTpl, setupPageData{Username: username, Error: "Passwords do not match."})
		return
	}
	if err := server.inv.SetCredentials(username, password); err != nil {
		_ = renderTemplate(w, server.setupTpl, setupPageData{Username: username, Error: err.Error()})
		return
	}
	if server.inv.Store() != nil {
		if err := server.startSession(w, r); err != nil {
			http.Error(w, "account created, but the session could not be started: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

type loginPageData struct {
	Username string
	Error    string
}

func (server *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	cfg := server.inv.Config()
	if cfg.Auth.Username == "" {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodGet {
		_ = renderTemplate(w, server.loginTpl, loginPageData{})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	if !strings.EqualFold(username, cfg.Auth.Username) || !cfg.VerifyPassword(password) {
		_ = renderTemplate(w, server.loginTpl, loginPageData{Username: username, Error: "Incorrect username or password."})
		return
	}
	if err := server.startSession(w, r); err != nil {
		http.Error(w, "signed in, but the session could not be started: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (server *Server) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil && server.inv.Store() != nil {
		_ = server.inv.Store().DeleteSession(cookie.Value)
	}
	clearSessionCookie(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

type settingsPageData struct {
	Error   string
	Success bool
}

func (server *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_ = renderTemplate(w, server.settingsTpl, settingsPageData{})
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
	if !cfg.VerifyPassword(r.FormValue("current_password")) {
		_ = renderTemplate(w, server.settingsTpl, settingsPageData{Error: "Current password is incorrect."})
		return
	}
	newPassword := r.FormValue("new_password")
	if newPassword != r.FormValue("confirm") {
		_ = renderTemplate(w, server.settingsTpl, settingsPageData{Error: "New passwords do not match."})
		return
	}
	if err := server.inv.SetCredentials(cfg.Auth.Username, newPassword); err != nil {
		_ = renderTemplate(w, server.settingsTpl, settingsPageData{Error: err.Error()})
		return
	}
	if server.inv.Store() != nil {
		_ = server.inv.Store().DeleteAllSessions()
		_ = server.startSession(w, r)
	}
	_ = renderTemplate(w, server.settingsTpl, settingsPageData{Success: true})
}
