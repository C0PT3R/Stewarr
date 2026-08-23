package inventory

import (
	"os"
	"path/filepath"
	"testing"
)

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
