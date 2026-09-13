package valuation

import (
	"stewarr/internal/config"
	"stewarr/internal/model"
	"testing"
	"time"
)

func testConfig() config.Config {
	var c config.Config
	c.Valuation.Weights.Rating = 40
	c.Valuation.Weights.LastWatchedAge = 15
	c.Valuation.Weights.LibraryAge = 10
	c.Valuation.Weights.LowPopularity = 10
	c.Valuation.Weights.OldRequest = 12
	c.Valuation.RequestValueBonus = 100
	c.Valuation.FavoriteValueBonus = 1000
	c.Valuation.KeepTagValueBonus = 10000
	c.Protection.Favorite = true
	c.RequestGrace = 365 * 24 * time.Hour
	c.Protection.KeepTags = []string{"keep"}
	return c
}
func TestLowestValueComesFirst(t *testing.T) {
	c := testConfig()
	items := []model.Media{{Title: "Strong", Rating: 8.5}, {Title: "Weak", Rating: 5.5}, {Title: "Weakest", Rating: 3.5}}
	ApplyMedia(items, c)
	if items[0].Title != "Weakest" || items[1].Title != "Weak" || items[2].Title != "Strong" {
		t.Fatalf("unexpected order: %s, %s, %s", items[0].Title, items[1].Title, items[2].Title)
	}
}

// TestTMDBRatingTakesPrecedenceOverRadarrSonarrOwnRating guards the
// override rule: once TMDB enrichment has a match (TMDBVoteCount>0), its
// rating/vote numbers are what gets scored, not Radarr/Sonarr's own.
func TestTMDBRatingTakesPrecedenceOverRadarrSonarrOwnRating(t *testing.T) {
	c := testConfig()
	items := []model.Media{{Title: "Enriched", Rating: 1.0, VoteCount: 5, TMDBRating: 9.0, TMDBVoteCount: 500}}
	ApplyMedia(items, c)
	foundRating := false
	for _, reason := range items[0].RetentionValueReasons {
		if reason.Label == "Rating" {
			foundRating = true
			if reason.Value != "9.0/10" {
				t.Fatalf("expected the TMDB rating to be scored, got %q", reason.Value)
			}
		}
	}
	if !foundRating {
		t.Fatal("expected a Rating reason")
	}
}

// TestEffectiveRatingFallsBackWithoutTMDBEnrichment guards the other half
// of the same rule: an item TMDB has never enriched (TMDBVoteCount==0)
// must keep using Radarr/Sonarr's own Rating/VoteCount — TMDB being
// unconfigured, unreachable, or unmatched must never remove the signal
// Radarr/Sonarr already provided.
func TestEffectiveRatingFallsBackWithoutTMDBEnrichment(t *testing.T) {
	rating, votes := effectiveRating(model.Media{Rating: 6.5, VoteCount: 300})
	if rating != 6.5 || votes != 300 {
		t.Fatalf("expected the Radarr/Sonarr rating to be used, got rating=%v votes=%v", rating, votes)
	}
}

// TestPopularityHasNoFallbackAndIsUnknownRatherThanZeroWhenAbsent guards
// Popularity's distinct treatment from Rating: it has no source to fall
// back to (Radarr/Sonarr don't supply an equivalent), so an unenriched
// item simply contributes nothing from it rather than being scored as
// "unpopular."
func TestPopularityHasNoFallbackAndIsUnknownRatherThanZeroWhenAbsent(t *testing.T) {
	c := testConfig()
	c.Valuation.Weights.Popularity = 20
	unenriched := []model.Media{{Title: "Unenriched"}}
	ApplyMedia(unenriched, c)
	for _, reason := range unenriched[0].RetentionValueReasons {
		if reason.Label == "Popularity" {
			t.Fatalf("expected no Popularity reason for an unenriched item, got %#v", reason)
		}
	}
	enriched := []model.Media{{Title: "Enriched", Popularity: 500}}
	ApplyMedia(enriched, c)
	found := false
	for _, reason := range enriched[0].RetentionValueReasons {
		if reason.Label == "Popularity" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a Popularity reason once TMDB enrichment has a value")
	}
	if enriched[0].RetentionValue <= unenriched[0].RetentionValue {
		t.Fatalf("expected the enriched, more popular item to score higher: enriched=%v unenriched=%v", enriched[0].RetentionValue, unenriched[0].RetentionValue)
	}
}

func TestKeepTagCreatesAbsoluteProtection(t *testing.T) {
	c := testConfig()
	items := []model.Media{{Title: "Tagged", Rating: 1, Tags: []string{"keep"}}, {Title: "Ordinary", Rating: 9}}
	ApplyMedia(items, c)
	if items[0].Title != "Ordinary" || items[1].Title != "Tagged" {
		t.Fatalf("value ordering failed: %#v", items)
	}
	if !items[1].Protected || items[1].ProtectionReason != "Keep tag" {
		t.Fatalf("keep-tagged media must be absolutely protected: %#v", items[1])
	}
}

func TestNeverWatchedWeightIsApplied(t *testing.T) {
	c := testConfig()
	c.Valuation.Weights.NeverWatched = 25
	items := []model.Media{{Title: "Unwatched"}}
	ApplyMedia(items, c)
	if items[0].RetentionValue != 25 || len(items[0].RetentionValueReasons) == 0 || items[0].RetentionValueReasons[0].Points != 25 {
		t.Fatalf("never-watched weight was ignored: %#v", items[0])
	}
}
func TestSizeBreaksEqualValueTie(t *testing.T) {
	c := testConfig()
	items := []model.Media{{Title: "Small", Rating: 5, SizeBytes: 10}, {Title: "Large", Rating: 5, SizeBytes: 100}}
	ApplyMedia(items, c)
	if items[0].Title != "Large" {
		t.Fatalf("larger equal-value media should fall first: %#v", items)
	}
}

func TestHistoricalTorrentsDoNotAddMediaValue(t *testing.T) {
	c := testConfig()
	c.Valuation.Weights.TorrentActivity = 10
	items := []model.Media{{
		Title: "Test",
		Torrents: []model.Torrent{
			{Hash: "current", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: true, LeechersSwarm: 1},
			{Hash: "old", AssociationStatus: "SUPERSEDED", LeechersSwarm: 1023, UploadSpeed: 1024 * 1024 * 1024},
		},
	}}
	ApplyMedia(items, c)
	// Only the single current leecher should count: 10 * log2(2) = 10.
	if items[0].RetentionValue != 10 {
		t.Fatalf("historical torrent activity must not inflate media value: got %.2f", items[0].RetentionValue)
	}
}

func TestCurrentTorrentMustBeHardlinkedToAddMediaValue(t *testing.T) {
	c := testConfig()
	c.Valuation.Weights.TorrentActivity = 10
	items := []model.Media{{
		Title: "Test",
		Torrents: []model.Torrent{
			{Hash: "copy", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: false, LeechersSwarm: 1023},
			{Hash: "unknown", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: false, LeechersSwarm: 1023},
		},
	}}
	ApplyMedia(items, c)
	if items[0].RetentionValue != 0 {
		t.Fatalf("non-hardlinked activity must not add media value: got %.2f", items[0].RetentionValue)
	}
	for _, reason := range items[0].RetentionValueReasons {
		if reason.Label == "Associated torrent" {
			t.Fatalf("non-hardlinked torrent produced a contribution: %#v", reason)
		}
	}
}

func TestHardlinkedTorrentContributionExplainsWhy(t *testing.T) {
	c := testConfig()
	c.Valuation.Weights.TorrentActivity = 10
	items := []model.Media{{Title: "Test", Torrents: []model.Torrent{{AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: true, LeechersSwarm: 1}}}}
	ApplyMedia(items, c)
	found := false
	for _, reason := range items[0].RetentionValueReasons {
		if reason.Label == "Associated torrent" && reason.Note != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing hardlink-gated explanation: %#v", items[0].RetentionValueReasons)
	}
}

// TestApplyMediaSetsRemovalRestrictedFromServiceOptIn guards the fix for
// series (and any other media) appearing as removal candidates even though
// their owning service hasn't checked "Allow automatic removal": an
// unrecognized or opted-out ServiceID must fail closed to restricted, an
// opted-in one must not, and a season must inherit its parent series' value
// exactly like Protected already does.
func TestApplyMediaSetsRemovalRestrictedFromServiceOptIn(t *testing.T) {
	c := testConfig()
	c.Services = []config.Service{
		{ID: "radarr-in", AllowAutomaticRemoval: true},
		{ID: "radarr-out", AllowAutomaticRemoval: false},
	}
	items := []model.Media{
		{Title: "Opted in", ServiceID: "radarr-in"},
		{Title: "Opted out", ServiceID: "radarr-out"},
		{Title: "Unknown service", ServiceID: "does-not-exist"},
		{Title: "Series", Type: model.Series, ServiceID: "radarr-out", Seasons: []model.Season{{Number: 1}}},
	}
	ApplyMedia(items, c)
	if items[0].RemovalRestricted {
		t.Fatalf("expected an opted-in service's media to not be removal-restricted: %#v", items[0])
	}
	if !items[1].RemovalRestricted {
		t.Fatalf("expected an opted-out service's media to be removal-restricted: %#v", items[1])
	}
	if !items[2].RemovalRestricted {
		t.Fatalf("expected an unrecognized service id to fail closed to removal-restricted: %#v", items[2])
	}
	if !items[3].RemovalRestricted || !items[3].Seasons[0].RemovalRestricted {
		t.Fatalf("expected a season to inherit its series' RemovalRestricted: %#v", items[3])
	}
}

func TestApplyTorrentValueSetsRemovalRestrictedFromServiceOptIn(t *testing.T) {
	c := testConfig()
	c.Services = []config.Service{{ID: "qbittorrent-in", AllowAutomaticRemoval: true}}
	torrents := []model.Torrent{
		{Hash: "in", ServiceID: "qbittorrent-in"},
		{Hash: "out", ServiceID: "does-not-exist"},
	}
	ApplyTorrentValue(torrents, c, nil)
	if torrents[0].RemovalRestricted {
		t.Fatalf("expected an opted-in service's torrent to not be removal-restricted: %#v", torrents[0])
	}
	if !torrents[1].RemovalRestricted {
		t.Fatalf("expected an unrecognized service id to fail closed to removal-restricted: %#v", torrents[1])
	}
}

func TestApplyMediaSeasonsInheritSeriesWideProtection(t *testing.T) {
	c := testConfig()
	items := []model.Media{{
		Type: model.Series, Title: "Show", Tags: []string{"keep"},
		Seasons: []model.Season{{Number: 1}, {Number: 2}},
	}}
	ApplyMedia(items, c)
	if !items[0].Protected {
		t.Fatalf("expected series to be protected via keep tag: %#v", items[0])
	}
	for _, season := range items[0].Seasons {
		if !season.Protected || season.ProtectionReason != items[0].ProtectionReason {
			t.Fatalf("expected every season to inherit series-wide protection: %#v", season)
		}
	}
}

func TestApplyMediaSeasonsDifferByRecency(t *testing.T) {
	c := testConfig()
	c.Valuation.Weights.SeasonRecency = 20
	now := time.Now()
	items := []model.Media{{
		Type: model.Series, Title: "Show",
		Seasons: []model.Season{
			{Number: 1, LastAiredAt: now.AddDate(-3, 0, 0)},
			{Number: 2, LastAiredAt: now},
		},
	}}
	ApplyMedia(items, c)
	old, recent := items[0].Seasons[0], items[0].Seasons[1]
	if recent.RetentionValue <= old.RetentionValue {
		t.Fatalf("expected the more recently aired season to have higher Retention Value: old=%#v recent=%#v", old, recent)
	}
}

func TestApplyMediaSeasonsScopeTorrentActivityToTheirOwnSeason(t *testing.T) {
	c := testConfig()
	c.Valuation.Weights.TorrentActivity = 10
	items := []model.Media{{
		Type: model.Series, Title: "Show",
		Torrents: []model.Torrent{{Hash: "s1", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: true, HardlinkedSeasons: []int{1}, LeechersSwarm: 100}},
		Seasons:  []model.Season{{Number: 1}, {Number: 2}},
	}}
	ApplyMedia(items, c)
	season1, season2 := items[0].Seasons[0], items[0].Seasons[1]
	if season1.RetentionValue <= season2.RetentionValue {
		t.Fatalf("expected season 1 alone to receive the hardlinked torrent's activity contribution: season1=%#v season2=%#v", season1, season2)
	}
	foundReason := false
	for _, r := range season1.RetentionValueReasons {
		if r.Label == "Associated torrent" {
			foundReason = true
		}
	}
	if !foundReason {
		t.Fatalf("expected season 1 to explain the torrent contribution: %#v", season1.RetentionValueReasons)
	}
	for _, r := range season2.RetentionValueReasons {
		if r.Label == "Associated torrent" {
			t.Fatalf("season 2 must not receive a season-1-only torrent's contribution: %#v", season2.RetentionValueReasons)
		}
	}
}

func TestApplyTorrentValueProtectsBelowMinimumRatio(t *testing.T) {
	c := testConfig()
	c.Protection.MinTorrentRatio = 1.0
	items := []model.Torrent{{Name: "low", Ratio: 0.5}, {Name: "high", Ratio: 2.0}}
	ApplyTorrentValue(items, c, nil)
	if !items[0].Protected || items[0].ProtectionReason != "Below minimum ratio" {
		t.Fatalf("expected below-ratio torrent to be protected: %#v", items[0])
	}
	if items[1].Protected {
		t.Fatalf("expected above-ratio torrent to remain unprotected: %#v", items[1])
	}
}

func TestApplyTorrentValueProtectsKeepTaggedTorrentsRegardlessOfRatio(t *testing.T) {
	c := testConfig()
	c.Protection.KeepTorrentTags = []string{"keep"}
	items := []model.Torrent{{Name: "tagged", Ratio: 5, Tags: "other, Keep "}, {Name: "untagged", Ratio: 5, Tags: "other"}}
	ApplyTorrentValue(items, c, nil)
	if !items[0].Protected || items[0].ProtectionReason != "Keep tag" {
		t.Fatalf("expected keep-tagged torrent to be protected: %#v", items[0])
	}
	if items[1].Protected {
		t.Fatalf("expected untagged torrent to remain unprotected: %#v", items[1])
	}
}

func TestApplyTorrentValueMinRatioZeroProtectsNothing(t *testing.T) {
	c := testConfig()
	items := []model.Torrent{{Name: "zero-ratio", Ratio: 0}}
	ApplyTorrentValue(items, c, nil)
	if items[0].Protected {
		t.Fatalf("MinTorrentRatio of zero (unset) must never protect anything: %#v", items[0])
	}
}

func TestApplyTorrentValueBothRatioAndTagCanProtectTheSameTorrent(t *testing.T) {
	c := testConfig()
	c.Protection.MinTorrentRatio = 1.0
	c.Protection.KeepTorrentTags = []string{"keep"}
	items := []model.Torrent{{Name: "both", Ratio: 0.1, Tags: "keep"}}
	ApplyTorrentValue(items, c, nil)
	if !items[0].Protected {
		t.Fatalf("expected torrent satisfying both protection rules to be protected: %#v", items[0])
	}
}
