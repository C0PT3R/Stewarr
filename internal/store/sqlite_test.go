package store

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"togetharr/internal/model"
)

func TestStoreRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "togetharr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	items := []model.Media{{Type: model.Movie, SourceID: 42, Title: "Warrior", SizeBytes: 1234, Value: 7.5}}
	if err := db.SaveMedia(items); err != nil {
		t.Fatal(err)
	}
	got, _, err := db.LoadMedia()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SourceID != 42 || got[0].Value != 7.5 {
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

func TestConcurrentWritesAreSerialized(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "togetharr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for worker := 0; worker < 8; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iteration := 0; iteration < 20; iteration++ {
				id := worker*100 + iteration
				if err := db.SaveMedia([]model.Media{{Type: model.Movie, SourceID: id, Title: fmt.Sprintf("Movie %d", id)}}); err != nil {
					errs <- err
					return
				}
				if err := db.SaveUnclaimedFiles([]model.UnclaimedFile{{Path: fmt.Sprintf("/data/%d.mkv", id)}}); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent write failed: %v", err)
	}
}

func TestCleanupStatisticsStartAtZero(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "togetharr.db"))
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

func TestFileModelRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "togetharr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	files := []model.File{{Path: "/data/Films/A.mkv", SizeBytes: 123, Exists: true, IdentityKnown: true, Device: 7, Inode: 9, Links: 2}}
	mr := []model.MediaFileRef{{MediaType: model.Series, MediaID: 4, Source: "sonarr", SourceFileID: 11, Path: "/data/Films/A.mkv", Parts: []model.MediaFilePart{{Group: "Season 1", Label: "S01E05 · Test", Order: 100005, SourcePartID: 55}}}}
	tr := []model.TorrentFileRef{{Client: "qBittorrent", Hash: "ABC", FileIndex: 0, Path: "/data/downloads/A.mkv"}}
	files = append(files, model.File{Path: "/data/downloads/A.mkv", SizeBytes: 123, Exists: true, IdentityKnown: true, Device: 7, Inode: 9, Links: 2})
	if err := db.ReplaceFiles(files, mr, tr); err != nil {
		t.Fatal(err)
	}
	got, gmr, gtr, updated, err := db.LoadFiles()
	if err != nil {
		t.Fatal(err)
	}
	if updated.IsZero() || len(got) != 2 || len(gmr) != 1 || len(gtr) != 1 {
		t.Fatalf("unexpected file model: files=%#v media=%#v torrent=%#v updated=%v", got, gmr, gtr, updated)
	}
	if gtr[0].Hash != "abc" || got[0].Device != 7 {
		t.Fatalf("unexpected normalized file model: %#v %#v", got, gtr)
	}
	if len(gmr[0].Parts) != 1 || gmr[0].Parts[0].SourcePartID != 55 || gmr[0].Parts[0].Group != "Season 1" {
		t.Fatalf("managed file parts did not round-trip: %#v", gmr[0])
	}
}

func TestRemovalHistoryRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "togetharr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.SaveHistoryEvent(HistoryEvent{EventType: "removal", Status: "dry_run", DryRun: true, RequestedKind: "media", RequestedKey: "movie:42", RequestedLabel: "Movie", ReclaimableBytes: 1234, Payload: []byte(`{"x":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	xs, err := db.HistoryEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(xs) != 1 || xs[0].RequestedKey != "movie:42" || !xs[0].DryRun || xs[0].ReclaimableBytes != 1234 {
		t.Fatalf("unexpected history: %#v", xs)
	}
}
