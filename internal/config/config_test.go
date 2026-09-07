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
	updated, err := SetDeviceThreshold(c, "/data", 80, 90)
	if err != nil {
		t.Fatal(err)
	}
	target, critical := updated.ThresholdsFor("/data")
	if target != 80 || critical != 90 {
		t.Fatalf("thresholds after set = %v/%v", target, critical)
	}
	updatedAgain, err := SetDeviceThreshold(updated, "/data", 70, 85)
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
	if _, err := SetDeviceThreshold(c, "/data", 0, 90); err == nil {
		t.Fatal("expected an error for a non-positive target percent")
	}
	if _, err := SetDeviceThreshold(c, "/data", 80, 100); err == nil {
		t.Fatal("expected an error for a critical percent >= 100")
	}
	if _, err := SetDeviceThreshold(c, "", 80, 90); err == nil {
		t.Fatal("expected an error for an empty representativePath")
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
	updated := SetRemovalSettings(c, true, true, false)
	if !updated.Removal.AutoEnabled || !updated.Removal.AutoRemoveUnassociatedTorrents || updated.Removal.DryRun {
		t.Fatalf("expected all three fields to be set as given, got %#v", updated.Removal)
	}
	reverted := SetRemovalSettings(updated, false, false, true)
	if reverted.Removal.AutoEnabled || reverted.Removal.AutoRemoveUnassociatedTorrents || !reverted.Removal.DryRun {
		t.Fatalf("expected all three fields to flip independently, got %#v", reverted.Removal)
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

func TestExplicitZeroTorrentWeightsArePreserved(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	body := `{"valuation":{"torrent_weights":{"seeds":0,"leechers":0,"upload_rate":0}},"storage":{}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Valuation.TorrentWeights != (TorrentValueWeights{}) {
		t.Fatalf("explicit zero torrent weights were replaced: %#v", c.Valuation.TorrentWeights)
	}
}

// TestLoadDefaultsPopularityWeightForConfigWrittenBeforeItExisted guards
// the fix for a real gap: Popularity is a newer weight than the rest of
// Weights, so a config.json predating it has no value at all — which
// would otherwise silently make TMDB's popularity signal count for
// nothing even with TMDB fully enabled and fetching, until the user
// happened to notice and add the key themselves. Mirrors how
// TorrentWeights already gets a one-time default for the same reason.
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
