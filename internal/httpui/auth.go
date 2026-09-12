package httpui

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookieName = "stewarr_session"
	// sessionDuration is deliberately long: Stewarr is a single-user app
	// meant to stay signed in on the devices you actually use, not a
	// multi-tenant service where a short session limits blast radius.
	sessionDuration = 30 * 24 * time.Hour
)

// loginLimiter throttles repeated failed login attempts per client address.
// Bcrypt's own per-attempt cost already slows brute-forcing, but does
// nothing to stop a sustained scripted run over hours; this adds an
// escalating lockout on top of it. It is intentionally in-memory and
// per-process — Stewarr has one admin account and one process, so nothing
// durable is lost by resetting on restart.
type loginLimiter struct {
	mu    sync.Mutex
	state map[string]*loginAttemptState
}

type loginAttemptState struct {
	failures    int
	lockedUntil time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{state: map[string]*loginAttemptState{}}
}

// locked reports whether key is currently locked out, and for how much
// longer.
func (limiter *loginLimiter) locked(key string) (time.Duration, bool) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	state := limiter.state[key]
	if state == nil {
		return 0, false
	}
	if remaining := time.Until(state.lockedUntil); remaining > 0 {
		return remaining, true
	}
	return 0, false
}

// recordFailure counts one failed attempt and, past a small threshold,
// starts an escalating lockout (15s per failure past the threshold, capped
// at 5 minutes) rather than an unlimited-attempt password oracle.
func (limiter *loginLimiter) recordFailure(key string) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	state := limiter.state[key]
	if state == nil {
		state = &loginAttemptState{}
		limiter.state[key] = state
	}
	state.failures++
	const freeAttempts = 4
	if state.failures > freeAttempts {
		backoff := time.Duration(state.failures-freeAttempts) * 15 * time.Second
		if backoff > 5*time.Minute {
			backoff = 5 * time.Minute
		}
		state.lockedUntil = time.Now().Add(backoff)
	}
}

// recordSuccess clears key's failure history after a correct login.
func (limiter *loginLimiter) recordSuccess(key string) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	delete(limiter.state, key)
}

// clientLoginKey identifies the caller for login throttling. It strips the
// port from RemoteAddr since the same browser reconnects on a new one every
// request; behind a reverse proxy every client shares one address (and
// therefore one lockout bucket), which is an acceptable tradeoff for a
// single-admin app rather than trusting a spoofable forwarded-for header.
func clientLoginKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func newSessionToken() (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

// requestIsHTTPS reports whether the browser's connection to Stewarr (not
// necessarily this process's own listener) was HTTPS, so the session
// cookie's Secure flag reflects reality whether Stewarr terminates TLS
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

// authGate enforces Stewarr's single-admin login in front of every route.
// It fails open when no durable store is available (Store() nil) because
// sessions cannot be persisted at all without one — every real deployment
// opens a store before constructing Server (see cmd/stewarr/main.go), so
// this only ever applies to handler-level tests built without one. That
// bypass is loud, not silent: the first request it affects logs a warning,
// so a future caller that unexpectedly ends up here in a real deployment
// has a visible signal something is wrong rather than a quietly open door.
func (server *Server) authGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if server.inv == nil || server.inv.Store() == nil {
			server.authBypassWarned.Do(func() {
				log.Printf("[http] WARNING: no durable store configured — authentication is disabled for every route")
			})
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

// setupPage handles Stewarr's one-time "create the admin account" screen.
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
	key := clientLoginKey(r)
	if remaining, locked := server.loginLimiter.locked(key); locked {
		_ = renderTemplate(w, server.loginTpl, loginPageData{Username: username, Error: fmt.Sprintf("Too many attempts. Try again in %d seconds.", int(remaining.Seconds())+1)})
		return
	}
	// Both checks always run, even when the username is already known to be
	// wrong: short-circuiting past VerifyPassword's bcrypt comparison would
	// make a wrong username return near-instantly while a right-username-
	// wrong-password takes bcrypt's cost — a timing side-channel that
	// discloses the admin username without ever guessing its password.
	usernameCorrect := strings.EqualFold(username, cfg.Auth.Username)
	passwordCorrect := cfg.VerifyPassword(password)
	if !usernameCorrect || !passwordCorrect {
		server.loginLimiter.recordFailure(key)
		_ = renderTemplate(w, server.loginTpl, loginPageData{Username: username, Error: "Incorrect username or password."})
		return
	}
	server.loginLimiter.recordSuccess(key)
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
