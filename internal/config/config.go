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

func integrationID(t, u string) string {
	h := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(t)) + "\x00" + strings.TrimRight(strings.TrimSpace(u), "/")))
	return strings.ToLower(strings.TrimSpace(t)) + "-" + hex.EncodeToString(h[:6])
}

func (i Integration) Enabled() bool {
	return strings.TrimSpace(i.URL) != "" || strings.TrimSpace(i.RootPath) != ""
}

func (c Config) IntegrationsOfType(t string) []Integration {
	out := []Integration{}
	for _, i := range c.Integrations {
		if strings.EqualFold(i.Type, t) {
			out = append(out, i)
		}
	}
	return out
}
func (c Config) FirstIntegration(t string) (Integration, bool) {
	for _, i := range c.Integrations {
		if strings.EqualFold(i.Type, t) {
			return i, true
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
		Path                 string  `json:"path"`
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

func firstNonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

func Load(path string) (Config, error) {
	var c Config
	c.Removal.DryRun = true
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	// Migrate the legacy one-service-per-type shape in memory. New configs should
	// use integrations[]. The stable internal ID deliberately excludes Name so
	// renaming an integration does not sever persisted ownership.
	if len(c.Integrations) == 0 {
		if c.Radarr.URL != "" {
			c.Integrations = append(c.Integrations, Integration{Type: "radarr", Name: "Movies", URL: c.Radarr.URL, APIKey: c.Radarr.APIKey})
		}
		if c.Sonarr.URL != "" {
			c.Integrations = append(c.Integrations, Integration{Type: "sonarr", Name: "Series", URL: c.Sonarr.URL, APIKey: c.Sonarr.APIKey})
		}
		if c.Jellyfin.URL != "" {
			c.Integrations = append(c.Integrations, Integration{Type: "jellyfin", Name: "Jellyfin", URL: c.Jellyfin.URL, APIKey: c.Jellyfin.APIKey})
		}
		if c.Seerr.URL != "" {
			c.Integrations = append(c.Integrations, Integration{Type: "seerr", Name: "Seerr", URL: c.Seerr.URL, APIKey: c.Seerr.APIKey})
		}
		if c.QBittorrent.URL != "" {
			c.Integrations = append(c.Integrations, Integration{Type: "qbittorrent", Name: firstNonEmpty(c.QBittorrent.Name, "Downloader"), URL: c.QBittorrent.URL, APIKey: c.QBittorrent.APIKey, Username: c.QBittorrent.Username, Password: c.QBittorrent.Password})
		}
	}
	seenNames := map[string]bool{}
	typeCounts := map[string]int{}
	for n := range c.Integrations {
		i := &c.Integrations[n]
		i.Type = strings.ToLower(strings.TrimSpace(i.Type))
		i.Name = strings.TrimSpace(i.Name)
		i.URL = strings.TrimRight(strings.TrimSpace(i.URL), "/")
		i.RootPath = strings.TrimSpace(i.RootPath)
		if i.Type == "" {
			return c, fmt.Errorf("integrations[%d].type is required", n)
		}
		if i.Name == "" {
			return c, fmt.Errorf("integrations[%d].name is required", n)
		}
		nameKey := strings.ToLower(i.Name)
		if nameKey == "unclaimed" {
			return c, fmt.Errorf("integration name %q is reserved", i.Name)
		}
		if seenNames[nameKey] {
			return c, fmt.Errorf("integration name %q must be unique (case-insensitive)", i.Name)
		}
		seenNames[nameKey] = true
		endpoint := i.URL
		if endpoint == "" {
			endpoint = i.RootPath
		}
		i.ID = integrationID(i.Type, endpoint)
		typeCounts[i.Type]++
		switch i.Type {
		case "radarr", "sonarr", "qbittorrent", "jellyfin", "seerr":
			if i.URL == "" {
				return c, fmt.Errorf("integration %q (%s) requires url", i.Name, i.Type)
			}
		default:
			if i.RootPath == "" {
				return c, fmt.Errorf("integration %q (%s) does not expose roots; root_path is required", i.Name, i.Type)
			}
		}
	}
	// The config/domain now has stable integration instances, but the current
	// runtime still has one adapter slot per integration type. Fail closed rather
	// than silently ignoring a second owner and falsely classifying its files as
	// Unclaimed. This guard can be removed when adapter fan-out is completed.
	for typ, n := range typeCounts {
		if n > 1 {
			return c, fmt.Errorf("multiple %s integration instances are not supported by this runtime yet", typ)
		}
	}
	// Populate legacy fields for code paths that are intentionally still
	// single-instance while the integration-instance migration proceeds.
	if i, ok := c.FirstIntegration("radarr"); ok {
		c.Radarr = Service{URL: i.URL, APIKey: i.APIKey}
	}
	if i, ok := c.FirstIntegration("sonarr"); ok {
		c.Sonarr = Service{URL: i.URL, APIKey: i.APIKey}
	}
	if i, ok := c.FirstIntegration("jellyfin"); ok {
		c.Jellyfin = Service{URL: i.URL, APIKey: i.APIKey}
	}
	if i, ok := c.FirstIntegration("seerr"); ok {
		c.Seerr = Service{URL: i.URL, APIKey: i.APIKey}
	}
	if i, ok := c.FirstIntegration("qbittorrent"); ok {
		c.QBittorrent = QBittorrentService{Name: i.Name, URL: i.URL, APIKey: i.APIKey, Username: i.Username, Password: i.Password}
	}
	// Accept pre-Value configs without keeping the old terminology in the domain model.
	if c.Valuation.Weights == (ValueWeights{}) && c.Valuation.RequestValueBonus == 0 && c.Valuation.FavoriteValueBonus == 0 && c.Valuation.KeepTagValueBonus == 0 {
		c.Valuation = c.LegacyScoring
	}
	if c.Valuation.RequestValueBonus == 0 {
		c.Valuation.RequestValueBonus = c.Valuation.RequestStrengthBonus
	}
	if c.Valuation.FavoriteValueBonus == 0 {
		c.Valuation.FavoriteValueBonus = c.Valuation.FavoriteStrengthBonus
	}
	if c.Valuation.KeepTagValueBonus == 0 {
		c.Valuation.KeepTagValueBonus = c.Valuation.KeepTagStrengthBonus
	}
	if c.Server.Listen == "" {
		c.Server.Listen = ":8088"
	}
	if c.Server.RefreshInterval == "" {
		c.Server.RefreshInterval = "30m"
	}
	c.RefreshInterval, err = time.ParseDuration(c.Server.RefreshInterval)
	if err != nil {
		return c, fmt.Errorf("server.refresh_interval: %w", err)
	}
	if c.Protection.SeerrRequestGrace == "" {
		c.Protection.SeerrRequestGrace = "8760h"
	}
	c.RequestGrace, err = time.ParseDuration(c.Protection.SeerrRequestGrace)
	if err != nil {
		return c, fmt.Errorf("protection.seerr_request_grace: %w", err)
	}
	if c.Valuation.TorrentWeights.Seeds == 0 && c.Valuation.TorrentWeights.Leechers == 0 && c.Valuation.TorrentWeights.UploadRate == 0 {
		c.Valuation.TorrentWeights.Seeds = 1
		c.Valuation.TorrentWeights.Leechers = 5
		c.Valuation.TorrentWeights.UploadRate = 5
	}
	if c.Storage.Path == "" {
		c.Storage.Path = "/data"
	}
	if c.Storage.TargetUsagePercent <= 0 {
		c.Storage.TargetUsagePercent = 90
	}
	if c.Storage.TargetUsagePercent >= 100 {
		return c, fmt.Errorf("storage.target_usage_percent must be greater than 0 and less than 100")
	}
	if c.Storage.CriticalUsagePercent <= 0 {
		c.Storage.CriticalUsagePercent = c.Storage.TargetUsagePercent
	}
	if c.Storage.CriticalUsagePercent >= 100 {
		return c, fmt.Errorf("storage.critical_usage_percent must be greater than 0 and less than 100")
	}
	if c.Storage.CriticalUsagePercent < c.Storage.TargetUsagePercent {
		return c, fmt.Errorf("storage.critical_usage_percent must be greater than or equal to storage.target_usage_percent")
	}
	return c, nil
}
