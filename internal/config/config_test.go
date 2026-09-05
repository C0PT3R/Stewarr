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

func TestRejectsUnknownIntegrationType(t *testing.T) {
	p := writeConfig(t, `{"integrations":[{"type":"mystery","name":"Mystery","root_path":"/data"}]}`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "unsupported type") {
		t.Fatalf("error=%v", err)
	}
}

func TestRejectsRootPathForCurrentAdapters(t *testing.T) {
	p := writeConfig(t, `{"integrations":[{"type":"radarr","name":"Movies","url":"http://radarr","root_path":"/data"}]}`)
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
	p := writeConfig(t, `{"storage":{"target_usage_percent":90,"critical_usage_percent":80}}`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Storage.TargetUsagePercent != 90 || c.Storage.CriticalUsagePercent != 80 {
		t.Fatalf("storage=%+v", c.Storage)
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

func TestLoadNamedIntegrations(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	body := `{"integrations":[{"type":"radarr","name":"Movies","url":"http://radarr","api_key":"x"},{"type":"sonarr","name":"Series","url":"http://sonarr","api_key":"y"}],"storage":{"target_usage_percent":90,"critical_usage_percent":95}}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Integrations) != 2 {
		t.Fatalf("got %d integrations", len(c.Integrations))
	}
	if c.Integrations[0].ID == "" || c.Integrations[0].Name != "Movies" {
		t.Fatalf("bad integration: %+v", c.Integrations[0])
	}
	if c.Radarr.URL != "http://radarr" || c.Sonarr.URL != "http://sonarr" {
		t.Fatalf("legacy aliases not populated")
	}
}

func TestLoadRejectsReservedUnmanagedIntegrationName(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	body := `{"integrations":[{"type":"radarr","name":"uNmAnAgEd","url":"http://radarr"}],"storage":{"target_usage_percent":90,"critical_usage_percent":95}}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("expected reserved-name error")
	}
}

func TestLoadFailsClosedOnDuplicateRuntimeIntegrationType(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	body := `{"integrations":[{"type":"sonarr","name":"TV","url":"http://s1"},{"type":"sonarr","name":"Anime","url":"http://s2"}],"storage":{"target_usage_percent":90,"critical_usage_percent":95}}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("expected duplicate runtime type to fail closed")
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

func TestSaveThenLoadRoundTripsIntegrations(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	body := `{"integrations":[{"type":"radarr","name":"Movies","url":"http://radarr:7878","api_key":"key"}],"storage":{}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Integrations = append(loaded.Integrations, Integration{Type: "sonarr", Name: "Series", URL: "http://sonarr:8989", APIKey: "key2"})
	if err := Save(p, loaded); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Integrations) != 2 {
		t.Fatalf("expected 2 integrations after save+reload, got %#v", reloaded.Integrations)
	}
	if reloaded.Radarr.URL != "http://radarr:7878" || reloaded.Sonarr.URL != "http://sonarr:8989" {
		t.Fatalf("derived adapter fields did not survive save+reload: %#v / %#v", reloaded.Radarr, reloaded.Sonarr)
	}
}

func TestSaveIsAtomicNoPartialFileOnMarshalableConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := Save(p, Config{Integrations: []Integration{{Type: "seerr", Name: "Requests", URL: "http://seerr:5055"}}}); err != nil {
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

func TestAddIntegrationAppendsAndRefreshesDerivedFields(t *testing.T) {
	base := Config{Integrations: []Integration{{Type: "sonarr", Name: "Series", URL: "http://sonarr:8989", APIKey: "sk"}}}
	base.populateDerivedIntegrationFields()

	updated, err := AddIntegration(base, Integration{Type: "radarr", Name: "Movies", URL: "http://radarr:7878", APIKey: "rk"})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Integrations) != 2 {
		t.Fatalf("expected 2 integrations, got %#v", updated.Integrations)
	}
	if updated.Radarr.URL != "http://radarr:7878" || updated.Radarr.APIKey != "rk" {
		t.Fatalf("Radarr derived field was not populated: %#v", updated.Radarr)
	}
	if updated.Sonarr.URL != "http://sonarr:8989" {
		t.Fatalf("existing Sonarr derived field should be unaffected: %#v", updated.Sonarr)
	}
	added := updated.Integrations[1]
	if added.ID == "" {
		t.Fatal("expected the new integration to receive a stable ID")
	}
	// base must not have been mutated by AddIntegration.
	if len(base.Integrations) != 1 {
		t.Fatalf("AddIntegration must not mutate its input, got base=%#v", base.Integrations)
	}
}

func TestAddIntegrationRejectsSecondInstanceOfSameType(t *testing.T) {
	base := Config{Integrations: []Integration{{Type: "radarr", Name: "Movies", URL: "http://radarr:7878"}}}
	if _, err := AddIntegration(base, Integration{Type: "radarr", Name: "Movies 4K", URL: "http://radarr4k:7878"}); err == nil {
		t.Fatal("expected a second radarr instance to be rejected by this runtime")
	}
}

func TestAddIntegrationRejectsDuplicateNameAndMissingURL(t *testing.T) {
	base := Config{Integrations: []Integration{{Type: "radarr", Name: "Movies", URL: "http://radarr:7878"}}}
	if _, err := AddIntegration(base, Integration{Type: "sonarr", Name: "Movies", URL: "http://sonarr:8989"}); err == nil {
		t.Fatal("expected a duplicate name to be rejected")
	}
	if _, err := AddIntegration(base, Integration{Type: "sonarr", Name: "Series"}); err == nil {
		t.Fatal("expected a missing url to be rejected")
	}
}

func TestLoadAssignsAndPersistsIDOnceForIntegrationsMissingOne(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	// No "id" field at all: the shape of a config.json written before ID was
	// ever persisted.
	body := `{"integrations":[{"type":"radarr","name":"Movies","url":"http://radarr:7878"}],"storage":{}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if first.Integrations[0].ID == "" {
		t.Fatal("expected Load to assign an ID to an integration that has none")
	}
	assignedID := first.Integrations[0].ID

	// The assignment must have been persisted, not just held in memory: a
	// second Load must see the *same* ID, not generate a new one.
	second, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if second.Integrations[0].ID != assignedID {
		t.Fatalf("expected the assigned ID to be persisted and stable across loads, got %q then %q", assignedID, second.Integrations[0].ID)
	}
}

func TestEditIntegrationChangingURLPreservesID(t *testing.T) {
	base := Config{Integrations: []Integration{{Type: "radarr", Name: "Movies", URL: "http://old-host:7878", APIKey: "key"}}}
	added, err := AddIntegration(Config{}, base.Integrations[0])
	if err != nil {
		t.Fatal(err)
	}
	originalID := added.Integrations[0].ID

	updated, err := EditIntegration(added, originalID, Integration{Name: "Movies", URL: "http://brand-new-universe:7878", APIKey: "newkey"})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Integrations) != 1 {
		t.Fatalf("expected exactly one integration after edit, got %#v", updated.Integrations)
	}
	if updated.Integrations[0].ID != originalID {
		t.Fatalf("expected ID to survive a URL change: got %q, want %q", updated.Integrations[0].ID, originalID)
	}
	if updated.Integrations[0].URL != "http://brand-new-universe:7878" || updated.Integrations[0].APIKey != "newkey" {
		t.Fatalf("expected URL/APIKey to actually change, got %#v", updated.Integrations[0])
	}
	if updated.Radarr.URL != "http://brand-new-universe:7878" {
		t.Fatalf("expected derived Radarr field to reflect the edit, got %#v", updated.Radarr)
	}
}

func TestEditIntegrationRenamePreservesIDAndRejectsCollision(t *testing.T) {
	base := Config{Integrations: []Integration{
		{Type: "radarr", Name: "Movies", URL: "http://radarr:7878"},
		{Type: "sonarr", Name: "Series", URL: "http://sonarr:8989"},
	}}
	loaded, err := AddIntegration(Config{}, base.Integrations[1])
	if err != nil {
		t.Fatal(err)
	}
	loaded, err = AddIntegration(loaded, base.Integrations[0])
	if err != nil {
		t.Fatal(err)
	}
	var movieID string
	for _, i := range loaded.Integrations {
		if i.Type == "radarr" {
			movieID = i.ID
		}
	}

	renamed, err := EditIntegration(loaded, movieID, Integration{Name: "Films", URL: "http://radarr:7878"})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range renamed.Integrations {
		if i.Type == "radarr" && (i.Name != "Films" || i.ID != movieID) {
			t.Fatalf("expected rename to keep ID and change Name, got %#v", i)
		}
	}

	if _, err := EditIntegration(loaded, movieID, Integration{Name: "Series", URL: "http://radarr:7878"}); err == nil {
		t.Fatal("expected renaming to collide with the other integration's name to be rejected")
	}
}

func TestEditIntegrationRejectsUnknownID(t *testing.T) {
	base := Config{Integrations: []Integration{{Type: "radarr", Name: "Movies", URL: "http://radarr:7878", ID: "radarr-abc"}}}
	if _, err := EditIntegration(base, "does-not-exist", Integration{Name: "Movies", URL: "http://radarr:7878"}); err == nil {
		t.Fatal("expected editing an unknown ID to fail")
	}
}

func TestRemoveIntegrationDeletesAndResetsDerivedField(t *testing.T) {
	added, err := AddIntegration(Config{}, Integration{Type: "radarr", Name: "Movies", URL: "http://radarr:7878", APIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if added.Radarr.URL == "" {
		t.Fatal("test setup: expected Radarr derived field to be populated before removal")
	}
	id := added.Integrations[0].ID

	removed, err := RemoveIntegration(added, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Integrations) != 0 {
		t.Fatalf("expected the integration to be removed, got %#v", removed.Integrations)
	}
	if removed.Radarr != (Service{}) {
		t.Fatalf("expected the derived Radarr field to reset to zero value once its only integration is removed, got %#v", removed.Radarr)
	}
}

func TestRemoveIntegrationRejectsUnknownID(t *testing.T) {
	base := Config{Integrations: []Integration{{Type: "radarr", Name: "Movies", URL: "http://radarr:7878", ID: "radarr-abc"}}}
	if _, err := RemoveIntegration(base, "does-not-exist"); err == nil {
		t.Fatal("expected removing an unknown ID to fail")
	}
}

func TestValidateIntegrationRejectsDuplicateAndReservedNames(t *testing.T) {
	seen := map[string]bool{"movies": true}
	if err := validateIntegration(Integration{Type: "radarr", Name: "Movies", URL: "http://x"}, seen); err == nil {
		t.Fatal("expected a case-insensitive duplicate name to be rejected")
	}
	if err := validateIntegration(Integration{Type: "radarr", Name: "Unmanaged", URL: "http://x"}, map[string]bool{}); err == nil {
		t.Fatal("expected the reserved name \"Unmanaged\" to be rejected")
	}
	if err := validateIntegration(Integration{Type: "radarr", Name: "Movies", URL: "http://x"}, map[string]bool{}); err != nil {
		t.Fatalf("expected a valid new integration to be accepted, got %v", err)
	}
}
