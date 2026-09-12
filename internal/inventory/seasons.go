package inventory

import (
	"path/filepath"
	"sort"

	"stewarr/internal/filetopology"
	"stewarr/internal/model"
)

// seasonKey identifies one season across multiple configured Sonarr
// instances — seriesID (Media.SourceID) is only unique within one instance.
type seasonKey struct {
	svcID    string
	seriesID int
	season   int
}

// seasonFromParts recovers the season number and display group from a
// MediaFileRef's Parts, using Order's seasonNumber*100000+episodeNumber
// encoding (internal/integrations/sonarr/client.go) rather than parsing the
// "Season %d" display label back apart.
func seasonFromParts(parts []model.MediaFilePart) (number int, group string, ok bool) {
	if len(parts) == 0 || parts[0].Group == "" {
		return 0, "", false
	}
	return parts[0].Order / 100000, parts[0].Group, true
}

// aggregateSeasons groups Sonarr media file refs into per-season summaries,
// keyed by (ServiceID, SeriesID/Media.SourceID). A ref with no Parts
// (season grouping unknown) is skipped; Stewarr only knows season boundaries
// for Sonarr.
func aggregateSeasons(mediaRefs []model.MediaFileRef, files []model.File) map[ownerKey][]model.Season {
	sizeByPath := make(map[string]int64, len(files))
	for _, f := range files {
		sizeByPath[filepath.Clean(f.Path)] = f.SizeBytes
	}
	accumulators := map[seasonKey]*model.Season{}
	var order []seasonKey
	for _, ref := range mediaRefs {
		if ref.Source != "sonarr" {
			continue
		}
		seasonNumber, group, ok := seasonFromParts(ref.Parts)
		if !ok {
			continue
		}
		key := seasonKey{svcID: ref.ServiceID, seriesID: ref.MediaID, season: seasonNumber}
		season, exists := accumulators[key]
		if !exists {
			season = &model.Season{Number: seasonNumber, FileGroup: group}
			accumulators[key] = season
			order = append(order, key)
		}
		season.SizeBytes += sizeByPath[filepath.Clean(ref.Path)]
		season.EpisodeFileCount++
		for _, part := range ref.Parts {
			if part.AiredAt.After(season.LastAiredAt) {
				season.LastAiredAt = part.AiredAt
			}
		}
	}
	out := map[ownerKey][]model.Season{}
	for _, key := range order {
		seriesKey := ownerKey{ServiceID: key.svcID, OwnerID: key.seriesID}
		out[seriesKey] = append(out[seriesKey], *accumulators[key])
	}
	for seriesKey := range out {
		sort.Slice(out[seriesKey], func(i, j int) bool { return out[seriesKey][i].Number < out[seriesKey][j].Number })
	}
	return out
}

// attachSeasons computes and assigns Media.Seasons for every Series item
// from already-reconciled file refs, so it is available at every place a
// Media snapshot is built, not only right after a fresh Sonarr fetch.
func attachSeasons(items []model.Media, mediaRefs []model.MediaFileRef, files []model.File) {
	bySeries := aggregateSeasons(mediaRefs, files)
	for i := range items {
		if items[i].Type == model.Series {
			items[i].Seasons = bySeries[ownerKey{ServiceID: items[i].ServiceID, OwnerID: items[i].SourceID}]
		}
	}
}

// seasonPathsByKey groups every sonarr MediaFileRef's path by (svcID,
// seriesID, season number), for estimate/hardlink computations scoped to one
// season.
func seasonPathsByKey(mediaRefs []model.MediaFileRef) map[seasonKey][]string {
	out := map[seasonKey][]string{}
	for _, ref := range mediaRefs {
		if ref.Source != "sonarr" {
			continue
		}
		seasonNumber, _, ok := seasonFromParts(ref.Parts)
		if !ok {
			continue
		}
		key := seasonKey{svcID: ref.ServiceID, seriesID: ref.MediaID, season: seasonNumber}
		out[key] = append(out[key], ref.Path)
	}
	return out
}

// applySeasonFileEstimates computes each season's own reclaimable bytes,
// mirroring applyMediaFileEstimates but scoped to that season's files alone.
func applySeasonFileEstimates(items []model.Media, files []model.File, refs []model.MediaFileRef) {
	x := filetopology.New(files, refs, nil)
	paths := seasonPathsByKey(refs)
	for i := range items {
		if items[i].Type != model.Series {
			continue
		}
		for j := range items[i].Seasons {
			key := seasonKey{svcID: items[i].ServiceID, seriesID: items[i].SourceID, season: items[i].Seasons[j].Number}
			e := x.Estimate(paths[key])
			items[i].Seasons[j].ReclaimableKnown = e.Known
			items[i].Seasons[j].ReclaimableBytes = e.ReclaimableBytes
		}
	}
}

// applySeasonBundleEstimates computes, for every season, the bytes that
// would become reclaimable if that season and every torrent physically
// hardlinked to it (per Torrent.HardlinkedSeasons, already published by
// applyTorrentMediaHardlinks) were unlinked together. Mirrors
// applyMediaBundleEstimates at season granularity.
func applySeasonBundleEstimates(items []model.Media, torrents []model.Torrent, files []model.File, mediaRefs []model.MediaFileRef, torrentRefs []model.TorrentFileRef) {
	x := filetopology.New(files, mediaRefs, torrentRefs)
	seasonOwnPaths := seasonPathsByKey(mediaRefs)
	hardlinkPathsByKey := map[seasonKey][]string{}
	for _, t := range torrents {
		if model.NormalizeTorrentStatus(t.AssociationStatus) != model.TorrentCurrent {
			continue
		}
		for _, ref := range t.HardlinkedMediaItems {
			if ref.Type != model.Series {
				continue
			}
			for _, season := range t.HardlinkedSeasons {
				key := seasonKey{svcID: ref.ServiceID, seriesID: ref.SourceID, season: season}
				hardlinkPathsByKey[key] = append(hardlinkPathsByKey[key], x.TorrentPaths(t.Hash)...)
			}
		}
	}
	for i := range items {
		if items[i].Type != model.Series {
			continue
		}
		for j := range items[i].Seasons {
			key := seasonKey{svcID: items[i].ServiceID, seriesID: items[i].SourceID, season: items[i].Seasons[j].Number}
			e := x.Estimate(filetopology.Union(seasonOwnPaths[key], hardlinkPathsByKey[key]))
			items[i].Seasons[j].BundleReclaimableKnown = e.Known
			items[i].Seasons[j].BundleReclaimableBytes = e.ReclaimableBytes
		}
	}
}
