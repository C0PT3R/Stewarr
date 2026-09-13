package inventory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"stewarr/internal/config"
	"stewarr/internal/model"
	"stewarr/internal/services/qbittorrent"
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

func TestTorrentHistorySamplingMergesTrackerHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/trackers" {
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]any{map[string]any{"status": 2, "msg": ""}})
	}))
	defer srv.Close()

	db, err := store.Open(filepath.Join(t.TempDir(), "stewarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	service := New(config.Config{}, db)
	service.torrents = []model.Torrent{
		{ServiceID: "qb1", Client: "qbittorrent", Hash: "abc", State: "uploading"},
	}
	service.qb = map[string]*qbittorrent.Client{
		"qb1": qbittorrent.New("qBittorrent", srv.URL, "", "", "token"),
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
	if !got[0].TrackerWorking || !got[0].TrackerWorkingKnown {
		t.Fatalf("expected tracker health to be merged into the sample, got %#v", got[0])
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
