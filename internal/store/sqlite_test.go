package store

import (
	"path/filepath"
	"testing"
	"time"

	"spartarr/internal/model"
)

func TestStoreRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "spartarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	items := []model.Media{{Type: model.Movie, SourceID: 42, Title: "Warrior", SizeBytes: 1234, Strength: 7.5}}
	if err := db.SaveMedia(items); err != nil {
		t.Fatal(err)
	}
	got, _, err := db.LoadMedia()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SourceID != 42 || got[0].Strength != 7.5 {
		t.Fatalf("unexpected warriors: %#v", got)
	}

	now := time.Now().UTC().Truncate(time.Second)
	events := []ImportEvent{{Source: "radarr", OwnerID: 42, DownloadID: "ABC", ImportedAt: now}}
	if err := db.AddImportEvents(events); err != nil {
		t.Fatal(err)
	}
	if err := db.AddImportEvents(events); err != nil {
		t.Fatal(err)
	}
	gotEvents, err := db.ImportEvents("radarr")
	if err != nil {
		t.Fatal(err)
	}
	if len(gotEvents) != 1 || gotEvents[0].DownloadID != "abc" {
		t.Fatalf("unexpected imports: %#v", gotEvents)
	}
	hashes, err := db.AllImportHashes()
	if err != nil {
		t.Fatal(err)
	}
	if !hashes["abc"] {
		t.Fatal("missing import hash")
	}
}

func TestCleanupStatisticsStartAtZero(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "spartarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stats, err := db.CleanupStatistics()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Runs != 0 || stats.ReclaimedBytes != 0 || stats.MediaRemoved != 0 {
		t.Fatalf("unexpected non-zero cleanup stats: %+v", stats)
	}
}
