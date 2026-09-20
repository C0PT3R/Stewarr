package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDoesNotRequireDatabaseConfiguration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"server":{},"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
}

func TestLoadCreatesDefaultConfigWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "config.json")
	configuration, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(configuration.Services) != 0 {
		t.Fatalf("expected no demo services in the default config, got %d", len(configuration.Services))
	}
	if !configuration.Removal.DryRun {
		t.Fatal("expected the default config to keep dry_run true")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected Load to have written the default config to disk: %v", err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reloading the just-created default config failed: %v", err)
	}
	if reloaded.Server.Listen != configuration.Server.Listen {
		t.Fatalf("reloaded config diverged from the freshly created one: %+v vs %+v", reloaded, configuration)
	}
}

func TestRejectsUnknownServiceType(t *testing.T) {
	p := writeConfig(t, `{"services":[{"type":"mystery","name":"Mystery","root_path":"/data"}]}`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "unsupported type") {
		t.Fatalf("error=%v", err)
	}
}

func TestRejectsRootPathForCurrentAdapters(t *testing.T) {
	p := writeConfig(t, `{"services":[{"type":"radarr","name":"Movies","url":"http://radarr","root_path":"/data"}]}`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "does not support root_path") {
		t.Fatalf("error=%v", err)
	}
}

func TestRejectsNonPositiveRefreshInterval(t *testing.T) {
	for _, value := range []string{"0s", "-1m"} {
		p := writeConfig(t, fmt.Sprintf(`{"server":{"refresh_interval":%q}}`, value))
		if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "greater than zero") {
			t.Fatalf("%s error=%v", value, err)
		}
	}
}

func TestCriticalIsIndependentFromTarget(t *testing.T) {
	p := writeConfig(t, `{"storage":{"device_thresholds":{"/data":{"target_usage_percent":90,"critical_usage_percent":80}}}}`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	target, critical := c.ThresholdsFor("/data")
	if target != 90 || critical != 80 {
		t.Fatalf("thresholds for /data = %v/%v", target, critical)
	}
}

func TestThresholdsForFallsBackToDefaultsWithNoEntry(t *testing.T) {
	p := writeConfig(t, `{}`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	target, critical := c.ThresholdsFor("/unconfigured")
	if target != 90 || critical != 95 {
		t.Fatalf("default thresholds = %v/%v", target, critical)
	}
}

func TestThresholdsForIgnoresOutOfRangeEntry(t *testing.T) {
	p := writeConfig(t, `{"storage":{"device_thresholds":{"/data":{"target_usage_percent":0,"critical_usage_percent":150}}}}`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	target, critical := c.ThresholdsFor("/data")
	if target != 90 || critical != 95 {
		t.Fatalf("expected out-of-range entries to fall back to defaults, got %v/%v", target, critical)
	}
}

func TestSetDeviceThresholdIsIdempotentAndValidated(t *testing.T) {
	var c Config
	updated, err := SetDeviceThreshold(c, "/data", "", 80, 90, true)
	if err != nil {
		t.Fatal(err)
	}
	target, critical := updated.ThresholdsFor("/data")
	if target != 80 || critical != 90 {
		t.Fatalf("thresholds after set = %v/%v", target, critical)
	}
	updatedAgain, err := SetDeviceThreshold(updated, "/data", "", 70, 85, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(updatedAgain.Storage.DeviceThresholds) != 1 {
		t.Fatalf("expected one entry after re-setting the same path, got %#v", updatedAgain.Storage.DeviceThresholds)
	}
	target, critical = updatedAgain.ThresholdsFor("/data")
	if target != 70 || critical != 85 {
		t.Fatalf("thresholds after re-set = %v/%v", target, critical)
	}
	if _, err := SetDeviceThreshold(c, "/data", "", 0, 90, true); err == nil {
		t.Fatal("expected an error for a non-positive target percent")
	}
	if _, err := SetDeviceThreshold(c, "/data", "", 80, 100, true); err == nil {
		t.Fatal("expected an error for a critical percent >= 100")
	}
	if _, err := SetDeviceThreshold(c, "", "", 80, 90, true); err == nil {
		t.Fatal("expected an error for an empty representativePath")
	}
}

// TestAutomaticRemovalEnabledForDefaultsTrue guards the whole point of
// storing this inverted (AutomaticRemovalDisabled, not "enabled"): a
// device with no explicit entry at all — true for most devices — must
// still be eligible for automatic removal, not silently opted out the
// moment this field was introduced.
func TestAutomaticRemovalEnabledForDefaultsTrue(t *testing.T) {
	var c Config
	if !c.AutomaticRemovalEnabledFor("/data") {
		t.Fatal("expected a device with no explicit entry to default to enabled")
	}
}

func TestSetDeviceThresholdPersistsAutomaticRemovalEnablement(t *testing.T) {
	var c Config
	disabled, err := SetDeviceThreshold(c, "/data", "", 80, 90, false)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.AutomaticRemovalEnabledFor("/data") {
		t.Fatal("expected the device to be disabled after saving enabled=false")
	}
	reenabled, err := SetDeviceThreshold(disabled, "/data", "", 80, 90, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reenabled.AutomaticRemovalEnabledFor("/data") {
		t.Fatal("expected the device to be re-enabled after saving enabled=true")
	}
}

// TestSetDeviceThresholdPersistsAndTrimsName guards the Device settings
// overlay's new Name field: it's saved and trimmed alongside the
// thresholds in the same upsert, and DeviceName reads it back — empty
// (never set) is the "no name assigned yet" state callers check for.
func TestSetDeviceThresholdPersistsAndTrimsName(t *testing.T) {
	var c Config
	if got := c.DeviceName("/data"); got != "" {
		t.Fatalf("expected no name before anything is set, got %q", got)
	}
	updated, err := SetDeviceThreshold(c, "/data", "  Media Drive  ", 80, 90, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := updated.DeviceName("/data"); got != "Media Drive" {
		t.Fatalf("expected the trimmed name to be persisted, got %q", got)
	}
	cleared, err := SetDeviceThreshold(updated, "/data", "", 80, 90, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := cleared.DeviceName("/data"); got != "" {
		t.Fatalf("expected an empty name to clear it, got %q", got)
	}
}

// TestSetDeviceThresholdPreservesAssignedID guards a real bug: this
// function's original upsert built a fresh DeviceThreshold from scratch,
// which would have silently wiped whatever id SyncDeviceRegistry had
// already assigned every time someone saved the Device settings overlay.
func TestSetDeviceThresholdPreservesAssignedID(t *testing.T) {
	registered, changed := SyncDeviceRegistry(Config{}, []string{"/data"})
	if !changed || registered.Storage.DeviceThresholds["/data"].ID == 0 {
		t.Fatalf("setup: expected an id to be assigned, got %#v", registered.Storage.DeviceThresholds)
	}
	saved, err := SetDeviceThreshold(registered, "/data", "Media Drive", 70, 85, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.Storage.DeviceThresholds["/data"].ID; got != registered.Storage.DeviceThresholds["/data"].ID {
		t.Fatalf("expected the assigned id to survive a threshold/name save, got %d want %d", got, registered.Storage.DeviceThresholds["/data"].ID)
	}
}

// TestSetDeviceThresholdClearingNameRevertsToDefault guards the actual
// point of clearing a device's Name: it's how a user reverts to the
// default, not how they end up with no name at all — SyncDeviceRegistry
// only sets "Device #<id>" once, the first time a device is seen, so
// without this an empty submission would just stay blank forever instead
// of reverting.
func TestSetDeviceThresholdClearingNameRevertsToDefault(t *testing.T) {
	registered, _ := SyncDeviceRegistry(Config{}, []string{"/data"})
	renamed, err := SetDeviceThreshold(registered, "/data", "Media Drive", 70, 85, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := renamed.DeviceName("/data"); got != "Media Drive" {
		t.Fatalf("setup: expected the custom name to be saved, got %q", got)
	}
	reverted, err := SetDeviceThreshold(renamed, "/data", "", 70, 85, true)
	if err != nil {
		t.Fatal(err)
	}
	wantID := registered.Storage.DeviceThresholds["/data"].ID
	if got := reverted.DeviceName("/data"); got != fmt.Sprintf("Device #%d", wantID) {
		t.Fatalf("expected clearing the name to revert to the default, got %q", got)
	}
}

// TestSyncDeviceRegistryAssignsLowestUnusedIDAndDefaultName guards the
// actual assignment algorithm: a brand-new device gets the lowest id not
// already in use, and its Name is set to the literal "Device #<id>" string
// at that moment — a real stored value, not a computed fallback — only
// because it had no name at all yet.
func TestSyncDeviceRegistryAssignsLowestUnusedIDAndDefaultName(t *testing.T) {
	var c Config
	updated, changed := SyncDeviceRegistry(c, []string{"/data/a"})
	if !changed {
		t.Fatal("expected assigning a fresh device to report a change")
	}
	entry := updated.Storage.DeviceThresholds["/data/a"]
	if entry.ID != 1 || entry.Name != "Device #1" {
		t.Fatalf("expected id=1 name=%q, got %#v", "Device #1", entry)
	}

	// A second device fills the next slot, not necessarily 2 if 1 were
	// somehow taken — exercised properly below by the reuse test.
	updated2, changed2 := SyncDeviceRegistry(updated, []string{"/data/a", "/data/b"})
	if !changed2 {
		t.Fatal("expected adding a second device to report a change")
	}
	if got := updated2.Storage.DeviceThresholds["/data/b"].ID; got != 2 {
		t.Fatalf("expected the second device to get id=2, got %d", got)
	}
	// Re-running with nothing new must be a no-op.
	_, changed3 := SyncDeviceRegistry(updated2, []string{"/data/a", "/data/b"})
	if changed3 {
		t.Fatal("expected re-running with the same live set to report no change")
	}
}

// TestSyncDeviceRegistryNeverOverwritesAnExistingName guards a device that
// already has a name (whether user-chosen or a prior default) — gaining an
// id (e.g. backfilled for an entry that predates this feature) must never
// clobber it.
func TestSyncDeviceRegistryNeverOverwritesAnExistingName(t *testing.T) {
	var c Config
	c.Storage.DeviceThresholds = map[string]DeviceThreshold{"/data/a": {TargetUsagePercent: 80, CriticalUsagePercent: 90, Name: "Media Drive"}}
	updated, changed := SyncDeviceRegistry(c, []string{"/data/a"})
	if !changed {
		t.Fatal("expected backfilling a missing id to report a change")
	}
	entry := updated.Storage.DeviceThresholds["/data/a"]
	if entry.ID != 1 {
		t.Fatalf("expected the id to still be backfilled, got %#v", entry)
	}
	if entry.Name != "Media Drive" {
		t.Fatalf("expected the existing name to survive id backfill untouched, got %q", entry.Name)
	}
}

// TestSyncDeviceRegistryPrunesAndReusesIDs guards the actual point of
// pruning: once a device is no longer discovered, its entry (and the id
// it was holding) must be removed so a later new device can reuse that
// id — otherwise ids would only ever climb.
func TestSyncDeviceRegistryPrunesAndReusesIDs(t *testing.T) {
	var c Config
	withTwo, _ := SyncDeviceRegistry(c, []string{"/data/a", "/data/b"})
	if withTwo.Storage.DeviceThresholds["/data/a"].ID != 1 || withTwo.Storage.DeviceThresholds["/data/b"].ID != 2 {
		t.Fatalf("setup: expected ids 1 and 2, got %#v", withTwo.Storage.DeviceThresholds)
	}

	// /data/a disappears (unplugged, service removed, etc.).
	pruned, changed := SyncDeviceRegistry(withTwo, []string{"/data/b"})
	if !changed {
		t.Fatal("expected pruning a disappeared device to report a change")
	}
	if _, exists := pruned.Storage.DeviceThresholds["/data/a"]; exists {
		t.Fatalf("expected /data/a's entry to be pruned, got %#v", pruned.Storage.DeviceThresholds)
	}

	// A new device now reuses id 1, since it's free again.
	withNew, changed := SyncDeviceRegistry(pruned, []string{"/data/b", "/data/c"})
	if !changed {
		t.Fatal("expected the new device to report a change")
	}
	if got := withNew.Storage.DeviceThresholds["/data/c"].ID; got != 1 {
		t.Fatalf("expected the freed id 1 to be reused, got %d", got)
	}
	if got := withNew.Storage.DeviceThresholds["/data/b"].ID; got != 2 {
		t.Fatalf("expected /data/b's id to be untouched, got %d", got)
	}
}

func TestSetCredentialsHashesPasswordAndVerifyPasswordChecksIt(t *testing.T) {
	var c Config
	if c.VerifyPassword("anything") {
		t.Fatal("no account exists yet; VerifyPassword must not accept anything")
	}
	updated, err := SetCredentials(c, "  Admin  ", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Auth.Username != "Admin" {
		t.Fatalf("username = %q, want trimmed \"Admin\"", updated.Auth.Username)
	}
	if updated.Auth.PasswordHash == "" || updated.Auth.PasswordHash == "correct-horse-battery" {
		t.Fatalf("password must be hashed, not stored as-is: %q", updated.Auth.PasswordHash)
	}
	if !updated.VerifyPassword("correct-horse-battery") {
		t.Fatal("VerifyPassword rejected the correct password")
	}
	if updated.VerifyPassword("wrong-password") {
		t.Fatal("VerifyPassword accepted the wrong password")
	}
}

func TestSetCredentialsRejectsEmptyUsernameAndShortPassword(t *testing.T) {
	var c Config
	if _, err := SetCredentials(c, "   ", "correct-horse-battery"); err == nil {
		t.Fatal("expected an error for an empty username")
	}
	if _, err := SetCredentials(c, "admin", "short"); err == nil {
		t.Fatal("expected an error for a too-short password")
	}
}

func TestSetTMDBAPIKeyTrimsAndClears(t *testing.T) {
	var c Config
	updated := SetTMDBAPIKey(c, "  a-real-key  ")
	if updated.TMDB.APIKey != "a-real-key" {
		t.Fatalf("expected the key to be trimmed, got %q", updated.TMDB.APIKey)
	}
	cleared := SetTMDBAPIKey(updated, "")
	if cleared.TMDB.APIKey != "" {
		t.Fatalf("expected an empty string to clear the key, got %q", cleared.TMDB.APIKey)
	}
}

func TestSetRemovalSettings(t *testing.T) {
	var c Config
	updated, err := SetRemovalSettings(c, RemovalAutoAuto, true, false, 75, true, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Removal.AutoMode != RemovalAutoAuto || !updated.Removal.AutoRemoveUnassociatedTorrents || updated.Removal.DryRun || updated.Removal.TorrentCarePercent != 75 || !updated.Removal.AutoUnmonitor || !updated.Removal.AutoExcludeFromImportLists || !updated.Removal.AutoRemoveIncompleteTorrents {
		t.Fatalf("expected all seven fields to be set as given, got %#v", updated.Removal)
	}
	reverted, err := SetRemovalSettings(updated, RemovalAutoDisabled, false, true, 25, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if reverted.Removal.AutoMode != RemovalAutoDisabled || reverted.Removal.AutoRemoveUnassociatedTorrents || !reverted.Removal.DryRun || reverted.Removal.TorrentCarePercent != 25 || reverted.Removal.AutoUnmonitor || reverted.Removal.AutoExcludeFromImportLists || reverted.Removal.AutoRemoveIncompleteTorrents {
		t.Fatalf("expected all seven fields to flip independently, got %#v", reverted.Removal)
	}
}

func TestSetRemovalSettingsRejectsUnknownAutoMode(t *testing.T) {
	var c Config
	if _, err := SetRemovalSettings(c, "sometimes", false, false, 50, false, false, false); err == nil {
		t.Fatal("expected an unrecognized auto_mode to be rejected")
	}
}

func TestSetRemovalSettingsRejectsTorrentCarePercentOutOfRange(t *testing.T) {
	var c Config
	if _, err := SetRemovalSettings(c, RemovalAutoDisabled, false, false, -1, false, false, false); err == nil {
		t.Fatal("expected a negative torrent_care_percent to be rejected")
	}
	if _, err := SetRemovalSettings(c, RemovalAutoDisabled, false, false, 101, false, false, false); err == nil {
		t.Fatal("expected a torrent_care_percent above 100 to be rejected")
	}
}

func TestRemovalDryRunDefaultsTrue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"server":{},"storage":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Removal.DryRun {
		t.Fatal("removal.dry_run must default true")
	}
	if err := os.WriteFile(path, []byte(`{"server":{},"storage":{},"removal":{"dry_run":false}}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Removal.DryRun {
		t.Fatal("explicit dry_run=false must be honored")
	}
}

func TestLoadNamedServices(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	body := `{"services":[{"type":"radarr","name":"Movies","url":"http://radarr","api_key":"x"},{"type":"sonarr","name":"Series","url":"http://sonarr","api_key":"y"}],"storage":{"target_usage_percent":90,"critical_usage_percent":95}}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Services) != 2 {
		t.Fatalf("got %d services", len(c.Services))
	}
	if c.Services[0].ID == "" || c.Services[0].Name != "Movies" {
		t.Fatalf("bad service: %+v", c.Services[0])
	}
	if c.Radarr.URL != "http://radarr" || c.Sonarr.URL != "http://sonarr" {
		t.Fatalf("legacy aliases not populated")
	}
}

func TestLoadRejectsReservedUnmanagedServiceName(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	body := `{"services":[{"type":"radarr","name":"uNmAnAgEd","url":"http://radarr"}],"storage":{"target_usage_percent":90,"critical_usage_percent":95}}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("expected reserved-name error")
	}
}

func TestLoadAllowsMultipleSonarrInstances(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	body := `{"services":[{"type":"sonarr","name":"TV","url":"http://s1"},{"type":"sonarr","name":"Anime","url":"http://s2"}],"storage":{"target_usage_percent":90,"critical_usage_percent":95}}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("expected multiple sonarr instances to be allowed, got %v", err)
	}
	if len(cfg.Services) != 2 {
		t.Fatalf("expected 2 services, got %#v", cfg.Services)
	}
}

func TestLoadFailsClosedOnDuplicateSingleInstanceRuntimeType(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	body := `{"services":[{"type":"jellyfin","name":"J1","url":"http://j1"},{"type":"jellyfin","name":"J2","url":"http://j2"}],"storage":{"target_usage_percent":90,"critical_usage_percent":95}}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("expected duplicate jellyfin instances to fail closed")
	}
}

// TestLoadDefaultsPopularityWeightForConfigWrittenBeforeItExisted guards
// the fix for a real gap: Popularity is a newer weight than the rest of
// Weights, so a config.json predating it has no value at all — which
// would otherwise silently make TMDB's popularity signal count for
// nothing even with TMDB fully enabled and fetching, until the user
// happened to notice and add the key themselves.
func TestLoadDefaultsPopularityWeightForConfigWrittenBeforeItExisted(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	body := `{"valuation":{"weights":{"rating":40}},"storage":{}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Valuation.Weights.Popularity == 0 {
		t.Fatal("expected a nonzero default Popularity weight for a config.json that predates the key")
	}
}

func TestExplicitZeroPopularityWeightIsPreserved(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	body := `{"valuation":{"weights":{"popularity":0}},"storage":{}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Valuation.Weights.Popularity != 0 {
		t.Fatalf("explicit zero popularity weight was replaced: %v", c.Valuation.Weights.Popularity)
	}
}

func TestSaveThenLoadRoundTripsServices(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	body := `{"services":[{"type":"radarr","name":"Movies","url":"http://radarr:7878","api_key":"key"}],"storage":{}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Services = append(loaded.Services, Service{Type: "sonarr", Name: "Series", URL: "http://sonarr:8989", APIKey: "key2"})
	if err := Save(p, loaded); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Services) != 2 {
		t.Fatalf("expected 2 services after save+reload, got %#v", reloaded.Services)
	}
	if reloaded.Radarr.URL != "http://radarr:7878" || reloaded.Sonarr.URL != "http://sonarr:8989" {
		t.Fatalf("derived adapter fields did not survive save+reload: %#v / %#v", reloaded.Radarr, reloaded.Sonarr)
	}
}

func TestSaveIsAtomicNoPartialFileOnMarshalableConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := Save(p, Config{Services: []Service{{Type: "seerr", Name: "Requests", URL: "http://seerr:5055"}}}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "config.json" {
			t.Fatalf("expected only the final config.json to remain, found leftover %q", entry.Name())
		}
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected config.json to be 0600, got %v", info.Mode().Perm())
	}
}

func TestAddServiceAppendsAndRefreshesDerivedFields(t *testing.T) {
	base := Config{Services: []Service{{Type: "sonarr", Name: "Series", URL: "http://sonarr:8989", APIKey: "sk"}}}
	base.populateDerivedServiceFields()

	updated, err := AddService(base, Service{Type: "radarr", Name: "Movies", URL: "http://radarr:7878", APIKey: "rk"})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Services) != 2 {
		t.Fatalf("expected 2 services, got %#v", updated.Services)
	}
	if updated.Radarr.URL != "http://radarr:7878" || updated.Radarr.APIKey != "rk" {
		t.Fatalf("Radarr derived field was not populated: %#v", updated.Radarr)
	}
	if updated.Sonarr.URL != "http://sonarr:8989" {
		t.Fatalf("existing Sonarr derived field should be unaffected: %#v", updated.Sonarr)
	}
	added := updated.Services[1]
	if added.ID == "" {
		t.Fatal("expected the new service to receive a stable ID")
	}
	// base must not have been mutated by AddService.
	if len(base.Services) != 1 {
		t.Fatalf("AddService must not mutate its input, got base=%#v", base.Services)
	}
}

func TestAddServiceAllowsSecondInstanceOfMultiInstanceType(t *testing.T) {
	base := Config{Services: []Service{{Type: "radarr", Name: "Movies", URL: "http://radarr:7878"}}}
	updated, err := AddService(base, Service{Type: "radarr", Name: "Movies 4K", URL: "http://radarr4k:7878"})
	if err != nil {
		t.Fatalf("expected a second radarr instance to be allowed, got %v", err)
	}
	if len(updated.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(updated.Services))
	}
}

func TestAddServiceRejectsSecondInstanceOfSingleInstanceType(t *testing.T) {
	base := Config{Services: []Service{{Type: "jellyfin", Name: "Jellyfin", URL: "http://jellyfin:8096"}}}
	if _, err := AddService(base, Service{Type: "jellyfin", Name: "Jellyfin 2", URL: "http://jellyfin2:8096"}); err == nil {
		t.Fatal("expected a second jellyfin instance to be rejected")
	}
}

func TestAddServiceRejectsDuplicateNameAndMissingURL(t *testing.T) {
	base := Config{Services: []Service{{Type: "radarr", Name: "Movies", URL: "http://radarr:7878"}}}
	if _, err := AddService(base, Service{Type: "sonarr", Name: "Movies", URL: "http://sonarr:8989"}); err == nil {
		t.Fatal("expected a duplicate name to be rejected")
	}
	if _, err := AddService(base, Service{Type: "sonarr", Name: "Series"}); err == nil {
		t.Fatal("expected a missing url to be rejected")
	}
}

func TestLoadAssignsAndPersistsIDOnceForServicesMissingOne(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	// No "id" field at all: the shape of a config.json written before ID was
	// ever persisted.
	body := `{"services":[{"type":"radarr","name":"Movies","url":"http://radarr:7878"}],"storage":{}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if first.Services[0].ID == "" {
		t.Fatal("expected Load to assign an ID to a service that has none")
	}
	assignedID := first.Services[0].ID

	// The assignment must have been persisted, not just held in memory: a
	// second Load must see the *same* ID, not generate a new one.
	second, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if second.Services[0].ID != assignedID {
		t.Fatalf("expected the assigned ID to be persisted and stable across loads, got %q then %q", assignedID, second.Services[0].ID)
	}
}

func TestEditServiceChangingURLPreservesID(t *testing.T) {
	base := Config{Services: []Service{{Type: "radarr", Name: "Movies", URL: "http://old-host:7878", APIKey: "key"}}}
	added, err := AddService(Config{}, base.Services[0])
	if err != nil {
		t.Fatal(err)
	}
	originalID := added.Services[0].ID

	updated, err := EditService(added, originalID, Service{Name: "Movies", URL: "http://brand-new-universe:7878", APIKey: "newkey"})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Services) != 1 {
		t.Fatalf("expected exactly one service after edit, got %#v", updated.Services)
	}
	if updated.Services[0].ID != originalID {
		t.Fatalf("expected ID to survive a URL change: got %q, want %q", updated.Services[0].ID, originalID)
	}
	if updated.Services[0].URL != "http://brand-new-universe:7878" || updated.Services[0].APIKey != "newkey" {
		t.Fatalf("expected URL/APIKey to actually change, got %#v", updated.Services[0])
	}
	if updated.Radarr.URL != "http://brand-new-universe:7878" {
		t.Fatalf("expected derived Radarr field to reflect the edit, got %#v", updated.Radarr)
	}
}

func TestEditServiceRenamePreservesIDAndRejectsCollision(t *testing.T) {
	base := Config{Services: []Service{
		{Type: "radarr", Name: "Movies", URL: "http://radarr:7878"},
		{Type: "sonarr", Name: "Series", URL: "http://sonarr:8989"},
	}}
	loaded, err := AddService(Config{}, base.Services[1])
	if err != nil {
		t.Fatal(err)
	}
	loaded, err = AddService(loaded, base.Services[0])
	if err != nil {
		t.Fatal(err)
	}
	var movieID string
	for _, i := range loaded.Services {
		if i.Type == "radarr" {
			movieID = i.ID
		}
	}

	renamed, err := EditService(loaded, movieID, Service{Name: "Films", URL: "http://radarr:7878"})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range renamed.Services {
		if i.Type == "radarr" && (i.Name != "Films" || i.ID != movieID) {
			t.Fatalf("expected rename to keep ID and change Name, got %#v", i)
		}
	}

	if _, err := EditService(loaded, movieID, Service{Name: "Series", URL: "http://radarr:7878"}); err == nil {
		t.Fatal("expected renaming to collide with the other service's name to be rejected")
	}
}

func TestEditServiceRejectsUnknownID(t *testing.T) {
	base := Config{Services: []Service{{Type: "radarr", Name: "Movies", URL: "http://radarr:7878", ID: "radarr-abc"}}}
	if _, err := EditService(base, "does-not-exist", Service{Name: "Movies", URL: "http://radarr:7878"}); err == nil {
		t.Fatal("expected editing an unknown ID to fail")
	}
}

func TestRemoveServiceDeletesAndResetsDerivedField(t *testing.T) {
	added, err := AddService(Config{}, Service{Type: "radarr", Name: "Movies", URL: "http://radarr:7878", APIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if added.Radarr.URL == "" {
		t.Fatal("test setup: expected Radarr derived field to be populated before removal")
	}
	id := added.Services[0].ID

	removed, err := RemoveService(added, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Services) != 0 {
		t.Fatalf("expected the service to be removed, got %#v", removed.Services)
	}
	if removed.Radarr != (Connection{}) {
		t.Fatalf("expected the derived Radarr field to reset to zero value once its only service is removed, got %#v", removed.Radarr)
	}
}

func TestRemoveServiceRejectsUnknownID(t *testing.T) {
	base := Config{Services: []Service{{Type: "radarr", Name: "Movies", URL: "http://radarr:7878", ID: "radarr-abc"}}}
	if _, err := RemoveService(base, "does-not-exist"); err == nil {
		t.Fatal("expected removing an unknown ID to fail")
	}
}

func TestValidateServiceRejectsDuplicateAndReservedNames(t *testing.T) {
	seen := map[string]bool{"movies": true}
	if err := validateService(Service{Type: "radarr", Name: "Movies", URL: "http://x"}, seen); err == nil {
		t.Fatal("expected a case-insensitive duplicate name to be rejected")
	}
	if err := validateService(Service{Type: "radarr", Name: "Unmanaged", URL: "http://x"}, map[string]bool{}); err == nil {
		t.Fatal("expected the reserved name \"Unmanaged\" to be rejected")
	}
	if err := validateService(Service{Type: "radarr", Name: "Movies", URL: "http://x"}, map[string]bool{}); err != nil {
		t.Fatalf("expected a valid new service to be accepted, got %v", err)
	}
}
