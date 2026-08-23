package inventory

import (
	"testing"
	"time"
	"togetharr/internal/model"
)

func TestMediaWithoutFilesCannotClaimCurrentTorrent(t *testing.T) {
	missing := model.Media{Type: model.Movie, SourceID: 1, Title: "Missing", SizeBytes: 0}
	if mediaHasCurrentFiles(missing) {
		t.Fatal("logical media with no files must not claim a torrent as currently associated")
	}
	present := model.Media{Type: model.Movie, SourceID: 2, Title: "Present", SizeBytes: 1}
	if !mediaHasCurrentFiles(present) {
		t.Fatal("media with current file data should be eligible to claim its current torrent")
	}
}

func TestProjectTorrentRelationsIncludesHistoricalTorrent(t *testing.T) {
	media := []model.Media{{Type: model.Movie, SourceID: 10, Title: "Example"}}
	ref := model.MediaRef{Type: model.Movie, SourceID: 10, Title: "Example"}
	torrents := []model.Torrent{
		{Hash: "current", Name: "Current", AssociationStatus: "ASSOCIATED", MediaItems: []model.MediaRef{ref}},
		{Hash: "old", Name: "Old", AssociationStatus: "SUPERSEDED", FormerMediaItems: []model.MediaRef{ref}},
	}
	projectTorrentRelations(media, torrents)
	if len(media[0].Torrents) != 2 {
		t.Fatalf("expected current and historical torrent on media, got %#v", media[0].Torrents)
	}
	if media[0].Torrents[0].Hash != "current" || media[0].Torrents[1].Hash != "old" {
		t.Fatalf("unexpected projected torrents: %#v", media[0].Torrents)
	}
}

func TestPreserveJellyfinFacts(t *testing.T) {
	last := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	previous := []model.Media{{Type: model.Movie, SourceID: 7, Views: 4, UniqueViewers: 2, Favorite: true, LastWatched: &last}}
	fresh := []model.Media{{Type: model.Movie, SourceID: 7, Title: "Fresh title"}, {Type: model.Movie, SourceID: 8}}
	preserveJellyfinFacts(fresh, previous)
	if fresh[0].Views != 4 || fresh[0].UniqueViewers != 2 || !fresh[0].Favorite || fresh[0].LastWatched == nil || !fresh[0].LastWatched.Equal(last) {
		t.Fatalf("Jellyfin facts were not preserved: %+v", fresh[0])
	}
	if fresh[1].Views != 0 || fresh[1].Favorite {
		t.Fatalf("unmatched media inherited Jellyfin facts: %+v", fresh[1])
	}
}
