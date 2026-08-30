package inventory

import (
	"os"
	"path/filepath"
	"testing"

	"connarr/internal/model"
)

func TestSeasonScopedHardlinkAttributionAgainstRealFiles(t *testing.T) {
	root := t.TempDir()
	seriesRoot := filepath.Join(root, "series")
	downloadsRoot := filepath.Join(root, "downloads")
	season1Dir := filepath.Join(seriesRoot, "Season 1")
	season2Dir := filepath.Join(seriesRoot, "Season 2")
	if err := os.MkdirAll(season1Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(season2Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(downloadsRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	season1Episode := filepath.Join(season1Dir, "e01.mkv")
	if err := os.WriteFile(season1Episode, []byte("season one episode"), 0o644); err != nil {
		t.Fatal(err)
	}
	season2Episode := filepath.Join(season2Dir, "e01.mkv")
	if err := os.WriteFile(season2Episode, []byte("season two episode!"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The torrent's file is a hardlink to season 1's episode only.
	downloadPath := filepath.Join(downloadsRoot, "release.mkv")
	if err := os.Link(season1Episode, downloadPath); err != nil {
		t.Fatal(err)
	}

	files, err := walkRoots([]string{seriesRoot, downloadsRoot})
	if err != nil {
		t.Fatal(err)
	}

	media := []model.Media{{Type: model.Series, SourceID: 7, Title: "Show"}}
	mediaRefs := []model.MediaFileRef{
		{MediaType: model.Series, MediaID: 7, Source: "sonarr", SourceFileID: 1, Path: season1Episode, Parts: []model.MediaFilePart{{Group: "Season 1", Order: 1*100000 + 1}}},
		{MediaType: model.Series, MediaID: 7, Source: "sonarr", SourceFileID: 2, Path: season2Episode, Parts: []model.MediaFilePart{{Group: "Season 2", Order: 2*100000 + 1}}},
	}
	torrentRefs := []model.TorrentFileRef{{Hash: "abc", Path: downloadPath}}
	torrents := []model.Torrent{{Hash: "abc", AssociationStatus: model.TorrentSuperseded, FormerMediaItems: []model.MediaRef{{Type: model.Series, SourceID: 7}}}}

	applyTorrentMediaHardlinks(torrents, media, files, mediaRefs, torrentRefs)

	if torrents[0].AssociationStatus != model.TorrentCurrent {
		t.Fatalf("expected physical backing to promote the torrent to Current: %#v", torrents[0])
	}
	if len(torrents[0].HardlinkedMediaItems) != 1 {
		t.Fatalf("expected the physically-linked torrent to be proven hardlinked to the series: %#v", torrents[0])
	}
	if len(torrents[0].HardlinkedSeasons) != 1 || torrents[0].HardlinkedSeasons[0] != 1 {
		t.Fatalf("expected the torrent to attribute to season 1 only, got %#v", torrents[0].HardlinkedSeasons)
	}

	attachSeasons(media, mediaRefs, files)
	applySeasonFileEstimates(media, files, mediaRefs)
	applySeasonBundleEstimates(media, torrents, files, mediaRefs, torrentRefs)

	if len(media[0].Seasons) != 2 {
		t.Fatalf("expected two aggregated seasons, got %#v", media[0].Seasons)
	}
	season1, season2 := media[0].Seasons[0], media[0].Seasons[1]
	if season1.ReclaimableBytes != 0 {
		t.Fatalf("expected season 1 alone to report zero reclaimable bytes while the torrent still holds a hardlink: %#v", season1)
	}
	if season1.BundleReclaimableBytes != int64(len("season one episode")) {
		t.Fatalf("expected season 1's bundle estimate to report the full shared size: %#v", season1)
	}
	if season2.ReclaimableBytes != int64(len("season two episode!")) {
		t.Fatalf("expected season 2 (no hardlink) to report its own full size as reclaimable: %#v", season2)
	}
	if season2.BundleReclaimableBytes != season2.ReclaimableBytes {
		t.Fatalf("season 2 must not receive season 1's hardlinked torrent bytes: %#v", season2)
	}
}
