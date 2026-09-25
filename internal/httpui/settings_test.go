package httpui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"stewarr/internal/config"
	"stewarr/internal/inventory"
)

// TestSetIMDbEnabledPersistsAndRenders guards the actual wiring end to
// end: the checkbox posts to /settings/imdb, SetIMDbEnabled persists it,
// and the re-rendered page reflects the new state — no key field, unlike
// TMDB, since the dataset is free and keyless.
func TestSetIMDbEnabledPersistsAndRenders(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	inv := inventory.New(cfg, nil)
	inv.SetConfigPath(configPath)
	server, err := New(inv, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/settings/imdb", strings.NewReader(url.Values{"imdb_enabled": {"1"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "IMDb ratings enabled.") {
		t.Fatalf("expected the success message, got:\n%s", body)
	}
	if !strings.Contains(body, `name="imdb_enabled" value="1" checked`) {
		t.Fatalf("expected the checkbox to render checked, got:\n%s", body)
	}

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.IMDb.Disabled {
		t.Fatal("expected imdb.disabled to persist as false (enabled)")
	}

	recorder = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/settings/imdb", strings.NewReader(url.Values{}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(recorder, req)
	if !strings.Contains(recorder.Body.String(), "IMDb ratings disabled.") {
		t.Fatalf("expected the disabled message when the checkbox is omitted, got:\n%s", recorder.Body.String())
	}
}
