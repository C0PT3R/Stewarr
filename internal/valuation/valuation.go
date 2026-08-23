package valuation

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"togetharr/internal/config"
	"togetharr/internal/model"
)

func days(t time.Time) float64 { return time.Since(t).Hours() / 24 }
func clamp(x, a, b float64) float64 {
	if x < a {
		return a
	}
	if x > b {
		return b
	}
	return x
}
func hasTag(tags, keep []string) bool {
	for _, a := range tags {
		for _, b := range keep {
			if strings.EqualFold(a, b) {
				return true
			}
		}
	}
	return false
}

// Apply assigns Value to every media. Higher Value survives longer;
// cleanup ordering is lowest-value first. Value is intentionally unbounded.
func ApplyMedia(items []model.Media, c config.Config) {
	now := time.Now()
	for i := range items {
		m := &items[i]
		value := 0.0
		m.Reasons = nil
		if hasTag(m.Tags, c.Protection.KeepTags) {
			value += c.Valuation.KeepTagValueBonus
			m.Reasons = append(m.Reasons, model.Reason{Label: "Keep tag", Value: strings.Join(m.Tags, ", "), Points: c.Valuation.KeepTagValueBonus})
		}
		if c.Protection.Favorite && m.Favorite {
			value += c.Valuation.FavoriteValueBonus
			m.Reasons = append(m.Reasons, model.Reason{Label: "Favorite", Value: "yes", Points: c.Valuation.FavoriteValueBonus})
		}
		if m.Requested && m.RequestedAt != nil {
			age := now.Sub(*m.RequestedAt)
			p := c.Valuation.Weights.OldRequest
			label := "Old request"
			if age < c.RequestGrace {
				p = c.Valuation.RequestValueBonus
				label = "Recent request"
			}
			value += p
			m.Reasons = append(m.Reasons, model.Reason{Label: label, Value: fmt.Sprintf("%.0f days ago", age.Hours()/24), Points: p})
		}
		if m.Rating > 0 {
			p := clamp(m.Rating/10, 0, 1) * c.Valuation.Weights.Rating
			value += p
			m.Reasons = append(m.Reasons, model.Reason{Label: "Rating", Value: fmt.Sprintf("%.1f/10", m.Rating), Points: p})
		}
		if m.Views == 0 {
			m.Reasons = append(m.Reasons, model.Reason{Label: "Never watched", Value: "0 views", Points: 0})
		} else if m.LastWatched != nil {
			p := (1 - clamp(days(*m.LastWatched)/730, 0, 1)) * c.Valuation.Weights.LastWatchedAge
			value += p
			m.Reasons = append(m.Reasons, model.Reason{Label: "Last watched", Value: fmt.Sprintf("%.0f days ago", days(*m.LastWatched)), Points: p})
		}
		if !m.AddedAt.IsZero() {
			p := (1 - clamp(days(m.AddedAt)/1095, 0, 1)) * c.Valuation.Weights.LibraryAge
			value += p
			m.Reasons = append(m.Reasons, model.Reason{Label: "Library age", Value: fmt.Sprintf("%.0f days", days(m.AddedAt)), Points: p})
		}
		if m.VoteCount > 0 {
			p := clamp(math.Log10(float64(m.VoteCount)+1)/5, 0, 1) * c.Valuation.Weights.LowPopularity
			value += p
			m.Reasons = append(m.Reasons, model.Reason{Label: "Popularity", Value: fmt.Sprintf("%d votes", m.VoteCount), Points: p})
		}
		if len(m.Torrents) > 0 && c.Valuation.Weights.TorrentActivity != 0 {
			leechers := 0
			current := 0
			var up int64
			for _, t := range m.Torrents {
				// Historical/superseded torrents are useful context on the media page,
				// but they must not inflate the current media Value.
				if !strings.EqualFold(t.AssociationStatus, "ASSOCIATED") {
					continue
				}
				current++
				if t.LeechersSwarm > 0 {
					leechers += t.LeechersSwarm
				}
				if t.UploadSpeed > 0 {
					up += t.UploadSpeed
				}
			}
			if current > 0 {
				// Swarm demand and live upload activity are intentionally logarithmic:
				// popularity can keep adding Value without one huge swarm dominating everything.
				upMiB := float64(up) / (1024 * 1024)
				p := c.Valuation.Weights.TorrentActivity * (math.Log2(1+float64(leechers)) + math.Log2(1+upMiB))
				value += p
				m.Reasons = append(m.Reasons, model.Reason{Label: "Torrent activity", Value: fmt.Sprintf("%d torrents, %d swarm leechers, %.2f MiB/s up", current, leechers, upMiB), Points: p})
			}
		}
		m.Value = value
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Value != b.Value {
			return a.Value < b.Value
		}
		return a.SizeBytes > b.SizeBytes
	})
}

// ApplyTorrents assigns an independent retention Value to torrents. It uses
// current swarm facts only; media Value and storage cost deliberately do not
// leak into torrent Value.
func ApplyTorrents(items []model.Torrent, c config.Config) {
	for i := range items {
		t := &items[i]
		t.Value = 0
		t.ValueReasons = nil
		if t.SeedsSwarm > 0 && c.Valuation.TorrentWeights.Seeds != 0 {
			p := math.Log2(1+float64(t.SeedsSwarm)) * c.Valuation.TorrentWeights.Seeds
			t.Value += p
			t.ValueReasons = append(t.ValueReasons, model.Reason{Label: "Swarm seeds", Value: fmt.Sprintf("%d seeds", t.SeedsSwarm), Points: p})
		}
		if t.LeechersSwarm > 0 && c.Valuation.TorrentWeights.Leechers != 0 {
			p := math.Log2(1+float64(t.LeechersSwarm)) * c.Valuation.TorrentWeights.Leechers
			t.Value += p
			t.ValueReasons = append(t.ValueReasons, model.Reason{Label: "Swarm demand", Value: fmt.Sprintf("%d leechers", t.LeechersSwarm), Points: p})
		}
		if t.UploadSpeed > 0 && c.Valuation.TorrentWeights.UploadRate != 0 {
			mib := float64(t.UploadSpeed) / (1024 * 1024)
			p := math.Log2(1+mib) * c.Valuation.TorrentWeights.UploadRate
			t.Value += p
			t.ValueReasons = append(t.ValueReasons, model.Reason{Label: "Live upload", Value: fmt.Sprintf("%.2f MiB/s", mib), Points: p})
		}
	}
}
