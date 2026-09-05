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
)

type ValueWeights struct {
	Rating          float64 `json:"rating"`
	NeverWatched    float64 `json:"never_watched"`
	LastWatchedAge  float64 `json:"last_watched_age"`
	LibraryAge      float64 `json:"library_age"`
	LowPopularity   float64 `json:"low_popularity"`
	OldRequest      float64 `json:"old_request"`
	TorrentActivity float64 `json:"torrent_activity"`
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
}

type Integration struct {
	Type                  string `json:"type"`
	Name                  string `json:"name"`
	URL                   string `json:"url"`
	APIKey                string `json:"api_key,omitempty"`
	Username              string `json:"username,omitempty"`
	Password              string `json:"password,omitempty"`
	RootPath              string `json:"root_path,omitempty"`
	AllowAutomaticRemoval bool   `json:"allow_automatic_removal"`
	// ID is a stable, opaque identifier assigned once when the integration is
	// first added and never recomputed afterward — deliberately independent
	// of both Name and URL, so renaming an integration or moving it to a new
	// address (a different host, port, or network entirely) never severs the
	// ownership already attributed to it (MediaFileRef.IntegrationID,
	// Torrent.IntegrationID, ...). A config.json written before this field
	// existed has no id for its integrations; Load assigns one the first
	// time it sees such an entry and persists it immediately, so the
	// assignment only ever happens once per integration, not on every load.
	ID string `json:"id,omitempty"`
}

// newIntegrationID generates a fresh, random, opaque ID for a new
// integration. It is never derived from the integration's own fields (type
// aside, kept only as a human-readable prefix) so nothing about it needs to
// stay in sync with a later edit.
func newIntegrationID(integrationType string) (string, error) {
	randomBytes := make([]byte, 8)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("generate integration id: %w", err)
	}
	return strings.ToLower(strings.TrimSpace(integrationType)) + "-" + hex.EncodeToString(randomBytes), nil
}

// normalizeIntegration trims/lowercases the fields Load already normalizes,
// factored out so a live "add integration" flow applies identical rules to a
// single candidate without re-running the whole file loader.
func normalizeIntegration(integration *Integration) {
	integration.Type = strings.ToLower(strings.TrimSpace(integration.Type))
	integration.Name = strings.TrimSpace(integration.Name)
	integration.URL = strings.TrimRight(strings.TrimSpace(integration.URL), "/")
	integration.RootPath = strings.TrimSpace(integration.RootPath)
}

// validateIntegration checks one already-normalized integration against the
// rules Load enforces per-entry (required fields, reserved/duplicate names,
// supported type, url/root_path exclusivity). seenNames must contain every
// other integration's lowercased name already accepted in this batch/config.
func validateIntegration(integration Integration, seenNames map[string]bool) error {
	if integration.Type == "" {
		return fmt.Errorf("type is required")
	}
	if integration.Name == "" {
		return fmt.Errorf("name is required")
	}
	nameKey := strings.ToLower(integration.Name)
	if nameKey == "unmanaged" {
		return fmt.Errorf("integration name %q is reserved", integration.Name)
	}
	if seenNames[nameKey] {
		return fmt.Errorf("integration name %q must be unique (case-insensitive)", integration.Name)
	}
	switch integration.Type {
	case "radarr", "sonarr", "qbittorrent", "jellyfin", "seerr":
		if integration.URL == "" {
			return fmt.Errorf("integration %q (%s) requires url", integration.Name, integration.Type)
		}
		if integration.RootPath != "" {
			return fmt.Errorf("integration %q (%s) does not support root_path; storage roots are discovered by its adapter", integration.Name, integration.Type)
		}
	default:
		return fmt.Errorf("integration %q has unsupported type %q", integration.Name, integration.Type)
	}
	return nil
}

// populateDerivedIntegrationFields fills the single-adapter-per-type
// convenience fields (Radarr, Sonarr, ...) the runtime still reads directly,
// from Integrations (the only source of truth). Factored out of Load so a
// live "add integration" flow can refresh them the same way after mutating
// Integrations, without re-running the whole file loader.
func (configuration *Config) populateDerivedIntegrationFields() {
	if integration, ok := configuration.FirstIntegration("radarr"); ok {
		configuration.Radarr = Service{URL: integration.URL, APIKey: integration.APIKey}
	} else {
		configuration.Radarr = Service{}
	}
	if integration, ok := configuration.FirstIntegration("sonarr"); ok {
		configuration.Sonarr = Service{URL: integration.URL, APIKey: integration.APIKey}
	} else {
		configuration.Sonarr = Service{}
	}
	if integration, ok := configuration.FirstIntegration("jellyfin"); ok {
		configuration.Jellyfin = Service{URL: integration.URL, APIKey: integration.APIKey}
	} else {
		configuration.Jellyfin = Service{}
	}
	if integration, ok := configuration.FirstIntegration("seerr"); ok {
		configuration.Seerr = Service{URL: integration.URL, APIKey: integration.APIKey}
	} else {
		configuration.Seerr = Service{}
	}
	if integration, ok := configuration.FirstIntegration("qbittorrent"); ok {
		configuration.QBittorrent = QBittorrentService{Name: integration.Name, URL: integration.URL, APIKey: integration.APIKey, Username: integration.Username, Password: integration.Password}
	} else {
		configuration.QBittorrent = QBittorrentService{}
	}
}

func (integration Integration) Enabled() bool {
	return strings.TrimSpace(integration.URL) != "" || strings.TrimSpace(integration.RootPath) != ""
}

func (configuration Config) IntegrationsOfType(integrationType string) []Integration {
	matchingIntegrations := []Integration{}
	for _, integration := range configuration.Integrations {
		if strings.EqualFold(integration.Type, integrationType) {
			matchingIntegrations = append(matchingIntegrations, integration)
		}
	}
	return matchingIntegrations
}

// multiInstanceAllowed reports whether integrationType may have more than one
// configured entry. Radarr/Sonarr/qBittorrent are commonly run in more than
// one instance (separate quality-tier libraries, a seedbox alongside a local
// client); Jellyfin/Seerr are each a single centralized service in every
// known real-world deployment, so they keep the simpler one-instance shape
// (FirstIntegration-derived Config.Jellyfin/Config.Seerr fields).
func multiInstanceAllowed(integrationType string) bool {
	switch strings.ToLower(integrationType) {
	case "radarr", "sonarr", "qbittorrent":
		return true
	default:
		return false
	}
}

func (configuration Config) FirstIntegration(integrationType string) (Integration, bool) {
	for _, integration := range configuration.Integrations {
		if strings.EqualFold(integration.Type, integrationType) {
			return integration, true
		}
	}
	return Integration{}, false
}

type Config struct {
	Server struct {
		Listen          string `json:"listen"`
		RefreshInterval string `json:"refresh_interval"`
	} `json:"server"`
	Integrations []Integration `json:"integrations"`
	// Radarr, Sonarr, Jellyfin, Seerr and QBittorrent are not config keys; they
	// are populated below from Integrations for the current single-adapter-per-
	// type runtime.
	Radarr      Service            `json:"-"`
	Sonarr      Service            `json:"-"`
	Jellyfin    Service            `json:"-"`
	Seerr       Service            `json:"-"`
	QBittorrent QBittorrentService `json:"-"`
	Storage     struct {
		TargetUsagePercent   float64 `json:"target_usage_percent"`
		CriticalUsagePercent float64 `json:"critical_usage_percent"`
	} `json:"storage"`
	Protection struct {
		Favorite          bool     `json:"favorite"`
		SeerrRequestGrace string   `json:"seerr_request_grace"`
		KeepTags          []string `json:"keep_tags"`
		MinTorrentRatio   float64  `json:"min_torrent_ratio"`
		KeepTorrentTags   []string `json:"keep_torrent_tags"`
	} `json:"protection"`
	Valuation       ValuationConfig `json:"valuation"`
	Removal         RemovalConfig   `json:"removal"`
	RefreshInterval time.Duration   `json:"-"`
	RequestGrace    time.Duration   `json:"-"`
}

type Service struct {
	URL    string `json:"url"`
	APIKey string `json:"api_key"`
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
		} `json:"valuation"`
	}
	_ = json.Unmarshal(fileContents, &present)
	seenNames := map[string]bool{}
	typeCounts := map[string]int{}
	assignedFreshID := false
	for integrationIndex := range configuration.Integrations {
		integration := &configuration.Integrations[integrationIndex]
		normalizeIntegration(integration)
		if err := validateIntegration(*integration, seenNames); err != nil {
			return configuration, fmt.Errorf("integrations[%d]: %w", integrationIndex, err)
		}
		seenNames[strings.ToLower(integration.Name)] = true
		if integration.ID == "" {
			// A config.json written before ID was persisted (or a hand-added
			// entry) has none yet. Assign one now; the persist below makes
			// this a one-time event, not something that happens on every load.
			id, err := newIntegrationID(integration.Type)
			if err != nil {
				return configuration, fmt.Errorf("integrations[%d]: %w", integrationIndex, err)
			}
			integration.ID = id
			assignedFreshID = true
		}
		typeCounts[integration.Type]++
	}
	// Jellyfin/Seerr are each a single centralized service in every known
	// real-world deployment (see multiInstanceAllowed); fail closed on a
	// second one rather than silently using only the first and falsely
	// classifying the other's data as Unmanaged.
	for integrationType, count := range typeCounts {
		if count > 1 && !multiInstanceAllowed(integrationType) {
			return configuration, fmt.Errorf("multiple %s integration instances are not supported", integrationType)
		}
	}
	configuration.populateDerivedIntegrationFields()
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
	if configuration.Storage.TargetUsagePercent <= 0 {
		configuration.Storage.TargetUsagePercent = 90
	}
	if configuration.Storage.TargetUsagePercent >= 100 {
		return configuration, fmt.Errorf("storage.target_usage_percent must be greater than 0 and less than 100")
	}
	if configuration.Storage.CriticalUsagePercent <= 0 {
		configuration.Storage.CriticalUsagePercent = 95
	}
	if configuration.Storage.CriticalUsagePercent >= 100 {
		return configuration, fmt.Errorf("storage.critical_usage_percent must be greater than 0 and less than 100")
	}
	if assignedFreshID {
		if err := Save(path, configuration); err != nil {
			return configuration, fmt.Errorf("persist generated integration id: %w", err)
		}
	}
	return configuration, nil
}

// Save atomically writes configuration back to path: encode, write to a temp
// file in the same directory, then rename over the original. A crash or a
// concurrent read mid-write can never observe a corrupt or partial file.
// Callers that mutate a live Config (e.g. adding an integration) are
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

// AddIntegration validates candidate against the same rules Load applies to
// an entry parsed from disk (required fields, reserved/duplicate names,
// supported type, url/root_path exclusivity, one adapter slot per type) and
// returns configuration with it appended and derived adapter fields
// refreshed. It does not touch disk; the caller decides whether/how to
// persist the result (see Save) and whether to do a live connection check
// before committing to it.
func AddIntegration(configuration Config, candidate Integration) (Config, error) {
	normalizeIntegration(&candidate)
	// Live-adding a root_path-only integration isn't supported by this first
	// slice; every live-addable type requires url (validateIntegration
	// already enforces url/root_path exclusivity for these types).
	candidate.RootPath = ""

	seenNames := make(map[string]bool, len(configuration.Integrations))
	for _, existing := range configuration.Integrations {
		seenNames[strings.ToLower(existing.Name)] = true
	}
	if err := validateIntegration(candidate, seenNames); err != nil {
		return configuration, err
	}
	if !multiInstanceAllowed(candidate.Type) {
		for _, existing := range configuration.Integrations {
			if strings.EqualFold(existing.Type, candidate.Type) {
				return configuration, fmt.Errorf("a %s integration already exists; only one is supported", candidate.Type)
			}
		}
	}
	id, err := newIntegrationID(candidate.Type)
	if err != nil {
		return configuration, err
	}
	candidate.ID = id

	updated := configuration
	updated.Integrations = append(append([]Integration(nil), configuration.Integrations...), candidate)
	updated.populateDerivedIntegrationFields()
	return updated, nil
}

// EditIntegration updates the integration identified by id — Name, URL,
// APIKey, Username, Password — validated the same way AddIntegration
// validates a new one. Type and ID are immutable: Type because the adapter
// class it selects can't meaningfully change in place, and ID because it's
// the whole point — an edit, including moving the integration to an entirely
// different URL, must never sever the ownership already attributed to it.
func EditIntegration(configuration Config, id string, updates Integration) (Config, error) {
	index := -1
	for i, existing := range configuration.Integrations {
		if existing.ID == id {
			index = i
			break
		}
	}
	if index == -1 {
		return configuration, fmt.Errorf("integration not found")
	}

	candidate := updates
	candidate.Type = configuration.Integrations[index].Type
	candidate.ID = id
	normalizeIntegration(&candidate)
	candidate.RootPath = ""

	seenNames := make(map[string]bool, len(configuration.Integrations)-1)
	for i, existing := range configuration.Integrations {
		if i == index {
			continue
		}
		seenNames[strings.ToLower(existing.Name)] = true
	}
	if err := validateIntegration(candidate, seenNames); err != nil {
		return configuration, err
	}

	updated := configuration
	updated.Integrations = append([]Integration(nil), configuration.Integrations...)
	updated.Integrations[index] = candidate
	updated.populateDerivedIntegrationFields()
	return updated, nil
}

// RemoveIntegration deletes the integration identified by id. It does not
// touch anything that integration previously owned — a Media/Torrent ref
// tagged with this ID simply stops matching any configured integration on
// the next reconciliation and is reported as Unmanaged from then on, the
// same way it would be if the integration had never existed.
func RemoveIntegration(configuration Config, id string) (Config, error) {
	index := -1
	for i, existing := range configuration.Integrations {
		if existing.ID == id {
			index = i
			break
		}
	}
	if index == -1 {
		return configuration, fmt.Errorf("integration not found")
	}
	updated := configuration
	updated.Integrations = append(append([]Integration(nil), configuration.Integrations[:index]...), configuration.Integrations[index+1:]...)
	updated.populateDerivedIntegrationFields()
	return updated, nil
}
