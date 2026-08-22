package torrentstorage

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type Result struct {
	Visible          bool
	Known            bool
	ReclaimableBytes int64
	SharedBytes      int64
	TotalBytes       int64
	Files            int
	SharedFiles      int
	Error            string
}

type fileID struct {
	dev uint64
	ino uint64
}

type inodeUse struct {
	size       int64
	links      uint64
	pathsInSet int
}

// Inspect estimates the bytes that would become unreferenced if every regular
// file below path were removed. It groups paths by device/inode, so hardlinks
// within the torrent itself are handled correctly: an inode is reclaimable when
// the deletion set contains all of its links, and shared when links remain
// outside the set.
//
// The result intentionally does not claim to account for reflinks, snapshots,
// block-level deduplication, or storage outside the visible filesystem.
func Inspect(root, path string) Result {
	var out Result
	if path == "" {
		out.Error = "torrent content path is empty"
		return out
	}
	if root != "" {
		cleanRoot := filepath.Clean(root)
		cleanPath := filepath.Clean(path)
		rel, err := filepath.Rel(cleanRoot, cleanPath)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			out.Error = "torrent content path is outside configured storage path"
			return out
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Visible = true

	uses := map[fileID]*inodeUse{}
	visit := func(p string, info fs.FileInfo) error {
		if !info.Mode().IsRegular() {
			return nil
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("file identity/link count unavailable for %s", p)
		}
		out.Files++
		id := fileID{dev: uint64(st.Dev), ino: uint64(st.Ino)}
		u := uses[id]
		if u == nil {
			u = &inodeUse{size: info.Size(), links: uint64(st.Nlink)}
			uses[id] = u
		}
		u.pathsInSet++
		return nil
	}

	if info.Mode().IsRegular() {
		if err := visit(path, info); err != nil {
			out.Error = err.Error()
			return out
		}
	} else if info.IsDir() {
		err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
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
			return visit(p, info)
		})
		if err != nil {
			out.Error = err.Error()
			return out
		}
	} else {
		out.Error = "torrent content path is neither a regular file nor directory"
		return out
	}

	for _, u := range uses {
		out.TotalBytes += u.size
		if uint64(u.pathsInSet) >= u.links {
			out.ReclaimableBytes += u.size
		} else {
			out.SharedBytes += u.size
			out.SharedFiles += u.pathsInSet
		}
	}
	out.Known = true
	return out
}
