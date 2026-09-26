package httpui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"stewarr/internal/cleanup"
	"stewarr/internal/config"
	"stewarr/internal/inventory"
	"stewarr/internal/model"
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
					ServiceRoots: map[string][]string{"Movies": {"/data/movies", "/data/movies-4k"}},
				},
				Plan: cleanup.Plan{Available: true, UsagePercent: 60, TargetUsagePercent: 90, Message: "No cleanup: 60.00% used (target 90.0%)"},
				Name: "Media Drive", Enabled: true,
			},
			{
				Storage: inventory.StorageDevice{RepresentativePath: "/data/broken", Available: false, Error: "permission denied"},
			},
		},
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.storageTpl, data); err != nil {
		t.Fatalf("render storage template: %v", err)
	}
	body := recorder.Body.String()
	for _, want := range []string{
		"Media Drive", "downloads, movies", "ext2/ext3/ext4", "Movies: 500.0 B", "storage-legend",
		"/data/movies", "/data/movies-4k", "root-icon",
		"No cleanup", "/data/broken", "permission denied", "storage-bar",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected storage output to contain %q, got:\n%s", want, body)
		}
	}
}

// TestStorageTemplateScopesServiceRootsToTheirOwnDevice guards a real bug:
// a torrent client's incomplete-downloads directory can live on a different
// physical disk than its normal save path, so the same service claims bytes
// on two different devices. The path list under each device card must only
// ever show that device's own subset of the service's roots, not every
// root the service has across every device.
func TestStorageTemplateScopesServiceRootsToTheirOwnDevice(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := storageData{
		Devices: []deviceView{
			{
				Storage: inventory.StorageDevice{
					RepresentativePath: "/data/media", Available: true, TotalBytes: 1000, FreeBytes: 900, UsedBytes: 100,
					Claimed:      []inventory.ClaimedSegment{{Service: "Downloader", Bytes: 100}},
					ServiceRoots: map[string][]string{"Downloader": {"/data/downloads/complete"}},
				},
				Plan: cleanup.Plan{Available: true},
				Name: "Media", Enabled: true,
			},
			{
				Storage: inventory.StorageDevice{
					RepresentativePath: "/torrent-cache", Available: true, TotalBytes: 500, FreeBytes: 400, UsedBytes: 100,
					Claimed:      []inventory.ClaimedSegment{{Service: "Downloader", Bytes: 100}},
					ServiceRoots: map[string][]string{"Downloader": {"/torrent-cache"}},
				},
				Plan: cleanup.Plan{Available: true},
				Name: "Torrent cache", Enabled: true,
			},
		},
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.storageTpl, data); err != nil {
		t.Fatalf("render storage template: %v", err)
	}
	body := recorder.Body.String()
	mediaSection := body[:strings.Index(body, "Torrent cache")]
	if strings.Contains(mediaSection, "/torrent-cache") {
		t.Fatalf("expected the Media device's path list to exclude the other device's path, got:\n%s", mediaSection)
	}
	if !strings.Contains(mediaSection, "/data/downloads/complete") {
		t.Fatalf("expected the Media device's own path to still be shown, got:\n%s", mediaSection)
	}
}

// TestStorageTemplateShowsCleanupPlanButtonOnlyWhenThereAreActions guards
// the card-length fix: the detailed per-action Torrents/Media lists used
// to render inline in the card (making it uncomfortably long for a device
// with many candidates) and now live in the cleanup-plan overlay instead
// — the card only shows the aggregate summary plus a 📋 button, and that
// button must not appear when there's nothing to show it for.
func TestStorageTemplateShowsCleanupPlanButtonOnlyWhenThereAreActions(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	withActions := storageData{
		Devices: []deviceView{
			{
				Storage: inventory.StorageDevice{RepresentativePath: "/data/movies", Available: true, TotalBytes: 1000, FreeBytes: 100, UsedBytes: 900},
				Enabled: true,
				Plan: cleanup.Plan{
					Available: true, UsagePercent: 90, TargetUsagePercent: 80, NeedBytes: 100, SelectedBytes: 30, Message: "Need to reclaim 30.0 B to reach 80.0% usage",
					Actions: []cleanup.Action{
						{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{Name: "Old Release"}}, ReclaimableBytes: 10},
					},
				},
			},
		},
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.storageTpl, withActions); err != nil {
		t.Fatalf("render storage template: %v", err)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "/storage/cleanup-plan?path=") {
		t.Fatalf("expected the cleanup-plan button when actions are selected, got:\n%s", body)
	}
	if !strings.Contains(body, "1 action(s) selected") {
		t.Fatalf("expected the aggregate summary to remain inline, got:\n%s", body)
	}
	if strings.Contains(body, "Old Release") {
		t.Fatalf("expected the detailed action list to have moved out of the card, got:\n%s", body)
	}

	noActions := storageData{
		Devices: []deviceView{
			{
				Storage: inventory.StorageDevice{RepresentativePath: "/data/movies", Available: true, TotalBytes: 1000, FreeBytes: 900, UsedBytes: 100},
				Enabled: true,
				Plan:    cleanup.Plan{Available: true, UsagePercent: 10, TargetUsagePercent: 80, NeedBytes: 0, Message: "Under target"},
			},
		},
	}
	recorder = httptest.NewRecorder()
	if err := renderTemplate(recorder, server.storageTpl, noActions); err != nil {
		t.Fatalf("render storage template: %v", err)
	}
	if strings.Contains(recorder.Body.String(), "/storage/cleanup-plan?path=") {
		t.Fatalf("expected no cleanup-plan button when nothing needs cleanup, got:\n%s", recorder.Body.String())
	}
}

// TestCleanupPlanTemplateRendersActions guards the cleanup-plan overlay
// itself — the detailed per-action list that used to render inline on the
// Storage page card now lives here instead. A StandaloneSeason action has
// no Torrents at all, which guards a real crash where an earlier version
// of this same markup's catch-all "else" branch assumed every
// non-bundle/non-media action was a torrent and indexed into an empty
// Torrents slice.
func TestCleanupPlanTemplateRendersActions(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := cleanupPlanData{
		RepresentativePath: "/data/movies",
		Plan: cleanup.Plan{
			Available: true, UsagePercent: 90, TargetUsagePercent: 80, NeedBytes: 100, SelectedBytes: 30, Message: "Need to reclaim 30.0 B to reach 80.0% usage",
			Actions: []cleanup.Action{
				{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{Name: "Old Release"}}, ReclaimableBytes: 10},
				{Kind: cleanup.StandaloneMedia, Media: model.Media{Title: "Lonely Movie"}, ReclaimableBytes: 10},
				{Kind: cleanup.HardlinkedBundle, Media: model.Media{Title: "Bundled Movie"}, Torrents: []model.Torrent{{Name: "Bundled Release"}}, ReclaimableBytes: 10},
				{Kind: cleanup.StandaloneSeason, Media: model.Media{Title: "Some Show"}, Season: &model.Season{Number: 3}, ReclaimableBytes: 10},
			},
		},
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.cleanupPlanTpl, data); err != nil {
		t.Fatalf("render cleanup-plan template: %v", err)
	}
	body := recorder.Body.String()
	for _, want := range []string{"Old Release", "Lonely Movie", "Bundled Movie + 1 hardlinked torrent(s)", "Some Show · Season 3", "4 action(s) selected"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected cleanup-plan output to contain %q, got:\n%s", want, body)
		}
	}
}

// TestCleanupPlanTemplateGroupsActionsAndExplainsTorrents guards the fix
// for a real observability gap: a StandaloneTorrent candidate used to
// render as nothing but its raw release name, giving no way to tell
// whether removing it would touch any library copy at all. Torrent and
// media candidates are shown as two separate, labeled groups (matching
// the two tiers cleanup.rank() already computes), and each torrent line
// discloses its association status, whether it's a proven-independent
// (non-hardlinked) copy, and which media it relates to if any.
func TestCleanupPlanTemplateGroupsActionsAndExplainsTorrents(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := cleanupPlanData{
		RepresentativePath: "/data/movies",
		Plan: cleanup.Plan{
			Available: true, UsagePercent: 90, TargetUsagePercent: 80, NeedBytes: 100, SelectedBytes: 20,
			Actions: []cleanup.Action{
				{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{Name: "Show.S03E01.mkv", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: false, MediaItems: []model.MediaRef{{Title: "Some Show", Year: 2024}}}}, ReclaimableBytes: 10},
				{Kind: cleanup.StandaloneMedia, Media: model.Media{Title: "Lonely Movie", ServiceName: "Movies 4K"}, ReclaimableBytes: 10},
			},
		},
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.cleanupPlanTpl, data); err != nil {
		t.Fatalf("render cleanup-plan template: %v", err)
	}
	body := recorder.Body.String()
	torrentGroupIndex := strings.Index(body, "cleanup-group")
	if torrentGroupIndex < 0 {
		t.Fatalf("expected grouped cleanup output, got:\n%s", body)
	}
	for _, want := range []string{
		"Show.S03E01.mkv", "independent copy, not hardlinked to its media", "Some Show (2024)",
		"Lonely Movie", "Movies 4K", ">Torrents ", ">Media ",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected cleanup-plan output to contain %q, got:\n%s", want, body)
		}
	}
	if strings.Index(body, "Torrents") > strings.Index(body, "Lonely Movie") {
		t.Fatalf("torrent group should render before the media group, got:\n%s", body)
	}
}

// TestCleanupPlanTemplateRendersEmptyState guards the safety net for the
// 📋 button's non-reactive placement (it's rendered once with the rest of
// the card-head, not inside the live-patched fragment) — the plan can
// clear between page loads, so opening the overlay right after that must
// still show something sensible instead of an empty modal.
// TestCleanupPlanTemplateRendersExcludeProtectButtonsAndCarriesExcludedForward
// guards the live "skip and backfill" wiring end to end at the template
// layer: each row gets both a Skip and a Protect button carrying the
// right path/key data attributes, and any already-excluded keys (from a
// previous Skip/Protect in this same modal session) round-trip back out
// as hidden fields so Clean's own submission reproduces the identical
// filtered plan.
func TestCleanupPlanTemplateRendersExcludeProtectButtonsAndCarriesExcludedForward(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	action := cleanup.Action{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{Hash: "ABC123", ServiceID: "qb-1", Name: "Old Release"}}, ReclaimableBytes: 10}
	data := cleanupPlanData{
		RepresentativePath: "/data/movies",
		Excluded:           []string{"media:movie:radarr-1:1:"},
		Plan: cleanup.Plan{
			Available: true, NeedBytes: 100, SelectedBytes: 10,
			Actions: []cleanup.Action{action},
		},
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.cleanupPlanTpl, data); err != nil {
		t.Fatalf("render cleanup-plan template: %v", err)
	}
	body := recorder.Body.String()
	key := cleanup.ActionKey(action)
	for _, want := range []string{
		`data-cleanup-exclude data-path="/data/movies" data-key="` + key + `"`,
		`data-cleanup-protect data-path="/data/movies" data-key="` + key + `"`,
		`name="excluded" value="media:movie:radarr-1:1:"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected cleanup-plan output to contain %q, got:\n%s", want, body)
		}
	}
}

func TestCleanupPlanTemplateRendersEmptyState(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.cleanupPlanTpl, cleanupPlanData{RepresentativePath: "/data/movies"}); err != nil {
		t.Fatalf("render cleanup-plan template: %v", err)
	}
	if !strings.Contains(recorder.Body.String(), "Nothing currently needs cleanup") {
		t.Fatalf("expected an empty-state message, got:\n%s", recorder.Body.String())
	}
}

// TestDeviceSettingsTemplateShowsWholeDeviceIdentityNotJustOnePath guards
// the actual fix: showing the bare RepresentativePath made the modal look
// like its thresholds applied to that one path specifically, when they
// actually apply to the whole physical device — possibly several root
// paths grouped together. The modal must show the same
// labels-plus-filesystem identity the Storage page card itself already
// does, and say explicitly that this covers the whole device.
func TestDeviceSettingsTemplateShowsWholeDeviceIdentityNotJustOnePath(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := deviceSettingsData{
		RepresentativePath: "/data/movies",
		RootLabels:         []string{"downloads", "movies"},
		Filesystem:         "ext4",
		Name:               "Media Drive",
		TargetUsagePercent: 80,
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.deviceSettingsTpl, data); err != nil {
		t.Fatalf("render device-settings template: %v", err)
	}
	body := recorder.Body.String()
	for _, want := range []string{"downloads, movies", "ext4", "whole physical device", `value="Media Drive"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected device-settings overlay to contain %q, got:\n%s", want, body)
		}
	}
}

// TestDeviceSettingsTemplateHidesThresholdsWhenAutomaticRemovalDisabled
// guards the actual point of the "Enable on this device" checkbox: the
// Target field is meaningless once nothing evaluates it for this device,
// so it must start hidden (not just hide after a JS change event) when the
// saved state is already disabled, and visible again when enabled — the
// checkbox itself must reflect the saved state either way.
func TestDeviceSettingsTemplateHidesThresholdsWhenAutomaticRemovalDisabled(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		enabled bool
	}{
		{"enabled", true},
		{"disabled", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := deviceSettingsData{RepresentativePath: "/data/movies", TargetUsagePercent: 80, AutomaticRemovalEnabled: tc.enabled}
			recorder := httptest.NewRecorder()
			if err := renderTemplate(recorder, server.deviceSettingsTpl, data); err != nil {
				t.Fatalf("render device-settings template: %v", err)
			}
			body := recorder.Body.String()
			if !strings.Contains(body, "Enable on this device") {
				t.Fatalf("expected an Enable checkbox, got:\n%s", body)
			}
			checked := strings.Contains(body, "data-automatic-removal-toggle checked")
			if checked != tc.enabled {
				t.Fatalf("expected checkbox checked=%v, got body:\n%s", tc.enabled, body)
			}
			fieldRowHidden := strings.Contains(body, `data-threshold-field hidden`)
			if fieldRowHidden == tc.enabled {
				t.Fatalf("expected the threshold field row hidden=%v when enabled=%v, got body:\n%s", !tc.enabled, tc.enabled, body)
			}
		})
	}
}

// TestStorageTemplateHidesCleanupPlanWhenAutoRemovalDisabled guards the
// real fix: Removal.AutoMode Disabled must mean the whole notion of "here's
// what we'd suggest removing" doesn't exist on the Storage page, not just
// that nothing gets submitted unattended by the background task (a
// separate, already-independent gate). The bar/legend/usage numbers are
// unaffected — only the Cleanup active/inactive section and its action
// list disappear.
func TestStorageTemplateHidesCleanupPlanWhenAutoRemovalDisabled(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := storageData{
		AutoRemovalDisabled: true,
		Devices: []deviceView{
			{
				Storage: inventory.StorageDevice{RepresentativePath: "/data/movies", Available: true, TotalBytes: 1000, FreeBytes: 100, UsedBytes: 900},
				Enabled: true,
				Plan: cleanup.Plan{
					Available: true, UsagePercent: 90, TargetUsagePercent: 80, NeedBytes: 100, SelectedBytes: 30, Message: "Need to reclaim 30.0 B to reach 80.0% usage",
					Actions: []cleanup.Action{
						{Kind: cleanup.StandaloneMedia, Media: model.Media{Title: "Lonely Movie"}, ReclaimableBytes: 10},
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
	if strings.Contains(body, "Cleanup active") || strings.Contains(body, "Lonely Movie") || strings.Contains(body, "action(s) selected") {
		t.Fatalf("expected no cleanup plan/actions when automatic removal is disabled, got:\n%s", body)
	}
	if !strings.Contains(body, "Automatic removal is off") {
		t.Fatalf("expected an explanatory note when automatic removal is disabled, got:\n%s", body)
	}
	// The bar/usage numbers are a different concern and must still render.
	if !strings.Contains(body, "storage-bar") || !strings.Contains(body, "900.0 B used") {
		t.Fatalf("expected the usage bar/summary to still render regardless, got:\n%s", body)
	}
}

// TestStorageTemplateHidesThresholdMarkersAndPlanWhenDeviceDisabled guards
// the per-device counterpart to the global AutoMode gate above: opting one
// specific device out must drop its own threshold markers/cleanup section,
// without needing the global switch off and without touching any other
// device's bar.
func TestStorageTemplateHidesThresholdMarkersAndPlanWhenDeviceDisabled(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := storageData{
		Devices: []deviceView{
			{
				Storage: inventory.StorageDevice{RepresentativePath: "/data/movies", Available: true, TotalBytes: 1000, FreeBytes: 100, UsedBytes: 900},
				Enabled: false,
				Plan:    cleanup.Plan{TargetUsagePercent: 80, CriticalUsagePercent: 95},
			},
		},
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.storageTpl, data); err != nil {
		t.Fatalf("render storage template: %v", err)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "threshold target") || strings.Contains(body, "threshold critical") {
		t.Fatalf("expected no threshold markers for a disabled device, got:\n%s", body)
	}
	if !strings.Contains(body, "Automatic removal is off for this device") {
		t.Fatalf("expected the per-device off message, got:\n%s", body)
	}
	if !strings.Contains(body, "storage-bar") || !strings.Contains(body, "900.0 B used") {
		t.Fatalf("expected the usage bar/summary to still render regardless, got:\n%s", body)
	}
}

func TestStorageTemplateRendersCapabilities(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := storageData{
		Capabilities: []inventory.RootCapability{
			{Path: "/data/movies", ServiceName: "Movies", ServiceType: "radarr", Reachable: true, Filesystem: "ext4", FreeBytes: 400, ReservedBytes: 20},
			{Path: "/data/missing", ServiceName: "Downloader", ServiceType: "qbittorrent", Purpose: "incomplete downloads", Reachable: false, SelfReportedCount: 3, SelfReportedSizeBytes: 12345},
		},
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.storageTpl, data); err != nil {
		t.Fatalf("render storage template: %v", err)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "/data/movies") || !strings.Contains(body, "ext4") {
		t.Fatalf("expected reachable root's filesystem facts, got:\n%s", body)
	}
	if !strings.Contains(body, "/data/missing") || !strings.Contains(body, "not reachable") || !strings.Contains(body, "incomplete downloads") {
		t.Fatalf("expected unreachable root's warning and purpose, got:\n%s", body)
	}
	if !strings.Contains(body, "3 item(s)") {
		t.Fatalf("expected the unreachable root's self-reported item count, got:\n%s", body)
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

// TestStorageStatsServesCheapJSONWithoutARemovalPlan guards the actual point
// of the endpoint: it must answer from server.inv.StorageDevices() and
// config thresholds alone, with no Media()/Torrents() snapshot and no
// cleanup.Build call anywhere on its path — unlike the Storage page itself,
// this is the endpoint a raw disk-byte tick (watchStorageChanges, every 5s)
// hits, and it must stay that cheap regardless of library size.
func TestStorageStatsServesCheapJSONWithoutARemovalPlan(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	inv := inventory.New(cfg, nil)
	server, err := New(inv, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/storage/stats", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if got := strings.TrimSpace(recorder.Body.String()); got != "[]" {
		t.Fatalf("expected an empty JSON array with no known devices, got %q", got)
	}

	postRecorder := httptest.NewRecorder()
	handler.ServeHTTP(postRecorder, httptest.NewRequest(http.MethodPost, "/storage/stats", nil))
	if postRecorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST, got status=%d", postRecorder.Code)
	}
}

// TestDeviceSettingsFormRendersCurrentThresholds guards the wrench-icon
// overlay: it must pre-fill from the device's actual current thresholds
// (via config.ThresholdsFor, the same source the inline form used to read
// from), not just render blank fields.
func TestDeviceSettingsFormRendersCurrentThresholds(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{"device_thresholds":{"/data/movies":{"target_usage_percent":70,"critical_usage_percent":85,"name":"Media Drive"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(inventory.New(cfg, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/storage/device-settings?path=/data/movies", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{"/data/movies", `value="70"`, `value="Media Drive"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected device-settings overlay to contain %q, got:\n%s", want, body)
		}
	}
	// Critical isn't shown in the UI at all anymore (see
	// TestSetDeviceThresholdPersistsFromStoragePage for the config-layer
	// value it still preserves under the hood).
	if strings.Contains(body, "85") {
		t.Fatalf("expected no trace of the critical threshold in the rendered overlay, got:\n%s", body)
	}
}

// TestCleanupPlanFormRoutesAndRendersEmptyStateForUnknownDevice guards the
// actual HTTP wiring (route registered, handler reachable, template
// resolved) rather than just the template in isolation — a path that
// doesn't match any currently known device (e.g. the device was removed,
// or the request is stale) must render the same empty state as a device
// with nothing to clean up, not error out.
func TestCleanupPlanFormRoutesAndRendersEmptyStateForUnknownDevice(t *testing.T) {
	server, err := New(inventory.New(config.Config{}, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/storage/cleanup-plan?path=/data/movies", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Nothing currently needs cleanup") {
		t.Fatalf("expected the empty-state message, got:\n%s", recorder.Body.String())
	}
}

// TestCleanupPlanCleanAndProtectRouteToUnknownDevice guards the same HTTP
// wiring for the two new POST routes: a path that doesn't match any
// currently known device must report 404, not panic or silently no-op.
func TestCleanupPlanCleanAndProtectRouteToUnknownDevice(t *testing.T) {
	server, err := New(inventory.New(config.Config{}, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()

	for _, path := range []string{"/storage/cleanup-plan/clean", "/storage/cleanup-plan/protect"} {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(url.Values{"path": {"/data/movies"}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		handler.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s: expected 404, got status=%d body=%q", path, recorder.Code, recorder.Body.String())
		}
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

	// The real browser form submits via shell.ts's data-background-submit,
	// which always sends multipart/form-data (fetch + FormData), never
	// urlencoded. A urlencoded body here would mask the exact bug this once
	// shipped with: r.ParseForm alone never reads a multipart body, so every
	// field came back empty and every save failed with "target_usage_percent
	// must be a number" even though the form was filled in correctly.
	devicePath := "/data/movies"
	body, contentType := multipartServiceForm(t, map[string]string{"representative_path": devicePath, "name": "Media Drive", "target_usage_percent": "80"})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/storage/device-threshold", body)
	request.Header.Set("Content-Type", contentType)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got status=%d body=%q", recorder.Code, recorder.Body.String())
	}

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	// Critical isn't on this form anymore — the handler must preserve
	// whatever was already configured (the default, since this config
	// started with no device_thresholds entry at all) rather than reset it.
	target, critical := reloaded.ThresholdsFor(devicePath)
	if target != 80 {
		t.Fatalf("expected the submitted target threshold to be persisted for %s, got %v", devicePath, target)
	}
	if critical != 95 {
		t.Fatalf("expected the untouched critical threshold to keep its default for %s, got %v", devicePath, critical)
	}
	if got := reloaded.DeviceName(devicePath); got != "Media Drive" {
		t.Fatalf("expected the submitted name to be persisted for %s, got %q", devicePath, got)
	}
}
