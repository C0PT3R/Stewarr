package torrentstorage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectCountsHardlinksOutsideDeletionSetAsShared(t *testing.T) {
	root := t.TempDir()
	torrent := filepath.Join(root, "torrent")
	if err := os.Mkdir(torrent, 0o755); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(torrent, "a")
	external := filepath.Join(root, "library-a")
	c := filepath.Join(torrent, "c")
	if err := os.WriteFile(a, []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(a, external); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c, []byte("abcdefg"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Inspect(root, torrent)
	if !r.Known || !r.Visible {
		t.Fatalf("expected known visible result: %+v", r)
	}
	if r.Files != 2 || r.SharedFiles != 1 {
		t.Fatalf("unexpected file counts: %+v", r)
	}
	if r.ReclaimableBytes != 7 {
		t.Fatalf("reclaimable=%d want 7", r.ReclaimableBytes)
	}
	if r.SharedBytes != 5 {
		t.Fatalf("shared=%d want 5", r.SharedBytes)
	}
}

func TestInspectReclaimsInodeWhenAllHardlinksAreInsideDeletionSet(t *testing.T) {
	root := t.TempDir()
	torrent := filepath.Join(root, "torrent")
	if err := os.Mkdir(torrent, 0o755); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(torrent, "a")
	b := filepath.Join(torrent, "b")
	if err := os.WriteFile(a, []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(a, b); err != nil {
		t.Fatal(err)
	}
	r := Inspect(root, torrent)
	if r.ReclaimableBytes != 5 || r.SharedBytes != 0 {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestInspectRejectsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	p := filepath.Join(other, "x")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Inspect(root, p)
	if r.Known || r.Error == "" {
		t.Fatalf("expected rejection: %+v", r)
	}
}
