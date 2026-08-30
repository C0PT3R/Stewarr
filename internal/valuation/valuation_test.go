package valuation

import (
	"connarr/internal/config"
	"connarr/internal/model"
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

func TestTorrentValueIsIndependentFromMediaAndStorage(t *testing.T) {
	c := testConfig()
	c.Valuation.TorrentWeights.Seeds = 1
	c.Valuation.TorrentWeights.Leechers = 5
	c.Valuation.TorrentWeights.UploadRate = 5
	items := []model.Torrent{
		{Name: "active", SeedsSwarm: 100, LeechersSwarm: 10, UploadSpeed: 2 * 1024 * 1024, ReclaimableBytes: 99 << 30, AssociationStatus: model.TorrentUnassociated},
		{Name: "quiet", SeedsSwarm: 1, ReclaimableBytes: 0, AssociationStatus: model.TorrentCurrent},
	}
	ApplyTorrents(items, c)
	if items[0].SwarmValue <= items[1].SwarmValue {
		t.Fatalf("active swarm should have greater torrent value: %#v", items)
	}
	if len(items[0].SwarmValueReasons) != 3 {
		t.Fatalf("expected explainable torrent value reasons: %#v", items[0].SwarmValueReasons)
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
			{Number: 1, LastAddedAt: now.AddDate(-3, 0, 0)},
			{Number: 2, LastAddedAt: now},
		},
	}}
	ApplyMedia(items, c)
	old, recent := items[0].Seasons[0], items[0].Seasons[1]
	if recent.RetentionValue <= old.RetentionValue {
		t.Fatalf("expected the more recently added season to have higher Retention Value: old=%#v recent=%#v", old, recent)
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

func TestApplyTorrentsProtectsBelowMinimumRatio(t *testing.T) {
	c := testConfig()
	c.Protection.MinTorrentRatio = 1.0
	items := []model.Torrent{{Name: "low", Ratio: 0.5}, {Name: "high", Ratio: 2.0}}
	ApplyTorrents(items, c)
	if !items[0].Protected || items[0].ProtectionReason != "Below minimum ratio" {
		t.Fatalf("expected below-ratio torrent to be protected: %#v", items[0])
	}
	if items[1].Protected {
		t.Fatalf("expected above-ratio torrent to remain unprotected: %#v", items[1])
	}
}

func TestApplyTorrentsProtectsKeepTaggedTorrentsRegardlessOfRatio(t *testing.T) {
	c := testConfig()
	c.Protection.KeepTorrentTags = []string{"keep"}
	items := []model.Torrent{{Name: "tagged", Ratio: 5, Tags: "other, Keep "}, {Name: "untagged", Ratio: 5, Tags: "other"}}
	ApplyTorrents(items, c)
	if !items[0].Protected || items[0].ProtectionReason != "Keep tag" {
		t.Fatalf("expected keep-tagged torrent to be protected: %#v", items[0])
	}
	if items[1].Protected {
		t.Fatalf("expected untagged torrent to remain unprotected: %#v", items[1])
	}
}

func TestApplyTorrentsMinRatioZeroProtectsNothing(t *testing.T) {
	c := testConfig()
	items := []model.Torrent{{Name: "zero-ratio", Ratio: 0}}
	ApplyTorrents(items, c)
	if items[0].Protected {
		t.Fatalf("MinTorrentRatio of zero (unset) must never protect anything: %#v", items[0])
	}
}

func TestApplyTorrentsBothRatioAndTagCanProtectTheSameTorrent(t *testing.T) {
	c := testConfig()
	c.Protection.MinTorrentRatio = 1.0
	c.Protection.KeepTorrentTags = []string{"keep"}
	items := []model.Torrent{{Name: "both", Ratio: 0.1, Tags: "keep"}}
	ApplyTorrents(items, c)
	if !items[0].Protected {
		t.Fatalf("expected torrent satisfying both protection rules to be protected: %#v", items[0])
	}
}
