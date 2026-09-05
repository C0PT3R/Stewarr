package inventory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"connarr/internal/config"
)

func TestAddIntegrationPersistsAndActivatesLiveWithoutConfigPath(t *testing.T) {
	service := New(config.Config{}, nil)
	err := service.AddIntegration(context.Background(), config.Integration{Type: "radarr", Name: "Movies", URL: "http://radarr:7878"})
	if err == nil {
		t.Fatal("expected AddIntegration to fail when no config path is set")
	}
}

func TestAddIntegrationSucceedsAndActivatesClient(t *testing.T) {
	radarrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"5.0.0"}`))
	}))
	defer radarrSrv.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	baseCfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	service := New(baseCfg, nil)
	service.SetConfigPath(configPath)

	if err := service.AddIntegration(context.Background(), config.Integration{Type: "radarr", Name: "Movies", URL: radarrSrv.URL, APIKey: "key"}); err != nil {
		t.Fatalf("AddIntegration failed: %v", err)
	}

	// Persisted to disk.
	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Integrations) != 1 || reloaded.Integrations[0].Name != "Movies" {
		t.Fatalf("expected the integration to be persisted, got %#v", reloaded.Integrations)
	}
	if reloaded.Radarr.URL != radarrSrv.URL {
		t.Fatalf("expected derived Radarr field to be persisted, got %#v", reloaded.Radarr)
	}

	// Live-activated: the service's own config now reflects it too, and a
	// subsequent status check should be able to reach the fake Radarr.
	service.mu.RLock()
	activeCfg := service.cfg
	activeClient := service.rad
	service.mu.RUnlock()
	if activeCfg.Radarr.URL != radarrSrv.URL {
		t.Fatalf("expected service.cfg to reflect the new integration live, got %#v", activeCfg.Radarr)
	}
	if activeClient == nil {
		t.Fatal("expected service.rad to be rebuilt with the new integration's client")
	}
	if err := activeClient.WithContext(context.Background()).Validate(); err != nil {
		t.Fatalf("expected the newly activated client to reach the fake Radarr server, got %v", err)
	}
}

func TestAddIntegrationRejectsFailedConnectionCheckWithoutPersisting(t *testing.T) {
	brokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer brokenSrv.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	baseCfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	service := New(baseCfg, nil)
	service.SetConfigPath(configPath)

	if err := service.AddIntegration(context.Background(), config.Integration{Type: "radarr", Name: "Movies", URL: brokenSrv.URL, APIKey: "bad"}); err == nil {
		t.Fatal("expected a failing connection check to reject the integration")
	}

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Integrations) != 0 {
		t.Fatalf("expected nothing to be persisted after a failed connection check, got %#v", reloaded.Integrations)
	}
	service.mu.RLock()
	stillEmpty := service.cfg.Radarr.URL == ""
	service.mu.RUnlock()
	if !stillEmpty {
		t.Fatal("expected the service's live config to be unchanged after a failed connection check")
	}
}

func TestAddIntegrationRejectsSecondInstanceOfSameTypeAtRuntime(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"integrations":[{"type":"radarr","name":"Movies","url":"http://radarr:7878"}],"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	baseCfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	service := New(baseCfg, nil)
	service.SetConfigPath(configPath)

	if err := service.AddIntegration(context.Background(), config.Integration{Type: "radarr", Name: "Movies 4K", URL: "http://radarr4k:7878"}); err == nil {
		t.Fatal("expected a second radarr instance to be rejected at runtime, same as a static config would reject it")
	}
}

func TestEditIntegrationMovesToNewURLPreservingIDAndActivatesLive(t *testing.T) {
	oldRadarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer oldRadarr.Close()
	newRadarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer newRadarr.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	baseCfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	service := New(baseCfg, nil)
	service.SetConfigPath(configPath)
	if err := service.AddIntegration(context.Background(), config.Integration{Type: "radarr", Name: "Movies", URL: oldRadarr.URL, APIKey: "key"}); err != nil {
		t.Fatalf("setup AddIntegration failed: %v", err)
	}
	service.mu.RLock()
	originalID := service.cfg.Integrations[0].ID
	service.mu.RUnlock()

	if err := service.EditIntegration(context.Background(), originalID, config.Integration{Name: "Movies", URL: newRadarr.URL, APIKey: "newkey"}); err != nil {
		t.Fatalf("EditIntegration failed: %v", err)
	}

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Integrations) != 1 || reloaded.Integrations[0].ID != originalID {
		t.Fatalf("expected the same ID to survive the URL change, got %#v (want id %q)", reloaded.Integrations, originalID)
	}
	if reloaded.Radarr.URL != newRadarr.URL {
		t.Fatalf("expected the persisted config to reflect the new URL, got %#v", reloaded.Radarr)
	}

	service.mu.RLock()
	activeClient := service.rad
	service.mu.RUnlock()
	if err := activeClient.WithContext(context.Background()).Validate(); err != nil {
		t.Fatalf("expected the live client to now reach the new Radarr server, got %v", err)
	}
}

func TestEditIntegrationRejectsFailedConnectionCheckWithoutPersisting(t *testing.T) {
	oldRadarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer oldRadarr.Close()
	brokenRadarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer brokenRadarr.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	baseCfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	service := New(baseCfg, nil)
	service.SetConfigPath(configPath)
	if err := service.AddIntegration(context.Background(), config.Integration{Type: "radarr", Name: "Movies", URL: oldRadarr.URL, APIKey: "key"}); err != nil {
		t.Fatalf("setup AddIntegration failed: %v", err)
	}
	service.mu.RLock()
	id := service.cfg.Integrations[0].ID
	service.mu.RUnlock()

	if err := service.EditIntegration(context.Background(), id, config.Integration{Name: "Movies", URL: brokenRadarr.URL, APIKey: "bad"}); err == nil {
		t.Fatal("expected a failing connection check on the new URL to reject the edit")
	}

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Radarr.URL != oldRadarr.URL {
		t.Fatalf("expected the original URL to remain persisted after a rejected edit, got %#v", reloaded.Radarr)
	}
}

func TestRemoveIntegrationDeactivatesClientAndFallsBackToUnmanaged(t *testing.T) {
	radarrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer radarrSrv.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	baseCfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	service := New(baseCfg, nil)
	service.SetConfigPath(configPath)
	if err := service.AddIntegration(context.Background(), config.Integration{Type: "radarr", Name: "Movies", URL: radarrSrv.URL, APIKey: "key"}); err != nil {
		t.Fatalf("setup AddIntegration failed: %v", err)
	}
	service.mu.RLock()
	id := service.cfg.Integrations[0].ID
	service.mu.RUnlock()

	if err := service.RemoveIntegration(id); err != nil {
		t.Fatalf("RemoveIntegration failed: %v", err)
	}

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Integrations) != 0 || reloaded.Radarr.URL != "" {
		t.Fatalf("expected the integration and its derived field to be gone, got %#v / %#v", reloaded.Integrations, reloaded.Radarr)
	}

	statuses := service.StatusSnapshot()
	for _, status := range statuses {
		if status.Name == "Movies" {
			t.Fatalf("expected the removed integration's status entry to be cleared, got %#v", status)
		}
	}
	// The client must still be a valid, non-nil "unconfigured" client, not
	// left dangling or nil — every reconciliation loop assumes it's usable.
	service.mu.RLock()
	activeClient := service.rad
	service.mu.RUnlock()
	if activeClient == nil {
		t.Fatal("expected service.rad to be reset to a valid unconfigured client, not nil")
	}
	if err := activeClient.WithContext(context.Background()).Validate(); err != nil {
		t.Fatalf("expected an unconfigured client's Validate to no-op rather than error, got %v", err)
	}
}

func TestRemoveIntegrationRejectsUnknownID(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	baseCfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	service := New(baseCfg, nil)
	service.SetConfigPath(configPath)
	if err := service.RemoveIntegration("does-not-exist"); err == nil {
		t.Fatal("expected removing an unknown ID to fail")
	}
}
