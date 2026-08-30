package removal

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

type ObjectKind string

const (
	MediaObject     ObjectKind = "media"
	TorrentObject   ObjectKind = "torrent"
	UnclaimedObject ObjectKind = "unclaimed"
)

type FileOwner string

const (
	MediaOwner     FileOwner = "media"
	TorrentOwner   FileOwner = "torrent"
	UnclaimedOwner FileOwner = "unclaimed"
)

type CandidateFile struct {
	Path       string    `json:"path"`
	Owner      FileOwner `json:"owner"`
	OwnerKey   string    `json:"ownerKey"`
	Label      string    `json:"label"`
	Selected   bool      `json:"selected"`
	Selectable bool      `json:"selectable"`
}

type FileState struct {
	Path          string    `json:"path"`
	Owner         FileOwner `json:"owner"`
	OwnerKey      string    `json:"ownerKey"`
	Label         string    `json:"label"`
	Selected      bool      `json:"selected"`
	Selectable    bool      `json:"selectable"`
	Exists        bool      `json:"exists"`
	SizeBytes     int64     `json:"sizeBytes"`
	IdentityKnown bool      `json:"identityKnown"`
	Device        uint64    `json:"device,omitempty"`
	Inode         uint64    `json:"inode,omitempty"`
	Links         uint64    `json:"links,omitempty"`
	Error         string    `json:"error,omitempty"`
}

type DeviceImpact struct {
	Device uint64 `json:"device"`
	Bytes  int64  `json:"bytes"`
}

type RemovalPlan struct {
	Kind              ObjectKind     `json:"kind"`
	RequestedKey      string         `json:"requestedKey"`
	RequestedLabel    string         `json:"requestedLabel"`
	DryRun            bool           `json:"dryRun"`
	Files             []FileState    `json:"files"`
	SelectedPathBytes int64          `json:"selectedPathBytes"`
	ReclaimableBytes  int64          `json:"reclaimableBytes"`
	DeviceImpacts     []DeviceImpact `json:"deviceImpacts,omitempty"`
	Warnings          []string       `json:"warnings,omitempty"`
}

type physicalFileIdentity struct {
	device uint64
	inode  uint64
}

type physicalFileGroup struct {
	sizeBytes     int64
	linkCount     uint64
	identityKnown bool
	knownPaths    map[string]bool
	selectedPaths map[string]bool
}

// Build refreshes the filesystem facts for only the supplied known paths and
// calculates the physical bytes whose final link would disappear.
func Build(kind ObjectKind, key, label string, dryRun bool, candidates []CandidateFile) RemovalPlan {
	plan := RemovalPlan{
		Kind:           kind,
		RequestedKey:   key,
		RequestedLabel: label,
		DryRun:         dryRun,
	}
	physicalFiles := make(map[physicalFileIdentity]*physicalFileGroup)

	for _, candidate := range candidates {
		candidate.Path = filepath.Clean(candidate.Path)
		fileState := FileState{
			Path:       candidate.Path,
			Owner:      candidate.Owner,
			OwnerKey:   candidate.OwnerKey,
			Label:      candidate.Label,
			Selected:   candidate.Selected,
			Selectable: candidate.Selectable,
		}

		fileInfo, err := os.Stat(candidate.Path)
		if err != nil {
			if !os.IsNotExist(err) {
				fileState.Error = err.Error()
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s: %v", candidate.Path, err))
			}
			plan.Files = append(plan.Files, fileState)
			continue
		}

		fileState.Exists = true
		fileState.SizeBytes = fileInfo.Size()

		stat, identityAvailable := fileInfo.Sys().(*syscall.Stat_t)
		if !identityAvailable {
			// Without physical identity, Connarr cannot determine whether another
			// path references this File. Count a selected existing File once.
			if candidate.Selected {
				plan.SelectedPathBytes += fileState.SizeBytes
			}
			plan.Files = append(plan.Files, fileState)
			continue
		}

		fileState.IdentityKnown = true
		fileState.Device = uint64(stat.Dev)
		fileState.Inode = uint64(stat.Ino)
		fileState.Links = uint64(stat.Nlink)

		identity := physicalFileIdentity{device: fileState.Device, inode: fileState.Inode}
		physicalFile := physicalFiles[identity]
		if physicalFile == nil {
			physicalFile = &physicalFileGroup{
				sizeBytes:     fileState.SizeBytes,
				linkCount:     fileState.Links,
				identityKnown: true,
				knownPaths:    make(map[string]bool),
				selectedPaths: make(map[string]bool),
			}
			physicalFiles[identity] = physicalFile
		}

		physicalFile.knownPaths[fileState.Path] = true
		if candidate.Selected {
			physicalFile.selectedPaths[fileState.Path] = true
		}
		plan.Files = append(plan.Files, fileState)
	}

	// SelectedPathBytes is retained as the wire/API field name for compatibility,
	// but it represents physical File bytes that will actually cease to exist. A
	// hardlinked File contributes its size only when the selected actions remove
	// its final filesystem link. Selecting only some paths to a File contributes
	// zero bytes.
	bytesRemovedByDevice := make(map[uint64]int64)
	missingHardlinks := false

	for identity, physicalFile := range physicalFiles {
		if physicalFile.identityKnown && physicalFile.linkCount > uint64(len(physicalFile.knownPaths)) {
			missingHardlinks = true
		}

		removesFinalLink := physicalFile.identityKnown &&
			physicalFile.linkCount > 0 &&
			uint64(len(physicalFile.selectedPaths)) >= physicalFile.linkCount
		if !removesFinalLink {
			continue
		}

		plan.SelectedPathBytes += physicalFile.sizeBytes
		plan.ReclaimableBytes += physicalFile.sizeBytes
		bytesRemovedByDevice[identity.device] += physicalFile.sizeBytes
	}

	if missingHardlinks {
		plan.Warnings = append(plan.Warnings, "One or more hardlinks could not be located. Removing the selected paths may not free their space and may leave unclaimed files.")
	}

	for device, bytes := range bytesRemovedByDevice {
		plan.DeviceImpacts = append(plan.DeviceImpacts, DeviceImpact{Device: device, Bytes: bytes})
	}
	sort.Slice(plan.DeviceImpacts, func(left, right int) bool {
		return plan.DeviceImpacts[left].Device < plan.DeviceImpacts[right].Device
	})
	sort.SliceStable(plan.Files, func(left, right int) bool {
		if plan.Files[left].Selected != plan.Files[right].Selected {
			return plan.Files[left].Selected
		}
		return plan.Files[left].Path < plan.Files[right].Path
	})

	return plan
}
