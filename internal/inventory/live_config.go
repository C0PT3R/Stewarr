package inventory

import (
	"context"
	"fmt"

	"connarr/internal/config"
	"connarr/internal/integrations/jellyfin"
	"connarr/internal/integrations/qbittorrent"
	"connarr/internal/integrations/radarr"
	"connarr/internal/integrations/seerr"
	"connarr/internal/integrations/sonarr"
)

// AddIntegration is the first slice of live config editing: validate a new
// integration (structurally, then a real connection check), persist it, and
// activate its client immediately — all without disturbing the Service's
// existing in-memory reconciliation state (items, torrents, files, generation
// counters). Only *adding* a not-yet-configured integration type is
// supported today; the runtime still has one adapter slot per type (see
// config.AddIntegration's "multiple instances" guard), so this rejects a
// second integration of a type that already exists, exactly like a static
// config.json would.
func (service *Service) AddIntegration(ctx context.Context, candidate config.Integration) error {
	service.configMu.Lock()
	defer service.configMu.Unlock()

	if service.configPath == "" {
		return fmt.Errorf("live config editing is unavailable: no config file path is set")
	}

	service.mu.RLock()
	currentCfg := service.cfg
	service.mu.RUnlock()

	updatedCfg, err := config.AddIntegration(currentCfg, candidate)
	if err != nil {
		return err
	}
	// The newly added entry is always the last one AddIntegration appends.
	added := updatedCfg.Integrations[len(updatedCfg.Integrations)-1]

	if err := validateIntegrationConnection(ctx, added); err != nil {
		return fmt.Errorf("connection check failed: %w", err)
	}

	if err := config.Save(service.configPath, updatedCfg); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}

	service.mu.Lock()
	service.cfg = updatedCfg
	switch added.Type {
	case "radarr":
		service.rad = radarr.New(added.URL, added.APIKey)
	case "sonarr":
		service.son = sonarr.New(added.URL, added.APIKey)
	case "jellyfin":
		service.jf = jellyfin.New(added.URL, added.APIKey)
	case "seerr":
		service.seerr = seerr.New(added.URL, added.APIKey)
	case "qbittorrent":
		service.qb = qbittorrent.New(added.Name, added.URL, added.Username, added.Password, added.APIKey)
	}
	// The connection check just above already proved this integration is
	// reachable; record that now instead of leaving the Services page to
	// show "Not checked yet" until the next scheduled refresh happens to run.
	service.setStatusLocked(added.Name, true, true, nil)
	service.mu.Unlock()
	return nil
}

func validateIntegrationConnection(ctx context.Context, integration config.Integration) error {
	switch integration.Type {
	case "radarr":
		return radarr.New(integration.URL, integration.APIKey).WithContext(ctx).Validate()
	case "sonarr":
		return sonarr.New(integration.URL, integration.APIKey).WithContext(ctx).Validate()
	case "jellyfin":
		return jellyfin.New(integration.URL, integration.APIKey).WithContext(ctx).Validate()
	case "seerr":
		return seerr.New(integration.URL, integration.APIKey).WithContext(ctx).Validate()
	case "qbittorrent":
		return qbittorrent.New(integration.Name, integration.URL, integration.Username, integration.Password, integration.APIKey).WithContext(ctx).Validate()
	default:
		return fmt.Errorf("unsupported integration type %q", integration.Type)
	}
}
