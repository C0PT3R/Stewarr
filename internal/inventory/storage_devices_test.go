package inventory

import (
	"os"
	"path/filepath"
	"testing"

	"stewarr/internal/config"
	"stewarr/internal/model"
)

func TestStorageDevicesCollapsesRootsAndAttributesHardlinkedClaims(t *testing.T) {
	root := t.TempDir()
	radarrRoot := filepath.Join(root, "movies")
	qbRoot := filepath.Join(root, "downloads")
	if err := os.MkdirAll(radarrRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(qbRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	managed := filepath.Join(radarrRoot, "movie.mkv")
	if err := os.WriteFile(managed, []byte("managed content"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The torrent client still holds a hardlink to the same physical file;
	// its bytes must be counted once, under the media owner, not the torrent.
	shared := filepath.Join(qbRoot, "movie.mkv")
	if err := os.Link(managed, shared); err != nil {
		t.Fatal(err)
	}
	leftover := filepath.Join(qbRoot, "leftover.iso")
	if err := os.WriteFile(leftover, []byte("leftover data!!"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := walkRoots([]string{radarrRoot, qbRoot})
	if err != nil {
		t.Fatal(err)
	}

	service := New(config.Config{}, nil)
	service.mu.Lock()
	service.files = files
	service.mediaFileRefs = []model.MediaFileRef{{ServiceName: "Movies", MediaType: model.Movie, MediaID: 1, Path: managed}}
	service.torrentFileRefs = []model.TorrentFileRef{
		{ServiceName: "Downloader", Hash: "abc", Path: shared},
	}
	service.storageRoots = []storageRoot{
		{Path: radarrRoot, Service: config.Service{ID: "radarr", Name: "Movies"}, Label: "movies"},
		{Path: qbRoot, Service: config.Service{ID: "qbittorrent", Name: "Downloader"}, Label: "downloads"},
	}
	service.mu.Unlock()

	devices := service.StorageDevices()
	if len(devices) != 1 {
		t.Fatalf("expected roots on one filesystem to collapse into one device, got %d: %#v", len(devices), devices)
	}
	device := devices[0]
	if len(device.RootLabels) != 2 || device.RootLabels[0] != "downloads" || device.RootLabels[1] != "movies" {
		t.Fatalf("expected both root labels sorted, got %#v", device.RootLabels)
	}
	if len(device.Claimed) != 1 || device.Claimed[0].Service != "Movies" || device.Claimed[0].Bytes != uint64(len("managed content")) {
		t.Fatalf("expected the hardlinked file counted once under its media owner, got %#v", device.Claimed)
	}
	if device.UnmanagedBytes != uint64(len("leftover data!!")) {
		t.Fatalf("unmanaged=%d, want %d", device.UnmanagedBytes, len("leftover data!!"))
	}

	byDevice := service.MediaByDevice([]model.Media{{Type: model.Movie, SourceID: 1}, {Type: model.Movie, SourceID: 2}})
	if len(byDevice) != 1 {
		t.Fatalf("expected media partitioned onto the one known device, got %#v", byDevice)
	}
	for _, items := range byDevice {
		if len(items) != 1 || items[0].SourceID != 1 {
			t.Fatalf("expected only the media with a known file, got %#v", items)
		}
	}
}

func TestTorrentsByDeviceResolvesFromItsOwnFilesNotItsMedia(t *testing.T) {
	root := t.TempDir()
	radarrRoot := filepath.Join(root, "movies")
	qbRoot := filepath.Join(root, "downloads")
	if err := os.MkdirAll(radarrRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(qbRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	managed := filepath.Join(radarrRoot, "movie.mkv")
	if err := os.WriteFile(managed, []byte("managed content"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A separate copy, not a hardlink: TorrentsByDevice must resolve this
	// torrent's device from this path alone, never from the media it happens
	// to be Current for.
	copyPath := filepath.Join(qbRoot, "movie-copy.mkv")
	if err := os.WriteFile(copyPath, []byte("independent copy"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := walkRoots([]string{radarrRoot, qbRoot})
	if err != nil {
		t.Fatal(err)
	}

	service := New(config.Config{}, nil)
	service.mu.Lock()
	service.files = files
	service.mediaFileRefs = []model.MediaFileRef{{ServiceName: "Movies", MediaType: model.Movie, MediaID: 1, Path: managed}}
	service.torrentFileRefs = []model.TorrentFileRef{{ServiceName: "Downloader", Hash: "abc", Path: copyPath}}
	service.storageRoots = []storageRoot{
		{Path: radarrRoot, Service: config.Service{ID: "radarr", Name: "Movies"}, Label: "movies"},
		{Path: qbRoot, Service: config.Service{ID: "qbittorrent", Name: "Downloader"}, Label: "downloads"},
	}
	service.mu.Unlock()

	byDevice := service.TorrentsByDevice([]model.Torrent{{Hash: "abc"}, {Hash: "unknown"}})
	if len(byDevice) != 1 {
		t.Fatalf("expected the one torrent with a known file bucketed onto one device, got %#v", byDevice)
	}
	for representative, torrents := range byDevice {
		if len(torrents) != 1 || torrents[0].Hash != "abc" {
			t.Fatalf("expected only the torrent with a known file, got %#v under %q", torrents, representative)
		}
	}
	// This environment cannot fabricate a genuinely separate physical device
	// in a portable test, but the mechanism under test — resolving a
	// torrent's device strictly from its own torrentFileRefs path, via the
	// same group-by-representative-path keying MediaByDevice already uses —
	// is exactly what makes an independent copy on a truly different device
	// land in that device's own plan instead of leaking into its media's.
}

func TestServiceRootPathsGroupsByServiceNameAndDedupes(t *testing.T) {
	service := New(config.Config{}, nil)
	service.mu.Lock()
	service.storageRoots = []storageRoot{
		{Path: "/data/movies", Service: config.Service{ID: "radarr", Name: "Movies"}},
		{Path: "/data/movies-4k", Service: config.Service{ID: "radarr", Name: "Movies"}},
		{Path: "/data/movies", Service: config.Service{ID: "radarr", Name: "Movies"}}, // duplicate, must not double up
		{Path: "/data/downloads", Service: config.Service{ID: "qbittorrent", Name: "Downloader"}},
	}
	service.mu.Unlock()

	roots := service.ServiceRootPaths()
	if got := roots["Movies"]; len(got) != 2 || got[0] != "/data/movies" || got[1] != "/data/movies-4k" {
		t.Fatalf("unexpected Movies roots: %#v", got)
	}
	if got := roots["Downloader"]; len(got) != 1 || got[0] != "/data/downloads" {
		t.Fatalf("unexpected Downloader roots: %#v", got)
	}
	if len(roots) != 2 {
		t.Fatalf("expected exactly 2 services, got %#v", roots)
	}
}

func TestUnreachableServiceRootsReportsOnlyPathsStewarrCannotStat(t *testing.T) {
	present := t.TempDir()
	service := New(config.Config{}, nil)
	service.mu.Lock()
	service.storageRoots = []storageRoot{
		{Path: present, Service: config.Service{ID: "radarr", Name: "Movies", Type: "radarr"}},
		{Path: "/definitely/not/mounted/movies", Service: config.Service{ID: "radarr", Name: "Movies", Type: "radarr"}},
		{Path: "/definitely/not/mounted/downloads", Service: config.Service{ID: "deluge", Name: "Downloader", Type: "deluge"}},
		{Path: "/definitely/not/mounted/downloads", Service: config.Service{ID: "deluge", Name: "Downloader", Type: "deluge"}}, // duplicate, must not double up
		{Path: "/definitely/not/mounted/incomplete", Service: config.Service{ID: "deluge", Name: "Downloader", Type: "deluge"}, Purpose: "incomplete downloads"},
	}
	service.mu.Unlock()

	unreachable := service.UnreachableServiceRoots()
	if len(unreachable) != 3 {
		t.Fatalf("expected exactly 3 unreachable roots (the present one excluded, the duplicate collapsed), got %#v", unreachable)
	}
	if unreachable[0].Path != "/definitely/not/mounted/downloads" || unreachable[0].ServiceName != "Downloader" || unreachable[0].ServiceType != "deluge" || unreachable[0].Purpose != "" {
		t.Fatalf("unexpected first unreachable root: %#v", unreachable[0])
	}
	if unreachable[1].Path != "/definitely/not/mounted/incomplete" || unreachable[1].Purpose != "incomplete downloads" {
		t.Fatalf("expected the second unreachable root to carry its Purpose through: %#v", unreachable[1])
	}
	if unreachable[2].Path != "/definitely/not/mounted/movies" || unreachable[2].ServiceName != "Movies" {
		t.Fatalf("unexpected third unreachable root: %#v", unreachable[2])
	}
}

func TestStorageDevicesEmptyWhenNoRootsKnown(t *testing.T) {
	service := New(config.Config{}, nil)
	if devices := service.StorageDevices(); devices != nil {
		t.Fatalf("expected no devices before any reconciliation, got %#v", devices)
	}
	if paths := service.KnownStorageDevicePaths(); len(paths) != 0 {
		t.Fatalf("expected no known device paths, got %#v", paths)
	}
}
