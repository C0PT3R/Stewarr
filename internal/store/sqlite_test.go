package store

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"connarr/internal/model"
)

func TestStoreRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "connarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	items := []model.Media{{Type: model.Movie, SourceID: 42, Title: "Warrior", SizeBytes: 1234, RetentionValue: 7.5}}
	if err := db.SaveMedia(items); err != nil {
		t.Fatal(err)
	}
	got, _, err := db.LoadMedia()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SourceID != 42 || got[0].RetentionValue != 7.5 {
		t.Fatalf("unexpected media: %#v", got)
	}

	now := time.Now().UTC().Truncate(time.Second)
	events := []ImportEvent{{Source: "radarr", OwnerID: 42, DownloadID: "ABC", ImportedAt: now}}
	if err := db.AddImportEvents(events); err != nil {
		t.Fatal(err)
	}
	if err := db.AddImportEvents(events); err != nil {
		t.Fatal(err)
	}
	gotEvents, err := db.ImportEvents("radarr", "")
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
	db, err := Open(filepath.Join(t.TempDir(), "connarr.db"))
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
				if err := db.SaveUnmanagedFiles([]model.UnmanagedFile{{Path: fmt.Sprintf("/data/%d.mkv", id)}}); err != nil {
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

func TestPublishReconciliationRollsBackWholeGeneration(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "connarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	oldFiles := []model.File{{Path: "/data/old.mkv", Exists: true}}
	oldUnmanaged := []model.UnmanagedFile{{Path: "/data/old.mkv"}}
	oldMedia := []model.Media{{Type: model.Movie, SourceID: 1, Title: "Old"}}
	oldTorrents := []model.Torrent{{Client: "qBittorrent", Hash: "old", Name: "Old"}}
	if err := db.PublishReconciliation(1, oldFiles, nil, nil, oldUnmanaged, oldTorrents, oldMedia); err != nil {
		t.Fatal(err)
	}
	db.beforeCommit = func() error { return fmt.Errorf("injected commit failure") }
	err = db.PublishReconciliation(2,
		[]model.File{{Path: "/data/new.mkv", Exists: true}}, nil, nil,
		[]model.UnmanagedFile{{Path: "/data/new.mkv"}},
		[]model.Torrent{{Client: "qBittorrent", Hash: "new", Name: "New"}},
		[]model.Media{{Type: model.Movie, SourceID: 2, Title: "New"}},
	)
	db.beforeCommit = nil
	if err == nil {
		t.Fatal("expected injected publication failure")
	}
	files, _, _, _, err := db.LoadFiles()
	if err != nil {
		t.Fatal(err)
	}
	unmanaged, _, err := db.LoadUnmanagedFiles()
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := db.LoadMedia()
	if err != nil {
		t.Fatal(err)
	}
	torrents, err := db.LoadTorrents()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "/data/old.mkv" || len(unmanaged) != 1 || unmanaged[0].Path != "/data/old.mkv" || len(media) != 1 || media[0].SourceID != 1 || len(torrents) != 1 || torrents[0].Hash != "old" {
		t.Fatalf("mixed generation survived rollback: files=%#v unmanaged=%#v media=%#v torrents=%#v", files, unmanaged, media, torrents)
	}
}

func TestPublishReconciliationDeltaUpdatesOnlyScopedRows(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "connarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	files := []model.File{{Path: "/data/removed.mkv", Exists: true}, {Path: "/data/preserved.mkv", Exists: true}}
	torrentRefs := []model.TorrentFileRef{{Client: "qBittorrent", Hash: "removed", FileIndex: 0, Path: "/data/removed.mkv"}, {Client: "qBittorrent", Hash: "preserved", FileIndex: 0, Path: "/data/preserved.mkv"}}
	if err := db.PublishReconciliation(1, files, nil, torrentRefs, nil, []model.Torrent{{Client: "qBittorrent", Hash: "removed"}, {Client: "qBittorrent", Hash: "preserved"}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.SetMeta("scope", `{"paths":["/data/removed.mkv"]}`); err != nil {
		t.Fatal(err)
	}
	if err := db.PublishReconciliationDelta(ReconciliationDelta{
		Paths: []string{"/data/removed.mkv"}, RemovedTorrentHashes: []string{"removed"},
		Torrents: []model.Torrent{{Client: "qBittorrent", Hash: "preserved"}}, Generation: 2, ScopeMetadataKey: "scope",
	}); err != nil {
		t.Fatal(err)
	}
	gotFiles, _, gotTorrentRefs, _, err := db.LoadFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(gotFiles) != 1 || gotFiles[0].Path != "/data/preserved.mkv" || len(gotTorrentRefs) != 1 || gotTorrentRefs[0].Hash != "preserved" {
		t.Fatalf("files=%#v torrentRefs=%#v", gotFiles, gotTorrentRefs)
	}
	if scope, err := db.Meta("scope"); err != nil || scope != "" {
		t.Fatalf("scope=%q err=%v", scope, err)
	}
}

func TestPublishReconciliationDeltaRollsBackScopeAndRowsTogether(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "connarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.PublishReconciliation(1, []model.File{{Path: "/data/file.mkv", Exists: true}}, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.SetMeta("scope", "pending"); err != nil {
		t.Fatal(err)
	}
	db.beforeCommit = func() error { return fmt.Errorf("injected delta failure") }
	err = db.PublishReconciliationDelta(ReconciliationDelta{Paths: []string{"/data/file.mkv"}, Generation: 2, ScopeMetadataKey: "scope"})
	db.beforeCommit = nil
	if err == nil {
		t.Fatal("expected delta publication failure")
	}
	files, _, _, _, loadErr := db.LoadFiles()
	if loadErr != nil || len(files) != 1 || files[0].Path != "/data/file.mkv" {
		t.Fatalf("files=%#v err=%v", files, loadErr)
	}
	if scope, metaErr := db.Meta("scope"); metaErr != nil || scope != "pending" {
		t.Fatalf("scope=%q err=%v", scope, metaErr)
	}
}

func TestReadersCannotObserveReplacementTransaction(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "connarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SaveMedia([]model.Media{{Type: model.Movie, SourceID: 1, Title: "Old"}}); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	db.beforeCommit = func() error { close(entered); <-release; return nil }
	writeDone := make(chan error, 1)
	go func() { writeDone <- db.SaveMedia([]model.Media{{Type: model.Movie, SourceID: 2, Title: "New"}}) }()
	<-entered
	type readResult struct {
		items []model.Media
		err   error
	}
	readDone := make(chan readResult, 1)
	go func() { xs, _, e := db.LoadMedia(); readDone <- readResult{xs, e} }()
	select {
	case <-readDone:
		t.Fatal("reader entered an uncommitted replacement transaction")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	db.beforeCommit = nil
	r := <-readDone
	if r.err != nil || len(r.items) != 1 || r.items[0].SourceID != 2 {
		t.Fatalf("reader did not receive committed snapshot: items=%#v err=%v", r.items, r.err)
	}
}

func TestPublishInventoryRollsBackCursorAndSnapshotTogether(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "connarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	oldMedia := []model.Media{{Type: model.Movie, SourceID: 1, Title: "old"}}
	oldTorrents := []model.Torrent{{Hash: "old", Name: "old"}}
	if err := db.PublishInventory(1, map[string]int64{"qb1": 10}, oldTorrents, oldMedia); err != nil {
		t.Fatal(err)
	}

	db.beforeCommit = func() error { return fmt.Errorf("injected commit failure") }
	err = db.PublishInventory(2, map[string]int64{"qb1": 20},
		[]model.Torrent{{Hash: "new", Name: "new"}},
		[]model.Media{{Type: model.Movie, SourceID: 2, Title: "new"}},
	)
	db.beforeCommit = nil
	if err == nil {
		t.Fatal("expected injected failure")
	}

	got, err := db.MetaInt64("qbittorrent.qb1.rid")
	if err != nil {
		t.Fatal(err)
	}
	if got != 10 {
		t.Fatalf("cursor changed after rollback: got %d want 10", got)
	}
	media, _, err := db.LoadMedia()
	if err != nil {
		t.Fatal(err)
	}
	if len(media) != 1 || media[0].Title != "old" {
		t.Fatalf("media snapshot changed after rollback: %#v", media)
	}
	torrents, err := db.LoadTorrents()
	if err != nil {
		t.Fatal(err)
	}
	if len(torrents) != 1 || torrents[0].Hash != "old" {
		t.Fatalf("torrent snapshot changed after rollback: %#v", torrents)
	}
}

func TestPublishEnrichmentRollsBackMediaAndGenerationTogether(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "connarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.PublishEnrichment("jellyfin", 1, []model.Media{{Type: model.Movie, SourceID: 1, Title: "old"}}); err != nil {
		t.Fatal(err)
	}
	db.beforeCommit = func() error { return fmt.Errorf("injected commit failure") }
	if err := db.PublishEnrichment("jellyfin", 2, []model.Media{{Type: model.Movie, SourceID: 2, Title: "new"}}); err == nil {
		t.Fatal("expected injected failure")
	}
	db.beforeCommit = nil
	media, _, err := db.LoadMedia()
	if err != nil {
		t.Fatal(err)
	}
	if len(media) != 1 || media[0].Title != "old" {
		t.Fatalf("enrichment media changed after rollback: %#v", media)
	}
	generation, err := db.Meta("generation.enrichment.jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	if generation != "1" {
		t.Fatalf("enrichment generation changed after rollback: %q", generation)
	}
}

func TestCleanupStatisticsStartAtZero(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "connarr.db"))
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
	db, err := Open(filepath.Join(t.TempDir(), "connarr.db"))
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
	db, err := Open(filepath.Join(t.TempDir(), "connarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	id, err := db.SaveHistoryEvent(HistoryEvent{EventType: "removal", Status: "dry_run", DryRun: true, RequestedKind: "media", RequestedKey: "movie:42", RequestedLabel: "Movie", ReclaimableBytes: 1234, MediaBytes: 5678, Payload: []byte(`{"x":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	xs, err := db.HistoryEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(xs) != 1 || xs[0].RequestedKey != "movie:42" || !xs[0].DryRun || xs[0].ReclaimableBytes != 1234 || xs[0].MediaBytes != 5678 {
		t.Fatalf("unexpected history: %#v", xs)
	}
	byID, err := db.HistoryEventByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if byID.ID != id || byID.RequestedKey != "movie:42" || string(byID.Payload) != `{"x":1}` || byID.MediaBytes != 5678 {
		t.Fatalf("unexpected history lookup: %#v", byID)
	}
	if err := db.UpdateHistoryEvent(HistoryEvent{ID: id, EventType: "removal", Status: "success", RequestedKind: "media", RequestedKey: "movie:42", RequestedLabel: "Movie", ReclaimableBytes: 1000, MediaBytes: 2000}); err != nil {
		t.Fatal(err)
	}
	byID, err = db.HistoryEventByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if byID.MediaBytes != 2000 || byID.ReclaimableBytes != 1000 {
		t.Fatalf("unexpected history after update: %#v", byID)
	}
}

func TestCleanupStatisticsCountsSuccessfulRemovals(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "connarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SaveHistoryEvent(HistoryEvent{EventType: "removal", Status: "success", RequestedKind: "media", ReclaimableBytes: 100, MediaBytes: 150}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveHistoryEvent(HistoryEvent{EventType: "removal", Status: "success", RequestedKind: "torrent", ReclaimableBytes: 50, MediaBytes: 50}); err != nil {
		t.Fatal(err)
	}
	// A dry run and a failed attempt must not be counted as real removals.
	if _, err := db.SaveHistoryEvent(HistoryEvent{EventType: "removal", Status: "dry_run", RequestedKind: "media", ReclaimableBytes: 999, MediaBytes: 999}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveHistoryEvent(HistoryEvent{EventType: "removal", Status: "failed", RequestedKind: "media", ReclaimableBytes: 999, MediaBytes: 999}); err != nil {
		t.Fatal(err)
	}
	stats, err := db.CleanupStatistics()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Runs != 2 || stats.MediaRemoved != 1 || stats.TorrentsRemoved != 1 || stats.MediaBytes != 200 || stats.ReclaimedBytes != 150 {
		t.Fatalf("unexpected cleanup stats: %+v", stats)
	}
	if stats.Last30Runs != 2 || stats.Last30Media != 1 || stats.Last30Bytes != 150 {
		t.Fatalf("unexpected last-30-day cleanup stats: %+v", stats)
	}
}

func TestFileServiceIdentitySurvivesReload(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mr := []model.MediaFileRef{{ServiceID: "radarr-1", ServiceName: "Movies", MediaType: model.Movie, MediaID: 1, Source: "radarr", SourceFileID: 2, Path: "/movies/a.mkv"}}
	tr := []model.TorrentFileRef{{ServiceID: "qb-1", ServiceName: "Downloader", Client: "Downloader", Hash: "abc", FileIndex: 0, Path: "/downloads/a.mkv"}}
	if err := db.ReplaceFiles(nil, mr, tr); err != nil {
		t.Fatal(err)
	}
	_, gotM, gotT, _, err := db.LoadFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(gotM) != 1 || gotM[0].ServiceID != "radarr-1" || gotM[0].ServiceName != "Movies" {
		t.Fatalf("media refs=%#v", gotM)
	}
	if len(gotT) != 1 || gotT[0].ServiceID != "qb-1" || gotT[0].ServiceName != "Downloader" {
		t.Fatalf("torrent refs=%#v", gotT)
	}
}

func TestStartedRemovalCanBeFinalizedOrRecovered(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	id, err := db.SaveHistoryEvent(HistoryEvent{EventType: "removal", Status: "started", RequestedLabel: "A"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateHistoryEvent(HistoryEvent{ID: id, EventType: "removal", Status: "success", RequestedLabel: "A"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveHistoryEvent(HistoryEvent{EventType: "removal", Status: "started", RequestedLabel: "B"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveHistoryEvent(HistoryEvent{EventType: "removal", Status: "queued", RequestedLabel: "C"}); err != nil {
		t.Fatal(err)
	}
	inFlight, err := db.InFlightRemovalHistoryEvents()
	if err != nil {
		t.Fatal(err)
	}
	if len(inFlight) != 2 || inFlight[0].RequestedLabel != "B" || inFlight[1].RequestedLabel != "C" {
		t.Fatalf("in-flight events=%#v", inFlight)
	}
	if err := db.InterruptStartedHistoryEvents(); err != nil {
		t.Fatal(err)
	}
	xs, err := db.HistoryEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(xs) != 3 || xs[0].Status != "queued" || xs[1].Status != "interrupted" || xs[2].Status != "success" {
		t.Fatalf("events=%#v", xs)
	}
}

func TestSessionCreateValidateAndDelete(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "connarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if valid, err := db.SessionValid("unknown-token"); err != nil || valid {
		t.Fatalf("unknown token: valid=%v err=%v", valid, err)
	}
	if err := db.CreateSession("live-token", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if valid, err := db.SessionValid("live-token"); err != nil || !valid {
		t.Fatalf("live token: valid=%v err=%v", valid, err)
	}
	if err := db.DeleteSession("live-token"); err != nil {
		t.Fatal(err)
	}
	if valid, err := db.SessionValid("live-token"); err != nil || valid {
		t.Fatalf("deleted token: valid=%v err=%v", valid, err)
	}
}

// TestExpiredSessionIsRejectedAndPruned guards the actual point of storing
// an expiry: a session past it must stop being accepted, and checking it
// should clean the stale row up rather than leaving it to accumulate.
func TestExpiredSessionIsRejectedAndPruned(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "connarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.CreateSession("expired-token", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if valid, err := db.SessionValid("expired-token"); err != nil || valid {
		t.Fatalf("expired token: valid=%v err=%v", valid, err)
	}
	// SessionValid should have deleted it as a side effect of finding it
	// expired; a second check must not error on a now-missing row.
	if valid, err := db.SessionValid("expired-token"); err != nil || valid {
		t.Fatalf("re-checked expired token: valid=%v err=%v", valid, err)
	}
}

func TestDeleteAllSessionsClearsEveryOne(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "connarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.CreateSession("a", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession("b", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteAllSessions(); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"a", "b"} {
		if valid, err := db.SessionValid(token); err != nil || valid {
			t.Fatalf("token %q: valid=%v err=%v", token, valid, err)
		}
	}
}
