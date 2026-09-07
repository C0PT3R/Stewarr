package cleanup

import (
	"fmt"
	"sort"
	"strings"
	"syscall"

	"connarr/internal/model"
)

// ActionKind distinguishes the three shapes a cleanup candidate can take.
type ActionKind string

const (
	// StandaloneMedia is a Media item with no Current hardlinked torrent;
	// removing it alone frees its own bytes.
	StandaloneMedia ActionKind = "media"
	// StandaloneTorrent is a Torrent that is not part of any HardlinkedBundle
	// (Superseded, Orphaned, Unassociated, or a proven non-hardlinked
	// Current copy); removing it alone frees its own bytes.
	StandaloneTorrent ActionKind = "torrent"
	// HardlinkedBundle is a Media item (or one Season of it) plus every
	// Current torrent physically hardlinked to it. These share an inode and
	// must be removed together or not at all: removing only one frees none
	// of the shared bytes while still destroying real value.
	HardlinkedBundle ActionKind = "bundle"
	// StandaloneSeason is one Season of a Series with no Current hardlinked
	// torrent; removing it alone frees its own bytes. A Series with
	// populated Seasons is always ranked per season instead of as one
	// whole-series StandaloneMedia candidate.
	StandaloneSeason ActionKind = "season"
)

// Action is one indivisible cleanup candidate.
type Action struct {
	Kind ActionKind `json:"kind"`
	// Media is set for StandaloneMedia, StandaloneSeason, and
	// HardlinkedBundle; zero value for StandaloneTorrent.
	Media model.Media `json:"media,omitempty"`
	// Season is set for StandaloneSeason and for a HardlinkedBundle scoped to
	// one season of a Series; nil otherwise (a HardlinkedBundle for a Movie,
	// or a StandaloneMedia/StandaloneTorrent action).
	Season *model.Season `json:"season,omitempty"`
	// Torrents has length 1 for StandaloneTorrent, one or more for
	// HardlinkedBundle, and is nil for StandaloneMedia/StandaloneSeason.
	Torrents         []model.Torrent `json:"torrents,omitempty"`
	Value            float64         `json:"value"`
	ReclaimableBytes int64           `json:"reclaimableBytes"`
	Reasons          []model.Reason  `json:"reasons,omitempty"`
}

type Plan struct {
	TargetUsagePercent   float64  `json:"targetUsagePercent"`
	CriticalUsagePercent float64  `json:"criticalUsagePercent"`
	Reliable             bool     `json:"reliable"`
	Available            bool     `json:"available"`
	Path                 string   `json:"path"`
	TotalBytes           uint64   `json:"totalBytes"`
	UsedBytes            uint64   `json:"usedBytes"`
	FreeBytes            uint64   `json:"freeBytes"`
	UsagePercent         float64  `json:"usagePercent"`
	NeedBytes            uint64   `json:"needBytes"`
	Actions              []Action `json:"actions"`
	SelectedBytes        uint64   `json:"selectedBytes"`
	Message              string   `json:"message"`
	Error                string   `json:"error,omitempty"`
}

// rank classifies media and torrents into cleanup Actions and orders them:
// every Torrent-domain action (Superseded, Orphaned, Unassociated, or a
// proven non-hardlinked Current copy) sorted ascending by Swarm Value, followed by
// every Media-domain action (standalone media, or a media bundled with its
// hardlinked Current torrents) sorted ascending by Retention Value. Media
// Retention Value and Torrent Swarm Value are deliberately unrelated scores,
// so domains are never compared numerically against each other — only the
// tier order itself decides which domain is tried first.
func rank(media []model.Media, torrents []model.Torrent) []Action {
	var torrentTier, mediaTier []Action
	bundledHashes := map[string]bool{}

	for _, m := range media {
		if m.Type == model.Series && len(m.Seasons) > 0 {
			for _, season := range m.Seasons {
				hardlinked := m.CurrentHardlinkedTorrentsForSeason(season.Number)
				if len(hardlinked) > 0 {
					for _, t := range hardlinked {
						bundledHashes[strings.ToLower(t.Hash)] = true
					}
					anyTorrentRestricted := false
					for _, t := range hardlinked {
						if t.Protected || t.RemovalRestricted {
							anyTorrentRestricted = true
							break
						}
					}
					if season.Protected || season.RemovalRestricted || anyTorrentRestricted || !season.BundleReclaimableKnown || season.BundleReclaimableBytes <= 0 {
						continue
					}
					value := season.RetentionValue
					var reasons []model.Reason
					reasons = append(reasons, season.RetentionValueReasons...)
					for _, t := range hardlinked {
						value += t.SwarmValue
						reasons = append(reasons, t.SwarmValueReasons...)
					}
					mediaTier = append(mediaTier, Action{Kind: HardlinkedBundle, Media: m, Season: &season, Torrents: hardlinked, Value: value, ReclaimableBytes: season.BundleReclaimableBytes, Reasons: reasons})
					continue
				}
				if season.Protected || season.RemovalRestricted || !season.ReclaimableKnown || season.ReclaimableBytes <= 0 {
					continue
				}
				mediaTier = append(mediaTier, Action{Kind: StandaloneSeason, Media: m, Season: &season, Value: season.RetentionValue, ReclaimableBytes: season.ReclaimableBytes, Reasons: season.RetentionValueReasons})
			}
			continue
		}
		hardlinked := m.CurrentHardlinkedTorrents()
		if len(hardlinked) > 0 {
			for _, t := range hardlinked {
				bundledHashes[strings.ToLower(t.Hash)] = true
			}
			anyTorrentRestricted := false
			for _, t := range hardlinked {
				if t.Protected || t.RemovalRestricted {
					anyTorrentRestricted = true
					break
				}
			}
			if m.Protected || m.RemovalRestricted || anyTorrentRestricted || !m.BundleReclaimableKnown || m.BundleReclaimableBytes <= 0 {
				// Never offer the media or any of its hardlinked torrents
				// alone: doing so would free ~0 bytes while still destroying
				// real value, which is worse than doing nothing. A protected
				// hardlinked torrent vetoes the whole bundle for the same
				// reason: removing the bundle would still delete files that
				// torrent needs. A torrent or media item whose own service
				// hasn't allowed removal vetoes it the same way.
				continue
			}
			value := m.RetentionValue
			var reasons []model.Reason
			reasons = append(reasons, m.RetentionValueReasons...)
			for _, t := range hardlinked {
				value += t.SwarmValue
				reasons = append(reasons, t.SwarmValueReasons...)
			}
			mediaTier = append(mediaTier, Action{Kind: HardlinkedBundle, Media: m, Torrents: hardlinked, Value: value, ReclaimableBytes: m.BundleReclaimableBytes, Reasons: reasons})
			continue
		}
		if m.Protected || m.RemovalRestricted || !m.ReclaimableKnown || m.ReclaimableBytes <= 0 {
			continue
		}
		mediaTier = append(mediaTier, Action{Kind: StandaloneMedia, Media: m, Value: m.RetentionValue, ReclaimableBytes: m.ReclaimableBytes, Reasons: m.RetentionValueReasons})
	}

	for _, t := range torrents {
		if t.Protected || t.RemovalRestricted {
			continue
		}
		if model.NormalizeTorrentStatus(t.AssociationStatus) == model.TorrentCurrent {
			if bundledHashes[strings.ToLower(t.Hash)] {
				continue // already represented inside (or locked out by) a bundle above
			}
			if !t.MediaHardlinkKnown {
				// Hardlink status unproven: never recommend removing it alone,
				// since it might silently free ~0 bytes while destroying real
				// swarm value, the same risk a proven hardlink carries.
				continue
			}
			if t.MediaHardlinked {
				continue // proven hardlink, but its media wasn't in this device's list; stay conservative
			}
			// Current, proven not hardlinked: an independent copy.
		}
		if !t.ReclaimableKnown || t.ReclaimableBytes <= 0 {
			continue
		}
		torrentTier = append(torrentTier, Action{Kind: StandaloneTorrent, Torrents: []model.Torrent{t}, Value: t.SwarmValue, ReclaimableBytes: t.ReclaimableBytes, Reasons: t.SwarmValueReasons})
	}

	sort.SliceStable(torrentTier, func(i, j int) bool { return torrentTier[i].Value < torrentTier[j].Value })
	sort.SliceStable(mediaTier, func(i, j int) bool { return mediaTier[i].Value < mediaTier[j].Value })
	enforceSeasonOrder(mediaTier)
	return append(torrentTier, mediaTier...)
}

// enforceSeasonOrder is a safety net on top of season Retention Value
// (which now scores recency from each season's own last-aired date, not
// import time): it guarantees that within one show, an earlier season is
// never removed after a later one, even if per-season value ever ties or
// inverts — missing/bad air-date data, or a show that aired out of numeric
// order, for instance. A show's most recent content is meant to be kept
// the longest.
//
// This reassigns each show's own season actions into the exact ranked
// positions its members already occupy after the value sort above — so
// the overall cross-show removal priority (which positions belong to this
// show at all) is unchanged — but fills those positions by ascending
// season number instead of by value.
func enforceSeasonOrder(actions []Action) {
	positionsByShow := map[string][]int{}
	for index, action := range actions {
		if action.Season == nil {
			continue
		}
		key := fmt.Sprintf("%s:%s:%d", action.Media.Type, action.Media.ServiceID, action.Media.SourceID)
		positionsByShow[key] = append(positionsByShow[key], index)
	}
	for _, positions := range positionsByShow {
		if len(positions) < 2 {
			continue
		}
		seasons := make([]Action, len(positions))
		for i, position := range positions {
			seasons[i] = actions[position]
		}
		sort.SliceStable(seasons, func(i, j int) bool { return seasons[i].Season.Number < seasons[j].Season.Number })
		for i, position := range positions {
			actions[position] = seasons[i]
		}
	}
}

// Build uses Target as the sole storage-reclamation threshold. Critical is
// carried in the response for configuration compatibility, but belongs to a
// future emergency/alarm policy and never gates cleanup planning.
func Build(path string, targetUsage, criticalUsage float64, media []model.Media, torrents []model.Torrent, reliable bool) (Plan, error) {
	plan := Plan{Path: path, TargetUsagePercent: targetUsage, CriticalUsagePercent: criticalUsage, Reliable: reliable}
	var filesystemStats syscall.Statfs_t
	if err := syscall.Statfs(path, &filesystemStats); err != nil {
		plan.Message = "Storage unavailable; cleanup planning disabled"
		plan.Error = err.Error()
		return plan, fmt.Errorf("storage path %q: %w", path, err)
	}
	totalBytes := filesystemStats.Blocks * uint64(filesystemStats.Bsize)
	if totalBytes == 0 {
		err := fmt.Errorf("filesystem reports zero total capacity")
		plan.Message = "Storage unavailable; cleanup planning disabled"
		plan.Error = err.Error()
		return plan, fmt.Errorf("storage path %q: %w", path, err)
	}
	plan.Available = true
	plan.TotalBytes = totalBytes
	freeBytes := filesystemStats.Bavail * uint64(filesystemStats.Bsize)
	usedBytes := totalBytes - freeBytes
	plan.FreeBytes = freeBytes
	plan.UsedBytes = usedBytes
	usagePercent := float64(usedBytes) / float64(totalBytes) * 100
	plan.UsagePercent = usagePercent
	if usagePercent <= targetUsage {
		plan.Message = fmt.Sprintf("No cleanup: %.2f%% used (target %.1f%%)", usagePercent, targetUsage)
		return plan, nil
	}
	targetUsedBytes := uint64(float64(totalBytes) * targetUsage / 100)
	if usedBytes > targetUsedBytes {
		plan.NeedBytes = usedBytes - targetUsedBytes
	}
	if !reliable {
		plan.Message = "Cleanup planning paused: valuation or File topology is incomplete or stale"
		return plan, nil
	}
	if plan.NeedBytes == 0 {
		plan.Message = fmt.Sprintf("No cleanup: %.2f%% used (target %.1f%%)", usagePercent, targetUsage)
		return plan, nil
	}
	for _, action := range rank(media, torrents) {
		plan.Actions = append(plan.Actions, action)
		plan.SelectedBytes += uint64(action.ReclaimableBytes)
		if plan.SelectedBytes >= plan.NeedBytes {
			break
		}
	}
	plan.Message = fmt.Sprintf("Need to reclaim %s to reach %.1f%% usage", Human(plan.NeedBytes), targetUsage)
	return plan, nil
}

func Human(bytes uint64) string {
	const unitSize = 1024
	value := float64(bytes)
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	unitIndex := 0
	for value >= unitSize && unitIndex < len(units)-1 {
		value /= unitSize
		unitIndex++
	}
	return fmt.Sprintf("%.1f %s", value, units[unitIndex])
}
