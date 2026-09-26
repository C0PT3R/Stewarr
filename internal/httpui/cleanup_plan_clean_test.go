package httpui

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"stewarr/internal/cleanup"
	"stewarr/internal/config"
	"stewarr/internal/inventory"
	"stewarr/internal/model"
	"stewarr/internal/store"
	"stewarr/internal/tasks"
)

// TestActionKeyIsStableAndDistinctPerActionKind guards the identifier
// cleanupPlanProtect (and cleanup.Build's excluded parameter) use to
// re-locate one exact action in a freshly rebuilt plan — it must not
// depend on position in the slice, and different actions must never
// collide. The function itself lives in internal/cleanup (Build needs it
// too, to filter excluded candidates out of its own selection) — this
// guards it via the same httpui-facing name templates use.
func TestActionKeyIsStableAndDistinctPerActionKind(t *testing.T) {
	torrentAction := cleanup.Action{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{Hash: "ABC123", ServiceID: "qb-1"}}}
	if cleanup.ActionKey(torrentAction) != "torrent:abc123:qb-1" {
		t.Fatalf("unexpected torrent key: %q", cleanup.ActionKey(torrentAction))
	}
	// Case-insensitive hash, matching every other hash comparison in this
	// codebase (qBittorrent hashes aren't guaranteed consistent casing).
	if cleanup.ActionKey(torrentAction) != cleanup.ActionKey(cleanup.Action{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{Hash: "abc123", ServiceID: "qb-1"}}}) {
		t.Fatal("expected the torrent key to be case-insensitive on hash")
	}

	media := cleanup.Action{Kind: cleanup.StandaloneMedia, Media: model.Media{Type: model.Movie, ServiceID: "radarr-1", SourceID: 42}}
	season := cleanup.Action{Kind: cleanup.StandaloneSeason, Media: model.Media{Type: model.Series, ServiceID: "sonarr-1", SourceID: 7}, Season: &model.Season{Number: 3}}
	if cleanup.ActionKey(media) == cleanup.ActionKey(season) {
		t.Fatal("expected distinct keys for distinct media actions")
	}
	if cleanup.ActionKey(media) == cleanup.ActionKey(torrentAction) {
		t.Fatal("expected distinct keys across action kinds")
	}
	// Same series, different season: must not collide.
	otherSeason := cleanup.Action{Kind: cleanup.StandaloneSeason, Media: model.Media{Type: model.Series, ServiceID: "sonarr-1", SourceID: 7}, Season: &model.Season{Number: 4}}
	if cleanup.ActionKey(season) == cleanup.ActionKey(otherSeason) {
		t.Fatal("expected distinct keys for different seasons of the same series")
	}
}

func TestExcludedSetFromParsesRepeatedValuesAndIgnoresBlanks(t *testing.T) {
	if got := excludedSetFrom(nil); got != nil {
		t.Fatalf("expected nil for no values, got %#v", got)
	}
	got := excludedSetFrom([]string{"a", "", "b", "a"})
	if len(got) != 2 || !got["a"] || !got["b"] {
		t.Fatalf("unexpected set: %#v", got)
	}
}

// newTestServerWithRemovalTask mirrors newTestInventoryWithReconciledState
// (auto_removal_test.go) but also wires a real, working tasks.Manager with
// the "removal" task registered (a no-op PayloadRunner — this test cares
// about which actions get submitted, not the actual removal execution,
// which is already covered elsewhere) so submitAutoRemoval's
// journal-then-schedule path can actually run instead of panicking on a
// nil server.tasks.
func newTestServerWithRemovalTask(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	manager, err := tasks.NewPersistent(database,
		tasks.Definition{ID: removalTaskID, Name: "Removal", PayloadRunner: func(context.Context, json.RawMessage) error { return nil }, Recovery: tasks.RecoveryAttention},
	)
	if err != nil {
		t.Fatal(err)
	}
	files := []model.File{
		{Path: "/movies/a.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 1, Links: 1, SizeBytes: 100},
	}
	mediaRefs := []model.MediaFileRef{
		{ServiceID: "radarr-1", ServiceName: "Movies", MediaType: model.Movie, MediaID: 1, Source: "radarr", SourceFileID: 10, Path: "/movies/a.mkv"},
	}
	media := []model.Media{{Type: model.Movie, SourceID: 1, Title: "Test Movie", ServiceID: "radarr-1", ServiceName: "Movies"}}
	torrents := []model.Torrent{
		{Hash: "oldhash", Name: "Old Release", AssociationStatus: model.TorrentSuperseded, ServiceID: "qbittorrent-1", Client: "Downloader"},
	}
	if err := database.PublishReconciliation(1, files, mediaRefs, nil, nil, torrents, media); err != nil {
		t.Fatal(err)
	}
	return &Server{tasks: manager, inv: inventory.New(config.Config{}, database)}, database
}

// TestCleanActionsSubmitsEveryGivenAction guards cleanActions' own,
// narrower job now that exclusion happens earlier (inside cleanup.Build,
// before an excluded candidate is ever handed to this function at all —
// see TestBuildExcludedCandidateIsReplacedByTheNextBest in
// internal/cleanup): every action it's given gets submitted, and only
// those.
func TestCleanActionsSubmitsEveryGivenAction(t *testing.T) {
	server, database := newTestServerWithRemovalTask(t)
	torrentAction := cleanup.Action{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{Hash: "oldhash", Name: "Old Release", ServiceID: "qbittorrent-1"}}}

	errs := server.cleanActions([]cleanup.Action{torrentAction}, config.Config{})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %#v", errs)
	}

	events, err := database.HistoryEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 submitted removal, got %#v", events)
	}
	if events[0].RequestedKind != "torrent" || events[0].RequestedKey != "oldhash" {
		t.Fatalf("unexpected submitted event: %#v", events[0])
	}
}
