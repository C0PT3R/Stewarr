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

func TestPreserveTMDBFacts(t *testing.T) {
	previous := []model.Media{{Type: model.Movie, SourceID: 7, TMDBRating: 8.2, TMDBVoteCount: 500, Popularity: 42.5}}
	fresh := []model.Media{{Type: model.Movie, SourceID: 7, Title: "Fresh title"}, {Type: model.Movie, SourceID: 8}}
	preserveTMDBFacts(fresh, previous)
	if fresh[0].TMDBRating != 8.2 || fresh[0].TMDBVoteCount != 500 || fresh[0].Popularity != 42.5 {
		t.Fatalf("TMDB facts were not preserved: %+v", fresh[0])
	}
	if fresh[1].TMDBRating != 0 || fresh[1].Popularity != 0 {
		t.Fatalf("unmatched media inherited TMDB facts: %+v", fresh[1])
	}
}

// TestPreserveTMDBFactsBackfillsResolvedSeriesIDButNeverOverridesRadarrs
// guards the one asymmetry between TMDBID and the other TMDB facts: a
// series' resolved TMDBID (found via the one-time TVDB->TMDB lookup) must
// survive an unrelated base refresh so that lookup is never repeated, but
// a movie's freshly-fetched TMDBID from Radarr itself — always the
// freshest truth — must never be overridden by a stale cached value.
func TestPreserveTMDBFactsBackfillsResolvedSeriesIDButNeverOverridesRadarrs(t *testing.T) {
	previous := []model.Media{
		{Type: model.Series, SourceID: 8, TMDBID: 555},
		{Type: model.Movie, SourceID: 7, TMDBID: 111},
	}
	fresh := []model.Media{
		{Type: model.Series, SourceID: 8, TVDBID: 99}, // not yet resolved this cycle
		{Type: model.Movie, SourceID: 7, TMDBID: 222}, // Radarr's own, freshly fetched
	}
	preserveTMDBFacts(fresh, previous)
	if fresh[0].TMDBID != 555 {
		t.Fatalf("expected the resolved series TMDBID to be backfilled, got %d", fresh[0].TMDBID)
	}
	if fresh[1].TMDBID != 222 {
		t.Fatalf("expected Radarr's freshly-fetched TMDBID to win, got %d", fresh[1].TMDBID)
	}
}

func TestFreshTMDBFactsReplaceRatherThanAccumulateCachedValues(t *testing.T) {
	items := []model.Media{{Type: model.Movie, SourceID: 7, TMDBRating: 9.9, TMDBVoteCount: 999, Popularity: 999}}
	fresh := cloneMedia(items)
	clearTMDBFacts(fresh)
	fresh[0].TMDBRating = 6.5
	fresh[0].TMDBVoteCount = 40
	fresh[0].Popularity = 12.3
	mergeTMDBFacts(items, fresh)
	if items[0].TMDBRating != 6.5 || items[0].TMDBVoteCount != 40 || items[0].Popularity != 12.3 {
		t.Fatalf("fresh TMDB facts accumulated cached values: %+v", items[0])
	}
}

func TestClearTMDBFactsAlsoResetsEnrichedAt(t *testing.T) {
	items := []model.Media{{Type: model.Movie, SourceID: 7, TMDBRating: 9.9, TMDBEnrichedAt: time.Now()}}
	clearTMDBFacts(items)
	if !items[0].TMDBEnrichedAt.IsZero() {
		t.Fatalf("expected TMDBEnrichedAt to be reset alongside the other facts: %+v", items[0])
	}
}

// TestRefreshTMDBToleratesPerItemFailureWithoutErasingPriorData guards the
// point of RefreshTMDB no longer clearing facts before re-fetching: TMDB
// fetches one item at a time, so a per-item failure this cycle (the item
// simply isn't touched by Apply, unlike a fully cleared-then-refetched
// item) must leave its previous rating/popularity/timestamp exactly as
// they were — not wipe them to zero for one missed fetch — while an item
// that did refresh successfully still gets its new values.
func TestRefreshTMDBToleratesPerItemFailureWithoutErasingPriorData(t *testing.T) {
	previouslyGood := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	items := []model.Media{
		{Type: model.Movie, SourceID: 1, TMDBRating: 7.5, TMDBVoteCount: 500, Popularity: 20, TMDBEnrichedAt: previouslyGood},
		{Type: model.Movie, SourceID: 2, TMDBRating: 6.0, TMDBVoteCount: 100, Popularity: 5, TMDBEnrichedAt: previouslyGood},
	}
	// Simulates RefreshTMDB's "base" after Apply ran without clearing
	// first: item 1's fetch failed this cycle (left untouched, exactly as
	// cloneMedia carried it over); item 2's succeeded.
	base := cloneMedia(items)
	base[1].TMDBRating = 8.0
	base[1].TMDBVoteCount = 900
	base[1].Popularity = 55
	base[1].TMDBEnrichedAt = time.Now()

	mergeTMDBFacts(items, base)
	if items[0].TMDBRating != 7.5 || items[0].Popularity != 20 || !items[0].TMDBEnrichedAt.Equal(previouslyGood) {
		t.Fatalf("a failed per-item fetch must not erase prior good data: %+v", items[0])
	}
	if items[1].TMDBRating != 8.0 || items[1].Popularity != 55 || items[1].TMDBEnrichedAt.Equal(previouslyGood) {
		t.Fatalf("a successful per-item fetch must still update: %+v", items[1])
	}
}

// TestNeedsTMDBEnrichment guards the selection rule EnrichNewTMDBItems
// uses to pick out newly discovered media for an immediate fetch instead
// of waiting for RefreshTMDB's next scheduled pass: never-enriched items
// with an external id are eligible; already-enriched items and items with
// no TMDB/TVDB id to look up at all are not.
func TestNeedsTMDBEnrichment(t *testing.T) {
	if !needsTMDBEnrichment(model.Media{TMDBID: 42}) {
		t.Fatal("a never-enriched movie with a TMDBID should need enrichment")
	}
	if !needsTMDBEnrichment(model.Media{TVDBID: 99}) {
		t.Fatal("a never-enriched series with a TVDBID should need enrichment")
	}
	if needsTMDBEnrichment(model.Media{TMDBID: 42, TMDBEnrichedAt: time.Now()}) {
		t.Fatal("an already-enriched item should not need enrichment again")
	}
	if needsTMDBEnrichment(model.Media{}) {
		t.Fatal("an item with no external id at all has nothing to look up")
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
