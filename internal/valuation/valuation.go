package valuation

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"connarr/internal/config"
	"connarr/internal/model"
)

func daysSince(timestamp time.Time) float64 { return time.Since(timestamp).Hours() / 24 }
func clamp(value, minimum, maximum float64) float64 {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}
func hasTag(tags, protectedTags []string) bool {
	for _, mediaTag := range tags {
		for _, protectedTag := range protectedTags {
			if strings.EqualFold(mediaTag, protectedTag) {
				return true
			}
		}
	}
	return false
}

// Apply assigns Value to every media. Higher Value survives longer;
// cleanup ordering is lowest-value first. Value is intentionally unbounded.
func ApplyMedia(mediaItems []model.Media, configuration config.Config) {
	now := time.Now()
	for mediaIndex := range mediaItems {
		mediaItem := &mediaItems[mediaIndex]
		value := 0.0
		mediaItem.Reasons = nil
		mediaItem.Protected = false
		mediaItem.ProtectionReason = ""
		if hasTag(mediaItem.Tags, configuration.Protection.KeepTags) {
			mediaItem.Protected = true
			mediaItem.ProtectionReason = "Keep tag"
			value += configuration.Valuation.KeepTagValueBonus
			mediaItem.Reasons = append(mediaItem.Reasons, model.Reason{Label: "Keep tag", Value: strings.Join(mediaItem.Tags, ", "), Points: configuration.Valuation.KeepTagValueBonus})
		}
		if configuration.Protection.Favorite && mediaItem.Favorite {
			mediaItem.Protected = true
			if mediaItem.ProtectionReason == "" {
				mediaItem.ProtectionReason = "Jellyfin favorite"
			}
			value += configuration.Valuation.FavoriteValueBonus
			mediaItem.Reasons = append(mediaItem.Reasons, model.Reason{Label: "Favorite", Value: "yes", Points: configuration.Valuation.FavoriteValueBonus})
		}
		if mediaItem.Requested && mediaItem.RequestedAt != nil {
			requestAge := now.Sub(*mediaItem.RequestedAt)
			points := configuration.Valuation.Weights.OldRequest
			label := "Old request"
			if requestAge < configuration.RequestGrace {
				mediaItem.Protected = true
				if mediaItem.ProtectionReason == "" {
					mediaItem.ProtectionReason = "Recent request"
				}
				points = configuration.Valuation.RequestValueBonus
				label = "Recent request"
			}
			value += points
			mediaItem.Reasons = append(mediaItem.Reasons, model.Reason{Label: label, Value: fmt.Sprintf("%.0f days ago", requestAge.Hours()/24), Points: points})
		}
		if mediaItem.Rating > 0 {
			points := clamp(mediaItem.Rating/10, 0, 1) * configuration.Valuation.Weights.Rating
			value += points
			mediaItem.Reasons = append(mediaItem.Reasons, model.Reason{Label: "Rating", Value: fmt.Sprintf("%.1f/10", mediaItem.Rating), Points: points})
		}
		if mediaItem.Views == 0 {
			value += configuration.Valuation.Weights.NeverWatched
			mediaItem.Reasons = append(mediaItem.Reasons, model.Reason{Label: "Never watched", Value: "0 views", Points: configuration.Valuation.Weights.NeverWatched})
		} else if mediaItem.LastWatched != nil {
			points := (1 - clamp(daysSince(*mediaItem.LastWatched)/730, 0, 1)) * configuration.Valuation.Weights.LastWatchedAge
			value += points
			mediaItem.Reasons = append(mediaItem.Reasons, model.Reason{Label: "Last watched", Value: fmt.Sprintf("%.0f days ago", daysSince(*mediaItem.LastWatched)), Points: points})
		}
		if !mediaItem.AddedAt.IsZero() {
			points := (1 - clamp(daysSince(mediaItem.AddedAt)/1095, 0, 1)) * configuration.Valuation.Weights.LibraryAge
			value += points
			mediaItem.Reasons = append(mediaItem.Reasons, model.Reason{Label: "Library age", Value: fmt.Sprintf("%.0f days", daysSince(mediaItem.AddedAt)), Points: points})
		}
		if mediaItem.VoteCount > 0 {
			points := clamp(math.Log10(float64(mediaItem.VoteCount)+1)/5, 0, 1) * configuration.Valuation.Weights.LowPopularity
			value += points
			mediaItem.Reasons = append(mediaItem.Reasons, model.Reason{Label: "Popularity", Value: fmt.Sprintf("%d votes", mediaItem.VoteCount), Points: points})
		}
		if len(mediaItem.Torrents) > 0 && configuration.Valuation.Weights.TorrentActivity != 0 {
			leechers := 0
			contributingTorrentCount := 0
			var uploadBytesPerSecond int64
			for _, torrent := range mediaItem.Torrents {
				// Only a current relationship physically proven to be hardlinked to
				// this media can transfer torrent health into media Value. Mere import
				// provenance and unrelated hardlinks are insufficient.
				if model.NormalizeTorrentStatus(torrent.AssociationStatus) != model.TorrentCurrent || !torrent.MediaHardlinked {
					continue
				}
				contributingTorrentCount++
				if torrent.LeechersSwarm > 0 {
					leechers += torrent.LeechersSwarm
				}
				if torrent.UploadSpeed > 0 {
					uploadBytesPerSecond += torrent.UploadSpeed
				}
			}
			if contributingTorrentCount > 0 {
				// Swarm demand and live upload activity are intentionally logarithmic:
				// popularity can keep adding Value without one huge swarm dominating everything.
				uploadMiBPerSecond := float64(uploadBytesPerSecond) / (1024 * 1024)
				points := configuration.Valuation.Weights.TorrentActivity * (math.Log2(1+float64(leechers)) + math.Log2(1+uploadMiBPerSecond))
				value += points
				mediaItem.Reasons = append(mediaItem.Reasons, model.Reason{
					Label:  "Associated torrent",
					Value:  fmt.Sprintf("%d hardlinked torrent(s), %d swarm leechers, %.2f MiB/s up", contributingTorrentCount, leechers, uploadMiBPerSecond),
					Points: points,
					Note:   "The torrent health contributes to the media score because their files are hardlinked.",
				})
			}
		}
		mediaItem.Value = value
	}
	sort.SliceStable(mediaItems, func(leftIndex, rightIndex int) bool {
		leftMedia, rightMedia := mediaItems[leftIndex], mediaItems[rightIndex]
		if leftMedia.Value != rightMedia.Value {
			return leftMedia.Value < rightMedia.Value
		}
		return leftMedia.SizeBytes > rightMedia.SizeBytes
	})
}

// ApplyTorrents assigns an independent retention Value to torrents. It uses
// current swarm facts only; media Value and storage cost deliberately do not
// leak into torrent Value.
func ApplyTorrents(torrents []model.Torrent, configuration config.Config) {
	for torrentIndex := range torrents {
		torrent := &torrents[torrentIndex]
		torrent.Value = 0
		torrent.ValueReasons = nil
		if torrent.SeedsSwarm > 0 && configuration.Valuation.TorrentWeights.Seeds != 0 {
			points := math.Log2(1+float64(torrent.SeedsSwarm)) * configuration.Valuation.TorrentWeights.Seeds
			torrent.Value += points
			torrent.ValueReasons = append(torrent.ValueReasons, model.Reason{Label: "Swarm seeds", Value: fmt.Sprintf("%d seeds", torrent.SeedsSwarm), Points: points})
		}
		if torrent.LeechersSwarm > 0 && configuration.Valuation.TorrentWeights.Leechers != 0 {
			points := math.Log2(1+float64(torrent.LeechersSwarm)) * configuration.Valuation.TorrentWeights.Leechers
			torrent.Value += points
			torrent.ValueReasons = append(torrent.ValueReasons, model.Reason{Label: "Swarm demand", Value: fmt.Sprintf("%d leechers", torrent.LeechersSwarm), Points: points})
		}
		if torrent.UploadSpeed > 0 && configuration.Valuation.TorrentWeights.UploadRate != 0 {
			uploadMiBPerSecond := float64(torrent.UploadSpeed) / (1024 * 1024)
			points := math.Log2(1+uploadMiBPerSecond) * configuration.Valuation.TorrentWeights.UploadRate
			torrent.Value += points
			torrent.ValueReasons = append(torrent.ValueReasons, model.Reason{Label: "Live upload", Value: fmt.Sprintf("%.2f MiB/s", uploadMiBPerSecond), Points: points})
		}
	}
}
