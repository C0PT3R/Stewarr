package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTorrentHistorySaveAndRead(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "stewarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	older := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	newer := older.Add(30 * time.Minute)
	samples := []TorrentHistorySample{
		{Client: "qbittorrent", Hash: "abc", SampledAt: older, Ratio: 1.5, SeedsSwarm: 10, LeechersSwarm: 2, UploadedBytes: 1000, DownloadedBytes: 500, State: "uploading", LastActivity: 123},
		{Client: "qbittorrent", Hash: "abc", SampledAt: newer, Ratio: 1.6, SeedsSwarm: 9, LeechersSwarm: 3, UploadedBytes: 1100, DownloadedBytes: 500, State: "stalledUP", LastActivity: 456},
		{Client: "qbittorrent", Hash: "other", SampledAt: newer, Ratio: 0.2, SeedsSwarm: 1, LeechersSwarm: 0, UploadedBytes: 10, DownloadedBytes: 900, State: "downloading", LastActivity: 789},
	}
	if err := db.SaveTorrentHistorySamples(samples); err != nil {
		t.Fatal(err)
	}

	got, err := db.TorrentHistorySamples("qbittorrent", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 samples for abc, got %d", len(got))
	}
	if !got[0].SampledAt.Equal(older) || !got[1].SampledAt.Equal(newer) {
		t.Fatalf("expected samples ordered oldest first, got %#v", got)
	}
	if got[1].Ratio != 1.6 || got[1].SeedsSwarm != 9 || got[1].State != "stalledUP" {
		t.Fatalf("unexpected sample fields: %#v", got[1])
	}

	other, err := db.TorrentHistorySamples("qbittorrent", "other")
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 1 {
		t.Fatalf("expected 1 sample for other, got %d", len(other))
	}
}

func TestTorrentHistoryPruneRemovesOnlyOlderRows(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "stewarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cutoff := time.Now().UTC().Truncate(time.Second)
	older := cutoff.Add(-time.Hour)
	newer := cutoff.Add(time.Hour)
	samples := []TorrentHistorySample{
		{Client: "qbittorrent", Hash: "abc", SampledAt: older, State: "downloading"},
		{Client: "qbittorrent", Hash: "abc", SampledAt: newer, State: "downloading"},
	}
	if err := db.SaveTorrentHistorySamples(samples); err != nil {
		t.Fatal(err)
	}
	if err := db.PruneTorrentHistory(cutoff); err != nil {
		t.Fatal(err)
	}
	got, err := db.TorrentHistorySamples("qbittorrent", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].SampledAt.Equal(newer) {
		t.Fatalf("expected only the newer sample to survive pruning, got %#v", got)
	}
}

func TestSaveTorrentHistorySamplesNoOpOnEmpty(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "stewarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SaveTorrentHistorySamples(nil); err != nil {
		t.Fatal(err)
	}
}
