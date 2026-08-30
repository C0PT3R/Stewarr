package inventory

import (
	"connarr/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func TestKnownIntegrationRootFallbackOnlyWhenDiscoveryIsEmpty(t *testing.T) {
	i := config.Integration{Type: "radarr", Name: "Movies", RootPath: "/fallback"}
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
func TestWalkRootsFailsClosedOnMissingRoot(t *testing.T) {
	if _, err := walkRoots([]string{filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("expected missing root to fail reconciliation")
	}
}

func TestWalkStorageRootsMergesOverlappingIntegrationContexts(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "movie.mkv")
	if err := os.WriteFile(path, []byte("media"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := walkStorageRoots([]storageRoot{
		{Path: root, Integration: config.Integration{ID: "radarr", Type: "radarr", Name: "Movies"}},
		{Path: root, Integration: config.Integration{ID: "jellyfin", Type: "jellyfin", Name: "Jellyfin"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || len(files[0].StorageContexts) != 2 {
		t.Fatalf("files=%#v", files)
	}
}
