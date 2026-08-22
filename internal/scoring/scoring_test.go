package scoring

import (
	"spartarr/internal/config"
	"spartarr/internal/model"
	"testing"
	"time"
)

func testConfig() config.Config {
	var c config.Config
	c.Scoring.Weights.Rating = 40
	c.Scoring.Weights.LastWatchedAge = 15
	c.Scoring.Weights.LibraryAge = 10
	c.Scoring.Weights.LowPopularity = 10
	c.Scoring.Weights.OldRequest = 12
	c.Scoring.RequestStrengthBonus = 100
	c.Scoring.FavoriteStrengthBonus = 1000
	c.Scoring.KeepTagStrengthBonus = 10000
	c.Protection.Favorite = true
	c.RequestGrace = 365 * 24 * time.Hour
	c.Protection.KeepTags = []string{"keep"}
	return c
}
func TestWeakestStrengthComesFirst(t *testing.T) {
	c := testConfig()
	items := []model.Media{{Title: "Strong", Rating: 8.5}, {Title: "Weak", Rating: 5.5}, {Title: "Weakest", Rating: 3.5}}
	Apply(items, c)
	if items[0].Title != "Weakest" || items[1].Title != "Weak" || items[2].Title != "Strong" {
		t.Fatalf("unexpected order: %s, %s, %s", items[0].Title, items[1].Title, items[2].Title)
	}
}
func TestKeepTagAddsStrengthWithoutMakingItemImmortal(t *testing.T) {
	c := testConfig()
	items := []model.Media{{Title: "Tagged", Rating: 1, Tags: []string{"keep"}}, {Title: "Ordinary", Rating: 9}}
	Apply(items, c)
	if items[0].Title != "Ordinary" || items[1].Title != "Tagged" {
		t.Fatalf("strength ordering failed: %#v", items)
	}
}
func TestSizeBreaksEqualStrengthTie(t *testing.T) {
	c := testConfig()
	items := []model.Media{{Title: "Small", Rating: 5, SizeBytes: 10}, {Title: "Large", Rating: 5, SizeBytes: 100}}
	Apply(items, c)
	if items[0].Title != "Large" {
		t.Fatalf("larger equal-strength media should fall first: %#v", items)
	}
}
