package cleanup

import (
	"testing"

	"stewarr/internal/model"
)

func TestBuildUnavailableStorage(t *testing.T) {
	p, err := Build("/definitely/not/a/stewarr/storage/path", 90, 95, nil, nil, true)
	if err == nil {
		t.Fatal("expected storage probe error")
	}
	if p.Available {
		t.Fatal("unavailable storage must not be marked available")
	}
	if p.Path == "" || p.Error == "" {
		t.Fatalf("expected path and error in unavailable plan: %#v", p)
	}
	if p.SelectedBytes != 0 || len(p.Actions) != 0 {
		t.Fatal("unavailable storage must not produce a cleanup selection")
	}
}

func TestBuildUsesPhysicalReclaimabilityAndSkipsProtectedMedia(t *testing.T) {
	items := []model.Media{
		{Title: "Protected", Protected: true, ReclaimableKnown: true, ReclaimableBytes: 1000, SizeBytes: 1000},
		{Title: "Shared", ReclaimableKnown: true, ReclaimableBytes: 0, SizeBytes: 2000},
		{Title: "Physical", ReclaimableKnown: true, ReclaimableBytes: 7, SizeBytes: 3000},
	}
	p, err := Build(t.TempDir(), 0, 0, items, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 1 || p.Actions[0].Kind != StandaloneMedia || p.Actions[0].Media.Title != "Physical" || p.SelectedBytes != 7 {
		t.Fatalf("cleanup did not use safe physical bytes: %#v", p)
	}
}

func TestBuildPausesWhenValuationIsUnreliable(t *testing.T) {
	p, err := Build(t.TempDir(), 0, 0, []model.Media{{Title: "unsafe", SizeBytes: 1}}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Reliable || len(p.Actions) != 0 || p.SelectedBytes != 0 {
		t.Fatalf("unreliable valuation produced cleanup candidates: %#v", p)
	}
}

func TestBuildUsesTargetWithoutWaitingForCritical(t *testing.T) {
	items := []model.Media{{Title: "candidate", ReclaimableKnown: true, ReclaimableBytes: 1}}
	p, err := Build(t.TempDir(), 0, 99.999, items, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if p.NeedBytes == 0 || len(p.Actions) != 1 {
		t.Fatalf("critical incorrectly gated target planning: %#v", p)
	}
}

func TestBuildLocksHardlinkedMediaAndTorrentIntoOneBundle(t *testing.T) {
	torrent := model.Torrent{Hash: "abc", Name: "Release", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: true, TorrentValue: 5, ReclaimableKnown: true, ReclaimableBytes: 100}
	media := model.Media{Title: "Movie", RetentionValue: 2, BundleReclaimableKnown: true, BundleReclaimableBytes: 900, Torrents: []model.Torrent{torrent}}
	p, err := Build(t.TempDir(), 0, 0, []model.Media{media}, []model.Torrent{torrent}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 1 {
		t.Fatalf("expected exactly one bundled action, got %#v", p.Actions)
	}
	action := p.Actions[0]
	if action.Kind != HardlinkedBundle || action.Media.Title != "Movie" || len(action.Torrents) != 1 || action.Torrents[0].Hash != "abc" {
		t.Fatalf("expected media+torrent bundle, got %#v", action)
	}
	if action.Value != 7 {
		t.Fatalf("expected bundle value to sum retention and swarm value, got %v", action.Value)
	}
	if action.ReclaimableBytes != 900 {
		t.Fatalf("expected bundle to report the combined reclaim estimate, not either side alone: %#v", action)
	}
}

func TestBuildNeverOffersEitherSideOfAHardlinkedBundleAloneWhenMediaIsProtected(t *testing.T) {
	torrent := model.Torrent{Hash: "abc", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: true, TorrentValue: 5, ReclaimableKnown: true, ReclaimableBytes: 100}
	media := model.Media{Title: "Movie", Protected: true, RetentionValue: 2, BundleReclaimableKnown: true, BundleReclaimableBytes: 900, Torrents: []model.Torrent{torrent}}
	p, err := Build(t.TempDir(), 0, 0, []model.Media{media}, []model.Torrent{torrent}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 0 {
		t.Fatalf("protected media must lock out its hardlinked torrent entirely, got %#v", p.Actions)
	}
}

func TestBuildNeverOffersEitherSideOfAHardlinkedBundleAloneWhenBundleEstimateIsUnknown(t *testing.T) {
	torrent := model.Torrent{Hash: "abc", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: true, TorrentValue: 5, ReclaimableKnown: true, ReclaimableBytes: 100}
	media := model.Media{Title: "Movie", RetentionValue: 2, BundleReclaimableKnown: false, Torrents: []model.Torrent{torrent}}
	p, err := Build(t.TempDir(), 0, 0, []model.Media{media}, []model.Torrent{torrent}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 0 {
		t.Fatalf("unknown bundle estimate must lock out the hardlinked torrent entirely, got %#v", p.Actions)
	}
}

func TestBuildExcludesProtectedStandaloneTorrent(t *testing.T) {
	torrent := model.Torrent{Hash: "old", AssociationStatus: model.TorrentSuperseded, Protected: true, TorrentValue: 1, ReclaimableKnown: true, ReclaimableBytes: 100}
	p, err := Build(t.TempDir(), 0, 0, nil, []model.Torrent{torrent}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 0 {
		t.Fatalf("a protected torrent must never appear in the plan, got %#v", p.Actions)
	}
}

func TestBuildBlocksWholeBundleWhenAHardlinkedTorrentIsProtected(t *testing.T) {
	torrent := model.Torrent{Hash: "abc", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: true, Protected: true, TorrentValue: 5, ReclaimableKnown: true, ReclaimableBytes: 100}
	media := model.Media{Title: "Movie", RetentionValue: 2, BundleReclaimableKnown: true, BundleReclaimableBytes: 900, Torrents: []model.Torrent{torrent}}
	p, err := Build(t.TempDir(), 0, 0, []model.Media{media}, []model.Torrent{torrent}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 0 {
		t.Fatalf("a protected hardlinked torrent must veto the whole bundle, got %#v", p.Actions)
	}
}

// TestBuildExcludesMediaAndTorrentsFromNonOptedInServices guards a real
// bug: a standalone Media/Torrent item's own service not having checked
// "Allow automatic removal" must exclude it from the plan entirely — for
// manual review on the Storage page just as much as automatic execution,
// since both read the same Plan.Actions. RemovalRestricted is deliberately
// a separate field from Protected (see model.Media) so this isn't shown as
// a KeepTag/ratio-style "Protected" judgment elsewhere in the UI.
func TestBuildExcludesMediaAndTorrentsFromNonOptedInServices(t *testing.T) {
	media := model.Media{Title: "Restricted Movie", RemovalRestricted: true, ReclaimableKnown: true, ReclaimableBytes: 100, SizeBytes: 100}
	torrent := model.Torrent{Hash: "old", AssociationStatus: model.TorrentSuperseded, RemovalRestricted: true, ReclaimableKnown: true, ReclaimableBytes: 100}
	p, err := Build(t.TempDir(), 0, 0, []model.Media{media}, []model.Torrent{torrent}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 0 {
		t.Fatalf("a removal-restricted media item and torrent must never appear in the plan, got %#v", p.Actions)
	}
}

// TestBuildBlocksWholeBundleWhenEitherSideIsRemovalRestricted guards the
// bidirectional case: a bundle spans a Media service and one or more
// Torrent services, and either side not being opted in must veto the whole
// bundle, the same as either side being Protected already does — offering
// only the opted-in side alone would still delete files the other side
// needs.
func TestBuildBlocksWholeBundleWhenEitherSideIsRemovalRestricted(t *testing.T) {
	torrent := model.Torrent{Hash: "abc", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: true, RemovalRestricted: true, TorrentValue: 5, ReclaimableKnown: true, ReclaimableBytes: 100}
	media := model.Media{Title: "Movie", RetentionValue: 2, BundleReclaimableKnown: true, BundleReclaimableBytes: 900, Torrents: []model.Torrent{torrent}}
	p, err := Build(t.TempDir(), 0, 0, []model.Media{media}, []model.Torrent{torrent}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 0 {
		t.Fatalf("a removal-restricted hardlinked torrent must veto the whole bundle, got %#v", p.Actions)
	}

	restrictedMediaTorrent := model.Torrent{Hash: "def", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: true, TorrentValue: 5, ReclaimableKnown: true, ReclaimableBytes: 100}
	restrictedMedia := model.Media{Title: "Movie 2", RemovalRestricted: true, RetentionValue: 2, BundleReclaimableKnown: true, BundleReclaimableBytes: 900, Torrents: []model.Torrent{restrictedMediaTorrent}}
	p2, err := Build(t.TempDir(), 0, 0, []model.Media{restrictedMedia}, []model.Torrent{restrictedMediaTorrent}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p2.Actions) != 0 {
		t.Fatalf("a removal-restricted media item must veto its own hardlinked bundle too, got %#v", p2.Actions)
	}
}

func TestBuildRanksSeriesPerSeasonInsteadOfWholeSeries(t *testing.T) {
	series := model.Media{
		Type: model.Series, Title: "Show", RetentionValue: 999,
		Seasons: []model.Season{
			{Number: 1, ReclaimableKnown: true, ReclaimableBytes: 100, RetentionValue: 1},
			{Number: 2, ReclaimableKnown: true, ReclaimableBytes: 100, RetentionValue: 2},
		},
	}
	p, err := Build(t.TempDir(), 0, 0, []model.Media{series}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 2 {
		t.Fatalf("expected one action per season, got %#v", p.Actions)
	}
	for _, action := range p.Actions {
		if action.Kind != StandaloneSeason || action.Season == nil {
			t.Fatalf("expected a StandaloneSeason action, never a whole-series action, got %#v", action)
		}
	}
	if p.Actions[0].Season.Number != 1 || p.Actions[1].Season.Number != 2 {
		t.Fatalf("expected seasons ranked ascending by Retention Value, got %#v", p.Actions)
	}
}

// TestBuildEnforcesSeasonOrderEvenWhenValueInverts guards the safety net on
// top of season Retention Value: an earlier season must never be selected
// for removal after a later one from the same show, even if their computed
// values are inverted or tied (e.g. missing/bad air-date data) — a show's
// most recent content is meant to be kept the longest.
func TestBuildEnforcesSeasonOrderEvenWhenValueInverts(t *testing.T) {
	series := model.Media{
		Type: model.Series, Title: "Show",
		Seasons: []model.Season{
			// Season 2 has a *lower* value than season 1 here, on purpose —
			// this must not let season 2 rank ahead of (be removed before)
			// season 1 despite that.
			{Number: 1, ReclaimableKnown: true, ReclaimableBytes: 100, RetentionValue: 50},
			{Number: 2, ReclaimableKnown: true, ReclaimableBytes: 100, RetentionValue: 1},
		},
	}
	p, err := Build(t.TempDir(), 0, 0, []model.Media{series}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 2 {
		t.Fatalf("expected one action per season, got %#v", p.Actions)
	}
	if p.Actions[0].Season.Number != 1 || p.Actions[1].Season.Number != 2 {
		t.Fatalf("expected season 1 ranked before season 2 regardless of Retention Value, got %#v", p.Actions)
	}
}

func TestBuildSkipsProtectedOrUnknownSeasons(t *testing.T) {
	series := model.Media{
		Type: model.Series, Title: "Show",
		Seasons: []model.Season{
			{Number: 1, Protected: true, ReclaimableKnown: true, ReclaimableBytes: 100},
			{Number: 2, ReclaimableKnown: false, ReclaimableBytes: 100},
			{Number: 3, ReclaimableKnown: true, ReclaimableBytes: 0},
			{Number: 4, RemovalRestricted: true, ReclaimableKnown: true, ReclaimableBytes: 100},
		},
	}
	p, err := Build(t.TempDir(), 0, 0, []model.Media{series}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 0 {
		t.Fatalf("expected protected/unknown/zero-byte/removal-restricted seasons to produce no actions, got %#v", p.Actions)
	}
}

func TestBuildLocksHardlinkedSeasonBundle(t *testing.T) {
	torrent := model.Torrent{Hash: "s1", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: true, HardlinkedSeasons: []int{1}, TorrentValue: 5, ReclaimableKnown: true, ReclaimableBytes: 100}
	series := model.Media{
		Type: model.Series, Title: "Show", RetentionValue: 1,
		Torrents: []model.Torrent{torrent},
		Seasons:  []model.Season{{Number: 1, RetentionValue: 2, BundleReclaimableKnown: true, BundleReclaimableBytes: 900}},
	}
	p, err := Build(t.TempDir(), 0, 0, []model.Media{series}, []model.Torrent{torrent}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 1 || p.Actions[0].Kind != HardlinkedBundle || p.Actions[0].Season == nil || p.Actions[0].Season.Number != 1 {
		t.Fatalf("expected one season-scoped hardlinked bundle, got %#v", p.Actions)
	}
	if len(p.Actions[0].Torrents) != 1 || p.Actions[0].Torrents[0].Hash != "s1" {
		t.Fatalf("expected the season-hardlinked torrent bundled in, got %#v", p.Actions[0])
	}
}

func TestBuildMoviesAreUnaffectedBySeasonLogic(t *testing.T) {
	movie := model.Media{Type: model.Movie, Title: "Film", RetentionValue: 1, ReclaimableKnown: true, ReclaimableBytes: 100}
	p, err := Build(t.TempDir(), 0, 0, []model.Media{movie}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 1 || p.Actions[0].Kind != StandaloneMedia {
		t.Fatalf("expected a movie with no Seasons to still produce a whole-media action, got %#v", p.Actions)
	}
}

func TestBuildTreatsProvenNonHardlinkedCurrentTorrentAsIndependent(t *testing.T) {
	torrent := model.Torrent{Hash: "copy", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: false, TorrentValue: 3, ReclaimableKnown: true, ReclaimableBytes: 50}
	media := model.Media{Title: "Movie", RetentionValue: 1, ReclaimableKnown: true, ReclaimableBytes: 200}
	p, err := Build(t.TempDir(), 0, 0, []model.Media{media}, []model.Torrent{torrent}, true)
	if err != nil {
		t.Fatal(err)
	}
	var sawMedia, sawTorrent bool
	for _, action := range p.Actions {
		switch action.Kind {
		case StandaloneMedia:
			sawMedia = true
		case StandaloneTorrent:
			sawTorrent = true
		}
	}
	if !sawMedia || !sawTorrent {
		t.Fatalf("expected independent media and torrent actions, got %#v", p.Actions)
	}
}

func TestBuildTreatsSupersededAndUnassociatedTorrentsAsStandaloneRegardlessOfHardlinkFields(t *testing.T) {
	torrents := []model.Torrent{
		{Hash: "old", AssociationStatus: model.TorrentSuperseded, TorrentValue: 1, ReclaimableKnown: true, ReclaimableBytes: 10},
		{Hash: "orphan", AssociationStatus: model.TorrentUnassociated, TorrentValue: 2, ReclaimableKnown: true, ReclaimableBytes: 10},
	}
	p, err := Build(t.TempDir(), 0, 0, nil, torrents, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 2 || p.Actions[0].Kind != StandaloneTorrent || p.Actions[1].Kind != StandaloneTorrent {
		t.Fatalf("expected both historical torrents as standalone actions, got %#v", p.Actions)
	}
}

func TestBuildExcludesCurrentTorrentWithUnknownHardlinkStatus(t *testing.T) {
	torrent := model.Torrent{Hash: "unknown", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: false, TorrentValue: 1, ReclaimableKnown: true, ReclaimableBytes: 100}
	p, err := Build(t.TempDir(), 0, 0, nil, []model.Torrent{torrent}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 0 {
		t.Fatalf("a Current torrent with unproven hardlink status must never be offered alone: %#v", p.Actions)
	}
}

func TestBuildDrainsTorrentDomainBeforeEverTouchingMedia(t *testing.T) {
	// The torrent has a *higher* Value than the media, but must still be
	// ranked first: domains are never compared numerically, only the tier
	// order decides. Build's NeedBytes depends on the real filesystem behind
	// t.TempDir(), which this test cannot control, so it asserts ranking
	// order (rank's job) rather than how many actions the greedy accumulator
	// ends up keeping (Build's separate, pre-existing, unchanged job).
	torrent := model.Torrent{Hash: "old", AssociationStatus: model.TorrentSuperseded, TorrentValue: 50, ReclaimableKnown: true, ReclaimableBytes: 10}
	media := model.Media{Title: "Movie", RetentionValue: 1, ReclaimableKnown: true, ReclaimableBytes: 10}
	p, err := Build(t.TempDir(), 0, 0, []model.Media{media}, []model.Torrent{torrent}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) == 0 || p.Actions[0].Kind != StandaloneTorrent {
		t.Fatalf("expected the torrent-domain candidate to rank before the media-domain one, got %#v", p.Actions)
	}
	if len(p.Actions) > 1 && p.Actions[1].Kind != StandaloneMedia {
		t.Fatalf("expected the media action to follow the torrent action, got %#v", p.Actions)
	}
}

func TestBuildSortsTorrentTierAscendingByTorrentValue(t *testing.T) {
	torrents := []model.Torrent{
		{Hash: "high", AssociationStatus: model.TorrentUnassociated, TorrentValue: 9, ReclaimableKnown: true, ReclaimableBytes: 1},
		{Hash: "low", AssociationStatus: model.TorrentUnassociated, TorrentValue: 1, ReclaimableKnown: true, ReclaimableBytes: 1},
		{Hash: "mid", AssociationStatus: model.TorrentUnassociated, TorrentValue: 5, ReclaimableKnown: true, ReclaimableBytes: 1},
	}
	p, err := Build(t.TempDir(), 0, 90, nil, torrents, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 3 {
		t.Fatalf("expected all three torrents to be selected, got %#v", p.Actions)
	}
	wantOrder := []string{"low", "mid", "high"}
	for i, hash := range wantOrder {
		if p.Actions[i].Torrents[0].Hash != hash {
			t.Fatalf("expected ascending Swarm Value order %v, got %#v", wantOrder, p.Actions)
		}
	}
}
