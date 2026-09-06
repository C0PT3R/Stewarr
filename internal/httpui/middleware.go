package httpui

import (
	"net/http"
	"net/url"
	"strings"
)

// sameOriginWrites is a CSRF-style deployment-safety guard, layered
// underneath — not instead of — the session-based login enforced by
// authGate (see auth.go).
func sameOriginWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, r.Host) {
					http.Error(w, "cross-origin write rejected", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
