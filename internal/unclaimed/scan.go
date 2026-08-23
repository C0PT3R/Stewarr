package unclaimed

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"togetharr/internal/model"
)

type Result struct {
	Available        bool
	Files            []model.UnclaimedFile
	TotalBytes       int64
	ReclaimableBytes int64
	SharedBytes      int64
	Error            string
}

type fileID struct{ dev, ino uint64 }

type inodeGroup struct {
	size  int64
	links uint64
	count int
	idxs  []int
}

func inside(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func collapseRoots(paths []string) []string {
	uniq := map[string]bool{}
	for _, p := range paths {
		p = filepath.Clean(p)
		if p != "." && p != "" {
			uniq[p] = true
		}
	}
	xs := make([]string, 0, len(uniq))
	for p := range uniq {
		xs = append(xs, p)
	}
	sort.Slice(xs, func(i, j int) bool { return len(xs[i]) < len(xs[j]) })
	out := []string{}
	for _, p := range xs {
		nested := false
		for _, r := range out {
			if inside(r, p) {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, p)
		}
	}
	return out
}

// Scan enumerates regular files below the supplied download roots and returns
// files that are not present in the authoritative claimed-path set. The caller
// must only invoke this after every torrent file list has been loaded
// successfully; otherwise absence from claimedPaths is not proof of anything.
func Scan(storageRoot string, downloadRoots []string, claimedPaths map[string]bool) Result {
	var out Result
	roots := collapseRoots(downloadRoots)
	if len(roots) == 0 {
		out.Error = "no visible torrent save paths"
		return out
	}
	cleanStorage := filepath.Clean(storageRoot)
	for _, root := range roots {
		if !inside(cleanStorage, root) {
			out.Error = fmt.Sprintf("download path %s is outside configured storage path", root)
			return out
		}
		if _, err := os.Stat(root); err != nil {
			out.Error = fmt.Sprintf("download path %s: %v", root, err)
			return out
		}
	}

	groups := map[fileID]*inodeGroup{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			cp := filepath.Clean(path)
			if claimedPaths[cp] {
				return nil
			}
			st, ok := info.Sys().(*syscall.Stat_t)
			if !ok {
				return fmt.Errorf("file identity/link count unavailable for %s", path)
			}
			uf := model.UnclaimedFile{Path: cp, SizeBytes: info.Size(), ModifiedAt: info.ModTime(), Device: uint64(st.Dev), Inode: uint64(st.Ino), Links: uint64(st.Nlink), ReclaimableKnown: true}
			idx := len(out.Files)
			out.Files = append(out.Files, uf)
			out.TotalBytes += info.Size()
			id := fileID{uint64(st.Dev), uint64(st.Ino)}
			g := groups[id]
			if g == nil {
				g = &inodeGroup{size: info.Size(), links: uint64(st.Nlink)}
				groups[id] = g
			}
			g.count++
			g.idxs = append(g.idxs, idx)
			return nil
		})
		if err != nil {
			out.Error = err.Error()
			out.Files = nil
			return out
		}
	}
	for _, g := range groups {
		reclaim := uint64(g.count) >= g.links
		for _, idx := range g.idxs {
			if reclaim {
				out.Files[idx].ReclaimableBytes = out.Files[idx].SizeBytes
			} else {
				out.Files[idx].SharedBytes = out.Files[idx].SizeBytes
			}
		}
		if reclaim {
			out.ReclaimableBytes += g.size
		} else {
			out.SharedBytes += g.size
		}
	}
	sort.Slice(out.Files, func(i, j int) bool { return strings.ToLower(out.Files[i].Path) < strings.ToLower(out.Files[j].Path) })
	out.Available = true
	return out
}

var _ = time.Time{}
