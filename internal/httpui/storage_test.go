package httpui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connarr/internal/cleanup"
	"connarr/internal/config"
	"connarr/internal/inventory"
	"connarr/internal/model"
)

func TestStorageTemplateRendersPerServiceDetailAndRootPaths(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := storageData{
		Devices: []deviceView{
			{
				Storage: inventory.StorageDevice{
					RootLabels: []string{"downloads", "movies"}, RepresentativePath: "/data/movies", Filesystem: "ext2/ext3/ext4",
					Available: true, TotalBytes: 1000, FreeBytes: 400, UsedBytes: 600,
					Claimed:        []inventory.ClaimedSegment{{Service: "Movies", Bytes: 500}},
					UnmanagedBytes: 50, OtherBytes: 50,
				},
				Plan: cleanup.Plan{Available: true, UsagePercent: 60, TargetUsagePercent: 90, Message: "No cleanup: 60.00% used (target 90.0%)"},
			},
			{
				Storage: inventory.StorageDevice{RepresentativePath: "/data/broken", Available: false, Error: "permission denied"},
			},
		},
		ServiceRoots: map[string][]string{"Movies": {"/data/movies", "/data/movies-4k"}},
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.storageTpl, data); err != nil {
		t.Fatalf("render storage template: %v", err)
	}
	body := recorder.Body.String()
	for _, want := range []string{
		"downloads, movies", "ext2/ext3/ext4", "Movies: 500.0 B", "storage-legend",
		"/data/movies", "/data/movies-4k", "root-icon",
		"No cleanup", "/data/broken", "permission denied", "storage-bar",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected storage output to contain %q, got:\n%s", want, body)
		}
	}
}

func TestStorageTemplateRendersCleanupActions(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := storageData{
		Devices: []deviceView{
			{
				Storage: inventory.StorageDevice{RepresentativePath: "/data/movies", Available: true, TotalBytes: 1000, FreeBytes: 100, UsedBytes: 900},
				Plan: cleanup.Plan{
					Available: true, UsagePercent: 90, TargetUsagePercent: 80, NeedBytes: 100, SelectedBytes: 30, Message: "Need to reclaim 30.0 B to reach 80.0% usage",
					Actions: []cleanup.Action{
						{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{Name: "Old Release"}}, ReclaimableBytes: 10},
						{Kind: cleanup.StandaloneMedia, Media: model.Media{Title: "Lonely Movie"}, ReclaimableBytes: 10},
						{Kind: cleanup.HardlinkedBundle, Media: model.Media{Title: "Bundled Movie"}, Torrents: []model.Torrent{{Name: "Bundled Release"}}, ReclaimableBytes: 10},
					},
				},
			},
		},
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.storageTpl, data); err != nil {
		t.Fatalf("render storage template: %v", err)
	}
	body := recorder.Body.String()
	for _, want := range []string{"Old Release", "Lonely Movie", "Bundled Movie + 1 hardlinked torrent(s)", "3 action(s) selected"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected storage output to contain %q, got:\n%s", want, body)
		}
	}
}

func TestStorageTemplateRendersNoKnownDevices(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.storageTpl, storageData{}); err != nil {
		t.Fatalf("render storage template: %v", err)
	}
	if !strings.Contains(recorder.Body.String(), "No storage devices are known yet") {
		t.Fatalf("expected empty-state message, got:\n%s", recorder.Body.String())
	}
}

// TestSetDeviceThresholdPersistsFromStoragePage guards the actual fix for
// setting per-device thresholds after storage roots are discovered by a
// service's own adapter, rather than during add-service before any root
// path is even known (root_path can never be entered for a real service
// type — validateService rejects it outright).
func TestSetDeviceThresholdPersistsFromStoragePage(t *testing.T) {
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

	devicePath := "/data/movies"
	form := url.Values{"representative_path": {devicePath}, "target_usage_percent": {"80"}, "critical_usage_percent": {"88"}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/storage/device-threshold", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got status=%d body=%q", recorder.Code, recorder.Body.String())
	}

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	target, critical := reloaded.ThresholdsFor(devicePath)
	if target != 80 || critical != 88 {
		t.Fatalf("expected the submitted thresholds to be persisted for %s, got %v/%v", devicePath, target, critical)
	}
}
