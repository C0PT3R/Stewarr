package valuation

import (
	"testing"
	"time"
	"togetharr/internal/config"
	"togetharr/internal/model"
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
func TestKeepTagAddsValueWithoutMakingItemImmortal(t *testing.T) {
	c := testConfig()
	items := []model.Media{{Title: "Tagged", Rating: 1, Tags: []string{"keep"}}, {Title: "Ordinary", Rating: 9}}
	ApplyMedia(items, c)
	if items[0].Title != "Ordinary" || items[1].Title != "Tagged" {
		t.Fatalf("value ordering failed: %#v", items)
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
			{Hash: "current", AssociationStatus: "ASSOCIATED", LeechersSwarm: 1},
			{Hash: "old", AssociationStatus: "SUPERSEDED", LeechersSwarm: 1023, UploadSpeed: 1024 * 1024 * 1024},
		},
	}}
	ApplyMedia(items, c)
	// Only the single current leecher should count: 10 * log2(2) = 10.
	if items[0].Value != 10 {
		t.Fatalf("historical torrent activity must not inflate media value: got %.2f", items[0].Value)
	}
}

func TestTorrentValueIsIndependentFromMediaAndStorage(t *testing.T) {
	c := testConfig()
	c.Valuation.TorrentWeights.Seeds = 1
	c.Valuation.TorrentWeights.Leechers = 5
	c.Valuation.TorrentWeights.UploadRate = 5
	items := []model.Torrent{
		{Name: "active", SeedsSwarm: 100, LeechersSwarm: 10, UploadSpeed: 2 * 1024 * 1024, ReclaimableBytes: 99 << 30, AssociationStatus: "ORPHANED"},
		{Name: "quiet", SeedsSwarm: 1, ReclaimableBytes: 0, AssociationStatus: "ASSOCIATED"},
	}
	ApplyTorrents(items, c)
	if items[0].Value <= items[1].Value {
		t.Fatalf("active swarm should have greater torrent value: %#v", items)
	}
	if len(items[0].ValueReasons) != 3 {
		t.Fatalf("expected explainable torrent value reasons: %#v", items[0].ValueReasons)
	}
}
