package cleanup

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"stewarr/internal/model"
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
	Torrents []model.Torrent `json:"torrents,omitempty"`
	Value    float64         `json:"value"`
	// ComparableValue is Value after cross-domain scaling (see rank) — the
	// number actually used to order this action against actions from the
	// other domain. Equal to Value for every action except a
	// StandaloneTorrent. Not itself a value judgment a UI should display
	// as "the" value; Value is what a media/torrent detail page shows.
	ComparableValue  float64        `json:"-"`
	ReclaimableBytes int64          `json:"reclaimableBytes"`
	Reasons          []model.Reason `json:"reasons,omitempty"`
}

// ActionKey is a stable identifier for one Action, independent of its
// position in a Plan's Actions slice (which can shift between two Build
// calls, e.g. once an item is excluded and a replacement takes its
// place) — content-derived from exactly what already uniquely identifies
// a torrent or a media item/season everywhere else in this codebase.
// Used both to let a caller exclude a specific candidate from selection
// (see Build's excluded parameter) and to re-locate one exact action a
// UI is acting on (protecting, say) in a freshly rebuilt Plan.
func ActionKey(action Action) string {
	if action.Kind == StandaloneTorrent {
		if len(action.Torrents) == 0 {
			return ""
		}
		return "torrent:" + strings.ToLower(action.Torrents[0].Hash) + ":" + action.Torrents[0].ServiceID
	}
	season := ""
	if action.Season != nil {
		season = strconv.Itoa(action.Season.Number)
	}
	return "media:" + string(action.Media.Type) + ":" + action.Media.ServiceID + ":" + strconv.Itoa(action.Media.SourceID) + ":" + season
}

type Plan struct {
	TargetUsagePercent   float64 `json:"targetUsagePercent"`
	CriticalUsagePercent float64 `json:"criticalUsagePercent"`
	Reliable             bool    `json:"reliable"`
	Available            bool    `json:"available"`
	Path                 string  `json:"path"`
	TotalBytes           uint64  `json:"totalBytes"`
	UsedBytes            uint64  `json:"usedBytes"`
	FreeBytes            uint64  `json:"freeBytes"`
	// OtherBytes is the real disk usage this plan was told to set aside as
	// outside Stewarr's own view (see inventory.StorageDevice.OtherBytes) —
	// carried through so a caller can see what UsableBytes/UsagePercent were
	// actually computed against.
	OtherBytes uint64 `json:"otherBytes"`
	// UsableBytes is TotalBytes minus OtherBytes: the capacity
	// TargetUsagePercent/CriticalUsagePercent are evaluated against, not the
	// raw disk total. See UsagePercent.
	UsableBytes uint64 `json:"usableBytes"`
	// StewarrUsedBytes is UsedBytes minus OtherBytes.
	StewarrUsedBytes uint64 `json:"stewarrUsedBytes"`
	// UsagePercent is StewarrUsedBytes/UsableBytes, not UsedBytes/TotalBytes:
	// target/critical mean "percent of what Stewarr actually has to work
	// with," not percent of the raw disk, so space held by other services on
	// a shared pool is never something Stewarr's own thresholds silently
	// have to compete against.
	UsagePercent  float64  `json:"usagePercent"`
	NeedBytes     uint64   `json:"needBytes"`
	Actions       []Action `json:"actions"`
	SelectedBytes uint64   `json:"selectedBytes"`
	Message       string   `json:"message"`
	Error         string   `json:"error,omitempty"`
}

// classify splits media and torrents into removal-candidate Actions, one
// tier per domain, applying every granular eligibility rule (protection,
// hardlink safety, reclaimability) but not yet ordering or scaling either
// tier — rank does that, and fairMediaShare re-splits the media tier by
// owning service afterward. ComparableValue is set here for media (always
// equal to Value — media is never cross-domain scaled) so callers using
// classify's output directly never see a zero-value placeholder.
func classify(media []model.Media, torrents []model.Torrent) (mediaTier, torrentTier []Action) {
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
						value += t.TorrentValue
						reasons = append(reasons, t.TorrentValueReasons...)
					}
					mediaTier = append(mediaTier, Action{Kind: HardlinkedBundle, Media: m, Season: &season, Torrents: hardlinked, Value: value, ComparableValue: value, ReclaimableBytes: season.BundleReclaimableBytes, Reasons: reasons})
					continue
				}
				if season.Protected || season.RemovalRestricted || !season.ReclaimableKnown || season.ReclaimableBytes <= 0 {
					continue
				}
				mediaTier = append(mediaTier, Action{Kind: StandaloneSeason, Media: m, Season: &season, Value: season.RetentionValue, ComparableValue: season.RetentionValue, ReclaimableBytes: season.ReclaimableBytes, Reasons: season.RetentionValueReasons})
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
				value += t.TorrentValue
				reasons = append(reasons, t.TorrentValueReasons...)
			}
			mediaTier = append(mediaTier, Action{Kind: HardlinkedBundle, Media: m, Torrents: hardlinked, Value: value, ComparableValue: value, ReclaimableBytes: m.BundleReclaimableBytes, Reasons: reasons})
			continue
		}
		if m.Protected || m.RemovalRestricted || !m.ReclaimableKnown || m.ReclaimableBytes <= 0 {
			continue
		}
		mediaTier = append(mediaTier, Action{Kind: StandaloneMedia, Media: m, Value: m.RetentionValue, ComparableValue: m.RetentionValue, ReclaimableBytes: m.ReclaimableBytes, Reasons: m.RetentionValueReasons})
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
		torrentTier = append(torrentTier, Action{Kind: StandaloneTorrent, Torrents: []model.Torrent{t}, Value: t.TorrentValue, ReclaimableBytes: t.ReclaimableBytes, Reasons: t.TorrentValueReasons})
	}

	return mediaTier, torrentTier
}

// filterExcluded drops any action whose ActionKey is in excluded, as if
// it were never eligible in the first place — the rest of Build's
// selection logic is unaware exclusion even happened, so a different
// candidate naturally fills whatever share of NeedBytes the excluded one
// would have.
func filterExcluded(tier []Action, excluded map[string]bool) []Action {
	if len(excluded) == 0 {
		return tier
	}
	out := make([]Action, 0, len(tier))
	for _, action := range tier {
		if excluded[ActionKey(action)] {
			continue
		}
		out = append(out, action)
	}
	return out
}

// rank orders a media tier and torrent tier as one unified, ascending-value
// list: a StandaloneTorrent's Torrent Value is scaled by torrentCarePercent/
// 50 onto Media Retention Value's scale before comparison (50, the default,
// compares them directly; above 50 makes torrents relatively more worth
// keeping, below 50 less) — this is the only place the two domains are ever
// compared numerically, and it's deliberately a single dial rather than
// exposing how each domain's own value is computed.
//
// Build uses this ordering only to measure how many bytes the torrent side
// vs. the media side contribute in aggregate to reach NeedBytes — which
// individual media items actually get selected is then re-decided by
// fairMediaShare, so torrents are never part of that per-service split.
// Returns fresh Action copies; never mutates mediaTier or torrentTier.
func rank(mediaTier, torrentTier []Action, torrentCarePercent float64) []Action {
	careScale := torrentCarePercent / 50
	merged := make([]Action, 0, len(torrentTier)+len(mediaTier))
	for _, action := range torrentTier {
		action.ComparableValue = action.Value * careScale
		merged = append(merged, action)
	}
	merged = append(merged, mediaTier...)
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].ComparableValue < merged[j].ComparableValue })
	return merged
}

// fairMediaShare redistributes targetBytes — the total the media side needs
// to contribute, already decided by rank's cross-domain ordering (see
// Build) — across each media action's owning service (Action.Media.
// ServiceName), proportional to that service's current footprint on the
// device (claimedByService), rather than letting whichever items score
// lowest overall absorb almost all of it. Series consistently outscoring
// movies in Retention Value (real differential engagement, not a scoring
// defect — see ROADMAP) would otherwise mean movies structurally absorb
// nearly all of any shared device's overage.
//
// claimedByService with no entry for a service (nil map, or a service
// simply missing from it) contributes zero footprint for that service; if
// every candidate's service is unaccounted for this way, there is nothing
// to proportion by, so this falls back to the old unconstrained
// lowest-value-first order across every service combined — never silently
// under-select just because footprint data wasn't wired through.
//
// A service's own eligible candidates may fall short of its fair share (it
// simply doesn't have enough low-enough-value candidates); the remainder is
// filled from the lowest-value candidates left over across every other
// service, so the device still reaches its target even when strict
// fairness alone can't cover it.
func fairMediaShare(mediaTier []Action, claimedByService map[string]uint64, targetBytes int64) []Action {
	if targetBytes <= 0 || len(mediaTier) == 0 {
		return nil
	}
	byService := map[string][]Action{}
	for _, action := range mediaTier {
		byService[action.Media.ServiceName] = append(byService[action.Media.ServiceName], action)
	}
	for service := range byService {
		actions := byService[service]
		sort.SliceStable(actions, func(i, j int) bool { return actions[i].Value < actions[j].Value })
		byService[service] = actions
	}
	var totalClaimed uint64
	for service := range byService {
		totalClaimed += claimedByService[service]
	}
	if totalClaimed == 0 {
		ordered := append([]Action(nil), mediaTier...)
		sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Value < ordered[j].Value })
		var selected []Action
		var selectedBytes int64
		for _, action := range ordered {
			if selectedBytes >= targetBytes {
				break
			}
			selected = append(selected, action)
			selectedBytes += action.ReclaimableBytes
		}
		return selected
	}
	var selected []Action
	var selectedBytes int64
	leftoverByService := map[string][]Action{}
	for service, actions := range byService {
		share := int64(float64(targetBytes) * float64(claimedByService[service]) / float64(totalClaimed))
		var used int64
		index := 0
		for ; index < len(actions) && used < share; index++ {
			selected = append(selected, actions[index])
			used += actions[index].ReclaimableBytes
			selectedBytes += actions[index].ReclaimableBytes
		}
		leftoverByService[service] = actions[index:]
	}
	if selectedBytes < targetBytes {
		var leftover []Action
		for _, actions := range leftoverByService {
			leftover = append(leftover, actions...)
		}
		sort.SliceStable(leftover, func(i, j int) bool { return leftover[i].Value < leftover[j].Value })
		for _, action := range leftover {
			if selectedBytes >= targetBytes {
				break
			}
			selected = append(selected, action)
			selectedBytes += action.ReclaimableBytes
		}
	}
	return selected
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
//
// otherBytes is real disk usage outside Stewarr's own view — other services
// sharing the same pool (see inventory.StorageDevice.OtherBytes) — and is
// set aside from both sides of the usage calculation: targetUsage/
// criticalUsage are evaluated against UsableBytes (TotalBytes-otherBytes),
// not the raw disk total, so a target of 90% means 90% of what Stewarr
// actually has to work with, not 90% of a disk other services already have
// a share of.
//
// claimedByService is each service's current claimed bytes on this device
// (see inventory.StorageDevice.Claimed) — the footprint fairMediaShare
// splits media's share of any overage by, so movies and series (or any two
// media services sharing a device) each shed roughly their own proportion
// of it rather than whichever type scores lowest overall absorbing nearly
// all of it. Torrents are never part of that split; see rank/fairMediaShare.
// excluded is a set of ActionKey values to leave out of selection
// entirely, as if the candidates they name didn't exist — used for a
// live "what would replace this" recompute from the cleanup-plan
// overlay (excluding an item from one run, or one just protected but not
// yet reflected in Media/Torrents' own Protected field since the tag
// write hasn't round-tripped through that service's next reconciliation
// yet) without waiting for anything to actually change on disk. Every
// other caller passes nil.
func Build(path string, otherBytes uint64, claimedByService map[string]uint64, targetUsage, criticalUsage, torrentCarePercent float64, media []model.Media, torrents []model.Torrent, reliable bool, excluded map[string]bool) (Plan, error) {
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
	// otherBytes comes from a separate, independently timed stat pass
	// (inventory.StorageDevices) — clamp rather than trust it can never
	// exceed this call's own totalBytes/usedBytes.
	if otherBytes > totalBytes {
		otherBytes = totalBytes
	}
	usableBytes := totalBytes - otherBytes
	stewarrUsedBytes := usedBytes
	if otherBytes > usedBytes {
		stewarrUsedBytes = 0
	} else {
		stewarrUsedBytes = usedBytes - otherBytes
	}
	plan.OtherBytes = otherBytes
	plan.UsableBytes = usableBytes
	plan.StewarrUsedBytes = stewarrUsedBytes
	usagePercent := 0.0
	if usableBytes > 0 {
		usagePercent = float64(stewarrUsedBytes) / float64(usableBytes) * 100
	}
	plan.UsagePercent = usagePercent
	if usagePercent <= targetUsage {
		plan.Message = fmt.Sprintf("No cleanup: %.2f%% used (target %.1f%%)", usagePercent, targetUsage)
		return plan, nil
	}
	targetUsedBytes := uint64(float64(usableBytes) * targetUsage / 100)
	if stewarrUsedBytes > targetUsedBytes {
		plan.NeedBytes = stewarrUsedBytes - targetUsedBytes
	}
	if !reliable {
		plan.Message = "Cleanup planning paused: valuation or File topology is incomplete or stale"
		return plan, nil
	}
	if plan.NeedBytes == 0 {
		plan.Message = fmt.Sprintf("No cleanup: %.2f%% used (target %.1f%%)", usagePercent, targetUsage)
		return plan, nil
	}
	mediaTier, torrentTier := classify(media, torrents)
	if len(excluded) > 0 {
		mediaTier = filterExcluded(mediaTier, excluded)
		torrentTier = filterExcluded(torrentTier, excluded)
	}
	// baseline is the old, purely cross-domain-scaled ordering — used only
	// to measure how many bytes the torrent side vs. the media side
	// contribute in aggregate to reach NeedBytes, exactly as before. Which
	// individual torrents get selected here is final; which individual
	// media items get selected is not — see fairMediaShare below.
	var torrentSelected []Action
	var mediaBytesNeeded int64
	var baselineBytes int64
	for _, action := range rank(mediaTier, torrentTier, torrentCarePercent) {
		if baselineBytes >= int64(plan.NeedBytes) {
			break
		}
		if action.Kind == StandaloneTorrent {
			torrentSelected = append(torrentSelected, action)
		} else {
			mediaBytesNeeded += action.ReclaimableBytes
		}
		baselineBytes += action.ReclaimableBytes
	}
	mediaSelected := fairMediaShare(mediaTier, claimedByService, mediaBytesNeeded)
	final := make([]Action, 0, len(torrentSelected)+len(mediaSelected))
	final = append(final, torrentSelected...)
	final = append(final, mediaSelected...)
	sort.SliceStable(final, func(i, j int) bool { return final[i].ComparableValue < final[j].ComparableValue })
	enforceSeasonOrder(final)
	plan.Actions = final
	for _, action := range final {
		plan.SelectedBytes += uint64(action.ReclaimableBytes)
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
