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
	if items[0].Value != 25 || len(items[0].Reasons) == 0 || items[0].Reasons[0].Points != 25 {
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
	if items[0].Value != 10 {
		t.Fatalf("historical torrent activity must not inflate media value: got %.2f", items[0].Value)
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
	if items[0].Value != 0 {
		t.Fatalf("non-hardlinked activity must not add media value: got %.2f", items[0].Value)
	}
	for _, reason := range items[0].Reasons {
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
	for _, reason := range items[0].Reasons {
		if reason.Label == "Associated torrent" && reason.Note != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing hardlink-gated explanation: %#v", items[0].Reasons)
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
	if items[0].Value <= items[1].Value {
		t.Fatalf("active swarm should have greater torrent value: %#v", items)
	}
	if len(items[0].ValueReasons) != 3 {
		t.Fatalf("expected explainable torrent value reasons: %#v", items[0].ValueReasons)
	}
}
