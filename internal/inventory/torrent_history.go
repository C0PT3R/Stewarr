package inventory

import (
	"context"
	"time"

	"stewarr/internal/store"
	"stewarr/internal/valuation"
)

// TorrentHistoryRetention is how long sampled torrent readings are kept
// before TorrentHistorySampling prunes them. 30 days is enough to tell a
// sustained, multi-week pattern apart from a temporary blip, without the
// table growing unbounded.
const TorrentHistoryRetention = 30 * 24 * time.Hour

// TorrentHistorySampling records one reading per currently known torrent,
// then prunes samples older than TorrentHistoryRetention. It reads only
// the already-published in-memory torrent snapshot and writes to its own
// independent history table, so it never contends with inventory
// publication or owner/filesystem mutation and needs no resource claims.
func (service *Service) TorrentHistorySampling(ctx context.Context) error {
	torrents := service.TorrentSnapshot()
	now := time.Now().UTC()
	samples := make([]store.TorrentHistorySample, 0, len(torrents))
	for _, t := range torrents {
		samples = append(samples, store.TorrentHistorySample{
			Client:          t.Client,
			Hash:            t.Hash,
			SampledAt:       now,
			Ratio:           t.Ratio,
			SeedsSwarm:      t.SeedsSwarm,
			LeechersSwarm:   t.LeechersSwarm,
			UploadedBytes:   t.UploadedBytes,
			DownloadedBytes: t.DownloadedBytes,
			State:           t.State,
			LastActivity:    t.LastActivity,
		})
	}
	if err := service.db.SaveTorrentHistorySamples(samples); err != nil {
		return err
	}
	return service.db.PruneTorrentHistory(now.Add(-TorrentHistoryRetention))
}

// recentTorrentHistory fetches every retained sample within
// valuation.TorrentDemandWindow, for valuation.ApplyTorrentValue's
// history-derived signals. A best-effort empty result (rather than
// aborting the caller) keeps a database hiccup from blocking an otherwise
// normal refresh; every torrent just scores without those points that
// cycle, same as genuinely having too little history yet.
func (service *Service) recentTorrentHistory() map[string][]store.TorrentHistorySample {
	if service.db == nil {
		return map[string][]store.TorrentHistorySample{}
	}
	history, err := service.db.TorrentHistorySince(time.Now().Add(-valuation.TorrentContributionWindow))
	if err != nil {
		return map[string][]store.TorrentHistorySample{}
	}
	return history
}
