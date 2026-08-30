package unclaimed

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"connarr/internal/model"
)

type Result struct {
	Available        bool
	Files            []model.UnclaimedFile
	TotalBytes       int64
	ReclaimableBytes int64
	SharedBytes      int64
	Error            string
}

type fileIdentity struct {
	device uint64
	inode  uint64
}

type inodeGroup struct {
	sizeBytes       int64
	filesystemLinks uint64
	discoveredPaths int
	fileIndexes     []int
}

func inside(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func collapseRoots(paths []string) []string {
	uniqueRoots := map[string]bool{}
	for _, path := range paths {
		cleanPath := filepath.Clean(path)
		if cleanPath != "." && cleanPath != "" {
			uniqueRoots[cleanPath] = true
		}
	}
	candidates := make([]string, 0, len(uniqueRoots))
	for path := range uniqueRoots {
		candidates = append(candidates, path)
	}
	sort.Slice(candidates, func(leftIndex, rightIndex int) bool {
		return len(candidates[leftIndex]) < len(candidates[rightIndex])
	})
	collapsedRoots := []string{}
	for _, candidate := range candidates {
		nested := false
		for _, acceptedRoot := range collapsedRoots {
			if inside(acceptedRoot, candidate) {
				nested = true
				break
			}
		}
		if !nested {
			collapsedRoots = append(collapsedRoots, candidate)
		}
	}
	return collapsedRoots
}

// Scan enumerates regular files below the supplied download roots and returns
// files that are not present in the authoritative claimed-path set. The caller
// must only invoke this after every torrent file list has been loaded
// successfully; otherwise absence from claimedPaths is not proof of anything.
func Scan(storageRoot string, downloadRoots []string, claimedPaths map[string]bool) Result {
	var result Result
	roots := collapseRoots(downloadRoots)
	if len(roots) == 0 {
		result.Error = "no visible torrent save paths"
		return result
	}
	cleanStorage := filepath.Clean(storageRoot)
	for _, root := range roots {
		if !inside(cleanStorage, root) {
			result.Error = fmt.Sprintf("download path %s is outside configured storage path", root)
			return result
		}
		if _, err := os.Stat(root); err != nil {
			result.Error = fmt.Sprintf("download path %s: %v", root, err)
			return result
		}
	}

	inodeGroups := map[fileIdentity]*inodeGroup{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, directoryEntry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if directoryEntry.IsDir() {
				return nil
			}
			fileInfo, err := directoryEntry.Info()
			if err != nil {
				return err
			}
			if !fileInfo.Mode().IsRegular() {
				return nil
			}
			cleanPath := filepath.Clean(path)
			if claimedPaths[cleanPath] {
				return nil
			}
			fileStat, ok := fileInfo.Sys().(*syscall.Stat_t)
			if !ok {
				return fmt.Errorf("file identity/link count unavailable for %s", path)
			}
			unclaimedFile := model.UnclaimedFile{Path: cleanPath, SizeBytes: fileInfo.Size(), ModifiedAt: fileInfo.ModTime(), Device: uint64(fileStat.Dev), Inode: uint64(fileStat.Ino), Links: uint64(fileStat.Nlink), ReclaimableKnown: true}
			fileIndex := len(result.Files)
			result.Files = append(result.Files, unclaimedFile)
			result.TotalBytes += fileInfo.Size()
			identity := fileIdentity{device: uint64(fileStat.Dev), inode: uint64(fileStat.Ino)}
			group := inodeGroups[identity]
			if group == nil {
				group = &inodeGroup{sizeBytes: fileInfo.Size(), filesystemLinks: uint64(fileStat.Nlink)}
				inodeGroups[identity] = group
			}
			group.discoveredPaths++
			group.fileIndexes = append(group.fileIndexes, fileIndex)
			return nil
		})
		if err != nil {
			result.Error = err.Error()
			result.Files = nil
			return result
		}
	}
	for _, group := range inodeGroups {
		fullyDiscovered := uint64(group.discoveredPaths) >= group.filesystemLinks
		for _, fileIndex := range group.fileIndexes {
			if fullyDiscovered {
				result.Files[fileIndex].ReclaimableBytes = result.Files[fileIndex].SizeBytes
			} else {
				result.Files[fileIndex].SharedBytes = result.Files[fileIndex].SizeBytes
			}
		}
		if fullyDiscovered {
			result.ReclaimableBytes += group.sizeBytes
		} else {
			result.SharedBytes += group.sizeBytes
		}
	}
	sort.Slice(result.Files, func(leftIndex, rightIndex int) bool {
		return strings.ToLower(result.Files[leftIndex].Path) < strings.ToLower(result.Files[rightIndex].Path)
	})
	result.Available = true
	return result
}
