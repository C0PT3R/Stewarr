package inventory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"stewarr/internal/config"
	"stewarr/internal/model"
)

// TestProtectMediaTagsMovieAndRegistersKeepTag guards the whole path:
// the movie gets tagged directly in Radarr, and stewarr_keep is
// registered into Protection.KeepTags so the tag actually protects it in
// Stewarr's own valuation, not just existing inertly on the Radarr side.
func TestProtectMediaTagsMovieAndRegistersKeepTag(t *testing.T) {
	radarrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/tag":
			w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/tag":
			w.Write([]byte(`{"id":9,"label":"stewarr_keep"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie/42":
			w.Write([]byte(`{"id":42,"tags":[]}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v3/movie/42":
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected radarr request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer radarrSrv.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Services = []config.Service{{ID: "r1", Type: "radarr", Name: "Movies", URL: radarrSrv.URL}}
	service := New(cfg, nil)
	service.SetConfigPath(configPath)

	if err := service.ProtectMedia(context.Background(), model.Movie, 42, "r1"); err != nil {
		t.Fatal(err)
	}

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tag := range reloaded.Protection.KeepTags {
		if tag == "stewarr_keep" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected stewarr_keep to be registered in KeepTags, got %#v", reloaded.Protection.KeepTags)
	}
}

// TestProtectTorrentTagsTorrentAndRegistersKeepTag mirrors
// TestProtectMediaTagsMovieAndRegistersKeepTag for the qBittorrent path.
func TestProtectTorrentTagsTorrentAndRegistersKeepTag(t *testing.T) {
	tagged := false
	qbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/addTags" {
			t.Fatalf("unexpected qbittorrent request: %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("hashes") != "abc123" || r.Form.Get("tags") != "stewarr_keep" {
			t.Fatalf("unexpected form: %#v", r.Form)
		}
		tagged = true
		w.WriteHeader(http.StatusOK)
	}))
	defer qbSrv.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Services = []config.Service{{ID: "q1", Type: "qbittorrent", Name: "qB", URL: qbSrv.URL, APIKey: "token"}}
	service := New(cfg, nil)
	service.SetConfigPath(configPath)

	if err := service.ProtectTorrent(context.Background(), "abc123", "q1"); err != nil {
		t.Fatal(err)
	}
	if !tagged {
		t.Fatal("expected the torrent to be tagged")
	}

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tag := range reloaded.Protection.KeepTorrentTags {
		if tag == "stewarr_keep" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected stewarr_keep to be registered in KeepTorrentTags, got %#v", reloaded.Protection.KeepTorrentTags)
	}
}

// TestProtectMediaSkipsPersistWhenTagAlreadyRegistered guards the
// idempotent no-op: a second protect action must not keep appending
// duplicate config writes.
func TestProtectMediaSkipsPersistWhenTagAlreadyRegistered(t *testing.T) {
	radarrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/tag":
			w.Write([]byte(`[{"id":9,"label":"stewarr_keep"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie/42":
			w.Write([]byte(`{"id":42,"tags":[]}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v3/movie/42":
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected radarr request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer radarrSrv.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{},"protection":{"keep_tags":["stewarr_keep"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Services = []config.Service{{ID: "r1", Type: "radarr", Name: "Movies", URL: radarrSrv.URL}}
	service := New(cfg, nil)
	service.SetConfigPath(configPath)

	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ProtectMedia(context.Background(), model.Movie, 42, "r1"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("expected no config write when the tag was already registered, got:\nbefore: %s\nafter: %s", before, after)
	}
}
