package scoring

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

// Apply assigns Strength to every media. Higher Strength survives longer;
// cleanup ordering is weakest first. Strength is intentionally unbounded.
func Apply(items []model.Media, c config.Config) {
	now := time.Now()
	for i := range items {
		m := &items[i]
		strength := 0.0
		m.Reasons = nil
		if hasTag(m.Tags, c.Protection.KeepTags) {
			strength += c.Scoring.KeepTagStrengthBonus
			m.Reasons = append(m.Reasons, model.Reason{Label: "Keep tag", Value: strings.Join(m.Tags, ", "), Points: c.Scoring.KeepTagStrengthBonus})
		}
		if c.Protection.Favorite && m.Favorite {
			strength += c.Scoring.FavoriteStrengthBonus
			m.Reasons = append(m.Reasons, model.Reason{Label: "Favorite", Value: "yes", Points: c.Scoring.FavoriteStrengthBonus})
		}
		if m.Requested && m.RequestedAt != nil {
			age := now.Sub(*m.RequestedAt)
			p := c.Scoring.Weights.OldRequest
			label := "Old request"
			if age < c.RequestGrace {
				p = c.Scoring.RequestStrengthBonus
				label = "Recent request"
			}
			strength += p
			m.Reasons = append(m.Reasons, model.Reason{Label: label, Value: fmt.Sprintf("%.0f days ago", age.Hours()/24), Points: p})
		}
		if m.Rating > 0 {
			p := clamp(m.Rating/10, 0, 1) * c.Scoring.Weights.Rating
			strength += p
			m.Reasons = append(m.Reasons, model.Reason{Label: "Rating", Value: fmt.Sprintf("%.1f/10", m.Rating), Points: p})
		}
		if m.Views == 0 {
			m.Reasons = append(m.Reasons, model.Reason{Label: "Never watched", Value: "0 views", Points: 0})
		} else if m.LastWatched != nil {
			p := (1 - clamp(days(*m.LastWatched)/730, 0, 1)) * c.Scoring.Weights.LastWatchedAge
			strength += p
			m.Reasons = append(m.Reasons, model.Reason{Label: "Last watched", Value: fmt.Sprintf("%.0f days ago", days(*m.LastWatched)), Points: p})
		}
		if !m.AddedAt.IsZero() {
			p := (1 - clamp(days(m.AddedAt)/1095, 0, 1)) * c.Scoring.Weights.LibraryAge
			strength += p
			m.Reasons = append(m.Reasons, model.Reason{Label: "Library age", Value: fmt.Sprintf("%.0f days", days(m.AddedAt)), Points: p})
		}
		if m.VoteCount > 0 {
			p := clamp(math.Log10(float64(m.VoteCount)+1)/5, 0, 1) * c.Scoring.Weights.LowPopularity
			strength += p
			m.Reasons = append(m.Reasons, model.Reason{Label: "Popularity", Value: fmt.Sprintf("%d votes", m.VoteCount), Points: p})
		}
		if len(m.Torrents) > 0 && c.Scoring.Weights.TorrentActivity != 0 {
			leechers := 0
			current := 0
			var up int64
			for _, t := range m.Torrents {
				// Historical/superseded torrents are useful context on the media page,
				// but they must not inflate the current media Strength.
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
				// popularity can keep adding Strength without one huge swarm dominating everything.
				upMiB := float64(up) / (1024 * 1024)
				p := c.Scoring.Weights.TorrentActivity * (math.Log2(1+float64(leechers)) + math.Log2(1+upMiB))
				strength += p
				m.Reasons = append(m.Reasons, model.Reason{Label: "Torrent activity", Value: fmt.Sprintf("%d torrents, %d swarm leechers, %.2f MiB/s up", current, leechers, upMiB), Points: p})
			}
		}
		m.Strength = strength
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Strength != b.Strength {
			return a.Strength < b.Strength
		}
		return a.SizeBytes > b.SizeBytes
	})
}
