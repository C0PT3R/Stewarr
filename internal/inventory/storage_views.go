package inventory

import (
	"fmt"
	"sort"
	"stewarr/internal/filetopology"
	"stewarr/internal/model"
	"strings"
	"time"
)

func (service *Service) setFilesError(err error) error {
	service.mu.Lock()
	service.filesErr = err
	service.reliability.FileModel = "stale"
	service.mu.Unlock()
	return err
}

func (service *Service) FileSnapshot() ([]model.File, []model.MediaFileRef, []model.TorrentFileRef, time.Time, error) {
	service.mu.RLock()
	defer service.mu.RUnlock()
	files := append([]model.File(nil), service.files...)
	mr := append([]model.MediaFileRef(nil), service.mediaFileRefs...)
	tr := append([]model.TorrentFileRef(nil), service.torrentFileRefs...)
	return files, mr, tr, service.filesUpdated, service.filesErr
}

// ManagedFileRefs returns the files claimed by one specific media item.
// svcID disambiguates SourceID across multiple configured instances
// of the same service type (Radarr's own movie IDs, like Sonarr's series
// IDs, are unique only within one instance) — pass "" only when the caller
// has already independently proven (kind, id) can't collide, e.g. it was
// resolved from a single already-identified model.Media.
func (service *Service) ManagedFileRefs(kind model.MediaType, id int, svcID string) ([]model.MediaFileRef, time.Time, error) {
	_, refs, _, updated, err := service.FileSnapshot()
	out := []model.MediaFileRef{}
	for _, r := range refs {
		if r.MediaType != kind || r.MediaID != id {
			continue
		}
		if svcID != "" && r.ServiceID != svcID {
			continue
		}
		r.Parts = append([]model.MediaFilePart(nil), r.Parts...)
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	return out, updated, err
}

func (service *Service) TorrentFiles(hash string) ([]model.File, time.Time, error) {
	files, _, refs, updated, err := service.FileSnapshot()
	byPath := map[string]model.File{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	out := []model.File{}
	for _, r := range refs {
		if strings.EqualFold(r.Hash, hash) {
			if f, ok := byPath[r.Path]; ok {
				out = append(out, f)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	return out, updated, err
}

type TorrentFileOwner struct {
	Hash   string `json:"hash"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type FilePeer struct {
	Path     string             `json:"path"`
	Media    []model.MediaRef   `json:"media,omitempty"`
	Torrents []TorrentFileOwner `json:"torrents,omitempty"`
}

type FileView struct {
	File       model.File `json:"file"`
	SharedWith []FilePeer `json:"sharedWith,omitempty"`
}

type RemovalEstimate = filetopology.Estimate

type MediaStorageView struct {
	Files             []FileView      `json:"files"`
	RemoveMedia       RemovalEstimate `json:"removeMedia"`
	RemoveWithCurrent RemovalEstimate `json:"removeWithCurrentTorrents"`
}

func applyTorrentFileEstimates(torrents []model.Torrent, files []model.File, refs []model.TorrentFileRef) {
	x := filetopology.New(files, nil, refs)
	for i := range torrents {
		t := &torrents[i]
		t.ReclaimableKnown = false
		t.ReclaimableBytes = 0
		t.SharedBytes = 0
		t.InspectedBytes = 0
		t.InspectedFiles = 0
		t.SharedFiles = 0
		t.StorageError = ""
		paths := x.TorrentPaths(t.Hash)
		if len(paths) == 0 {
			continue
		}
		e := x.Estimate(paths)
		t.ReclaimableKnown = e.Known
		t.ReclaimableBytes = e.ReclaimableBytes
		t.SharedBytes = e.SharedBytes
		t.InspectedBytes = e.TotalBytes
		t.InspectedFiles = e.Files
		t.SharedFiles = e.SharedFiles
		if !e.Known && e.UnknownFiles > 0 {
			t.StorageError = fmt.Sprintf("file identity incomplete for %d torrent file(s); run File reconciliation", e.UnknownFiles)
		}
	}
}

func sameMediaRef(left, right model.MediaRef) bool {
	return left.Type == right.Type && left.ServiceID == right.ServiceID && left.SourceID == right.SourceID
}

func containsMediaRef(items []model.MediaRef, candidate model.MediaRef) bool {
	for _, item := range items {
		if sameMediaRef(item, candidate) {
			return true
		}
	}
	return false
}

// applyTorrentMediaHardlinks publishes relationship-specific topology facts.
// A torrent being shared somewhere is insufficient: the physical identity must
// specifically join that current torrent to that current media item.
func applyTorrentMediaHardlinks(torrents []model.Torrent, media []model.Media, files []model.File, mediaRefs []model.MediaFileRef, torrentRefs []model.TorrentFileRef) {
	index := filetopology.New(files, mediaRefs, torrentRefs)
	seasonPaths := seasonPathsByKey(mediaRefs)
	currentMediaByKey := make(map[string]model.MediaRef, len(media))
	for _, item := range media {
		currentMediaByKey[fmt.Sprintf("%s:%s:%d", item.Type, item.ServiceID, item.SourceID)] = model.MediaRef{ServiceID: item.ServiceID, Type: item.Type, SourceID: item.SourceID, Title: item.Title, Year: item.Year}
	}
	for torrentIndex := range torrents {
		torrent := &torrents[torrentIndex]
		torrent.AssociationStatus = model.NormalizeTorrentStatus(torrent.AssociationStatus)
		torrent.HardlinkKnownMediaItems = nil
		torrent.HardlinkedMediaItems = nil
		torrent.HardlinkedSeasons = nil
		torrent.MediaHardlinkKnown = false
		torrent.MediaHardlinked = false
		// Only media that physically shares an inode with one of this torrent's
		// paths can ever match; narrowing to that candidate set avoids scanning
		// the entire library for every torrent (O(torrents) instead of
		// O(torrents x media), which dominates refresh time on large libraries).
		for _, mediaItem := range candidateMediaRefs(index, torrent.Hash, currentMediaByKey) {
			matched, _ := index.TorrentMediaPhysicalMatch(torrent.Hash, mediaItem.Type, mediaItem.ServiceID, mediaItem.SourceID)
			if !matched {
				continue
			}
			if !containsMediaRef(torrent.MediaItems, mediaItem) {
				torrent.MediaItems = append(torrent.MediaItems, mediaItem)
			}
			former := torrent.FormerMediaItems[:0]
			for _, historical := range torrent.FormerMediaItems {
				if !sameMediaRef(historical, mediaItem) {
					former = append(former, historical)
				}
			}
			torrent.FormerMediaItems = former
			torrent.AssociationStatus = model.TorrentCurrent
			torrent.AssociationReason = "Current filesystem topology proves this torrent physically backs managed media."
		}
		for _, mediaItem := range torrent.MediaItems {
			hardlinked, known := index.TorrentMediaHardlink(torrent.Hash, mediaItem.Type, mediaItem.ServiceID, mediaItem.SourceID)
			if known {
				torrent.HardlinkKnownMediaItems = append(torrent.HardlinkKnownMediaItems, mediaItem)
			}
			if !hardlinked {
				continue
			}
			torrent.HardlinkedMediaItems = append(torrent.HardlinkedMediaItems, mediaItem)
			if mediaItem.Type != model.Series {
				continue
			}
			torrentPaths := index.TorrentPaths(torrent.Hash)
			for key, paths := range seasonPaths {
				if key.seriesID != mediaItem.SourceID || key.svcID != mediaItem.ServiceID {
					continue
				}
				if hl, _ := index.PathsHardlinked(paths, torrentPaths); hl {
					torrent.HardlinkedSeasons = append(torrent.HardlinkedSeasons, key.season)
				}
			}
			sort.Ints(torrent.HardlinkedSeasons)
		}
	}
}

// candidateMediaRefs returns the current media that physically share an inode
// with at least one of the torrent's paths, using the same identity data
// TorrentMediaPhysicalMatch itself relies on. Any media outside this set is
// guaranteed to not match, so this only skips calls that would return false.
func candidateMediaRefs(index *filetopology.Index, hash string, currentMediaByKey map[string]model.MediaRef) []model.MediaRef {
	seen := map[string]bool{}
	var candidates []model.MediaRef
	for _, torrentPath := range index.TorrentPaths(hash) {
		physicalPaths := append([]string{torrentPath}, index.SamePhysicalPaths(torrentPath)...)
		for _, path := range physicalPaths {
			for _, ref := range index.MediaOwners(path) {
				key := fmt.Sprintf("%s:%s:%d", ref.MediaType, ref.ServiceID, ref.MediaID)
				if seen[key] {
					continue
				}
				seen[key] = true
				if mediaItem, ok := currentMediaByKey[key]; ok {
					candidates = append(candidates, mediaItem)
				}
			}
		}
	}
	return candidates
}

func applyMediaFileEstimates(items []model.Media, files []model.File, refs []model.MediaFileRef) {
	x := filetopology.New(files, refs, nil)
	for i := range items {
		e := x.Estimate(x.MediaPaths(items[i].Type, items[i].ServiceID, items[i].SourceID))
		items[i].ReclaimableKnown = e.Known
		items[i].ReclaimableBytes = e.ReclaimableBytes
	}
}

// applyMediaBundleEstimates computes, for every media item, the bytes that
// would become reclaimable if the media and every torrent physically
// hardlinked to it (per HardlinkedMediaItems, already published by
// applyTorrentMediaHardlinks) were unlinked together. This is deliberately
// narrower than a "media plus every Current torrent" preview: a Current
// torrent that is merely a separate copy must never be folded into this
// figure, since removing it does not require also removing the media.
func applyMediaBundleEstimates(items []model.Media, torrents []model.Torrent, files []model.File, mediaRefs []model.MediaFileRef, torrentRefs []model.TorrentFileRef) {
	x := filetopology.New(files, mediaRefs, torrentRefs)
	hardlinkPathsByMediaKey := map[string][]string{}
	for _, t := range torrents {
		if model.NormalizeTorrentStatus(t.AssociationStatus) != model.TorrentCurrent {
			continue
		}
		for _, ref := range t.HardlinkedMediaItems {
			key := fmt.Sprintf("%s:%s:%d", ref.Type, ref.ServiceID, ref.SourceID)
			hardlinkPathsByMediaKey[key] = append(hardlinkPathsByMediaKey[key], x.TorrentPaths(t.Hash)...)
		}
	}
	for i := range items {
		key := fmt.Sprintf("%s:%s:%d", items[i].Type, items[i].ServiceID, items[i].SourceID)
		mediaPaths := x.MediaPaths(items[i].Type, items[i].ServiceID, items[i].SourceID)
		e := x.Estimate(filetopology.Union(mediaPaths, hardlinkPathsByMediaKey[key]))
		items[i].BundleReclaimableKnown = e.Known
		items[i].BundleReclaimableBytes = e.ReclaimableBytes
	}
}

func mediaRefMap(items []model.Media) map[string]model.MediaRef {
	out := map[string]model.MediaRef{}
	for _, m := range items {
		out[fmt.Sprintf("%s:%s:%d", m.Type, m.ServiceID, m.SourceID)] = model.MediaRef{ServiceID: m.ServiceID, Type: m.Type, SourceID: m.SourceID, Title: m.Title, Year: m.Year}
	}
	return out
}

func torrentOwnerMap(items []model.Torrent) map[string]TorrentFileOwner {
	out := map[string]TorrentFileOwner{}
	for _, t := range items {
		out[strings.ToLower(t.Hash)] = TorrentFileOwner{Hash: t.Hash, Name: t.Name, Status: t.AssociationStatus}
	}
	return out
}

func buildFileViews(paths []string, x *filetopology.Index, mediaRefs map[string]model.MediaRef, torrentOwners map[string]TorrentFileOwner) []FileView {
	views := make([]FileView, 0, len(paths))
	for _, path := range paths {
		f, ok := x.File(path)
		if !ok {
			continue
		}
		v := FileView{File: f}
		for _, peerPath := range x.SamePhysicalPaths(path) {
			peer := FilePeer{Path: peerPath}
			seenMedia := map[string]bool{}
			for _, r := range x.MediaOwners(peerPath) {
				key := fmt.Sprintf("%s:%s:%d", r.MediaType, r.ServiceID, r.MediaID)
				if ref, ok := mediaRefs[key]; ok && !seenMedia[key] {
					peer.Media = append(peer.Media, ref)
					seenMedia[key] = true
				}
			}
			seenTorrent := map[string]bool{}
			for _, r := range x.TorrentOwners(peerPath) {
				h := strings.ToLower(r.Hash)
				if owner, ok := torrentOwners[h]; ok && !seenTorrent[h] {
					peer.Torrents = append(peer.Torrents, owner)
					seenTorrent[h] = true
				}
			}
			v.SharedWith = append(v.SharedWith, peer)
		}
		views = append(views, v)
	}
	sort.Slice(views, func(i, j int) bool { return strings.ToLower(views[i].File.Path) < strings.ToLower(views[j].File.Path) })
	return views
}

func (service *Service) MediaStorage(kind model.MediaType, svcID string, id int) (MediaStorageView, time.Time, error) {
	files, mr, tr, updated, err := service.FileSnapshot()
	items, _, _ := service.Snapshot()
	torrents := service.TorrentSnapshot()
	x := filetopology.New(files, mr, tr)
	paths := x.MediaPaths(kind, svcID, id)
	view := MediaStorageView{Files: buildFileViews(paths, x, mediaRefMap(items), torrentOwnerMap(torrents))}
	view.RemoveMedia = x.Estimate(paths)
	var currentPaths []string
	for _, t := range torrents {
		if model.NormalizeTorrentStatus(t.AssociationStatus) != model.TorrentCurrent {
			continue
		}
		matched := false
		for _, ref := range t.MediaItems {
			if ref.Type == kind && ref.ServiceID == svcID && ref.SourceID == id {
				matched = true
				break
			}
		}
		if matched {
			currentPaths = append(currentPaths, x.TorrentPaths(t.Hash)...)
		}
	}
	view.RemoveWithCurrent = x.Estimate(filetopology.Union(paths, currentPaths))
	return view, updated, err
}

type TorrentStorageView struct {
	Files         []FileView      `json:"files"`
	RemoveTorrent RemovalEstimate `json:"removeTorrent"`
}

func (service *Service) TorrentStorage(hash string) (TorrentStorageView, time.Time, error) {
	files, mr, tr, updated, err := service.FileSnapshot()
	items, _, _ := service.Snapshot()
	torrents := service.TorrentSnapshot()
	x := filetopology.New(files, mr, tr)
	paths := x.TorrentPaths(hash)
	return TorrentStorageView{Files: buildFileViews(paths, x, mediaRefMap(items), torrentOwnerMap(torrents)), RemoveTorrent: x.Estimate(paths)}, updated, err
}
