package inventory

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"connarr/internal/config"
	"connarr/internal/model"
	"connarr/internal/store"
)

func TestRefreshTargetedPathsExpandsAndRestatsHardlinkPeers(t *testing.T) {
	root := t.TempDir()
	removed := filepath.Join(root, "download.mkv")
	preserved := filepath.Join(root, "library.mkv")
	if err := os.WriteFile(removed, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(removed, preserved); err != nil {
		t.Fatal(err)
	}
	files, err := walkRoots([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(removed); err != nil {
		t.Fatal(err)
	}
	affected, refreshed, err := refreshTargetedPaths(files, []string{removed})
	if err != nil {
		t.Fatal(err)
	}
	if len(affected) != 2 || !affected[removed] || !affected[preserved] {
		t.Fatalf("affected=%#v", affected)
	}
	if len(refreshed) != 1 || refreshed[0].Path != preserved || refreshed[0].Links != 1 {
		t.Fatalf("refreshed=%#v", refreshed)
	}
}

func TestTargetedTorrentRemovalPublishesWithoutFullScan(t *testing.T) {
	root := t.TempDir()
	removedPath := filepath.Join(root, "removed.mkv")
	preservedPath := filepath.Join(root, "preserved.mkv")
	if err := os.WriteFile(removedPath, []byte("removed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(preservedPath, []byte("preserved"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := walkRoots([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	refs := []model.TorrentFileRef{
		{Client: "qBittorrent", Hash: "removed", FileIndex: 0, Path: removedPath},
		{Client: "qBittorrent", Hash: "preserved", FileIndex: 0, Path: preservedPath},
	}
	if err := database.PublishReconciliation(1, files, nil, refs, nil, []model.Torrent{{Client: "qBittorrent", Hash: "removed", SavePath: root}, {Client: "qBittorrent", Hash: "preserved", SavePath: root}}, nil); err != nil {
		t.Fatal(err)
	}
	service := New(config.Config{}, database)
	service.mu.Lock()
	service.generation = 2
	service.mu.Unlock()
	if err := os.Remove(removedPath); err != nil {
		t.Fatal(err)
	}
	if err := service.QueueReconciliation(ReconciliationScope{Paths: []string{removedPath}, Torrents: []string{"removed"}}); err != nil {
		t.Fatal(err)
	}
	if err := service.reconcileTargeted(context.Background()); err != nil {
		t.Fatal(err)
	}
	gotFiles, _, gotRefs, _, err := database.LoadFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(gotFiles) != 1 || gotFiles[0].Path != preservedPath || len(gotRefs) != 1 || gotRefs[0].Hash != "preserved" {
		t.Fatalf("files=%#v refs=%#v", gotFiles, gotRefs)
	}
	if len(service.TorrentSnapshot()) != 1 || service.TorrentSnapshot()[0].Hash != "preserved" {
		t.Fatalf("torrents=%#v", service.TorrentSnapshot())
	}
	if scope, err := service.reconciliationScope(); err != nil || len(scope.Paths) != 0 {
		t.Fatalf("scope=%#v err=%v", scope, err)
	}
}

func TestMediaRefChangesOutsideScopeRequirePromotion(t *testing.T) {
	oldRefs := []model.MediaFileRef{{MediaType: model.Series, MediaID: 7, Path: "/series/old.mkv"}}
	newRefs := []model.MediaFileRef{{MediaType: model.Series, MediaID: 7, Path: "/series/unexpected.mkv"}}
	owners := map[string]bool{"series:7": true}
	if mediaRefChangesWithinScope(oldRefs, newRefs, owners, map[string]bool{"/series/old.mkv": true}) {
		t.Fatal("unexpected new owner path remained inside targeted scope")
	}
}
