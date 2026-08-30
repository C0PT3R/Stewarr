package inventory

import (
	"connarr/internal/model"
	"testing"
	"time"
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

func TestPhysicalBackingPromotesTorrentToCurrent(t *testing.T) {
	media := []model.Media{{Type: model.Movie, SourceID: 7, Title: "Example"}}
	ref := model.MediaRef{Type: model.Movie, SourceID: 7, Title: "Example"}
	torrents := []model.Torrent{{Hash: "linked", AssociationStatus: model.TorrentSuperseded, FormerMediaItems: []model.MediaRef{ref}}}
	files := []model.File{
		{Path: "/media/example.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 9, Links: 2},
		{Path: "/downloads/example.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 9, Links: 2},
	}
	mediaRefs := []model.MediaFileRef{{MediaType: model.Movie, MediaID: 7, Path: "/media/example.mkv"}}
	torrentRefs := []model.TorrentFileRef{{Hash: "linked", Path: "/downloads/example.mkv"}}
	applyTorrentMediaHardlinks(torrents, media, files, mediaRefs, torrentRefs)
	if torrents[0].AssociationStatus != model.TorrentCurrent || !containsMediaRef(torrents[0].MediaItems, ref) {
		t.Fatalf("physical backing did not promote torrent: %#v", torrents[0])
	}
	if containsMediaRef(torrents[0].FormerMediaItems, ref) || !containsMediaRef(torrents[0].HardlinkedMediaItems, ref) {
		t.Fatalf("promoted relationship facts are inconsistent: %#v", torrents[0])
	}
}

func TestCurrentCopiedTorrentRemainsCurrentWithoutHardlink(t *testing.T) {
	media := []model.Media{{Type: model.Movie, SourceID: 7, Title: "Example"}}
	ref := model.MediaRef{Type: model.Movie, SourceID: 7, Title: "Example"}
	torrents := []model.Torrent{{Hash: "copied", AssociationStatus: model.TorrentCurrent, MediaItems: []model.MediaRef{ref}}}
	files := []model.File{
		{Path: "/media/example.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 9, Links: 1},
		{Path: "/downloads/example.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 10, Links: 1},
	}
	applyTorrentMediaHardlinks(torrents, media, files,
		[]model.MediaFileRef{{MediaType: model.Movie, MediaID: 7, Path: "/media/example.mkv"}},
		[]model.TorrentFileRef{{Hash: "copied", Path: "/downloads/example.mkv"}},
	)
	if torrents[0].AssociationStatus != model.TorrentCurrent || !containsMediaRef(torrents[0].MediaItems, ref) || containsMediaRef(torrents[0].HardlinkedMediaItems, ref) {
		t.Fatalf("copied current relationship changed incorrectly: %#v", torrents[0])
	}
}

func TestApplyMediaBundleEstimatesCombinesHardlinkedTorrentBytes(t *testing.T) {
	media := []model.Media{{Type: model.Movie, SourceID: 7, Title: "Example"}}
	ref := model.MediaRef{Type: model.Movie, SourceID: 7, Title: "Example"}
	torrents := []model.Torrent{{Hash: "linked", AssociationStatus: model.TorrentSuperseded, FormerMediaItems: []model.MediaRef{ref}}}
	files := []model.File{
		{Path: "/media/example.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 9, Links: 2, SizeBytes: 1000},
		{Path: "/downloads/example.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 9, Links: 2, SizeBytes: 1000},
	}
	mediaRefs := []model.MediaFileRef{{MediaType: model.Movie, MediaID: 7, Path: "/media/example.mkv"}}
	torrentRefs := []model.TorrentFileRef{{Hash: "linked", Path: "/downloads/example.mkv"}}

	// applyTorrentMediaHardlinks promotes the torrent to Current and publishes
	// HardlinkedMediaItems, exactly as the real reconciliation flow does
	// before either estimate function runs.
	applyTorrentMediaHardlinks(torrents, media, files, mediaRefs, torrentRefs)
	applyMediaFileEstimates(media, files, mediaRefs)
	applyMediaBundleEstimates(media, torrents, files, mediaRefs, torrentRefs)

	if !media[0].ReclaimableKnown || media[0].ReclaimableBytes != 0 {
		t.Fatalf("expected the media alone to report zero reclaimable bytes while the torrent still holds a hardlink: %#v", media[0])
	}
	if !media[0].BundleReclaimableKnown || media[0].BundleReclaimableBytes != 1000 {
		t.Fatalf("expected the bundle estimate to report the full shared size once its hardlinked torrent is included: %#v", media[0])
	}
}

func TestApplyMediaBundleEstimatesIgnoresNonHardlinkedCurrentTorrent(t *testing.T) {
	media := []model.Media{{Type: model.Movie, SourceID: 7, Title: "Example"}}
	ref := model.MediaRef{Type: model.Movie, SourceID: 7, Title: "Example"}
	torrents := []model.Torrent{{Hash: "copied", AssociationStatus: model.TorrentCurrent, MediaItems: []model.MediaRef{ref}}}
	files := []model.File{
		{Path: "/media/example.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 9, Links: 1, SizeBytes: 1000},
		{Path: "/downloads/example.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 10, Links: 1, SizeBytes: 1000},
	}
	mediaRefs := []model.MediaFileRef{{MediaType: model.Movie, MediaID: 7, Path: "/media/example.mkv"}}
	torrentRefs := []model.TorrentFileRef{{Hash: "copied", Path: "/downloads/example.mkv"}}

	applyTorrentMediaHardlinks(torrents, media, files, mediaRefs, torrentRefs)
	applyMediaFileEstimates(media, files, mediaRefs)
	applyMediaBundleEstimates(media, torrents, files, mediaRefs, torrentRefs)

	if media[0].BundleReclaimableBytes != media[0].ReclaimableBytes {
		t.Fatalf("a separate copy must never be folded into the bundle estimate: %#v", media[0])
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

func TestFreshJellyfinFactsReplaceRatherThanAccumulateCachedCounts(t *testing.T) {
	items := []model.Media{{Type: model.Movie, SourceID: 7, Views: 99, UniqueViewers: 9, Favorite: true}}
	fresh := cloneMedia(items)
	clearJellyfinFacts(fresh)
	fresh[0].Views = 3
	fresh[0].UniqueViewers = 1
	mergeJellyfinFacts(items, fresh)
	if items[0].Views != 3 || items[0].UniqueViewers != 1 {
		t.Fatalf("fresh Jellyfin counts accumulated cached values: %+v", items[0])
	}
}

func TestPreserveSeerrFactsUntilFreshEnrichmentSucceeds(t *testing.T) {
	requestedAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	previous := []model.Media{{Type: model.Series, SourceID: 8, Requested: true, RequestedAt: &requestedAt}}
	fresh := []model.Media{{Type: model.Series, SourceID: 8}}
	preserveSeerrFacts(fresh, previous)
	if !fresh[0].Requested || fresh[0].RequestedAt == nil || !fresh[0].RequestedAt.Equal(requestedAt) {
		t.Fatalf("Seerr facts were not preserved: %+v", fresh[0])
	}
}

func TestEnrichmentFingerprintIgnoresStorageAndTorrentChanges(t *testing.T) {
	a := []model.Media{{Type: model.Movie, SourceID: 7, TMDBID: 42, SizeBytes: 100, Path: "/old/movie.mkv"}}
	b := []model.Media{{Type: model.Movie, SourceID: 7, TMDBID: 42, SizeBytes: 0, Path: "/new/location"}}
	if mediaEnrichmentFingerprint(a) != mediaEnrichmentFingerprint(b) {
		t.Fatal("storage-only change incorrectly invalidated media enrichment")
	}
	b = append(b, model.Media{Type: model.Series, SourceID: 8, TVDBID: 99})
	if mediaEnrichmentFingerprint(a) == mediaEnrichmentFingerprint(b) {
		t.Fatal("media-set change did not invalidate enrichment")
	}
}
