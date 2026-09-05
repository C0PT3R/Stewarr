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
	service.activateClientLocked(added)
	// The connection check just above already proved this integration is
	// reachable; record that now instead of leaving the Services page to
	// show "Not checked yet" until the next scheduled refresh happens to run.
	service.setStatusLocked(added.Name, true, true, nil)
	service.mu.Unlock()
	return nil
}

// EditIntegration updates an existing integration's Name/URL/credentials —
// re-validating the connection against the *new* values before persisting,
// exactly like AddIntegration — and re-activates its client with them. The
// integration's ID (and therefore everything already attributed to it) never
// changes, even when the URL does: moving an integration to an entirely
// different address is exactly the point, not a side effect to guard against.
func (service *Service) EditIntegration(ctx context.Context, id string, updates config.Integration) error {
	service.configMu.Lock()
	defer service.configMu.Unlock()

	if service.configPath == "" {
		return fmt.Errorf("live config editing is unavailable: no config file path is set")
	}

	service.mu.RLock()
	currentCfg := service.cfg
	service.mu.RUnlock()

	updatedCfg, err := config.EditIntegration(currentCfg, id, updates)
	if err != nil {
		return err
	}
	var edited config.Integration
	for _, integration := range updatedCfg.Integrations {
		if integration.ID == id {
			edited = integration
			break
		}
	}

	if err := validateIntegrationConnection(ctx, edited); err != nil {
		return fmt.Errorf("connection check failed: %w", err)
	}

	if err := config.Save(service.configPath, updatedCfg); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}

	service.mu.Lock()
	oldName := ""
	for _, integration := range currentCfg.Integrations {
		if integration.ID == id {
			oldName = integration.Name
			break
		}
	}
	service.cfg = updatedCfg
	service.activateClientLocked(edited)
	if oldName != "" && oldName != edited.Name {
		delete(service.statuses, oldName)
	}
	service.setStatusLocked(edited.Name, true, true, nil)
	service.mu.Unlock()
	return nil
}

// RemoveIntegration deletes an existing integration and deactivates its
// client immediately, without disturbing the Service's existing in-memory
// reconciliation state. Whatever it previously owned is not touched here —
// it simply stops matching any configured integration on the next
// reconciliation and is reported as Unmanaged from then on.
func (service *Service) RemoveIntegration(id string) error {
	service.configMu.Lock()
	defer service.configMu.Unlock()

	if service.configPath == "" {
		return fmt.Errorf("live config editing is unavailable: no config file path is set")
	}

	service.mu.RLock()
	currentCfg := service.cfg
	service.mu.RUnlock()

	var removed config.Integration
	for _, integration := range currentCfg.Integrations {
		if integration.ID == id {
			removed = integration
			break
		}
	}

	updatedCfg, err := config.RemoveIntegration(currentCfg, id)
	if err != nil {
		return err
	}

	if err := config.Save(service.configPath, updatedCfg); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}

	service.mu.Lock()
	service.cfg = updatedCfg
	// Deactivate by reconstructing the same "unconfigured" client shape New
	// already uses at startup for a type with no configured integration —
	// every reconciliation loop assumes this field is never nil.
	service.activateClientLocked(config.Integration{Type: removed.Type})
	delete(service.statuses, removed.Name)
	service.mu.Unlock()
	return nil
}

// activateClientLocked (re)builds the client for integration's type from its
// current fields. Called with service.mu already held for writing.
func (service *Service) activateClientLocked(integration config.Integration) {
	switch integration.Type {
	case "radarr":
		service.rad = radarr.New(integration.URL, integration.APIKey)
	case "sonarr":
		service.son = sonarr.New(integration.URL, integration.APIKey)
	case "jellyfin":
		service.jf = jellyfin.New(integration.URL, integration.APIKey)
	case "seerr":
		service.seerr = seerr.New(integration.URL, integration.APIKey)
	case "qbittorrent":
		service.qb = qbittorrent.New(integration.Name, integration.URL, integration.Username, integration.Password, integration.APIKey)
	}
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
