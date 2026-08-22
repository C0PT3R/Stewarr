package storagecap

import (
	"fmt"
	"os"
	"path/filepath"
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
	c := Capabilities{Path: path}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		c.Error = err.Error()
		return c
	}
	c.Visible = true
	c.Filesystem = filesystemName(int64(fs.Type))

	fi, err := os.Stat(path)
	if err != nil {
		c.Error = err.Error()
		return c
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		c.Device = uint64(st.Dev)
		c.FileIdentity = true
		c.HardlinkDetection = true
	}

	// Shared extents/reflinks need filesystem-specific extent inspection. Keep
	// this capability explicit rather than pretending inode checks solve it.
	c.SharedExtentDetection = false
	// Exact reclaim calculation also needs every surviving reference (including
	// torrent data) to be visible and inspected. That layer is not implemented yet.
	c.ExactReclaimEstimate = false
	return c
}

func SameFile(a, b string) (bool, error) {
	ai, err := os.Stat(filepath.Clean(a))
	if err != nil {
		return false, err
	}
	bi, err := os.Stat(filepath.Clean(b))
	if err != nil {
		return false, err
	}
	as, aok := ai.Sys().(*syscall.Stat_t)
	bs, bok := bi.Sys().(*syscall.Stat_t)
	if !aok || !bok {
		return false, fmt.Errorf("file identity is unavailable")
	}
	return as.Dev == bs.Dev && as.Ino == bs.Ino, nil
}

func filesystemName(t int64) string {
	switch uint64(t) {
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
		return fmt.Sprintf("0x%x", uint64(t))
	}
}
