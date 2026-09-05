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
