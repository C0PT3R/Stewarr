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
