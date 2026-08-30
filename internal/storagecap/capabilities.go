package storagecap

import (
	"fmt"
	"os"
	"syscall"
)

type Capabilities struct {
	Path                  string `json:"path"`
	Visible               bool   `json:"visible"`
	Filesystem            string `json:"filesystem"`
	Device                uint64 `json:"device"`
	FileIdentity          bool   `json:"fileIdentity"`
	HardlinkDetection     bool   `json:"hardlinkDetection"`
	SharedExtentDetection bool   `json:"sharedExtentDetection"`
	ExactReclaimEstimate  bool   `json:"exactReclaimEstimate"`
	Error                 string `json:"error,omitempty"`
}

func Inspect(path string) Capabilities {
	capabilities := Capabilities{Path: path}
	var filesystemStats syscall.Statfs_t
	if err := syscall.Statfs(path, &filesystemStats); err != nil {
		capabilities.Error = err.Error()
		return capabilities
	}
	capabilities.Visible = true
	capabilities.Filesystem = filesystemName(int64(filesystemStats.Type))

	fileInfo, err := os.Stat(path)
	if err != nil {
		capabilities.Error = err.Error()
		return capabilities
	}
	if fileStat, ok := fileInfo.Sys().(*syscall.Stat_t); ok {
		capabilities.Device = uint64(fileStat.Dev)
		capabilities.FileIdentity = true
		capabilities.HardlinkDetection = true
	}

	// Shared extents/reflinks need filesystem-specific extent inspection. Keep
	// this capability explicit rather than pretending inode checks solve it.
	capabilities.SharedExtentDetection = false
	// Exact reclaim calculation also needs every surviving reference (including
	// torrent data) to be visible and inspected. That layer is not implemented yet.
	capabilities.ExactReclaimEstimate = false
	return capabilities
}

func filesystemName(filesystemType int64) string {
	switch uint64(filesystemType) {
	case 0xEF53:
		return "ext2/ext3/ext4"
	case 0x58465342:
		return "XFS"
	case 0x9123683E:
		return "Btrfs"
	case 0x2FC12FC1:
		return "ZFS"
	case 0x6969:
		return "NFS"
	case 0xFF534D42:
		return "CIFS/SMB"
	case 0x01021994:
		return "tmpfs"
	case 0x794C7630:
		return "overlayfs"
	default:
		return fmt.Sprintf("0x%x", uint64(filesystemType))
	}
}
