package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type Config struct {
	Server struct {
		Listen          string `json:"listen"`
		RefreshInterval string `json:"refresh_interval"`
	} `json:"server"`
	Radarr      Service            `json:"radarr"`
	Sonarr      Service            `json:"sonarr"`
	Jellyfin    Service            `json:"jellyfin"`
	Seerr       Service            `json:"seerr"`
	QBittorrent QBittorrentService `json:"qbittorrent"`
	Storage     struct {
		Path                 string  `json:"path"`
		TargetUsagePercent   float64 `json:"target_usage_percent"`
		CriticalUsagePercent float64 `json:"critical_usage_percent"`
	} `json:"storage"`
	Protection struct {
		Favorite          bool     `json:"favorite"`
		SeerrRequestGrace string   `json:"seerr_request_grace"`
		KeepTags          []string `json:"keep_tags"`
	} `json:"protection"`
	Scoring struct {
		Weights struct {
			Rating          float64 `json:"rating"`
			NeverWatched    float64 `json:"never_watched"`
			LastWatchedAge  float64 `json:"last_watched_age"`
			LibraryAge      float64 `json:"library_age"`
			LowPopularity   float64 `json:"low_popularity"`
			OldRequest      float64 `json:"old_request"`
			TorrentActivity float64 `json:"torrent_activity"`
		} `json:"weights"`
		RequestStrengthBonus  float64 `json:"request_strength_bonus"`
		FavoriteStrengthBonus float64 `json:"favorite_strength_bonus"`
		KeepTagStrengthBonus  float64 `json:"keep_tag_strength_bonus"`
	} `json:"scoring"`
	Safety struct {
		AllowDelete bool `json:"allow_delete"`
		AutoCleanup bool `json:"auto_cleanup"`
	} `json:"safety"`

	RefreshInterval time.Duration `json:"-"`
	RequestGrace    time.Duration `json:"-"`
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
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
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
