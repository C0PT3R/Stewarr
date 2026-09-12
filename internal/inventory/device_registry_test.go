package inventory

import (
	"os"
	"path/filepath"
	"testing"

	"stewarr/internal/config"
)

// TestSyncDeviceRegistryPersistsIDAndDefaultNameForANewDevice guards the
// actual wiring: syncDeviceRegistry must derive the live device set from
// the service's current storageRoots (the same source knownDeviceRoots
// already uses) and persist an assigned id/default name to the real
// config file.
func TestSyncDeviceRegistryPersistsIDAndDefaultNameForANewDevice(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	service := New(cfg, nil)
	service.SetConfigPath(configPath)
	service.mu.Lock()
	service.storageRoots = []storageRoot{{Path: root, Service: config.Service{ID: "radarr-1", Name: "Movies"}, Label: "movies"}}
	service.mu.Unlock()

	service.syncDeviceRegistry()

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := reloaded.Storage.DeviceThresholds[root]
	if !ok {
		t.Fatalf("expected a device entry to be persisted for %s, got %#v", root, reloaded.Storage.DeviceThresholds)
	}
	if entry.ID != 1 {
		t.Fatalf("expected the first discovered device to get id=1, got %d", entry.ID)
	}
	if entry.Name != "Device #1" {
		t.Fatalf("expected the default name to be the literal stored string, got %q", entry.Name)
	}

	// Running again with the same live set must be a no-op — no spurious
	// rewrite, no id churn.
	service.syncDeviceRegistry()
	reloadedAgain, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if reloadedAgain.Storage.DeviceThresholds[root] != entry {
		t.Fatalf("expected re-running with the same live device to change nothing, got %#v", reloadedAgain.Storage.DeviceThresholds[root])
	}
}

// TestSyncDeviceRegistryPrunesADeviceNoLongerDiscovered guards the other
// half: once a device's root disappears from storageRoots (its service
// was removed, or the disk is gone), the next sync must prune its entry.
func TestSyncDeviceRegistryPrunesADeviceNoLongerDiscovered(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	service := New(cfg, nil)
	service.SetConfigPath(configPath)
	service.mu.Lock()
	service.storageRoots = []storageRoot{{Path: root, Service: config.Service{ID: "radarr-1", Name: "Movies"}}}
	service.mu.Unlock()
	service.syncDeviceRegistry()

	if reloaded, err := config.Load(configPath); err != nil || len(reloaded.Storage.DeviceThresholds) != 1 {
		t.Fatalf("setup: expected one device entry, got %#v (err=%v)", reloaded.Storage.DeviceThresholds, err)
	}

	// The root disappears entirely — no known storage roots left.
	service.mu.Lock()
	service.storageRoots = nil
	service.mu.Unlock()
	service.syncDeviceRegistry()

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Storage.DeviceThresholds) != 0 {
		t.Fatalf("expected the disappeared device's entry to be pruned, got %#v", reloaded.Storage.DeviceThresholds)
	}
}
