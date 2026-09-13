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

func TestApplyTorrentValueSustainedDemandNeedsEnoughHistory(t *testing.T) {
	c := testConfig()
	now := time.Now()
	items := []model.Torrent{
		{Name: "sparse", Client: "qb", Hash: "a"},
		{Name: "sustained", Client: "qb", Hash: "b"},
	}
	history := map[string][]store.TorrentHistorySample{
		"qb|a": {{SampledAt: now.Add(-time.Hour), SeedsSwarm: 1, LeechersSwarm: 20}},
		"qb|b": {
			{SampledAt: now.Add(-6 * 24 * time.Hour), SeedsSwarm: 1, LeechersSwarm: 20},
			{SampledAt: now.Add(-5 * 24 * time.Hour), SeedsSwarm: 1, LeechersSwarm: 20},
			{SampledAt: now.Add(-4 * 24 * time.Hour), SeedsSwarm: 1, LeechersSwarm: 20},
			{SampledAt: now.Add(-3 * 24 * time.Hour), SeedsSwarm: 1, LeechersSwarm: 20},
		},
	}
	ApplyTorrentValue(items, c, history)
	if items[0].TorrentValue != 0 {
		t.Fatalf("a single sample must not be enough to earn demand points: %#v", items[0])
	}
	if items[1].TorrentValue <= 0 {
		t.Fatalf("expected sustained demand across enough samples to earn points: %#v", items[1])
	}
}

func TestApplyTorrentValueSustainedDemandIgnoresSamplesOutsideWindow(t *testing.T) {
	c := testConfig()
	now := time.Now()
	items := []model.Torrent{{Name: "old-demand-only", Client: "qb", Hash: "a"}}
	old := now.Add(-TorrentDemandWindow - 24*time.Hour)
	history := map[string][]store.TorrentHistorySample{
		"qb|a": {
			{SampledAt: old, SeedsSwarm: 1, LeechersSwarm: 50},
			{SampledAt: old.Add(time.Hour), SeedsSwarm: 1, LeechersSwarm: 50},
			{SampledAt: old.Add(2 * time.Hour), SeedsSwarm: 1, LeechersSwarm: 50},
			{SampledAt: old.Add(3 * time.Hour), SeedsSwarm: 1, LeechersSwarm: 50},
		},
	}
	ApplyTorrentValue(items, c, history)
	if items[0].TorrentValue != 0 {
		t.Fatalf("samples entirely outside the demand window must not contribute: %#v", items[0])
	}
}
