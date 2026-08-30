package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
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
	// Pre-0.1.13 aliases, accepted only for config migration.
	RequestStrengthBonus  float64 `json:"request_strength_bonus"`
	FavoriteStrengthBonus float64 `json:"favorite_strength_bonus"`
	KeepTagStrengthBonus  float64 `json:"keep_tag_strength_bonus"`
}

type RemovalConfig struct {
	DryRun bool `json:"dry_run"`
}

type Integration struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	APIKey   string `json:"api_key,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	RootPath string `json:"root_path,omitempty"`
	ID       string `json:"-"`
}

func integrationID(integrationType, endpoint string) string {
	normalizedType := strings.ToLower(strings.TrimSpace(integrationType))
	normalizedEndpoint := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	digest := sha256.Sum256([]byte(normalizedType + "\x00" + normalizedEndpoint))
	return normalizedType + "-" + hex.EncodeToString(digest[:6])
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
	Integrations []Integration      `json:"integrations"`
	Radarr       Service            `json:"radarr"`
	Sonarr       Service            `json:"sonarr"`
	Jellyfin     Service            `json:"jellyfin"`
	Seerr        Service            `json:"seerr"`
	QBittorrent  QBittorrentService `json:"qbittorrent"`
	Storage      struct {
		TargetUsagePercent   float64 `json:"target_usage_percent"`
		CriticalUsagePercent float64 `json:"critical_usage_percent"`
	} `json:"storage"`
	Protection struct {
		Favorite          bool     `json:"favorite"`
		SeerrRequestGrace string   `json:"seerr_request_grace"`
		KeepTags          []string `json:"keep_tags"`
	} `json:"protection"`
	Valuation       ValuationConfig `json:"valuation"`
	LegacyScoring   ValuationConfig `json:"scoring"`
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

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
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
		Scoring struct {
			TorrentWeights *json.RawMessage `json:"torrent_weights"`
		} `json:"scoring"`
	}
	_ = json.Unmarshal(fileContents, &present)
	// Migrate the legacy one-service-per-type shape in memory. New configs should
	// use integrations[]. The stable internal ID deliberately excludes Name so
	// renaming an integration does not sever persisted ownership.
	if len(configuration.Integrations) == 0 {
		if configuration.Radarr.URL != "" {
			configuration.Integrations = append(configuration.Integrations, Integration{Type: "radarr", Name: "Movies", URL: configuration.Radarr.URL, APIKey: configuration.Radarr.APIKey})
		}
		if configuration.Sonarr.URL != "" {
			configuration.Integrations = append(configuration.Integrations, Integration{Type: "sonarr", Name: "Series", URL: configuration.Sonarr.URL, APIKey: configuration.Sonarr.APIKey})
		}
		if configuration.Jellyfin.URL != "" {
			configuration.Integrations = append(configuration.Integrations, Integration{Type: "jellyfin", Name: "Jellyfin", URL: configuration.Jellyfin.URL, APIKey: configuration.Jellyfin.APIKey})
		}
		if configuration.Seerr.URL != "" {
			configuration.Integrations = append(configuration.Integrations, Integration{Type: "seerr", Name: "Seerr", URL: configuration.Seerr.URL, APIKey: configuration.Seerr.APIKey})
		}
		if configuration.QBittorrent.URL != "" {
			configuration.Integrations = append(configuration.Integrations, Integration{Type: "qbittorrent", Name: firstNonEmpty(configuration.QBittorrent.Name, "Downloader"), URL: configuration.QBittorrent.URL, APIKey: configuration.QBittorrent.APIKey, Username: configuration.QBittorrent.Username, Password: configuration.QBittorrent.Password})
		}
	}
	seenNames := map[string]bool{}
	typeCounts := map[string]int{}
	for integrationIndex := range configuration.Integrations {
		integration := &configuration.Integrations[integrationIndex]
		integration.Type = strings.ToLower(strings.TrimSpace(integration.Type))
		integration.Name = strings.TrimSpace(integration.Name)
		integration.URL = strings.TrimRight(strings.TrimSpace(integration.URL), "/")
		integration.RootPath = strings.TrimSpace(integration.RootPath)
		if integration.Type == "" {
			return configuration, fmt.Errorf("integrations[%d].type is required", integrationIndex)
		}
		if integration.Name == "" {
			return configuration, fmt.Errorf("integrations[%d].name is required", integrationIndex)
		}
		nameKey := strings.ToLower(integration.Name)
		if nameKey == "unclaimed" {
			return configuration, fmt.Errorf("integration name %q is reserved", integration.Name)
		}
		if seenNames[nameKey] {
			return configuration, fmt.Errorf("integration name %q must be unique (case-insensitive)", integration.Name)
		}
		seenNames[nameKey] = true
		endpoint := integration.URL
		if endpoint == "" {
			endpoint = integration.RootPath
		}
		integration.ID = integrationID(integration.Type, endpoint)
		typeCounts[integration.Type]++
		switch integration.Type {
		case "radarr", "sonarr", "qbittorrent", "jellyfin", "seerr":
			if integration.URL == "" {
				return configuration, fmt.Errorf("integration %q (%s) requires url", integration.Name, integration.Type)
			}
			if integration.RootPath != "" {
				return configuration, fmt.Errorf("integration %q (%s) does not support root_path; storage roots are discovered by its adapter", integration.Name, integration.Type)
			}
		default:
			return configuration, fmt.Errorf("integration %q has unsupported type %q", integration.Name, integration.Type)
		}
	}
	// The config/domain now has stable integration instances, but the current
	// runtime still has one adapter slot per integration type. Fail closed rather
	// than silently ignoring a second owner and falsely classifying its files as
	// Unclaimed. This guard can be removed when adapter fan-out is completed.
	for integrationType, count := range typeCounts {
		if count > 1 {
			return configuration, fmt.Errorf("multiple %s integration instances are not supported by this runtime yet", integrationType)
		}
	}
	// Populate legacy fields for code paths that are intentionally still
	// single-instance while the integration-instance migration proceeds.
	if integration, ok := configuration.FirstIntegration("radarr"); ok {
		configuration.Radarr = Service{URL: integration.URL, APIKey: integration.APIKey}
	}
	if integration, ok := configuration.FirstIntegration("sonarr"); ok {
		configuration.Sonarr = Service{URL: integration.URL, APIKey: integration.APIKey}
	}
	if integration, ok := configuration.FirstIntegration("jellyfin"); ok {
		configuration.Jellyfin = Service{URL: integration.URL, APIKey: integration.APIKey}
	}
	if integration, ok := configuration.FirstIntegration("seerr"); ok {
		configuration.Seerr = Service{URL: integration.URL, APIKey: integration.APIKey}
	}
	if integration, ok := configuration.FirstIntegration("qbittorrent"); ok {
		configuration.QBittorrent = QBittorrentService{Name: integration.Name, URL: integration.URL, APIKey: integration.APIKey, Username: integration.Username, Password: integration.Password}
	}
	// Accept pre-Value configs without keeping the old terminology in the domain model.
	if configuration.Valuation.Weights == (ValueWeights{}) && configuration.Valuation.RequestValueBonus == 0 && configuration.Valuation.FavoriteValueBonus == 0 && configuration.Valuation.KeepTagValueBonus == 0 {
		configuration.Valuation = configuration.LegacyScoring
	}
	if configuration.Valuation.RequestValueBonus == 0 {
		configuration.Valuation.RequestValueBonus = configuration.Valuation.RequestStrengthBonus
	}
	if configuration.Valuation.FavoriteValueBonus == 0 {
		configuration.Valuation.FavoriteValueBonus = configuration.Valuation.FavoriteStrengthBonus
	}
	if configuration.Valuation.KeepTagValueBonus == 0 {
		configuration.Valuation.KeepTagValueBonus = configuration.Valuation.KeepTagStrengthBonus
	}
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
	switch {
	case present.Valuation.TorrentWeights == nil && present.Scoring.TorrentWeights == nil:
		configuration.Valuation.TorrentWeights.Seeds = 1
		configuration.Valuation.TorrentWeights.Leechers = 5
		configuration.Valuation.TorrentWeights.UploadRate = 5
	case present.Valuation.TorrentWeights == nil && present.Scoring.TorrentWeights != nil:
		// The wholesale Valuation = LegacyScoring migration above only fires when
		// every Valuation field is unset; a config mixing a legacy scoring block
		// with other new-style valuation fields still needs its torrent weights
		// carried over individually.
		configuration.Valuation.TorrentWeights = configuration.LegacyScoring.TorrentWeights
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
	return configuration, nil
}
