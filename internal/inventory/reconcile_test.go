package inventory

import (
	"context"
	"os"
	"path/filepath"
	"stewarr/internal/config"
	"testing"
)

// realDiskSize returns a file's real physical/block-allocated size on the
// test filesystem (see physicalSizeBytes) — used by tests that expect a
// reclaimable/claimed byte total to match a specific real file, since a
// tiny test file still occupies at least one full filesystem block and
// block size is environment-dependent, not a value tests should hardcode.
func realDiskSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return physicalSizeBytes(info.Size(), info.Sys())
}

func TestKnownServiceRootFallbackOnlyWhenDiscoveryIsEmpty(t *testing.T) {
	i := config.Service{Type: "radarr", Name: "Movies", RootPath: "/fallback"}
	got := configuredOrDiscoveredRoots(i, []string{"/authoritative"})
	if len(got) != 1 || got[0].Path != "/authoritative" {
		t.Fatalf("fallback overrode discovery: %#v", got)
	}
	got = configuredOrDiscoveredRoots(i, nil)
	if len(got) != 1 || got[0].Path != "/fallback" {
		t.Fatalf("missing configured fallback: %#v", got)
	}
}

func TestCollapseStorageRootsAvoidsDuplicateSubtree(t *testing.T) {
	got := collapse([]string{"/data/Series", "/data", "/data/Series", "/downloads"})
	if len(got) != 2 {
		t.Fatalf("got %v, want two independent roots", got)
	}
}
func TestWalkRootsCreatesOnlyExistingRegularFilesAndPreservesHardlinkIdentity(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a.mkv")
	b := filepath.Join(root, "b.mkv")
	if err := os.WriteFile(a, []byte("media"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(a, b); err != nil {
		t.Fatal(err)
	}
	fs, err := walkRoots([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 2 {
		t.Fatalf("got %d files", len(fs))
	}
	for _, f := range fs {
		if !f.Exists || !f.IdentityKnown {
			t.Fatalf("bad file: %+v", f)
		}
	}
	if fs[0].Device != fs[1].Device || fs[0].Inode != fs[1].Inode {
		t.Fatalf("hardlinks not recognized as same physical identity: %+v %+v", fs[0], fs[1])
	}
}
// TestWalkRootsReportsPhysicalSizeNotApparentSizeForSparseFiles guards the
// bug where reclaimable/unmanaged/library byte totals overstated real disk
// usage: a torrent client can preallocate a download's final size well
// before any data has actually been written, leaving a sparse file whose
// apparent size is far larger than what it actually occupies on disk.
func TestWalkRootsReportsPhysicalSizeNotApparentSizeForSparseFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sparse.mkv")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	const apparentSize = 64 * 1024 * 1024 // 64 MiB, no data actually written
	if err := f.Truncate(apparentSize); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	fs, err := walkRoots([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 {
		t.Fatalf("got %d files, want 1", len(fs))
	}
	if fs[0].SizeBytes >= apparentSize {
		t.Fatalf("expected physical size to be far smaller than the %d-byte apparent size of an unwritten sparse file, got %d", apparentSize, fs[0].SizeBytes)
	}
}

func TestWalkRootsFailsClosedOnMissingRoot(t *testing.T) {
	if _, err := walkRoots([]string{filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("expected missing root to fail reconciliation")
	}
}

func TestWalkStorageRootsMergesOverlappingServiceContexts(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "movie.mkv")
	if err := os.WriteFile(path, []byte("media"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := walkStorageRoots([]storageRoot{
		{Path: root, Service: config.Service{ID: "radarr", Type: "radarr", Name: "Movies"}},
		{Path: root, Service: config.Service{ID: "jellyfin", Type: "jellyfin", Name: "Jellyfin"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || len(files[0].StorageContexts) != 2 {
		t.Fatalf("files=%#v", files)
	}
}

// TestReconcileFilesSucceedsWithNoServicesConfigured guards a fresh
// install: zero storage-owning services configured is a legitimate,
// expected state (nothing to discover yet), not a failure. reconcileFiles
// must publish an empty-but-reliable file topology instead of erroring —
// it used to hard-fail with "no service storage roots are available"
// purely because no services existed yet, well before the user had any
// chance to add one.
func TestReconcileFilesSucceedsWithNoServicesConfigured(t *testing.T) {
	service := New(config.Config{}, nil)
	if err := service.ReconcileFiles(context.Background()); err != nil {
		t.Fatalf("expected reconciliation with zero services to succeed, got %v", err)
	}
	if got := service.ReliabilitySnapshot().FileModel; got != "reliable" {
		t.Fatalf("FileModel=%q, want reliable", got)
	}
}
