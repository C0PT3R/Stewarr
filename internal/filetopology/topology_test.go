package filetopology

import (
	"connarr/internal/model"
	"testing"
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

func TestTorrentMediaHardlinkRelationship(t *testing.T) {
	files := []model.File{
		{Path: "/media/a.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 7, Links: 2},
		{Path: "/downloads/a.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 7, Links: 2},
		{Path: "/media/b.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 8, Links: 1},
		{Path: "/downloads/b.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 9, Links: 1},
	}
	mediaRefs := []model.MediaFileRef{
		{MediaType: model.Movie, MediaID: 1, Path: "/media/a.mkv"},
		{MediaType: model.Movie, MediaID: 2, Path: "/media/b.mkv"},
	}
	torrentRefs := []model.TorrentFileRef{
		{Hash: "linked", Path: "/downloads/a.mkv"},
		{Hash: "copied", Path: "/downloads/b.mkv"},
	}
	index := New(files, mediaRefs, torrentRefs)
	if linked, known := index.TorrentMediaHardlink("linked", model.Movie, 1); !known || !linked {
		t.Fatalf("hardlinked relationship = (%v, %v), want (true, true)", linked, known)
	}
	if linked, known := index.TorrentMediaHardlink("copied", model.Movie, 2); !known || linked {
		t.Fatalf("copied relationship = (%v, %v), want (false, true)", linked, known)
	}
	if linked, known := index.TorrentMediaHardlink("missing", model.Movie, 1); known || linked {
		t.Fatalf("missing relationship = (%v, %v), want (false, false)", linked, known)
	}
}

func TestSamePathClaimIsNotAHardlinkRelationship(t *testing.T) {
	files := []model.File{{Path: "/shared/a.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 7, Links: 1}}
	index := New(files,
		[]model.MediaFileRef{{MediaType: model.Movie, MediaID: 1, Path: "/shared/a.mkv"}},
		[]model.TorrentFileRef{{Hash: "same", Path: "/shared/a.mkv"}},
	)
	if linked, known := index.TorrentMediaHardlink("same", model.Movie, 1); !known || linked {
		t.Fatalf("same-path relationship = (%v, %v), want (false, true)", linked, known)
	}
	if matched, known := index.TorrentMediaPhysicalMatch("same", model.Movie, 1); !known || !matched {
		t.Fatalf("same-path physical backing = (%v, %v), want (true, true)", matched, known)
	}
}
