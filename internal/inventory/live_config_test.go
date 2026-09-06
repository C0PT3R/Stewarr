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

func TestAddServicePersistsAndActivatesLiveWithoutConfigPath(t *testing.T) {
	service := New(config.Config{}, nil)
	err := service.AddService(context.Background(), config.Service{Type: "radarr", Name: "Movies", URL: "http://radarr:7878"})
	if err == nil {
		t.Fatal("expected AddService to fail when no config path is set")
	}
}

func TestAddServiceSucceedsAndActivatesClient(t *testing.T) {
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

	if err := service.AddService(context.Background(), config.Service{Type: "radarr", Name: "Movies", URL: radarrSrv.URL, APIKey: "key"}); err != nil {
		t.Fatalf("AddService failed: %v", err)
	}

	// Persisted to disk.
	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Services) != 1 || reloaded.Services[0].Name != "Movies" {
		t.Fatalf("expected the service to be persisted, got %#v", reloaded.Services)
	}
	if reloaded.Radarr.URL != radarrSrv.URL {
		t.Fatalf("expected derived Radarr field to be persisted, got %#v", reloaded.Radarr)
	}

	// Live-activated: the service's own config now reflects it too, and a
	// subsequent status check should be able to reach the fake Radarr.
	service.mu.RLock()
	activeCfg := service.cfg
	activeClient := service.rad[reloaded.Services[0].ID]
	service.mu.RUnlock()
	if activeCfg.Radarr.URL != radarrSrv.URL {
		t.Fatalf("expected service.cfg to reflect the new service live, got %#v", activeCfg.Radarr)
	}
	if activeClient == nil {
		t.Fatal("expected service.rad to be rebuilt with the new service's client")
	}
	if err := activeClient.WithContext(context.Background()).Validate(); err != nil {
		t.Fatalf("expected the newly activated client to reach the fake Radarr server, got %v", err)
	}
}

func TestAddServiceRejectsFailedConnectionCheckWithoutPersisting(t *testing.T) {
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

	if err := service.AddService(context.Background(), config.Service{Type: "radarr", Name: "Movies", URL: brokenSrv.URL, APIKey: "bad"}); err == nil {
		t.Fatal("expected a failing connection check to reject the service")
	}

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Services) != 0 {
		t.Fatalf("expected nothing to be persisted after a failed connection check, got %#v", reloaded.Services)
	}
	service.mu.RLock()
	stillEmpty := service.cfg.Radarr.URL == ""
	service.mu.RUnlock()
	if !stillEmpty {
		t.Fatal("expected the service's live config to be unchanged after a failed connection check")
	}
}

func TestAddServiceAllowsSecondRadarrInstanceAtRuntime(t *testing.T) {
	radarrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer radarrSrv.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"services":[{"type":"radarr","name":"Movies","url":"http://radarr:7878"}],"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	baseCfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	service := New(baseCfg, nil)
	service.SetConfigPath(configPath)

	if err := service.AddService(context.Background(), config.Service{Type: "radarr", Name: "Movies 4K", URL: radarrSrv.URL}); err != nil {
		t.Fatalf("expected a second radarr instance to be allowed at runtime, got %v", err)
	}
	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Services) != 2 {
		t.Fatalf("expected 2 services, got %#v", reloaded.Services)
	}
}

func TestAddServiceRejectsSecondInstanceOfSingleInstanceTypeAtRuntime(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"services":[{"type":"jellyfin","name":"Jellyfin","url":"http://jellyfin:8096"}],"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	baseCfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	service := New(baseCfg, nil)
	service.SetConfigPath(configPath)

	if err := service.AddService(context.Background(), config.Service{Type: "jellyfin", Name: "Jellyfin 2", URL: "http://jellyfin2:8096"}); err == nil {
		t.Fatal("expected a second jellyfin instance to be rejected at runtime, same as a static config would reject it")
	}
}

func TestEditServiceMovesToNewURLPreservingIDAndActivatesLive(t *testing.T) {
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
	if err := service.AddService(context.Background(), config.Service{Type: "radarr", Name: "Movies", URL: oldRadarr.URL, APIKey: "key"}); err != nil {
		t.Fatalf("setup AddService failed: %v", err)
	}
	service.mu.RLock()
	originalID := service.cfg.Services[0].ID
	service.mu.RUnlock()

	if err := service.EditService(context.Background(), originalID, config.Service{Name: "Movies", URL: newRadarr.URL, APIKey: "newkey"}); err != nil {
		t.Fatalf("EditService failed: %v", err)
	}

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Services) != 1 || reloaded.Services[0].ID != originalID {
		t.Fatalf("expected the same ID to survive the URL change, got %#v (want id %q)", reloaded.Services, originalID)
	}
	if reloaded.Radarr.URL != newRadarr.URL {
		t.Fatalf("expected the persisted config to reflect the new URL, got %#v", reloaded.Radarr)
	}

	service.mu.RLock()
	activeClient := service.rad[originalID]
	service.mu.RUnlock()
	if err := activeClient.WithContext(context.Background()).Validate(); err != nil {
		t.Fatalf("expected the live client to now reach the new Radarr server, got %v", err)
	}
}

func TestEditServiceRejectsFailedConnectionCheckWithoutPersisting(t *testing.T) {
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
	if err := service.AddService(context.Background(), config.Service{Type: "radarr", Name: "Movies", URL: oldRadarr.URL, APIKey: "key"}); err != nil {
		t.Fatalf("setup AddService failed: %v", err)
	}
	service.mu.RLock()
	id := service.cfg.Services[0].ID
	service.mu.RUnlock()

	if err := service.EditService(context.Background(), id, config.Service{Name: "Movies", URL: brokenRadarr.URL, APIKey: "bad"}); err == nil {
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

func TestRemoveServiceDeactivatesClientAndFallsBackToUnmanaged(t *testing.T) {
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
	if err := service.AddService(context.Background(), config.Service{Type: "radarr", Name: "Movies", URL: radarrSrv.URL, APIKey: "key"}); err != nil {
		t.Fatalf("setup AddService failed: %v", err)
	}
	service.mu.RLock()
	id := service.cfg.Services[0].ID
	service.mu.RUnlock()

	if err := service.RemoveService(id); err != nil {
		t.Fatalf("RemoveService failed: %v", err)
	}

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Services) != 0 || reloaded.Radarr.URL != "" {
		t.Fatalf("expected the service and its derived field to be gone, got %#v / %#v", reloaded.Services, reloaded.Radarr)
	}

	statuses := service.StatusSnapshot()
	for _, status := range statuses {
		if status.Name == "Movies" {
			t.Fatalf("expected the removed service's status entry to be cleared, got %#v", status)
		}
	}
	// Radarr supports multiple instances, so a removed instance's client is
	// deleted from the map entirely rather than left as a placeholder — there
	// is no single "the" radarr client to fall back to.
	service.mu.RLock()
	_, stillPresent := service.rad[id]
	service.mu.RUnlock()
	if stillPresent {
		t.Fatal("expected the removed service's client to be deleted from service.rad")
	}
}

func TestRemoveServiceRejectsUnknownID(t *testing.T) {
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
	if err := service.RemoveService("does-not-exist"); err == nil {
		t.Fatal("expected removing an unknown ID to fail")
	}
}
