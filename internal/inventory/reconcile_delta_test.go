package inventory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connarr/internal/config"
	"connarr/internal/integrations/radarr"
	"connarr/internal/model"
	"connarr/internal/store"
)

func TestComputeInventoryDeltaDetectsNewChangedAndRemoved(t *testing.T) {
	oldMedia := []model.Media{
		{Type: model.Movie, SourceID: 1, Path: "/movies/a", SizeBytes: 100},
		{Type: model.Movie, SourceID: 2, Path: "/movies/b", SizeBytes: 200},
	}
	newMedia := []model.Media{
		{Type: model.Movie, SourceID: 1, Path: "/movies/a", SizeBytes: 999}, // changed (size settled)
		{Type: model.Movie, SourceID: 3, Path: "/movies/c", SizeBytes: 300}, // new
	}
	oldTorrents := []model.Torrent{
		{Hash: "AAA", SavePath: "/downloads/incomplete/aaa"},
		{Hash: "BBB", SavePath: "/downloads/bbb"},
	}
	newTorrents := []model.Torrent{
		{Hash: "aaa", SavePath: "/downloads/complete/aaa"}, // changed (moved on completion)
		{Hash: "ccc", SavePath: "/downloads/ccc"},          // new
	}
	delta := computeInventoryDelta(oldMedia, newMedia, oldTorrents, newTorrents)
	if len(delta.newOwners) != 1 || delta.newOwners[0].ID != 3 {
		t.Fatalf("newOwners=%#v", delta.newOwners)
	}
	if len(delta.changedOwners) != 1 || delta.changedOwners[0].ID != 1 {
		t.Fatalf("changedOwners=%#v", delta.changedOwners)
	}
	if len(delta.removedOwners) != 1 || delta.removedOwners[0].ID != 2 {
		t.Fatalf("removedOwners=%#v", delta.removedOwners)
	}
	if len(delta.newTorrentHashes) != 1 || delta.newTorrentHashes[0] != "ccc" {
		t.Fatalf("newTorrentHashes=%#v", delta.newTorrentHashes)
	}
	if len(delta.changedTorrentHashes) != 1 || delta.changedTorrentHashes[0] != "aaa" {
		t.Fatalf("changedTorrentHashes=%#v", delta.changedTorrentHashes)
	}
	if len(delta.removedTorrentHashes) != 1 || delta.removedTorrentHashes[0] != "bbb" {
		t.Fatalf("removedTorrentHashes=%#v", delta.removedTorrentHashes)
	}
	if delta.empty() {
		t.Fatal("a real delta must not report empty")
	}
}

func TestComputeInventoryDeltaIsEmptyWhenNothingChanged(t *testing.T) {
	media := []model.Media{{Type: model.Movie, SourceID: 1, Path: "/movies/a", SizeBytes: 100}}
	torrents := []model.Torrent{{Hash: "AAA", SavePath: "/downloads/aaa"}}
	delta := computeInventoryDelta(media, media, torrents, torrents)
	if !delta.empty() {
		t.Fatalf("expected no delta when nothing changed, got %#v", delta)
	}
}

func TestReconcileInventoryDeltaAddsNewMovieWithoutFullScan(t *testing.T) {
	root := t.TempDir()
	moviePath := filepath.Join(root, "New Movie (2026)")
	if err := os.MkdirAll(moviePath, 0o755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(moviePath, "movie.mkv")
	if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	radarrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]radarr.FileRecord{{ID: 55, MovieID: 1, Relative: "movie.mkv", Size: 7}})
	}))
	defer radarrSrv.Close()

	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := New(config.Config{Radarr: config.Service{URL: radarrSrv.URL, APIKey: "key"}}, database)
	service.mu.Lock()
	service.generation = 1
	service.filesUpdated = time.Now()
	service.mu.Unlock()

	oldMedia := []model.Media{}
	newMedia := []model.Media{{Type: model.Movie, SourceID: 1, Path: moviePath}}
	delta := computeInventoryDelta(oldMedia, newMedia, nil, nil)
	if len(delta.newOwners) != 1 {
		t.Fatalf("expected exactly one new owner, got %#v", delta.newOwners)
	}
	result, err := service.reconcileInventoryDelta(context.Background(), delta, oldMedia, newMedia, nil, nil)
	if err != nil {
		t.Fatalf("inline delta reconciliation failed: %v", err)
	}
	if len(result.mediaRefs) != 1 || result.mediaRefs[0].Path != filepath.Clean(filePath) {
		t.Fatalf("expected the new movie's file to be claimed, got mediaRefs=%#v", result.mediaRefs)
	}
	found := false
	for _, f := range result.files {
		if f.Path == filepath.Clean(filePath) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the new movie's directory to have been walked, got files=%#v", result.files)
	}
}

func TestReconcileInventoryDeltaHandlesTorrentSavePathMoveAndRemoval(t *testing.T) {
	root := t.TempDir()
	movedTorrentDir := filepath.Join(root, "complete")
	if err := os.MkdirAll(movedTorrentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	movedFile := filepath.Join(movedTorrentDir, "release.mkv")
	if err := os.WriteFile(movedFile, []byte("release"), 0o644); err != nil {
		t.Fatal(err)
	}
	removedDir := filepath.Join(root, "gone")
	if err := os.MkdirAll(removedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	removedFile := filepath.Join(removedDir, "old.mkv")
	if err := os.WriteFile(removedFile, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	qbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{map[string]any{"index": 0, "name": "release.mkv", "size": 7}})
	}))
	defer qbSrv.Close()

	oldFiles, err := walkRoots([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	oldRefs := []model.TorrentFileRef{
		{Client: "qBittorrent", Hash: "moved", FileIndex: 0, Path: removedFile}, // pre-move location for "moved" hash
		{Client: "qBittorrent", Hash: "gone", FileIndex: 0, Path: removedFile},
	}
	if err := database.PublishReconciliation(1, oldFiles, nil, oldRefs, nil,
		[]model.Torrent{{Hash: "moved", SavePath: removedDir}, {Hash: "gone", SavePath: removedDir}}, nil); err != nil {
		t.Fatal(err)
	}
	service := New(config.Config{QBittorrent: config.QBittorrentService{Name: "qBittorrent", URL: qbSrv.URL, APIKey: "token"}}, database)
	service.mu.Lock()
	service.generation = 2
	service.filesUpdated = time.Now()
	service.files, service.torrentFileRefs = oldFiles, oldRefs
	service.torrents = []model.Torrent{{Hash: "moved", SavePath: removedDir}, {Hash: "gone", SavePath: removedDir}}
	service.mu.Unlock()

	oldTorrents := []model.Torrent{{Hash: "moved", SavePath: removedDir}, {Hash: "gone", SavePath: removedDir}}
	newTorrents := []model.Torrent{{Hash: "moved", SavePath: movedTorrentDir}} // "gone" no longer present at all
	if err := os.Remove(removedFile); err != nil {
		t.Fatal(err)
	}
	delta := computeInventoryDelta(nil, nil, oldTorrents, newTorrents)
	if len(delta.changedTorrentHashes) != 1 || delta.changedTorrentHashes[0] != "moved" {
		t.Fatalf("changedTorrentHashes=%#v", delta.changedTorrentHashes)
	}
	if len(delta.removedTorrentHashes) != 1 || delta.removedTorrentHashes[0] != "gone" {
		t.Fatalf("removedTorrentHashes=%#v", delta.removedTorrentHashes)
	}
	result, err := service.reconcileInventoryDelta(context.Background(), delta, nil, nil, oldTorrents, newTorrents)
	if err != nil {
		t.Fatalf("inline delta reconciliation failed: %v", err)
	}
	if len(result.torrentRefs) != 1 || result.torrentRefs[0].Hash != "moved" || result.torrentRefs[0].Path != filepath.Clean(movedFile) {
		t.Fatalf("expected only the moved torrent's ref under its new path, got %#v", result.torrentRefs)
	}
	for _, f := range result.files {
		if f.Path == filepath.Clean(removedFile) {
			t.Fatalf("removed torrent's deleted file should not linger: %#v", result.files)
		}
	}
}
