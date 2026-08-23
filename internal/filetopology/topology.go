package filetopology

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"togetharr/internal/model"
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

func mediaKey(kind model.MediaType, id int) string { return string(kind) + ":" + strconv.Itoa(id) }

func New(files []model.File, mediaRefs []model.MediaFileRef, torrentRefs []model.TorrentFileRef) *Index {
	x := &Index{
		files:         map[string]model.File{},
		identityPaths: map[identity][]string{},
		mediaPaths:    map[string][]string{},
		torrentPaths:  map[string][]string{},
		mediaByPath:   map[string][]model.MediaFileRef{},
		torrentByPath: map[string][]model.TorrentFileRef{},
	}
	for _, f := range files {
		p := filepath.Clean(f.Path)
		f.Path = p
		x.files[p] = f
		if f.Exists && f.IdentityKnown {
			id := identity{f.Device, f.Inode}
			x.identityPaths[id] = appendUnique(x.identityPaths[id], p)
		}
	}
	for _, r := range mediaRefs {
		p := filepath.Clean(r.Path)
		x.mediaPaths[mediaKey(r.MediaType, r.MediaID)] = appendUnique(x.mediaPaths[mediaKey(r.MediaType, r.MediaID)], p)
		x.mediaByPath[p] = append(x.mediaByPath[p], r)
	}
	for _, r := range torrentRefs {
		p := filepath.Clean(r.Path)
		h := strings.ToLower(r.Hash)
		x.torrentPaths[h] = appendUnique(x.torrentPaths[h], p)
		x.torrentByPath[p] = append(x.torrentByPath[p], r)
	}
	for _, ps := range x.identityPaths {
		sort.Strings(ps)
	}
	return x
}

func appendUnique(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

func (x *Index) MediaPaths(kind model.MediaType, id int) []string {
	return append([]string(nil), x.mediaPaths[mediaKey(kind, id)]...)
}

func (x *Index) TorrentPaths(hash string) []string {
	return append([]string(nil), x.torrentPaths[strings.ToLower(hash)]...)
}

func (x *Index) File(path string) (model.File, bool) {
	f, ok := x.files[filepath.Clean(path)]
	return f, ok
}

func (x *Index) SamePhysicalPaths(path string) []string {
	f, ok := x.File(path)
	if !ok || !f.Exists || !f.IdentityKnown {
		return nil
	}
	out := []string{}
	for _, p := range x.identityPaths[identity{f.Device, f.Inode}] {
		if p != filepath.Clean(path) {
			out = append(out, p)
		}
	}
	return out
}

func (x *Index) MediaOwners(path string) []model.MediaFileRef {
	return append([]model.MediaFileRef(nil), x.mediaByPath[filepath.Clean(path)]...)
}

func (x *Index) TorrentOwners(path string) []model.TorrentFileRef {
	return append([]model.TorrentFileRef(nil), x.torrentByPath[filepath.Clean(path)]...)
}

// Estimate reports the unique bytes that would become unreferenced if all of
// the supplied paths were unlinked. A physical inode is reclaimable only when
// the deletion set contains every filesystem link to that inode. Unknown or
// missing paths make Known false rather than being guessed.
func (x *Index) Estimate(paths []string) Estimate {
	var out Estimate
	selected := map[string]bool{}
	for _, p := range paths {
		selected[filepath.Clean(p)] = true
	}
	type use struct {
		size          int64
		links         uint64
		selected      int
		selectedFiles int
	}
	uses := map[identity]*use{}
	for p := range selected {
		f, ok := x.files[p]
		if !ok || !f.Exists || !f.IdentityKnown || f.Links == 0 {
			out.UnknownFiles++
			continue
		}
		out.Files++
		id := identity{f.Device, f.Inode}
		u := uses[id]
		if u == nil {
			u = &use{size: f.SizeBytes, links: f.Links}
			uses[id] = u
		}
		u.selected++
		u.selectedFiles++
	}
	for _, u := range uses {
		out.TotalBytes += u.size
		if uint64(u.selected) >= u.links {
			out.ReclaimableBytes += u.size
		} else {
			out.SharedBytes += u.size
			out.SharedFiles += u.selectedFiles
		}
	}
	out.Known = len(selected) > 0 && out.UnknownFiles == 0
	return out
}

func Union(pathSets ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, xs := range pathSets {
		for _, p := range xs {
			p = filepath.Clean(p)
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}
