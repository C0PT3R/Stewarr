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
