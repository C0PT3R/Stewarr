package unclaimed

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanFindsOnlyUnclaimedAndAccountsHardlinks(t *testing.T) {
	root := t.TempDir()
	dl := filepath.Join(root, "downloads")
	if err := os.MkdirAll(dl, 0o755); err != nil {
		t.Fatal(err)
	}

	claimed := filepath.Join(dl, "claimed.mkv")
	unique := filepath.Join(dl, "unique.mkv")
	shared := filepath.Join(dl, "shared.mkv")
	external := filepath.Join(root, "library.mkv")
	if err := os.WriteFile(claimed, []byte("claimed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unique, []byte("unique-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared, []byte("shared-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(shared, external); err != nil {
		t.Fatal(err)
	}

	r := Scan(root, []string{dl}, map[string]bool{filepath.Clean(claimed): true})
	if !r.Available || r.Error != "" {
		t.Fatalf("scan unavailable: %+v", r)
	}
	if len(r.Files) != 2 {
		t.Fatalf("got %d files, want 2", len(r.Files))
	}
	if r.ReclaimableBytes != int64(len("unique-data")) {
		t.Fatalf("reclaimable=%d", r.ReclaimableBytes)
	}
	if r.SharedBytes != int64(len("shared-data")) {
		t.Fatalf("shared=%d", r.SharedBytes)
	}
}

func TestScanCountsAllLinksInsideDeletionSetAsReclaimable(t *testing.T) {
	root := t.TempDir()
	dl := filepath.Join(root, "downloads")
	if err := os.MkdirAll(dl, 0o755); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(dl, "a")
	b := filepath.Join(dl, "b")
	if err := os.WriteFile(a, []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(a, b); err != nil {
		t.Fatal(err)
	}
	r := Scan(root, []string{dl}, map[string]bool{})
	if !r.Available {
		t.Fatalf("scan unavailable: %s", r.Error)
	}
	if r.ReclaimableBytes != 4 {
		t.Fatalf("reclaimable=%d want 4", r.ReclaimableBytes)
	}
}
