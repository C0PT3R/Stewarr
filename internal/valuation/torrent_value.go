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
	// torrentContributionWeight/ConsistencyWeight/Window/MinSamples score how
	// a torrent actually contributes to its swarm over time — real bytes
	// pushed, not a live speed reading (which reflects the exact instant a
	// calculation runs, not any lasting property of the torrent) and not a
	// single seeds/leechers snapshot (which can be misleading during a stall
	// or a temporary tracker error).
	torrentContributionWeight = 10
	torrentConsistencyWeight  = 8
	// TorrentContributionWindow is exported so callers assembling the
	// history map (e.g. inventory.Service) know how far back they need to
	// fetch — fetching less would silently under-count realized
	// contribution.
	TorrentContributionWindow     = 7 * 24 * time.Hour
	torrentContributionMinSamples = 4
	// torrentSeedingTimeWeight/HorizonDays reward accumulated seeding time,
	// hard-capped so an old torrent can't earn unbounded points just for
	// existing a long time.
	torrentSeedingTimeWeight      = 10
	torrentSeedingTimeHorizonDays = 180
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

		if realizedBytes, consistency, ok := torrentContributionStats(history[torrent.Client+"|"+torrent.Hash]); ok {
			mib := float64(realizedBytes) / (1024 * 1024)
			points := math.Log2(1+mib) * torrentContributionWeight
			torrent.TorrentValue += points
			torrent.TorrentValueReasons = append(torrent.TorrentValueReasons, model.Reason{Label: "Uploaded (7d)", Value: fmt.Sprintf("%.0f MiB", mib), Points: points})

			points = consistency * torrentConsistencyWeight
			torrent.TorrentValue += points
			torrent.TorrentValueReasons = append(torrent.TorrentValueReasons, model.Reason{Label: "Consistency", Value: fmt.Sprintf("%.0f%% of samples", consistency*100), Points: points})
		}

		if torrent.SeedingTime > 0 {
			days := float64(torrent.SeedingTime) / 86400
			points := clamp(days/torrentSeedingTimeHorizonDays, 0, 1) * torrentSeedingTimeWeight
			torrent.TorrentValue += points
			torrent.TorrentValueReasons = append(torrent.TorrentValueReasons, model.Reason{Label: "Seeding time", Value: fmt.Sprintf("%.0f days", days), Points: points})
		}

		if torrent.Private {
			torrent.TorrentValue += torrentPrivateBonus
			torrent.TorrentValueReasons = append(torrent.TorrentValueReasons, model.Reason{Label: "Private tracker", Value: "yes", Points: torrentPrivateBonus})
		}
	}
}

// torrentContributionStats derives realized upload bytes and consistency of
// contribution from consecutive UploadedBytes samples — how a torrent
// actually contributes to its swarm, not a single live reading. samples are
// assumed to already be scoped to the trailing TorrentContributionWindow
// (that's what store.TorrentHistorySince filters on) and ordered oldest
// first. A negative delta (a client-side counter reset, e.g. after a
// recheck) contributes nothing rather than subtracting. ok is false when
// there isn't enough recent history to trust yet.
func torrentContributionStats(samples []store.TorrentHistorySample) (realizedBytes int64, consistency float64, ok bool) {
	if len(samples) < torrentContributionMinSamples {
		return 0, 0, false
	}
	var positivePairs, totalPairs int
	for i := 1; i < len(samples); i++ {
		delta := samples[i].UploadedBytes - samples[i-1].UploadedBytes
		totalPairs++
		if delta > 0 {
			realizedBytes += delta
			positivePairs++
		}
	}
	if totalPairs == 0 {
		return 0, 0, false
	}
	return realizedBytes, float64(positivePairs) / float64(totalPairs), true
}
