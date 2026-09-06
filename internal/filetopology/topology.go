package filetopology

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"connarr/internal/model"
)

type identity struct {
	device uint64
	inode  uint64
}

type Estimate struct {
	Known            bool  `json:"known"`
	ReclaimableBytes int64 `json:"reclaimableBytes"`
	SharedBytes      int64 `json:"sharedBytes"`
	TotalBytes       int64 `json:"totalBytes"`
	Files            int   `json:"files"`
	SharedFiles      int   `json:"sharedFiles"`
	UnknownFiles     int   `json:"unknownFiles"`
}

type Index struct {
	files         map[string]model.File
	identityPaths map[identity][]string
	mediaPaths    map[string][]string
	torrentPaths  map[string][]string
	mediaByPath   map[string][]model.MediaFileRef
	torrentByPath map[string][]model.TorrentFileRef
}

// mediaKey identifies one media item across multiple configured instances of
// the same service type. Radarr/Sonarr assign their own movie/series IDs
// sequentially starting from 1 per instance, so two instances routinely
// share numeric IDs — serviceID is required to avoid silently merging
// two unrelated movies/series' file sets.
func mediaKey(kind model.MediaType, serviceID string, id int) string {
	return string(kind) + ":" + serviceID + ":" + strconv.Itoa(id)
}

func New(files []model.File, mediaRefs []model.MediaFileRef, torrentRefs []model.TorrentFileRef) *Index {
	index := &Index{
		files:         map[string]model.File{},
		identityPaths: map[identity][]string{},
		mediaPaths:    map[string][]string{},
		torrentPaths:  map[string][]string{},
		mediaByPath:   map[string][]model.MediaFileRef{},
		torrentByPath: map[string][]model.TorrentFileRef{},
	}
	for _, file := range files {
		cleanPath := filepath.Clean(file.Path)
		file.Path = cleanPath
		index.files[cleanPath] = file
		if file.Exists && file.IdentityKnown {
			fileIdentity := identity{file.Device, file.Inode}
			index.identityPaths[fileIdentity] = appendUnique(index.identityPaths[fileIdentity], cleanPath)
		}
	}
	for _, mediaRef := range mediaRefs {
		cleanPath := filepath.Clean(mediaRef.Path)
		key := mediaKey(mediaRef.MediaType, mediaRef.ServiceID, mediaRef.MediaID)
		index.mediaPaths[key] = appendUnique(index.mediaPaths[key], cleanPath)
		index.mediaByPath[cleanPath] = append(index.mediaByPath[cleanPath], mediaRef)
	}
	for _, torrentRef := range torrentRefs {
		cleanPath := filepath.Clean(torrentRef.Path)
		normalizedHash := strings.ToLower(torrentRef.Hash)
		index.torrentPaths[normalizedHash] = appendUnique(index.torrentPaths[normalizedHash], cleanPath)
		index.torrentByPath[cleanPath] = append(index.torrentByPath[cleanPath], torrentRef)
	}
	for _, paths := range index.identityPaths {
		sort.Strings(paths)
	}
	return index
}

func appendUnique(values []string, candidate string) []string {
	for _, value := range values {
		if value == candidate {
			return values
		}
	}
	return append(values, candidate)
}

func (index *Index) MediaPaths(kind model.MediaType, serviceID string, id int) []string {
	return append([]string(nil), index.mediaPaths[mediaKey(kind, serviceID, id)]...)
}

func (index *Index) TorrentPaths(hash string) []string {
	return append([]string(nil), index.torrentPaths[strings.ToLower(hash)]...)
}

func (index *Index) File(path string) (model.File, bool) {
	file, found := index.files[filepath.Clean(path)]
	return file, found
}

func (index *Index) SamePhysicalPaths(path string) []string {
	file, found := index.File(path)
	if !found || !file.Exists || !file.IdentityKnown {
		return nil
	}
	physicalPaths := []string{}
	for _, physicalPath := range index.identityPaths[identity{file.Device, file.Inode}] {
		if physicalPath != filepath.Clean(path) {
			physicalPaths = append(physicalPaths, physicalPath)
		}
	}
	return physicalPaths
}

func (index *Index) MediaOwners(path string) []model.MediaFileRef {
	return append([]model.MediaFileRef(nil), index.mediaByPath[filepath.Clean(path)]...)
}

func (index *Index) TorrentOwners(path string) []model.TorrentFileRef {
	return append([]model.TorrentFileRef(nil), index.torrentByPath[filepath.Clean(path)]...)
}

// TorrentMediaHardlink reports whether at least one torrent path and one media
// path are distinct directory entries for the same physical inode. known is
// false when either owner has no indexed paths or any required identity fact is
// missing; a proven hardlink remains authoritative even if another file in the
// relationship is unknown.
func (index *Index) TorrentMediaHardlink(hash string, kind model.MediaType, serviceID string, id int) (hardlinked, known bool) {
	mediaPaths := index.MediaPaths(kind, serviceID, id)
	torrentPaths := index.TorrentPaths(hash)
	if len(mediaPaths) == 0 || len(torrentPaths) == 0 {
		return false, false
	}

	mediaIdentities := map[identity]map[string]bool{}
	complete := true
	for _, path := range mediaPaths {
		file, found := index.File(path)
		if !found || !file.Exists || !file.IdentityKnown || file.Links == 0 {
			complete = false
			continue
		}
		fileIdentity := identity{file.Device, file.Inode}
		if mediaIdentities[fileIdentity] == nil {
			mediaIdentities[fileIdentity] = map[string]bool{}
		}
		mediaIdentities[fileIdentity][filepath.Clean(path)] = true
	}
	for _, path := range torrentPaths {
		file, found := index.File(path)
		if !found || !file.Exists || !file.IdentityKnown || file.Links == 0 {
			complete = false
			continue
		}
		mediaOwners := mediaIdentities[identity{file.Device, file.Inode}]
		for mediaPath := range mediaOwners {
			if mediaPath != filepath.Clean(path) && file.Links > 1 {
				return true, true
			}
		}
	}
	return false, complete
}

// TorrentMediaPhysicalMatch reports whether a torrent and managed media claim
// the same physical inode. Unlike TorrentMediaHardlink, the two owners may
// claim the exact same pathname; that still proves the torrent currently backs
// the managed file even though it is not a distinct hardlink directory entry.
func (index *Index) TorrentMediaPhysicalMatch(hash string, kind model.MediaType, serviceID string, id int) (matched, known bool) {
	mediaPaths := index.MediaPaths(kind, serviceID, id)
	torrentPaths := index.TorrentPaths(hash)
	if len(mediaPaths) == 0 || len(torrentPaths) == 0 {
		return false, false
	}
	mediaIdentities := map[identity]bool{}
	complete := true
	for _, path := range mediaPaths {
		file, found := index.File(path)
		if !found || !file.Exists || !file.IdentityKnown {
			complete = false
			continue
		}
		mediaIdentities[identity{file.Device, file.Inode}] = true
	}
	for _, path := range torrentPaths {
		file, found := index.File(path)
		if !found || !file.Exists || !file.IdentityKnown {
			complete = false
			continue
		}
		if mediaIdentities[identity{file.Device, file.Inode}] {
			return true, true
		}
	}
	return false, complete
}

// PathsHardlinked reports whether at least one path in each set is a
// distinct directory entry for the same physical inode, mirroring
// TorrentMediaHardlink's semantics but for two arbitrary path sets rather
// than a specific torrent/media pair — used to scope hardlink attribution to
// a subset of a media's files, such as one season of a Series.
func (index *Index) PathsHardlinked(pathsA, pathsB []string) (hardlinked, known bool) {
	if len(pathsA) == 0 || len(pathsB) == 0 {
		return false, false
	}
	identitiesA := map[identity]map[string]bool{}
	complete := true
	for _, path := range pathsA {
		file, found := index.File(path)
		if !found || !file.Exists || !file.IdentityKnown || file.Links == 0 {
			complete = false
			continue
		}
		fileIdentity := identity{file.Device, file.Inode}
		if identitiesA[fileIdentity] == nil {
			identitiesA[fileIdentity] = map[string]bool{}
		}
		identitiesA[fileIdentity][filepath.Clean(path)] = true
	}
	for _, path := range pathsB {
		file, found := index.File(path)
		if !found || !file.Exists || !file.IdentityKnown || file.Links == 0 {
			complete = false
			continue
		}
		owners := identitiesA[identity{file.Device, file.Inode}]
		for ownerPath := range owners {
			if ownerPath != filepath.Clean(path) && file.Links > 1 {
				return true, true
			}
		}
	}
	return false, complete
}

// Estimate reports the unique bytes that would become unreferenced if all of
// the supplied paths were unlinked. A physical inode is reclaimable only when
// the deletion set contains every filesystem link to that inode. Unknown or
// missing paths make Known false rather than being guessed.
func (index *Index) Estimate(paths []string) Estimate {
	var estimate Estimate
	selected := map[string]bool{}
	for _, path := range paths {
		selected[filepath.Clean(path)] = true
	}
	type use struct {
		size          int64
		links         uint64
		selected      int
		selectedFiles int
	}
	uses := map[identity]*use{}
	for path := range selected {
		file, found := index.files[path]
		if !found || !file.Exists || !file.IdentityKnown || file.Links == 0 {
			estimate.UnknownFiles++
			continue
		}
		estimate.Files++
		fileIdentity := identity{file.Device, file.Inode}
		usage := uses[fileIdentity]
		if usage == nil {
			usage = &use{size: file.SizeBytes, links: file.Links}
			uses[fileIdentity] = usage
		}
		usage.selected++
		usage.selectedFiles++
	}
	for _, usage := range uses {
		estimate.TotalBytes += usage.size
		if uint64(usage.selected) >= usage.links {
			estimate.ReclaimableBytes += usage.size
		} else {
			estimate.SharedBytes += usage.size
			estimate.SharedFiles += usage.selectedFiles
		}
	}
	estimate.Known = len(selected) > 0 && estimate.UnknownFiles == 0
	return estimate
}

func Union(pathSets ...[]string) []string {
	seen := map[string]bool{}
	var union []string
	for _, paths := range pathSets {
		for _, path := range paths {
			cleanPath := filepath.Clean(path)
			if !seen[cleanPath] {
				seen[cleanPath] = true
				union = append(union, cleanPath)
			}
		}
	}
	sort.Strings(union)
	return union
}
