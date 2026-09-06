package httpui

import (
	"path/filepath"
	"testing"

	"connarr/internal/cleanup"
	"connarr/internal/config"
	"connarr/internal/inventory"
	"connarr/internal/model"
	"connarr/internal/removal"
	"connarr/internal/store"
)

func TestActionServicesOptedInRequiresEveryInvolvedService(t *testing.T) {
	serviceByID := map[string]config.Service{
		"radarr-in":      {ID: "radarr-in", AllowAutomaticRemoval: true},
		"radarr-out":     {ID: "radarr-out", AllowAutomaticRemoval: false},
		"qbittorrent-in": {ID: "qbittorrent-in", AllowAutomaticRemoval: true},
	}

	media := cleanup.Action{Kind: cleanup.StandaloneMedia, Media: model.Media{ServiceID: "radarr-in"}}
	if !actionServicesOptedIn(media, serviceByID) {
		t.Fatal("expected opted-in media action to be allowed")
	}

	mediaOut := cleanup.Action{Kind: cleanup.StandaloneMedia, Media: model.Media{ServiceID: "radarr-out"}}
	if actionServicesOptedIn(mediaOut, serviceByID) {
		t.Fatal("expected opted-out media action to be rejected")
	}

	torrent := cleanup.Action{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{ServiceID: "qbittorrent-in"}}}
	if !actionServicesOptedIn(torrent, serviceByID) {
		t.Fatal("expected opted-in torrent action to be allowed")
	}

	bundle := cleanup.Action{Kind: cleanup.HardlinkedBundle, Media: model.Media{ServiceID: "radarr-in"}, Torrents: []model.Torrent{{ServiceID: "qbittorrent-in"}}}
	if !actionServicesOptedIn(bundle, serviceByID) {
		t.Fatal("expected fully opted-in bundle to be allowed")
	}

	mixedBundle := cleanup.Action{Kind: cleanup.HardlinkedBundle, Media: model.Media{ServiceID: "radarr-out"}, Torrents: []model.Torrent{{ServiceID: "qbittorrent-in"}}}
	if actionServicesOptedIn(mixedBundle, serviceByID) {
		t.Fatal("expected a bundle with any opted-out service to be rejected entirely")
	}

	unknownService := cleanup.Action{Kind: cleanup.StandaloneMedia, Media: model.Media{ServiceID: "does-not-exist"}}
	if actionServicesOptedIn(unknownService, serviceByID) {
		t.Fatal("expected an unrecognized service id to be treated as not opted in")
	}
}

func TestRunAutoRemovalEvaluationNoOpsWhenGloballyDisabled(t *testing.T) {
	server := &Server{inv: inventory.New(config.Config{Removal: config.RemovalConfig{AutoEnabled: false}}, nil)}
	if err := server.runAutoRemovalEvaluation(nil); err != nil {
		t.Fatalf("expected the disabled global switch to short-circuit cleanly, got %v", err)
	}
}

func newTestInventoryWithReconciledState(t *testing.T) *inventory.Service {
	t.Helper()
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	files := []model.File{
		{Path: "/movies/a.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 1, Links: 1, SizeBytes: 100},
	}
	mediaRefs := []model.MediaFileRef{
		{ServiceID: "radarr-1", ServiceName: "Movies", MediaType: model.Movie, MediaID: 1, Source: "radarr", SourceFileID: 10, Path: "/movies/a.mkv"},
	}
	media := []model.Media{{Type: model.Movie, SourceID: 1, Title: "Test Movie", ServiceID: "radarr-1"}}
	torrents := []model.Torrent{
		{Hash: "bundlehash", Name: "Bundled Release", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: true, ServiceID: "qbittorrent-1"},
		{Hash: "oldhash", Name: "Old Release", AssociationStatus: model.TorrentSuperseded, ServiceID: "qbittorrent-1"},
	}
	if err := database.PublishReconciliation(1, files, mediaRefs, nil, nil, torrents, media); err != nil {
		t.Fatal(err)
	}
	return inventory.New(config.Config{}, database)
}

func TestFormForActionStandaloneMediaRoundTripsThroughAdmitRemoval(t *testing.T) {
	server := &Server{inv: newTestInventoryWithReconciledState(t)}
	action := cleanup.Action{Kind: cleanup.StandaloneMedia, Media: model.Media{Type: model.Movie, SourceID: 1, Title: "Test Movie"}}
	form, err := server.formForAction(action)
	if err != nil {
		t.Fatal(err)
	}
	if form.Get("kind") != "media" || form.Get("media_type") != "movie" || form.Get("media_id") != "1" || form.Get("managed_file") != "radarr:10" {
		t.Fatalf("unexpected form: %#v", form)
	}
	admission, err := server.admitRemoval(form)
	if err != nil {
		t.Fatalf("constructed form was rejected by admitRemoval: %v", err)
	}
	if admission.Kind != removal.MediaObject || admission.Key != "movie:1:radarr-1" || admission.Label != "Test Movie" {
		t.Fatalf("unexpected admission: %#v", admission)
	}
}

func TestFormForActionHardlinkedBundleIncludesTorrentAndRoundTrips(t *testing.T) {
	server := &Server{inv: newTestInventoryWithReconciledState(t)}
	torrent := model.Torrent{Hash: "bundlehash", Name: "Bundled Release"}
	action := cleanup.Action{Kind: cleanup.HardlinkedBundle, Media: model.Media{Type: model.Movie, SourceID: 1, Title: "Test Movie"}, Torrents: []model.Torrent{torrent}}
	form, err := server.formForAction(action)
	if err != nil {
		t.Fatal(err)
	}
	if form.Get("kind") != "media" || form.Get("torrent") != "bundlehash" || form.Get("managed_file") != "radarr:10" {
		t.Fatalf("unexpected bundle form: %#v", form)
	}
	if _, err := server.admitRemoval(form); err != nil {
		t.Fatalf("constructed bundle form was rejected by admitRemoval: %v", err)
	}
}

func TestFormForActionStandaloneTorrentRoundTripsThroughAdmitRemoval(t *testing.T) {
	server := &Server{inv: newTestInventoryWithReconciledState(t)}
	action := cleanup.Action{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{Hash: "OldHash", Name: "Old Release"}}}
	form, err := server.formForAction(action)
	if err != nil {
		t.Fatal(err)
	}
	if form.Get("kind") != "torrent" || form.Get("hash") != "oldhash" || form.Get("target") != "1" {
		t.Fatalf("unexpected torrent form: %#v", form)
	}
	admission, err := server.admitRemoval(form)
	if err != nil {
		t.Fatalf("constructed form was rejected by admitRemoval: %v", err)
	}
	if admission.Kind != removal.TorrentObject || admission.Key != "oldhash" {
		t.Fatalf("unexpected admission: %#v", admission)
	}
}

func TestFormForActionErrorsWhenMediaHasNoManagedFiles(t *testing.T) {
	server := &Server{inv: newTestInventoryWithReconciledState(t)}
	action := cleanup.Action{Kind: cleanup.StandaloneMedia, Media: model.Media{Type: model.Movie, SourceID: 999, Title: "Missing"}}
	if _, err := server.formForAction(action); err == nil {
		t.Fatal("expected an error for a media action with no managed files")
	}
}
