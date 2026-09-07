package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type ValueWeights struct {
	Rating          float64 `json:"rating"`
	NeverWatched    float64 `json:"never_watched"`
	LastWatchedAge  float64 `json:"last_watched_age"`
	LibraryAge      float64 `json:"library_age"`
	LowPopularity   float64 `json:"low_popularity"`
	OldRequest      float64 `json:"old_request"`
	TorrentActivity float64 `json:"torrent_activity"`
	// Popularity scores TMDB's own popularity metric (Media.Popularity) —
	// zero unless TMDB enrichment is configured and has a match for this
	// item, distinct from LowPopularity (which scores raw vote count, a
	// weaker proxy Radarr/Sonarr already provide without TMDB).
	Popularity float64 `json:"popularity"`
	// SeasonRecency scores a Series season by how recently its episodes were
	// added, independent of LibraryAge (which describes the whole series).
	SeasonRecency float64 `json:"season_recency"`
}

type TorrentValueWeights struct {
	Seeds      float64 `json:"seeds"`
	Leechers   float64 `json:"leechers"`
	UploadRate float64 `json:"upload_rate"`
}

type ValuationConfig struct {
	Weights            ValueWeights        `json:"weights"`
	TorrentWeights     TorrentValueWeights `json:"torrent_weights"`
	RequestValueBonus  float64             `json:"request_value_bonus"`
	FavoriteValueBonus float64             `json:"favorite_value_bonus"`
	KeepTagValueBonus  float64             `json:"keep_tag_value_bonus"`
}

type RemovalConfig struct {
	DryRun      bool `json:"dry_run"`
	AutoEnabled bool `json:"auto_enabled"`
	// AutoRemoveUnassociatedTorrents gates automatic removal of torrents
	// Connarr has no owning-media relationship for. Defaults to false: an
	// Unassociated torrent may simply be something the user downloaded
	// through that client for their own purposes, or from a service
	// Connarr doesn't track — automatic removal has no basis to judge those
	// are safe to delete unattended, unlike a torrent it can prove is
	// Superseded or an independent copy of managed media. This gate is
	// separate from and in addition to Service.AllowAutomaticRemoval: that
	// opts a whole service's torrents into automatic removal at all, this
	// narrows it further to exclude the specific case of no known owner.
	AutoRemoveUnassociatedTorrents bool `json:"auto_remove_unassociated_torrents"`
}

type Service struct {
	Type                  string `json:"type"`
	Name                  string `json:"name"`
	URL                   string `json:"url"`
	APIKey                string `json:"api_key,omitempty"`
	Username              string `json:"username,omitempty"`
	Password              string `json:"password,omitempty"`
	RootPath              string `json:"root_path,omitempty"`
	AllowAutomaticRemoval bool   `json:"allow_automatic_removal"`
	// ID is a stable, opaque identifier assigned once when the service is
	// first added and never recomputed afterward — deliberately independent
	// of both Name and URL, so renaming a service or moving it to a new
	// address (a different host, port, or network entirely) never severs the
	// ownership already attributed to it (MediaFileRef.ServiceID,
	// Torrent.ServiceID, ...). A config.json written before this field
	// existed has no id for its services; Load assigns one the first
	// time it sees such an entry and persists it immediately, so the
	// assignment only ever happens once per service, not on every load.
	ID string `json:"id,omitempty"`
}

// newServiceID generates a fresh, random, opaque ID for a new
// service. It is never derived from the service's own fields (type
// aside, kept only as a human-readable prefix) so nothing about it needs to
// stay in sync with a later edit.
func newServiceID(serviceType string) (string, error) {
	randomBytes := make([]byte, 8)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("generate service id: %w", err)
	}
	return strings.ToLower(strings.TrimSpace(serviceType)) + "-" + hex.EncodeToString(randomBytes), nil
}

// normalizeService trims/lowercases the fields Load already normalizes,
// factored out so a live "add service" flow applies identical rules to a
// single candidate without re-running the whole file loader.
func normalizeService(service *Service) {
	service.Type = strings.ToLower(strings.TrimSpace(service.Type))
	service.Name = strings.TrimSpace(service.Name)
	service.URL = strings.TrimRight(strings.TrimSpace(service.URL), "/")
	service.RootPath = strings.TrimSpace(service.RootPath)
}

// validateService checks one already-normalized service against the
// rules Load enforces per-entry (required fields, reserved/duplicate names,
// supported type, url/root_path exclusivity). seenNames must contain every
// other service's lowercased name already accepted in this batch/config.
func validateService(service Service, seenNames map[string]bool) error {
	if service.Type == "" {
		return fmt.Errorf("type is required")
	}
	if service.Name == "" {
		return fmt.Errorf("name is required")
	}
	nameKey := strings.ToLower(service.Name)
	if nameKey == "unmanaged" {
		return fmt.Errorf("service name %q is reserved", service.Name)
	}
	if seenNames[nameKey] {
		return fmt.Errorf("service name %q must be unique (case-insensitive)", service.Name)
	}
	switch service.Type {
	case "radarr", "sonarr", "qbittorrent", "jellyfin", "seerr":
		if service.URL == "" {
			return fmt.Errorf("service %q (%s) requires url", service.Name, service.Type)
		}
		if service.RootPath != "" {
			return fmt.Errorf("service %q (%s) does not support root_path; storage roots are discovered by its adapter", service.Name, service.Type)
		}
	default:
		return fmt.Errorf("service %q has unsupported type %q", service.Name, service.Type)
	}
	return nil
}

// populateDerivedServiceFields fills the single-adapter-per-type
// convenience fields (Radarr, Sonarr, ...) the runtime still reads directly,
// from Services (the only source of truth). Factored out of Load so a
// live "add service" flow can refresh them the same way after mutating
// Services, without re-running the whole file loader.
func (configuration *Config) populateDerivedServiceFields() {
	if service, ok := configuration.FirstService("radarr"); ok {
		configuration.Radarr = Connection{URL: service.URL, APIKey: service.APIKey}
	} else {
		configuration.Radarr = Connection{}
	}
	if service, ok := configuration.FirstService("sonarr"); ok {
		configuration.Sonarr = Connection{URL: service.URL, APIKey: service.APIKey}
	} else {
		configuration.Sonarr = Connection{}
	}
	if service, ok := configuration.FirstService("jellyfin"); ok {
		configuration.Jellyfin = Connection{URL: service.URL, APIKey: service.APIKey}
	} else {
		configuration.Jellyfin = Connection{}
	}
	if service, ok := configuration.FirstService("seerr"); ok {
		configuration.Seerr = Connection{URL: service.URL, APIKey: service.APIKey}
	} else {
		configuration.Seerr = Connection{}
	}
	if service, ok := configuration.FirstService("qbittorrent"); ok {
		configuration.QBittorrent = QBittorrentService{Name: service.Name, URL: service.URL, APIKey: service.APIKey, Username: service.Username, Password: service.Password}
	} else {
		configuration.QBittorrent = QBittorrentService{}
	}
}

func (service Service) Enabled() bool {
	return strings.TrimSpace(service.URL) != "" || strings.TrimSpace(service.RootPath) != ""
}

func (configuration Config) ServicesOfType(serviceType string) []Service {
	matchingServices := []Service{}
	for _, service := range configuration.Services {
		if strings.EqualFold(service.Type, serviceType) {
			matchingServices = append(matchingServices, service)
		}
	}
	return matchingServices
}

// multiInstanceAllowed reports whether serviceType may have more than one
// configured entry. Radarr/Sonarr/qBittorrent are commonly run in more than
// one instance (separate quality-tier libraries, a seedbox alongside a local
// client); Jellyfin/Seerr are each a single centralized service in every
// known real-world deployment, so they keep the simpler one-instance shape
// (FirstService-derived Config.Jellyfin/Config.Seerr fields).
func multiInstanceAllowed(serviceType string) bool {
	switch strings.ToLower(serviceType) {
	case "radarr", "sonarr", "qbittorrent":
		return true
	default:
		return false
	}
}

func (configuration Config) FirstService(serviceType string) (Service, bool) {
	for _, service := range configuration.Services {
		if strings.EqualFold(service.Type, serviceType) {
			return service, true
		}
	}
	return Service{}, false
}

type Config struct {
	Server struct {
		Listen          string `json:"listen"`
		RefreshInterval string `json:"refresh_interval"`
	} `json:"server"`
	Services []Service `json:"services"`
	// Radarr, Sonarr, Jellyfin, Seerr and QBittorrent are not config keys; they
	// are populated below from Services for the current single-adapter-per-
	// type runtime.
	Radarr      Connection         `json:"-"`
	Sonarr      Connection         `json:"-"`
	Jellyfin    Connection         `json:"-"`
	Seerr       Connection         `json:"-"`
	QBittorrent QBittorrentService `json:"-"`
	Storage     struct {
		// DeviceThresholds is keyed by a storage device's RepresentativePath
		// (inventory.StorageDevice's stable external key — see
		// inventory.knownDeviceRoots). A device with no entry here falls back
		// to the defaults in ThresholdsFor; there is no longer a single
		// global percentage applied to every device.
		DeviceThresholds map[string]DeviceThreshold `json:"device_thresholds,omitempty"`
	} `json:"storage"`
	Protection struct {
		Favorite          bool     `json:"favorite"`
		SeerrRequestGrace string   `json:"seerr_request_grace"`
		KeepTags          []string `json:"keep_tags"`
		MinTorrentRatio   float64  `json:"min_torrent_ratio"`
		KeepTorrentTags   []string `json:"keep_torrent_tags"`
	} `json:"protection"`
	Valuation ValuationConfig `json:"valuation"`
	Removal   RemovalConfig   `json:"removal"`
	// TMDB is configured directly, unlike Radarr/Sonarr/Jellyfin/Seerr: it
	// isn't self-hosted, has no URL, and there is only ever one instance —
	// so it lives here as a standalone field (set from the Settings page)
	// rather than as a config.Service.
	TMDB struct {
		APIKey string `json:"api_key,omitempty"`
	} `json:"tmdb"`
	// Auth holds the single admin account's credentials. An empty Username
	// means no account has been created yet — the app's first-run setup
	// screen is the only route reachable until SetCredentials is called.
	// There is deliberately no separate "reset" flow: erasing this field
	// from config.json (and restarting) puts the app back into first-run
	// setup, the same pattern used across the *arr ecosystem.
	Auth            Auth          `json:"auth"`
	RefreshInterval time.Duration `json:"-"`
	RequestGrace    time.Duration `json:"-"`
}

type Auth struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
}

// SetCredentials replaces the admin account's username/password, hashing the
// password with bcrypt. Used both for first-run setup and for changing the
// password later — the caller is responsible for verifying any existing
// password before calling this with a new one.
func SetCredentials(configuration Config, username, password string) (Config, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return configuration, fmt.Errorf("username must not be empty")
	}
	if len(password) < 8 {
		return configuration, fmt.Errorf("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return configuration, fmt.Errorf("hash password: %w", err)
	}
	updated := configuration
	updated.Auth = Auth{Username: username, PasswordHash: string(hash)}
	return updated, nil
}

// VerifyPassword reports whether password matches the admin account's
// stored hash. It also reports false (without error) when no account has
// been created yet, so callers never need a separate existence check first.
func (configuration Config) VerifyPassword(password string) bool {
	if configuration.Auth.PasswordHash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(configuration.Auth.PasswordHash), []byte(password)) == nil
}

type Connection struct {
	URL    string `json:"url"`
	APIKey string `json:"api_key"`
}

// SetTMDBAPIKey idempotently sets (or clears, with an empty string) the
// TMDB enrichment API key. Clearing it is a deliberate, supported way to
// disable TMDB enrichment entirely — there is no separate on/off switch.
func SetTMDBAPIKey(configuration Config, apiKey string) Config {
	updated := configuration
	updated.TMDB.APIKey = strings.TrimSpace(apiKey)
	return updated
}

// SetRemovalSettings updates the global removal switches together, since
// the Settings page always submits all three as one form. autoEnabled and
// autoRemoveUnassociated are meaningless without a service's own
// Service.AllowAutomaticRemoval also opted in (see actionServicesOptedIn);
// dryRun applies to every removal, manual or automatic, not just this one.
func SetRemovalSettings(configuration Config, autoEnabled, autoRemoveUnassociated, dryRun bool) Config {
	updated := configuration
	updated.Removal.AutoEnabled = autoEnabled
	updated.Removal.AutoRemoveUnassociatedTorrents = autoRemoveUnassociated
	updated.Removal.DryRun = dryRun
	return updated
}

// DeviceThreshold is one storage device's reclamation targets. Values
// outside (0,100) are treated as absent by ThresholdsFor rather than
// rejected outright, so one malformed entry in a hand-edited config.json
// doesn't take the whole file down.
type DeviceThreshold struct {
	TargetUsagePercent   float64 `json:"target_usage_percent"`
	CriticalUsagePercent float64 `json:"critical_usage_percent"`
}

// defaultTargetUsagePercent and defaultCriticalUsagePercent are used for any
// storage device with no explicit entry in Storage.DeviceThresholds.
const (
	defaultTargetUsagePercent   = 90.0
	defaultCriticalUsagePercent = 95.0
)

// ThresholdsFor returns representativePath's configured reclamation
// thresholds, or the defaults if it has no entry (or an invalid one).
func (configuration Config) ThresholdsFor(representativePath string) (target, critical float64) {
	target, critical = defaultTargetUsagePercent, defaultCriticalUsagePercent
	threshold, ok := configuration.Storage.DeviceThresholds[representativePath]
	if !ok {
		return target, critical
	}
	if threshold.TargetUsagePercent > 0 && threshold.TargetUsagePercent < 100 {
		target = threshold.TargetUsagePercent
	}
	if threshold.CriticalUsagePercent > 0 && threshold.CriticalUsagePercent < 100 {
		critical = threshold.CriticalUsagePercent
	}
	return target, critical
}

// SetDeviceThreshold idempotently upserts one storage device's reclamation
// thresholds, keyed by its RepresentativePath. Editing the same device from
// any service overlay that happens to share it converges to this one entry.
func SetDeviceThreshold(configuration Config, representativePath string, target, critical float64) (Config, error) {
	if strings.TrimSpace(representativePath) == "" {
		return configuration, fmt.Errorf("representativePath must not be empty")
	}
	if target <= 0 || target >= 100 {
		return configuration, fmt.Errorf("target usage percent must be greater than 0 and less than 100")
	}
	if critical <= 0 || critical >= 100 {
		return configuration, fmt.Errorf("critical usage percent must be greater than 0 and less than 100")
	}
	updated := configuration
	updated.Storage.DeviceThresholds = make(map[string]DeviceThreshold, len(configuration.Storage.DeviceThresholds)+1)
	for path, threshold := range configuration.Storage.DeviceThresholds {
		updated.Storage.DeviceThresholds[path] = threshold
	}
	updated.Storage.DeviceThresholds[representativePath] = DeviceThreshold{TargetUsagePercent: target, CriticalUsagePercent: critical}
	return updated, nil
}

type QBittorrentService struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
	APIKey   string `json:"api_key"`
}

func Load(path string) (Config, error) {
	var configuration Config
	configuration.Removal.DryRun = true
	fileContents, err := os.ReadFile(path)
	if err != nil {
		return configuration, err
	}
	if err := json.Unmarshal(fileContents, &configuration); err != nil {
		return configuration, err
	}
	var present struct {
		Valuation struct {
			TorrentWeights *json.RawMessage `json:"torrent_weights"`
			Weights        struct {
				Popularity *json.RawMessage `json:"popularity"`
			} `json:"weights"`
		} `json:"valuation"`
	}
	_ = json.Unmarshal(fileContents, &present)
	seenNames := map[string]bool{}
	typeCounts := map[string]int{}
	assignedFreshID := false
	for serviceIndex := range configuration.Services {
		service := &configuration.Services[serviceIndex]
		normalizeService(service)
		if err := validateService(*service, seenNames); err != nil {
			return configuration, fmt.Errorf("services[%d]: %w", serviceIndex, err)
		}
		seenNames[strings.ToLower(service.Name)] = true
		if service.ID == "" {
			// A config.json written before ID was persisted (or a hand-added
			// entry) has none yet. Assign one now; the persist below makes
			// this a one-time event, not something that happens on every load.
			id, err := newServiceID(service.Type)
			if err != nil {
				return configuration, fmt.Errorf("services[%d]: %w", serviceIndex, err)
			}
			service.ID = id
			assignedFreshID = true
		}
		typeCounts[service.Type]++
	}
	// Jellyfin/Seerr are each a single centralized service in every known
	// real-world deployment (see multiInstanceAllowed); fail closed on a
	// second one rather than silently using only the first and falsely
	// classifying the other's data as Unmanaged.
	for serviceType, count := range typeCounts {
		if count > 1 && !multiInstanceAllowed(serviceType) {
			return configuration, fmt.Errorf("multiple %s service instances are not supported", serviceType)
		}
	}
	configuration.populateDerivedServiceFields()
	if configuration.Server.Listen == "" {
		configuration.Server.Listen = ":8088"
	}
	if configuration.Server.RefreshInterval == "" {
		configuration.Server.RefreshInterval = "30m"
	}
	configuration.RefreshInterval, err = time.ParseDuration(configuration.Server.RefreshInterval)
	if err != nil {
		return configuration, fmt.Errorf("server.refresh_interval: %w", err)
	}
	if configuration.RefreshInterval <= 0 {
		return configuration, fmt.Errorf("server.refresh_interval must be greater than zero")
	}
	if configuration.Protection.SeerrRequestGrace == "" {
		configuration.Protection.SeerrRequestGrace = "8760h"
	}
	configuration.RequestGrace, err = time.ParseDuration(configuration.Protection.SeerrRequestGrace)
	if err != nil {
		return configuration, fmt.Errorf("protection.seerr_request_grace: %w", err)
	}
	if configuration.RequestGrace < 0 {
		return configuration, fmt.Errorf("protection.seerr_request_grace must not be negative")
	}
	if present.Valuation.TorrentWeights == nil {
		configuration.Valuation.TorrentWeights.Seeds = 1
		configuration.Valuation.TorrentWeights.Leechers = 5
		configuration.Valuation.TorrentWeights.UploadRate = 5
	}
	// Popularity is a newer weight than the rest of Weights; a config.json
	// written before it existed has no value for it at all, which would
	// otherwise silently make TMDB's popularity signal count for nothing —
	// even with TMDB fully enabled and fetching — until the user happened
	// to add the key themselves.
	if present.Valuation.Weights.Popularity == nil {
		configuration.Valuation.Weights.Popularity = 15
	}
	if assignedFreshID {
		if err := Save(path, configuration); err != nil {
			return configuration, fmt.Errorf("persist generated service id: %w", err)
		}
	}
	return configuration, nil
}

// Save atomically writes configuration back to path: encode, write to a temp
// file in the same directory, then rename over the original. A crash or a
// concurrent read mid-write can never observe a corrupt or partial file.
// Callers that mutate a live Config (e.g. adding a service) are
// responsible for their own serialization of concurrent Save calls; this
// function only guarantees the write itself is atomic.
func Save(path string, configuration Config) error {
	data, err := json.MarshalIndent(configuration, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".config-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp config file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath) // no-op once the rename below succeeds
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write temp config file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync temp config file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temp config file: %w", err)
	}
	if err := os.Chmod(tempPath, 0o600); err != nil {
		return fmt.Errorf("chmod temp config file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace config file: %w", err)
	}
	return nil
}

// AddService validates candidate against the same rules Load applies to
// an entry parsed from disk (required fields, reserved/duplicate names,
// supported type, url/root_path exclusivity, one adapter slot per type) and
// returns configuration with it appended and derived adapter fields
// refreshed. It does not touch disk; the caller decides whether/how to
// persist the result (see Save) and whether to do a live connection check
// before committing to it.
func AddService(configuration Config, candidate Service) (Config, error) {
	normalizeService(&candidate)
	// Live-adding a root_path-only service isn't supported by this first
	// slice; every live-addable type requires url (validateService
	// already enforces url/root_path exclusivity for these types).
	candidate.RootPath = ""

	seenNames := make(map[string]bool, len(configuration.Services))
	for _, existing := range configuration.Services {
		seenNames[strings.ToLower(existing.Name)] = true
	}
	if err := validateService(candidate, seenNames); err != nil {
		return configuration, err
	}
	if !multiInstanceAllowed(candidate.Type) {
		for _, existing := range configuration.Services {
			if strings.EqualFold(existing.Type, candidate.Type) {
				return configuration, fmt.Errorf("a %s service already exists; only one is supported", candidate.Type)
			}
		}
	}
	id, err := newServiceID(candidate.Type)
	if err != nil {
		return configuration, err
	}
	candidate.ID = id

	updated := configuration
	updated.Services = append(append([]Service(nil), configuration.Services...), candidate)
	updated.populateDerivedServiceFields()
	return updated, nil
}

// EditService updates the service identified by id — Name, URL,
// APIKey, Username, Password — validated the same way AddService
// validates a new one. Type and ID are immutable: Type because the adapter
// class it selects can't meaningfully change in place, and ID because it's
// the whole point — an edit, including moving the service to an entirely
// different URL, must never sever the ownership already attributed to it.
func EditService(configuration Config, id string, updates Service) (Config, error) {
	index := -1
	for i, existing := range configuration.Services {
		if existing.ID == id {
			index = i
			break
		}
	}
	if index == -1 {
		return configuration, fmt.Errorf("service not found")
	}

	candidate := updates
	candidate.Type = configuration.Services[index].Type
	candidate.ID = id
	normalizeService(&candidate)
	candidate.RootPath = ""

	seenNames := make(map[string]bool, len(configuration.Services)-1)
	for i, existing := range configuration.Services {
		if i == index {
			continue
		}
		seenNames[strings.ToLower(existing.Name)] = true
	}
	if err := validateService(candidate, seenNames); err != nil {
		return configuration, err
	}

	updated := configuration
	updated.Services = append([]Service(nil), configuration.Services...)
	updated.Services[index] = candidate
	updated.populateDerivedServiceFields()
	return updated, nil
}

// RemoveService deletes the service identified by id. It does not
// touch anything that service previously owned — a Media/Torrent ref
// tagged with this ID simply stops matching any configured service on
// the next reconciliation and is reported as Unmanaged from then on, the
// same way it would be if the service had never existed.
func RemoveService(configuration Config, id string) (Config, error) {
	index := -1
	for i, existing := range configuration.Services {
		if existing.ID == id {
			index = i
			break
		}
	}
	if index == -1 {
		return configuration, fmt.Errorf("service not found")
	}
	updated := configuration
	updated.Services = append(append([]Service(nil), configuration.Services[:index]...), configuration.Services[index+1:]...)
	updated.populateDerivedServiceFields()
	return updated, nil
}
