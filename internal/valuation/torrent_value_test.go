package valuation

import (
	"testing"
	"time"

	"stewarr/internal/model"
	"stewarr/internal/store"
)

func TestApplyTorrentValueScoresRatioRecencyAndPrivateBonus(t *testing.T) {
	c := testConfig()
	items := []model.Torrent{
		{Name: "valuable", Client: "qb", Hash: "a", Ratio: 2.0, LastActivity: time.Now().Unix(), Private: true},
		{Name: "bare", Client: "qb", Hash: "b"},
	}
	ApplyTorrentValue(items, c, nil)
	if items[0].TorrentValue <= items[1].TorrentValue {
		t.Fatalf("expected the valuable torrent to score higher: %#v", items)
	}
	labels := map[string]bool{}
	for _, r := range items[0].TorrentValueReasons {
		labels[r.Label] = true
	}
	for _, want := range []string{"Ratio", "Recent activity", "Private tracker"} {
		if !labels[want] {
			t.Fatalf("expected a %q reason, got %#v", want, items[0].TorrentValueReasons)
		}
	}
	if len(items[1].TorrentValueReasons) != 0 {
		t.Fatalf("expected no reasons for a bare torrent, got %#v", items[1].TorrentValueReasons)
	}
}

func TestApplyTorrentValueStaleActivityScoresLessThanRecent(t *testing.T) {
	c := testConfig()
	items := []model.Torrent{
		{Name: "recent", Client: "qb", Hash: "a", LastActivity: time.Now().Unix()},
		{Name: "stale", Client: "qb", Hash: "b", LastActivity: time.Now().Add(-20 * 24 * time.Hour).Unix()},
	}
	ApplyTorrentValue(items, c, nil)
	if items[0].TorrentValue <= items[1].TorrentValue {
		t.Fatalf("expected recent activity to score higher than stale activity: %#v", items)
	}
}

func TestApplyTorrentValueContributionNeedsEnoughSamples(t *testing.T) {
	c := testConfig()
	now := time.Now()
	items := []model.Torrent{
		{Name: "sparse", Client: "qb", Hash: "a"},
		{Name: "contributing", Client: "qb", Hash: "b"},
	}
	history := map[string][]store.TorrentHistorySample{
		"qb|a": {{SampledAt: now.Add(-time.Hour), UploadedBytes: 100}},
		"qb|b": {
			{SampledAt: now.Add(-6 * 24 * time.Hour), UploadedBytes: 0},
			{SampledAt: now.Add(-5 * 24 * time.Hour), UploadedBytes: 100 * 1024 * 1024},
			{SampledAt: now.Add(-4 * 24 * time.Hour), UploadedBytes: 200 * 1024 * 1024},
			{SampledAt: now.Add(-3 * 24 * time.Hour), UploadedBytes: 300 * 1024 * 1024},
		},
	}
	ApplyTorrentValue(items, c, history)
	if items[0].TorrentValue != 0 {
		t.Fatalf("a single sample must not be enough to earn contribution points: %#v", items[0])
	}
	if items[1].TorrentValue <= 0 {
		t.Fatalf("expected realized upload bytes across enough samples to earn points: %#v", items[1])
	}
}

func TestApplyTorrentValueConsistencyReflectsPositiveDeltaFraction(t *testing.T) {
	c := testConfig()
	now := time.Now()
	items := []model.Torrent{{Name: "mixed", Client: "qb", Hash: "a"}}
	history := map[string][]store.TorrentHistorySample{
		"qb|a": {
			{SampledAt: now.Add(-6 * 24 * time.Hour), UploadedBytes: 100},
			{SampledAt: now.Add(-5 * 24 * time.Hour), UploadedBytes: 200}, // +
			{SampledAt: now.Add(-4 * 24 * time.Hour), UploadedBytes: 150}, // reset, ignored
			{SampledAt: now.Add(-3 * 24 * time.Hour), UploadedBytes: 250}, // +
		},
	}
	ApplyTorrentValue(items, c, history)
	var consistency *model.Reason
	for i, r := range items[0].TorrentValueReasons {
		if r.Label == "Consistency" {
			consistency = &items[0].TorrentValueReasons[i]
		}
	}
	if consistency == nil {
		t.Fatalf("expected a Consistency reason, got %#v", items[0].TorrentValueReasons)
	}
	if consistency.Value != "67% of samples" {
		t.Fatalf("expected 2 of 3 deltas positive (67%%), got %q", consistency.Value)
	}
}

func TestApplyTorrentValueSeedingTimeCapsAtHorizon(t *testing.T) {
	c := testConfig()
	items := []model.Torrent{
		{Name: "past-horizon", Client: "qb", Hash: "a", SeedingTime: int64(365 * 24 * 3600)},
		{Name: "half-horizon", Client: "qb", Hash: "b", SeedingTime: int64(90 * 24 * 3600)},
	}
	ApplyTorrentValue(items, c, nil)
	if items[0].TorrentValue <= items[1].TorrentValue {
		t.Fatalf("expected more seeding time to score at least as high up to the cap: %#v", items)
	}
	var reason *model.Reason
	for i, r := range items[0].TorrentValueReasons {
		if r.Label == "Seeding time" {
			reason = &items[0].TorrentValueReasons[i]
		}
	}
	if reason == nil {
		t.Fatalf("expected a Seeding time reason, got %#v", items[0].TorrentValueReasons)
	}
	if reason.Points != torrentSeedingTimeWeight {
		t.Fatalf("expected seeding time to be capped at the full weight past the horizon, got %#v", reason)
	}
}
