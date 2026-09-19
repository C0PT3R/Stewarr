package inventory

import (
	"context"
	"path/filepath"
	"testing"

	"stewarr/internal/config"
	"stewarr/internal/model"
	"stewarr/internal/store"
)

func TestTorrentHistorySamplingRecordsCurrentTorrents(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "stewarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	service := New(config.Config{}, db)
	service.torrents = []model.Torrent{
		{Client: "qbittorrent", Hash: "abc", Ratio: 1.5, SeedsSwarm: 3, LeechersSwarm: 1, UploadedBytes: 100, DownloadedBytes: 50, State: "uploading", LastActivity: 42},
	}

	if err := service.TorrentHistorySampling(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, err := db.TorrentHistorySamples("qbittorrent", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 recorded sample, got %d", len(got))
	}
	if got[0].Ratio != 1.5 || got[0].SeedsSwarm != 3 || got[0].State != "uploading" {
		t.Fatalf("unexpected sample: %#v", got[0])
	}
}

func TestTorrentHistorySamplingNoTorrentsIsNotAnError(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "stewarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	service := New(config.Config{}, db)
	if err := service.TorrentHistorySampling(context.Background()); err != nil {
		t.Fatal(err)
	}
}
