package inventory

import (
	"context"
	"fmt"
	"log"

	"stewarr/internal/config"
	"stewarr/internal/services/imdb"
	"stewarr/internal/services/jellyfin"
	"stewarr/internal/services/qbittorrent"
	"stewarr/internal/services/radarr"
	"stewarr/internal/services/seerr"
	"stewarr/internal/services/sonarr"
	"stewarr/internal/services/tmdb"
	"stewarr/internal/valuation"
)

// AddService is the first slice of live config editing: validate a new
// service (structurally, then a real connection check), persist it, and
// activate its client immediately — all without disturbing the Service's
// existing in-memory reconciliation state (items, torrents, files, generation
// counters). Only *adding* a not-yet-configured service type is
// supported today; the runtime still has one adapter slot per type (see
// config.AddService's "multiple instances" guard), so this rejects a
// second service of a type that already exists, exactly like a static
// config.json would.
func (service *Service) AddService(ctx context.Context, candidate config.Service) error {
	service.configMu.Lock()
	defer service.configMu.Unlock()

	if service.configPath == "" {
		return fmt.Errorf("live config editing is unavailable: no config file path is set")
	}

	service.mu.RLock()
	currentCfg := service.cfg
	service.mu.RUnlock()

	updatedCfg, err := config.AddService(currentCfg, candidate)
	if err != nil {
		return err
	}
	// The newly added entry is always the last one AddService appends.
	added := updatedCfg.Services[len(updatedCfg.Services)-1]

	if err := validateServiceConnection(ctx, added); err != nil {
		return fmt.Errorf("connection check failed: %w", err)
	}

	if err := config.Save(service.configPath, updatedCfg); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}

	service.mu.Lock()
	service.cfg = updatedCfg
	service.activateClientLocked(added)
	// The connection check just above already proved this service is
	// reachable; record that now instead of leaving the Services page to
	// show "Not checked yet" until the next scheduled refresh happens to run.
	service.setStatusLocked(added.Name, true, true, nil)
	service.mu.Unlock()
	return nil
}

// TestServiceConnection runs the same live connection check AddService and
// EditService already perform internally, but on its own — no config
// mutation, no persistence, no client activation. This is step 1 of the
// service setup overlay: fast feedback on whether the entered
// URL/credentials actually work, before showing step 2's extra fields.
func (service *Service) TestServiceConnection(ctx context.Context, candidate config.Service) error {
	return validateServiceConnection(ctx, candidate)
}

// EditService updates an existing service's Name/URL/credentials —
// re-validating the connection against the *new* values before persisting,
// exactly like AddService — and re-activates its client with them. The
// service's ID (and therefore everything already attributed to it) never
// changes, even when the URL does: moving a service to an entirely
// different address is exactly the point, not a side effect to guard against.
func (service *Service) EditService(ctx context.Context, id string, updates config.Service) error {
	service.configMu.Lock()
	defer service.configMu.Unlock()

	if service.configPath == "" {
		return fmt.Errorf("live config editing is unavailable: no config file path is set")
	}

	service.mu.RLock()
	currentCfg := service.cfg
	service.mu.RUnlock()

	updatedCfg, err := config.EditService(currentCfg, id, updates)
	if err != nil {
		return err
	}
	var edited config.Service
	for _, svc := range updatedCfg.Services {
		if svc.ID == id {
			edited = svc
			break
		}
	}

	if err := validateServiceConnection(ctx, edited); err != nil {
		return fmt.Errorf("connection check failed: %w", err)
	}

	if err := config.Save(service.configPath, updatedCfg); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}

	service.mu.Lock()
	oldName := ""
	for _, svc := range currentCfg.Services {
		if svc.ID == id {
			oldName = svc.Name
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

// RemoveService deletes an existing service and deactivates its
// client immediately, without disturbing the Service's existing in-memory
// reconciliation state. Whatever it previously owned is not touched here —
// it simply stops matching any configured service on the next
// reconciliation and is reported as Unmanaged from then on.
func (service *Service) RemoveService(id string) error {
	service.configMu.Lock()
	defer service.configMu.Unlock()

	if service.configPath == "" {
		return fmt.Errorf("live config editing is unavailable: no config file path is set")
	}

	service.mu.RLock()
	currentCfg := service.cfg
	service.mu.RUnlock()

	var removed config.Service
	for _, svc := range currentCfg.Services {
		if svc.ID == id {
			removed = svc
			break
		}
	}

	updatedCfg, err := config.RemoveService(currentCfg, id)
	if err != nil {
		return err
	}

	if err := config.Save(service.configPath, updatedCfg); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}

	service.mu.Lock()
	service.cfg = updatedCfg
	service.deactivateClientLocked(removed)
	delete(service.statuses, removed.Name)
	service.mu.Unlock()
	return nil
}

// SetDeviceThreshold idempotently persists one storage device's reclamation
// thresholds, name, and automatic-removal enablement together. Unlike
// AddService/EditService this isn't a live-service concept — no connection
// check runs, and no client is (de)activated. Editing the same device from
// any service overlay that shares it converges to the same
// config.SetDeviceThreshold entry.
func (service *Service) SetDeviceThreshold(representativePath, name string, target, critical float64, automaticRemovalEnabled bool) error {
	service.configMu.Lock()
	defer service.configMu.Unlock()

	if service.configPath == "" {
		return fmt.Errorf("live config editing is unavailable: no config file path is set")
	}

	service.mu.RLock()
	currentCfg := service.cfg
	service.mu.RUnlock()

	updatedCfg, err := config.SetDeviceThreshold(currentCfg, representativePath, name, target, critical, automaticRemovalEnabled)
	if err != nil {
		return err
	}

	if err := config.Save(service.configPath, updatedCfg); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}

	service.mu.Lock()
	service.cfg = updatedCfg
	service.mu.Unlock()
	return nil
}

// syncDeviceRegistry assigns a stable id/default name to any newly
// discovered device and prunes entries for devices no longer discovered —
// see config.SyncDeviceRegistry. Called once at the end of a full file
// reconciliation (reconcileFiles), the one point with the complete live
// device set; must never be called from the targeted/delta reconciliation
// path, which only knows a partial scope. Best-effort: the authoritative
// file/media state has already been committed by the time this runs, so a
// persist failure here is logged rather than surfaced as a reconciliation
// failure — cosmetic device identity isn't worth discarding real work
// over.
func (service *Service) syncDeviceRegistry() {
	if service.configPath == "" {
		return
	}
	groups, order := service.knownDeviceRoots()
	livePaths := make([]string, 0, len(order))
	for _, device := range order {
		livePaths = append(livePaths, groups[device].representative)
	}

	service.configMu.Lock()
	defer service.configMu.Unlock()

	service.mu.RLock()
	currentCfg := service.cfg
	service.mu.RUnlock()

	updatedCfg, changed := config.SyncDeviceRegistry(currentCfg, livePaths)
	if !changed {
		return
	}
	if err := config.Save(service.configPath, updatedCfg); err != nil {
		log.Printf("[inventory] persist device registry sync: %v", err)
		return
	}
	service.mu.Lock()
	service.cfg = updatedCfg
	service.mu.Unlock()
}

// SetTMDBAPIKey idempotently persists the TMDB enrichment API key (an
// empty string disables it). Like SetCredentials/SetDeviceThreshold this
// isn't a config.Service concept — TMDB has no URL and only ever one
// instance — so it lives here directly rather than going through
// AddService/EditService. No connection check runs before persisting;
// RefreshTMDB's own Preflight (ValidateTMDB) catches a bad key on its next
// scheduled run instead.
func (service *Service) SetTMDBAPIKey(apiKey string) error {
	service.configMu.Lock()
	defer service.configMu.Unlock()

	if service.configPath == "" {
		return fmt.Errorf("live config editing is unavailable: no config file path is set")
	}

	service.mu.RLock()
	currentCfg := service.cfg
	service.mu.RUnlock()

	updatedCfg := config.SetTMDBAPIKey(currentCfg, apiKey)

	if err := config.Save(service.configPath, updatedCfg); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}

	service.mu.Lock()
	service.cfg = updatedCfg
	service.tmdb = tmdb.New(updatedCfg.TMDB.APIKey)
	// Turning TMDB off is a deliberate opt-out: stop influencing valuation
	// immediately rather than leaving last-known rating/popularity values
	// to linger until something else happens to overwrite them.
	if updatedCfg.TMDB.APIKey == "" && service.generation > 0 {
		items := cloneMedia(service.items)
		clearTMDBFacts(items)
		valuation.ApplyMedia(items, service.cfg, service.imdbRatings)
		if service.db != nil {
			_ = service.db.PublishEnrichment("tmdb", service.generation, items)
		}
		service.items = items
		service.reliability.TMDB = "not configured"
		service.reliability.Valuation = enrichmentReliable(service.reliability.Jellyfin) && enrichmentReliable(service.reliability.Seerr) && enrichmentReliable(service.reliability.TMDB)
	}
	service.mu.Unlock()
	return nil
}

// SetIMDbEnabled toggles IMDb ratings enrichment. Enabling immediately
// loads whatever snapshot is already cached on disk (from a previous
// enabled period) into memory rather than waiting for the next scheduled
// daily fetch — cheap, no network involved, and re-scores right away so
// re-enabling doesn't sit inert until the next fetch window. Disabling
// clears the in-memory map (not the on-disk table, so re-enabling later
// doesn't force a redundant re-download) and re-scores immediately, the
// same deliberate-opt-out treatment SetTMDBAPIKey gives clearing its key.
func (service *Service) SetIMDbEnabled(enabled bool) error {
	service.configMu.Lock()
	defer service.configMu.Unlock()

	if service.configPath == "" {
		return fmt.Errorf("live config editing is unavailable: no config file path is set")
	}

	service.mu.RLock()
	currentCfg := service.cfg
	service.mu.RUnlock()

	updatedCfg := config.SetIMDbEnabled(currentCfg, enabled)

	if err := config.Save(service.configPath, updatedCfg); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}

	var loaded map[string]imdb.Rating
	if enabled && service.db != nil {
		if ratings, _, err := service.db.LoadIMDbRatings(); err == nil {
			loaded = ratings
		} else {
			log.Printf("[inventory] load cached IMDb ratings on enable: %v", err)
		}
	}

	service.mu.Lock()
	service.cfg = updatedCfg
	service.imdbRatings = loaded
	if service.generation > 0 {
		items := cloneMedia(service.items)
		valuation.ApplyMedia(items, service.cfg, service.imdbRatings)
		if service.db != nil {
			_ = service.db.PublishEnrichment("imdb", service.generation, items)
		}
		service.items = items
	}
	service.mu.Unlock()
	return nil
}

// SetRemovalSettings idempotently persists the global auto-removal switches
// (auto-removal is also still gated per-service by Service.
// AllowAutomaticRemoval — see EditService). Nothing here needs a connection
// check or an in-memory side effect beyond swapping the config: unlike
// TMDB, these flags only change what the next scheduled evaluation or
// removal submission does, never anything already published.
func (service *Service) SetRemovalSettings(autoMode string, autoRemoveUnassociated, dryRun bool, torrentCarePercent float64, autoUnmonitor, autoExcludeFromImportLists, autoRemoveIncomplete bool) error {
	service.configMu.Lock()
	defer service.configMu.Unlock()

	if service.configPath == "" {
		return fmt.Errorf("live config editing is unavailable: no config file path is set")
	}

	service.mu.RLock()
	currentCfg := service.cfg
	service.mu.RUnlock()

	updatedCfg, err := config.SetRemovalSettings(currentCfg, autoMode, autoRemoveUnassociated, dryRun, torrentCarePercent, autoUnmonitor, autoExcludeFromImportLists, autoRemoveIncomplete)
	if err != nil {
		return err
	}

	if err := config.Save(service.configPath, updatedCfg); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}

	service.mu.Lock()
	service.cfg = updatedCfg
	service.mu.Unlock()
	return nil
}

// buildRadarrClients/buildSonarrClients/buildQBittorrentClients construct one
// live client per configured instance of their type, keyed by the
// service's stable ID. Radarr/Sonarr/qBittorrent are the three types
// that support more than one configured instance (see
// config.multiInstanceAllowed); Jellyfin/Seerr stay single-client.
func buildRadarrClients(configuration config.Config) map[string]*radarr.Client {
	out := map[string]*radarr.Client{}
	for _, svc := range configuration.ServicesOfType("radarr") {
		out[svc.ID] = radarr.New(svc.URL, svc.APIKey)
	}
	return out
}

func buildSonarrClients(configuration config.Config) map[string]*sonarr.Client {
	out := map[string]*sonarr.Client{}
	for _, svc := range configuration.ServicesOfType("sonarr") {
		out[svc.ID] = sonarr.New(svc.URL, svc.APIKey)
	}
	return out
}

func buildQBittorrentClients(configuration config.Config) map[string]*qbittorrent.Client {
	out := map[string]*qbittorrent.Client{}
	for _, svc := range configuration.ServicesOfType("qbittorrent") {
		out[svc.ID] = qbittorrent.New(svc.Name, svc.URL, svc.Username, svc.Password, svc.APIKey)
	}
	return out
}

// activateClientLocked (re)builds the client for service from its
// current fields and activates it under its stable ID (for the
// multi-instance types) or as the single client (Jellyfin/Seerr). Called
// with service.mu already held for writing.
func (service *Service) activateClientLocked(svc config.Service) {
	switch svc.Type {
	case "radarr":
		service.rad[svc.ID] = radarr.New(svc.URL, svc.APIKey)
	case "sonarr":
		service.son[svc.ID] = sonarr.New(svc.URL, svc.APIKey)
	case "jellyfin":
		service.jf = jellyfin.New(svc.URL, svc.APIKey)
	case "seerr":
		service.seerr = seerr.New(svc.URL, svc.APIKey)
	case "qbittorrent":
		service.qb[svc.ID] = qbittorrent.New(svc.Name, svc.URL, svc.Username, svc.Password, svc.APIKey)
	}
}

// deactivateClientLocked removes service's client entirely (for the
// multi-instance types, so a removed instance stops matching any future
// lookup by ID) or resets it to the "unconfigured" shape every
// reconciliation loop assumes is never nil (Jellyfin/Seerr). Called with
// service.mu already held for writing.
func (service *Service) deactivateClientLocked(svc config.Service) {
	switch svc.Type {
	case "radarr":
		delete(service.rad, svc.ID)
	case "sonarr":
		delete(service.son, svc.ID)
	case "qbittorrent":
		delete(service.qb, svc.ID)
	case "jellyfin":
		service.jf = jellyfin.New("", "")
	case "seerr":
		service.seerr = seerr.New("", "")
	}
}

func validateServiceConnection(ctx context.Context, svc config.Service) error {
	switch svc.Type {
	case "radarr":
		return radarr.New(svc.URL, svc.APIKey).WithContext(ctx).Validate()
	case "sonarr":
		return sonarr.New(svc.URL, svc.APIKey).WithContext(ctx).Validate()
	case "jellyfin":
		return jellyfin.New(svc.URL, svc.APIKey).WithContext(ctx).Validate()
	case "seerr":
		return seerr.New(svc.URL, svc.APIKey).WithContext(ctx).Validate()
	case "qbittorrent":
		return qbittorrent.New(svc.Name, svc.URL, svc.Username, svc.Password, svc.APIKey).WithContext(ctx).Validate()
	default:
		return fmt.Errorf("unsupported service type %q", svc.Type)
	}
}

// SetCredentials idempotently persists the single admin account's
// username/password (bcrypt-hashed by config.SetCredentials). Used for both
// first-run setup and later password changes — like SetDeviceThreshold this
// isn't a live-service concept, so no connection check or client
// (de)activation happens here.
func (service *Service) SetCredentials(username, password string) error {
	service.configMu.Lock()
	defer service.configMu.Unlock()

	if service.configPath == "" {
		return fmt.Errorf("live config editing is unavailable: no config file path is set")
	}

	service.mu.RLock()
	currentCfg := service.cfg
	service.mu.RUnlock()

	updatedCfg, err := config.SetCredentials(currentCfg, username, password)
	if err != nil {
		return err
	}

	if err := config.Save(service.configPath, updatedCfg); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}

	service.mu.Lock()
	service.cfg = updatedCfg
	service.mu.Unlock()
	return nil
}
