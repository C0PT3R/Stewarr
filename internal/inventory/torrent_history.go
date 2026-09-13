package inventory

import (
	"context"
	"strings"
	"time"

	"stewarr/internal/model"
	"stewarr/internal/store"
)

// TorrentHistoryRetention is how long sampled torrent health readings are
// kept before TorrentHistorySampling prunes them. 30 days is enough to
// tell a sustained, multi-week pattern (a dead swarm, a private-tracker
// grace period) apart from a temporary blip, without the table growing
// unbounded.
const TorrentHistoryRetention = 30 * 24 * time.Hour

// TorrentHistorySampling records one health-scan reading per currently
// known torrent, then prunes samples older than TorrentHistoryRetention.
// It reads only the already-published in-memory torrent snapshot (plus a
// live per-torrent tracker-health fetch from each configured torrent
// client) and writes to its own independent history table, so it never
// contends with inventory publication or owner/filesystem mutation and
// needs no resource claims.
func (service *Service) TorrentHistorySampling(ctx context.Context) error {
	torrents := service.TorrentSnapshot()
	health := service.trackerHealthByKey(ctx, torrents)

	now := time.Now().UTC()
	samples := make([]store.TorrentHistorySample, 0, len(torrents))
	for _, t := range torrents {
		h := health[torrentKey(t.ServiceID, t.Hash)]
		samples = append(samples, store.TorrentHistorySample{
			Client:              t.Client,
			Hash:                t.Hash,
			SampledAt:           now,
			Ratio:               t.Ratio,
			SeedsSwarm:          t.SeedsSwarm,
			LeechersSwarm:       t.LeechersSwarm,
			UploadedBytes:       t.UploadedBytes,
			DownloadedBytes:     t.DownloadedBytes,
			State:               t.State,
			LastActivity:        t.LastActivity,
			TrackerWorking:      h.Working,
			TrackerWorkingKnown: h.Known,
			TrackerMessage:      h.Message,
		})
	}
	if err := service.db.SaveTorrentHistorySamples(samples); err != nil {
		return err
	}
	return service.db.PruneTorrentHistory(now.Add(-TorrentHistoryRetention))
}

// trackerHealthByKey fetches tracker health per configured qBittorrent
// instance and returns it keyed the same way syncTorrents keys its own
// output (torrentKey(serviceID, hash)), so a multi-instance setup can't mix
// up two instances' torrents that happen to share a hash. A client that
// can't report tracker health (or isn't configured at all) simply
// contributes nothing for its torrents; those samples persist with
// TrackerWorkingKnown=false, not an error that would abort the whole scan.
func (service *Service) trackerHealthByKey(ctx context.Context, torrents []model.Torrent) map[string]model.TrackerHealth {
	byInstance := map[string]map[string]model.Torrent{}
	for _, t := range torrents {
		if byInstance[t.ServiceID] == nil {
			byInstance[t.ServiceID] = map[string]model.Torrent{}
		}
		byInstance[t.ServiceID][strings.ToLower(t.Hash)] = t
	}

	service.mu.RLock()
	qbClients := service.qb
	service.mu.RUnlock()

	out := map[string]model.TrackerHealth{}
	for svcID, byHash := range byInstance {
		client, ok := qbClients[svcID]
		if !ok {
			continue
		}
		health, err := client.WithContext(ctx).TrackerHealth(byHash)
		if err != nil {
			// Best-effort: this scan's tracker data for this instance is
			// missing, not fatal to recording everything else it already
			// knows (ratio, swarm counts, state).
			continue
		}
		for hash, h := range health {
			out[torrentKey(svcID, hash)] = h
		}
	}
	return out
}
