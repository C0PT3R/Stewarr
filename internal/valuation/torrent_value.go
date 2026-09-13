package valuation

import (
	"fmt"
	"math"
	"time"

	"stewarr/internal/config"
	"stewarr/internal/model"
	"stewarr/internal/store"
)

// Torrent Value inputs are entirely hardcoded — unlike Media's ValueWeights,
// nothing about how a torrent's own value is computed is user-configurable.
// The goal is what Stewarr considers objectively true about a torrent's
// health, not what a user thinks makes one healthy.
const (
	torrentRatioWeight = 8
	// torrentActivityWeight/HorizonDays score recency of real activity
	// (Torrent.LastActivity, a live fact the client itself reports), the
	// same decay shape as Media's LastWatchedAge — not instantaneous
	// upload/download speed, which depends entirely on the exact instant a
	// calculation runs.
	torrentActivityWeight      = 12
	torrentActivityHorizonDays = 30
	// torrentDemandWeight/Window/MinSamples score sustained leecher demand
	// relative to seed supply, confirmed over real history rather than a
	// single live reading (which can be misleading during a stall or a
	// temporary tracker error).
	torrentDemandWeight = 10
	// TorrentDemandWindow is exported so callers assembling the history map
	// (e.g. inventory.Service) know how far back they need to fetch —
	// fetching less would silently under-count sustained demand.
	TorrentDemandWindow     = 7 * 24 * time.Hour
	torrentDemandMinSamples = 4
	// torrentPrivateBonus reflects that losing standing on a private
	// tracker (ratio requirements, warnings, bans) has real consequences a
	// public-tracker torrent never faces — a static fact, not a guess.
	torrentPrivateBonus = 15
)

// ApplyTorrentValue assigns an independent Torrent Value (and Protected/
// RemovalRestricted, folded in the same pass as the retired ApplyTorrents
// used to do) replacing the old SwarmValue/TorrentValueWeights scheme:
// seeds and leechers used to earn points independently with no real
// comparison between the two numbers. history is every recently retained
// torrent_history sample, keyed by "client|hash" (store.TorrentHistorySince's
// key shape) — a torrent with too little history simply doesn't earn
// demand points yet, it isn't an error.
func ApplyTorrentValue(torrents []model.Torrent, configuration config.Config, history map[string][]store.TorrentHistorySample) {
	now := time.Now()
	for torrentIndex := range torrents {
		torrent := &torrents[torrentIndex]
		torrent.TorrentValue = 0
		torrent.TorrentValueReasons = nil
		torrent.Protected = false
		torrent.ProtectionReason = ""
		torrent.RemovalRestricted = !serviceAllowsRemoval(configuration, torrent.ServiceID)
		if configuration.Protection.MinTorrentRatio > 0 && torrent.Ratio < configuration.Protection.MinTorrentRatio {
			torrent.Protected = true
			torrent.ProtectionReason = "Below minimum ratio"
		}
		if hasTag(splitTags(torrent.Tags), configuration.Protection.KeepTorrentTags) {
			torrent.Protected = true
			if torrent.ProtectionReason == "" {
				torrent.ProtectionReason = "Keep tag"
			}
		}

		if torrent.Ratio > 0 {
			points := math.Log2(1+torrent.Ratio) * torrentRatioWeight
			torrent.TorrentValue += points
			torrent.TorrentValueReasons = append(torrent.TorrentValueReasons, model.Reason{Label: "Ratio", Value: fmt.Sprintf("%.2f", torrent.Ratio), Points: points})
		}

		if torrent.LastActivity > 0 {
			lastActivity := time.Unix(torrent.LastActivity, 0)
			days := daysSince(lastActivity)
			points := (1 - clamp(days/torrentActivityHorizonDays, 0, 1)) * torrentActivityWeight
			torrent.TorrentValue += points
			torrent.TorrentValueReasons = append(torrent.TorrentValueReasons, model.Reason{Label: "Recent activity", Value: fmt.Sprintf("%.0f days ago", days), Points: points})
		}

		if demand, ok := sustainedDemand(history[torrent.Client+"|"+torrent.Hash], now); ok {
			points := math.Log2(1+demand) * torrentDemandWeight
			torrent.TorrentValue += points
			torrent.TorrentValueReasons = append(torrent.TorrentValueReasons, model.Reason{Label: "Sustained demand", Value: fmt.Sprintf("%.2f leechers per seed", demand), Points: points})
		}

		if torrent.Private {
			torrent.TorrentValue += torrentPrivateBonus
			torrent.TorrentValueReasons = append(torrent.TorrentValueReasons, model.Reason{Label: "Private tracker", Value: "yes", Points: torrentPrivateBonus})
		}
	}
}

// sustainedDemand reports leechers-per-seed aggregated across every sample
// in the trailing torrentDemandWindow, so a single misleading live reading
// (a stall, a temporary tracker error) can't move the score on its own.
// ok is false when there isn't enough recent history to trust yet.
func sustainedDemand(samples []store.TorrentHistorySample, now time.Time) (demand float64, ok bool) {
	cutoff := now.Add(-TorrentDemandWindow)
	var totalSeeds, totalLeechers, count int64
	for _, s := range samples {
		if s.SampledAt.Before(cutoff) {
			continue
		}
		totalSeeds += int64(s.SeedsSwarm)
		totalLeechers += int64(s.LeechersSwarm)
		count++
	}
	if count < torrentDemandMinSamples {
		return 0, false
	}
	if totalSeeds == 0 && totalLeechers == 0 {
		return 0, false
	}
	denominator := totalSeeds
	if denominator < 1 {
		denominator = 1
	}
	return float64(totalLeechers) / float64(denominator), true
}
