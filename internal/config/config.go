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
		if nameKey == "unmanaged" {
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
		// The stable internal ID deliberately excludes Name so renaming an
		// integration does not sever persisted ownership.
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
	// Unmanaged. This guard can be removed when adapter fan-out is completed.
	for integrationType, count := range typeCounts {
		if count > 1 {
			return configuration, fmt.Errorf("multiple %s integration instances are not supported by this runtime yet", integrationType)
		}
	}
	// Populate the single-adapter-per-type fields the current runtime still
	// reads directly, derived from Integrations (the only source of truth).
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
	return configuration, nil
}
