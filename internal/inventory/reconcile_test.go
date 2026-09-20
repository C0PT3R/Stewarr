package inventory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// TestWalkRootsSkipsMissingRootAndContinues guards the "graceful
// degradation" fix: a single unreachable root (a missing/mismatched Docker
// volume mount, almost always) must not abort the whole walk — every other
// configured root's files should still come back normally, so cleanup
// planning for unaffected devices isn't held hostage by one bad path.
func TestWalkRootsSkipsMissingRootAndContinues(t *testing.T) {
	present := t.TempDir()
	if err := os.WriteFile(filepath.Join(present, "movie.mkv"), []byte("media"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	files, err := walkRoots([]string{present, missing})
	if err != nil {
		t.Fatalf("expected the missing root to be skipped rather than failing, got %v", err)
	}
	if len(files) != 1 || files[0].Path != filepath.Join(present, "movie.mkv") {
		t.Fatalf("expected the present root's file despite the missing one, got %#v", files)
	}
}

// TestWalkRootsSkipsNonDirectoryRoot covers a root that exists but is a
// regular file, not a directory — the same "skip, don't abort" treatment
// as a genuinely missing path.
func TestWalkRootsSkipsNonDirectoryRoot(t *testing.T) {
	present := t.TempDir()
	if err := os.WriteFile(filepath.Join(present, "movie.mkv"), []byte("media"), 0o644); err != nil {
		t.Fatal(err)
	}
	notADir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(notADir, []byte("oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := walkRoots([]string{present, notADir})
	if err != nil {
		t.Fatalf("expected the non-directory root to be skipped rather than failing, got %v", err)
	}
	if len(files) != 1 || files[0].Path != filepath.Join(present, "movie.mkv") {
		t.Fatalf("expected the present root's file despite the non-directory one, got %#v", files)
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
// TestReconcileFilesToleratesOneUnreachableRoot guards the end-to-end
// behavior the walkStorageRoots skip-and-continue change exists for: a
// service reporting one root Stewarr can't see (alongside one it can) must
// not stop the whole reconciliation cycle from completing — FileModel
// should still reach "reliable" (unblocking cleanup planning for every
// unaffected device) and the reachable root's files should still be
// published.
func TestReconcileFilesToleratesOneUnreachableRoot(t *testing.T) {
	reachable := t.TempDir()
	if err := os.WriteFile(filepath.Join(reachable, "movie.mkv"), []byte("media"), 0o644); err != nil {
		t.Fatal(err)
	}
	unreachable := filepath.Join(t.TempDir(), "missing")

	radarrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/system/status":
			_, _ = w.Write([]byte(`{}`))
		case "/api/v3/rootfolder":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"path": reachable},
				{"path": unreachable},
			})
		default:
			t.Fatalf("unexpected radarr request: %s", r.URL.Path)
		}
	}))
	defer radarrSrv.Close()

	cfg := config.Config{Services: []config.Service{
		{ID: "r1", Type: "radarr", Name: "Movies", URL: radarrSrv.URL},
	}}
	service := New(cfg, nil)
	if err := service.ReconcileFiles(context.Background()); err != nil {
		t.Fatalf("expected reconciliation to tolerate the unreachable root, got %v", err)
	}
	if got := service.ReliabilitySnapshot().FileModel; got != "reliable" {
		t.Fatalf("FileModel=%q, want reliable", got)
	}
	files, _, _, _, ferr := service.FileSnapshot()
	if ferr != nil {
		t.Fatalf("unexpected file snapshot error: %v", ferr)
	}
	want := filepath.Join(reachable, "movie.mkv")
	found := false
	for _, f := range files {
		if f.Path == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the reachable root's file to be published, got %#v", files)
	}
	capabilities := service.StorageCapabilities()
	unreachableCount := 0
	for _, c := range capabilities {
		if !c.Reachable {
			unreachableCount++
			if c.Path != unreachable {
				t.Fatalf("expected the unreachable root to be reported, got %#v", c)
			}
		}
	}
	if unreachableCount != 1 {
		t.Fatalf("expected exactly 1 unreachable root, got %#v", capabilities)
	}
}

func TestReconcileFilesSucceedsWithNoServicesConfigured(t *testing.T) {
	service := New(config.Config{}, nil)
	if err := service.ReconcileFiles(context.Background()); err != nil {
		t.Fatalf("expected reconciliation with zero services to succeed, got %v", err)
	}
	if got := service.ReliabilitySnapshot().FileModel; got != "reliable" {
		t.Fatalf("FileModel=%q, want reliable", got)
	}
}
