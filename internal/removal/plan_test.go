package removal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildHardlinkEffects(t *testing.T) {
	tempDir := t.TempDir()
	primaryPath := filepath.Join(tempDir, "a")
	hardlinkPath := filepath.Join(tempDir, "b")
	if err := os.WriteFile(primaryPath, make([]byte, 1234), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(primaryPath, hardlinkPath); err != nil {
		t.Fatal(err)
	}
	plan := Build(MediaObject, "x", "x", true, []CandidateFile{{Path: primaryPath, Selected: true, Owner: MediaOwner}, {Path: hardlinkPath, Selected: false, Owner: TorrentOwner}})
	if plan.ReclaimableBytes != 0 {
		t.Fatalf("one link reclaimed %d", plan.ReclaimableBytes)
	}
	plan = Build(MediaObject, "x", "x", true, []CandidateFile{{Path: primaryPath, Selected: true, Owner: MediaOwner}, {Path: hardlinkPath, Selected: true, Owner: TorrentOwner}})
	if plan.ReclaimableBytes != 1234 {
		t.Fatalf("both links reclaimed %d", plan.ReclaimableBytes)
	}
}

func TestBuildCountsHardlinkedFileSizeOnce(t *testing.T) {
	tempDir := t.TempDir()
	primaryPath := filepath.Join(tempDir, "a")
	hardlinkPath := filepath.Join(tempDir, "b")
	if err := os.WriteFile(primaryPath, make([]byte, 1234), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(primaryPath, hardlinkPath); err != nil {
		t.Fatal(err)
	}
	plan := Build(UnmanagedObject, "x", "x", true, []CandidateFile{{Path: primaryPath, Selected: true, Owner: UnmanagedOwner}, {Path: hardlinkPath, Selected: true, Owner: UnmanagedOwner}})
	if plan.SelectedPathBytes != 1234 {
		t.Fatalf("selected bytes = %d, want 1234", plan.SelectedPathBytes)
	}
}

func TestBuildDoesNotCountPartiallyRemovedHardlinkedFile(t *testing.T) {
	tempDir := t.TempDir()
	primaryPath := filepath.Join(tempDir, "a")
	hardlinkPath := filepath.Join(tempDir, "b")
	if err := os.WriteFile(primaryPath, make([]byte, 1234), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(primaryPath, hardlinkPath); err != nil {
		t.Fatal(err)
	}
	plan := Build(TorrentObject, "x", "x", true, []CandidateFile{
		{Path: primaryPath, Selected: true, Owner: TorrentOwner},
		{Path: hardlinkPath, Selected: false, Owner: UnmanagedOwner},
	})
	if plan.SelectedPathBytes != 0 {
		t.Fatalf("selected bytes = %d, want 0 while another hardlink survives", plan.SelectedPathBytes)
	}
}

func TestBuildCountsIndependentSelectedFileBesidePartialHardlink(t *testing.T) {
	tempDir := t.TempDir()
	primaryPath := filepath.Join(tempDir, "a")
	hardlinkPath := filepath.Join(tempDir, "b")
	nfo := filepath.Join(tempDir, "x.nfo")
	if err := os.WriteFile(primaryPath, make([]byte, 1234), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(primaryPath, hardlinkPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nfo, make([]byte, 184), 0644); err != nil {
		t.Fatal(err)
	}
	plan := Build(TorrentObject, "x", "x", true, []CandidateFile{
		{Path: primaryPath, Selected: true, Owner: TorrentOwner},
		{Path: hardlinkPath, Selected: false, Owner: UnmanagedOwner},
		{Path: nfo, Selected: true, Owner: TorrentOwner},
	})
	if plan.SelectedPathBytes != 184 {
		t.Fatalf("selected bytes = %d, want 184", plan.SelectedPathBytes)
	}
}

func TestBuildWarnsWhenHardlinkPathsAreMissing(t *testing.T) {
	plan := Build(TorrentObject, "abc", "torrent", true, []CandidateFile{{Path: testTempHardlinkPath(t, true), Owner: TorrentOwner, OwnerKey: "abc", Selected: true}})
	if len(plan.Warnings) == 0 {
		t.Fatalf("expected missing hardlink warning")
	}
}

func testTempHardlinkPath(t *testing.T, keepSibling bool) string {
	t.Helper()
	tempDir := t.TempDir()
	primaryPath := filepath.Join(tempDir, "a")
	hardlinkPath := filepath.Join(tempDir, "b")
	if err := os.WriteFile(primaryPath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(primaryPath, hardlinkPath); err != nil {
		t.Fatal(err)
	}
	if !keepSibling {
		_ = os.Remove(hardlinkPath)
	}
	return primaryPath
}
