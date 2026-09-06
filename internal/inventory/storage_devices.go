package inventory

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"

	"connarr/internal/model"
	"connarr/internal/storagecap"
)

// StorageDevice reports one physical storage device known from the last file
// reconciliation's service-discovered roots, with real filesystem usage
// alongside Connarr's own attribution of claimed bytes by service. The
// two are kept separate rather than forced to agree: OtherBytes is whatever
// real usage Connarr's claims do not account for, preserved as Unknown
// rather than guessed away.
type StorageDevice struct {
	Filesystem         string
	RootLabels         []string
	RepresentativePath string
	Available          bool
	Error              string
	TotalBytes         uint64
	FreeBytes          uint64
	UsedBytes          uint64
	Claimed            []ClaimedSegment
	UnmanagedBytes     uint64
	OtherBytes         uint64
}

// ClaimedSegment is one service's share of a device's used bytes, sorted
// by service name for deterministic rendering.
type ClaimedSegment struct {
	Service string
	Bytes   uint64
}

type deviceGroup struct {
	device         uint64
	rootLabels     map[string]bool
	representative string
}

// knownDeviceRoots groups the storage roots discovered by the last
// successful file reconciliation by physical device (multiple service
// roots can share one disk). It touches no file-level data, only a stat of
// each already-known root path, so it is cheap enough to call on a short
// interval purely to detect capacity changes.
func (service *Service) knownDeviceRoots() (groups map[uint64]*deviceGroup, order []uint64) {
	service.mu.RLock()
	roots := append([]storageRoot(nil), service.storageRoots...)
	service.mu.RUnlock()

	groups = map[uint64]*deviceGroup{}
	for _, root := range roots {
		info, err := os.Stat(root.Path)
		if err != nil {
			continue
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			continue
		}
		device := uint64(stat.Dev)
		group, exists := groups[device]
		if !exists {
			group = &deviceGroup{device: device, rootLabels: map[string]bool{}, representative: root.Path}
			groups[device] = group
			order = append(order, device)
		}
		if root.Label != "" {
			group.rootLabels[root.Label] = true
		}
	}
	return groups, order
}

// KnownStorageDevicePaths returns the representative root path for each
// currently known physical device. It is the cheap half of StorageDevices,
// safe to call on a short interval (it never touches file-level data).
func (service *Service) KnownStorageDevicePaths() []string {
	groups, order := service.knownDeviceRoots()
	out := make([]string, 0, len(order))
	for _, device := range order {
		out = append(out, groups[device].representative)
	}
	return out
}

// DeviceForPath resolves a path (typically a service's RootPath, entered
// live and not yet reconciled) to a known physical device. If path's device
// number matches an already-known group's, representativePath is that
// group's existing representative — the caller should treat this as "the
// same device," not a new one, so a threshold configured through one
// service's overlay is visible/editable from any other service sharing that
// device. Otherwise isNewDevice is true and path itself is returned, since
// once this service is saved it will become that device's own first known
// root.
func (service *Service) DeviceForPath(path string) (representativePath string, isNewDevice bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", false, fmt.Errorf("stat %s: not a syscall.Stat_t", path)
	}
	groups, _ := service.knownDeviceRoots()
	if group, exists := groups[uint64(stat.Dev)]; exists {
		return group.representative, false, nil
	}
	return path, true, nil
}

// deviceGroups additionally snapshots file-level data for the full
// StorageDevices/MediaByDevice aggregation; unlike knownDeviceRoots it is not
// meant for frequent polling.
func (service *Service) deviceGroups() (groups map[uint64]*deviceGroup, order []uint64, files []model.File, mediaRefs []model.MediaFileRef, torrentRefs []model.TorrentFileRef) {
	groups, order = service.knownDeviceRoots()
	service.mu.RLock()
	files = append([]model.File(nil), service.files...)
	mediaRefs = append([]model.MediaFileRef(nil), service.mediaFileRefs...)
	torrentRefs = append([]model.TorrentFileRef(nil), service.torrentFileRefs...)
	service.mu.RUnlock()
	return groups, order, files, mediaRefs, torrentRefs
}

// StorageDevices reports one entry per physical device, with real filesystem
// usage and Connarr's own attribution of claimed bytes by service.
func (service *Service) StorageDevices() []StorageDevice {
	groups, order, files, mediaRefs, torrentRefs := service.deviceGroups()
	if len(groups) == 0 {
		return nil
	}

	mediaOwner := map[string]string{}
	for _, ref := range mediaRefs {
		if _, claimed := mediaOwner[ref.Path]; !claimed {
			mediaOwner[ref.Path] = ref.ServiceName
		}
	}
	torrentOwner := map[string]string{}
	for _, ref := range torrentRefs {
		if _, claimed := torrentOwner[ref.Path]; !claimed {
			torrentOwner[ref.Path] = ref.ServiceName
		}
	}

	// A hardlinked file has one path per link; its owner must be decided by
	// the physical identity as a whole (media takes priority over torrent),
	// not by whichever of its paths happens to be walked first.
	type identity struct {
		device uint64
		inode  uint64
	}
	type physicalFile struct {
		device uint64
		size   int64
		paths  []string
	}
	physicalFiles := map[identity]*physicalFile{}
	var physicalOrder []identity
	for _, f := range files {
		if !f.Exists || !f.IdentityKnown {
			continue
		}
		if _, tracked := groups[f.Device]; !tracked {
			continue
		}
		id := identity{f.Device, f.Inode}
		entry, exists := physicalFiles[id]
		if !exists {
			entry = &physicalFile{device: f.Device, size: f.SizeBytes}
			physicalFiles[id] = entry
			physicalOrder = append(physicalOrder, id)
		}
		entry.paths = append(entry.paths, f.Path)
	}

	claimedByDevice := make(map[uint64]map[string]uint64, len(groups))
	unmanagedByDevice := make(map[uint64]uint64, len(groups))
	for device := range groups {
		claimedByDevice[device] = map[string]uint64{}
	}
	for _, id := range physicalOrder {
		entry := physicalFiles[id]
		owner := ""
		for _, path := range entry.paths {
			if name := mediaOwner[path]; name != "" {
				owner = name
				break
			}
		}
		if owner == "" {
			for _, path := range entry.paths {
				if name := torrentOwner[path]; name != "" {
					owner = name
					break
				}
			}
		}
		if owner != "" {
			claimedByDevice[entry.device][owner] += uint64(entry.size)
		} else {
			unmanagedByDevice[entry.device] += uint64(entry.size)
		}
	}

	out := make([]StorageDevice, 0, len(order))
	for _, device := range order {
		group := groups[device]
		capabilities := storagecap.Inspect(group.representative)
		result := StorageDevice{
			RootLabels: sortedKeys(group.rootLabels), RepresentativePath: group.representative,
			Available: capabilities.Visible, Error: capabilities.Error, Filesystem: capabilities.Filesystem,
			TotalBytes: capabilities.TotalBytes, FreeBytes: capabilities.FreeBytes, UnmanagedBytes: unmanagedByDevice[device],
		}
		var claimedNames []string
		for name := range claimedByDevice[device] {
			claimedNames = append(claimedNames, name)
		}
		sort.Strings(claimedNames)
		var claimedTotal uint64
		for _, name := range claimedNames {
			bytes := claimedByDevice[device][name]
			result.Claimed = append(result.Claimed, ClaimedSegment{Service: name, Bytes: bytes})
			claimedTotal += bytes
		}
		if result.Available && result.TotalBytes > 0 {
			result.UsedBytes = result.TotalBytes - result.FreeBytes
			accounted := claimedTotal + result.UnmanagedBytes
			if result.UsedBytes > accounted {
				result.OtherBytes = result.UsedBytes - accounted
			}
		}
		out = append(out, result)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RepresentativePath < out[j].RepresentativePath })
	return out
}

// MediaByDevice partitions the given media by the physical device its known
// files resolve to, keyed by that device's representative root path (the
// same Key used by StorageDevices). Media whose device cannot be resolved
// (no known file yet) is omitted; callers that need every item regardless
// should fall back to the full list when no devices are known at all.
func (service *Service) MediaByDevice(items []model.Media) map[string][]model.Media {
	groups, _, files, mediaRefs, _ := service.deviceGroups()
	if len(groups) == 0 {
		return nil
	}
	deviceByPath := make(map[string]uint64, len(files))
	for _, f := range files {
		if f.IdentityKnown {
			deviceByPath[f.Path] = f.Device
		}
	}
	deviceByMediaKey := map[string]uint64{}
	for _, ref := range mediaRefs {
		key := fmt.Sprintf("%s:%s:%d", ref.MediaType, ref.ServiceID, ref.MediaID)
		if _, known := deviceByMediaKey[key]; known {
			continue
		}
		if device, ok := deviceByPath[ref.Path]; ok {
			deviceByMediaKey[key] = device
		}
	}
	out := map[string][]model.Media{}
	for _, item := range items {
		device, ok := deviceByMediaKey[fmt.Sprintf("%s:%s:%d", item.Type, item.ServiceID, item.SourceID)]
		if !ok {
			continue
		}
		group, tracked := groups[device]
		if !tracked {
			continue
		}
		out[group.representative] = append(out[group.representative], item)
	}
	return out
}

// TorrentsByDevice partitions the given torrents by the physical device their
// own known files resolve to, keyed by that device's representative root path
// (the same key used by StorageDevices/MediaByDevice). A torrent's device is
// resolved from its own files, never from any media it may be associated
// with, so an independent copy on a different device than its media lands in
// that device's own list rather than leaking into the media's. A torrent
// whose device cannot be resolved (no known file yet) is omitted.
func (service *Service) TorrentsByDevice(items []model.Torrent) map[string][]model.Torrent {
	groups, _, files, _, torrentRefs := service.deviceGroups()
	if len(groups) == 0 {
		return nil
	}
	deviceByPath := make(map[string]uint64, len(files))
	for _, f := range files {
		if f.IdentityKnown {
			deviceByPath[f.Path] = f.Device
		}
	}
	deviceByHash := map[string]uint64{}
	for _, ref := range torrentRefs {
		hash := strings.ToLower(ref.Hash)
		if _, known := deviceByHash[hash]; known {
			continue
		}
		if device, ok := deviceByPath[ref.Path]; ok {
			deviceByHash[hash] = device
		}
	}
	out := map[string][]model.Torrent{}
	for _, item := range items {
		device, ok := deviceByHash[strings.ToLower(item.Hash)]
		if !ok {
			continue
		}
		group, tracked := groups[device]
		if !tracked {
			continue
		}
		out[group.representative] = append(out[group.representative], item)
	}
	return out
}

// ServiceRootPaths returns, for each service currently contributing a
// storage root, the root path(s) the last file reconciliation
// configured/discovered for it — keyed by the service's display name so
// the Storage/Services pages can show each service's own paths alongside
// its usage/connection status.
func (service *Service) ServiceRootPaths() map[string][]string {
	service.mu.RLock()
	roots := append([]storageRoot(nil), service.storageRoots...)
	service.mu.RUnlock()

	seen := map[string]map[string]bool{}
	for _, root := range roots {
		name := root.Service.Name
		if name == "" {
			continue
		}
		if seen[name] == nil {
			seen[name] = map[string]bool{}
		}
		seen[name][root.Path] = true
	}
	out := make(map[string][]string, len(seen))
	for name, paths := range seen {
		out[name] = sortedKeys(paths)
	}
	return out
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
