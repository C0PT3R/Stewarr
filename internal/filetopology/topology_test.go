package filetopology

import (
	"testing"
	"togetharr/internal/model"
)

func TestHardlinkedMediaAndTorrentRemoval(t *testing.T) {
	files := []model.File{
		{Path: "/media/a.mkv", SizeBytes: 100, Exists: true, IdentityKnown: true, Device: 1, Inode: 7, Links: 2},
		{Path: "/downloads/a.mkv", SizeBytes: 100, Exists: true, IdentityKnown: true, Device: 1, Inode: 7, Links: 2},
	}
	mr := []model.MediaFileRef{{MediaType: model.Movie, MediaID: 1, Path: "/media/a.mkv"}}
	tr := []model.TorrentFileRef{{Hash: "ABC", Path: "/downloads/a.mkv"}}
	x := New(files, mr, tr)
	m := x.Estimate(x.MediaPaths(model.Movie, 1))
	if !m.Known || m.ReclaimableBytes != 0 || m.SharedBytes != 100 {
		t.Fatalf("media estimate=%+v", m)
	}
	q := x.Estimate(x.TorrentPaths("abc"))
	if !q.Known || q.ReclaimableBytes != 0 || q.SharedBytes != 100 {
		t.Fatalf("torrent estimate=%+v", q)
	}
	both := x.Estimate(Union(x.MediaPaths(model.Movie, 1), x.TorrentPaths("abc")))
	if !both.Known || both.ReclaimableBytes != 100 || both.SharedBytes != 0 {
		t.Fatalf("combined=%+v", both)
	}
}

func TestSeparateCopiesAreIndependentlyReclaimable(t *testing.T) {
	files := []model.File{
		{Path: "/media/a.mkv", SizeBytes: 100, Exists: true, IdentityKnown: true, Device: 1, Inode: 7, Links: 1},
		{Path: "/downloads/a.mkv", SizeBytes: 100, Exists: true, IdentityKnown: true, Device: 2, Inode: 8, Links: 1},
	}
	mr := []model.MediaFileRef{{MediaType: model.Movie, MediaID: 1, Path: "/media/a.mkv"}}
	tr := []model.TorrentFileRef{{Hash: "abc", Path: "/downloads/a.mkv"}}
	x := New(files, mr, tr)
	if got := x.Estimate(x.MediaPaths(model.Movie, 1)).ReclaimableBytes; got != 100 {
		t.Fatalf("media=%d", got)
	}
	if got := x.Estimate(x.TorrentPaths("abc")).ReclaimableBytes; got != 100 {
		t.Fatalf("torrent=%d", got)
	}
	if got := x.Estimate(Union(x.MediaPaths(model.Movie, 1), x.TorrentPaths("abc"))).ReclaimableBytes; got != 200 {
		t.Fatalf("both=%d", got)
	}
}
