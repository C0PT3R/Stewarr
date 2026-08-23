package config

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestLoadRejectsReservedUnclaimedIntegrationName(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	body := `{"integrations":[{"type":"radarr","name":"uNcLaImEd","url":"http://radarr"}],"storage":{"target_usage_percent":90,"critical_usage_percent":95}}`
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
