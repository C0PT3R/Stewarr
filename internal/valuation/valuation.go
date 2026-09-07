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

// serviceAllowsRemoval reports whether serviceID's own service has checked
// "Allow automatic removal" — an unknown or empty serviceID fails closed,
// the same as a removed/never-configured service would.
func serviceAllowsRemoval(configuration config.Config, serviceID string) bool {
	for _, service := range configuration.Services {
		if service.ID == serviceID {
			return service.AllowAutomaticRemoval
		}
	}
	return false
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

// splitTags parses qBittorrent's comma-joined free-text Tags field into
// individual, trimmed tag names.
func splitTags(tags string) []string {
	if strings.TrimSpace(tags) == "" {
		return nil
	}
	parts := strings.Split(tags, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// effectiveRating returns the rating/vote data valuation should score:
// TMDB's own numbers when TMDB enrichment has a match for this item
// (TMDBVoteCount>0), otherwise Radarr/Sonarr's own Rating/VoteCount. An
// item with genuinely zero TMDB votes wouldn't carry meaningful rating
// data anyway, so falling back to Radarr/Sonarr's in that edge case is
// harmless — the point is that TMDB being unconfigured, unreachable, or
// having no match for this title never removes the signal Radarr/Sonarr
// already provided.
func effectiveRating(mediaItem model.Media) (rating float64, votes int) {
	if mediaItem.TMDBVoteCount > 0 {
		return mediaItem.TMDBRating, mediaItem.TMDBVoteCount
	}
	return mediaItem.Rating, mediaItem.VoteCount
}

// Apply assigns Retention Value to every media. Higher Retention Value
// survives longer; cleanup ordering is lowest-value first. Retention Value is
// intentionally unbounded.
func ApplyMedia(mediaItems []model.Media, configuration config.Config) {
	now := time.Now()
	for mediaIndex := range mediaItems {
		mediaItem := &mediaItems[mediaIndex]
		value := 0.0
		mediaItem.RetentionValueReasons = nil
		mediaItem.Protected = false
		mediaItem.ProtectionReason = ""
		mediaItem.RemovalRestricted = !serviceAllowsRemoval(configuration, mediaItem.ServiceID)
		if hasTag(mediaItem.Tags, configuration.Protection.KeepTags) {
			mediaItem.Protected = true
			mediaItem.ProtectionReason = "Keep tag"
			value += configuration.Valuation.KeepTagValueBonus
			mediaItem.RetentionValueReasons = append(mediaItem.RetentionValueReasons, model.Reason{Label: "Keep tag", Value: strings.Join(mediaItem.Tags, ", "), Points: configuration.Valuation.KeepTagValueBonus})
		}
		if configuration.Protection.Favorite && mediaItem.Favorite {
			mediaItem.Protected = true
			if mediaItem.ProtectionReason == "" {
				mediaItem.ProtectionReason = "Jellyfin favorite"
			}
			value += configuration.Valuation.FavoriteValueBonus
			mediaItem.RetentionValueReasons = append(mediaItem.RetentionValueReasons, model.Reason{Label: "Favorite", Value: "yes", Points: configuration.Valuation.FavoriteValueBonus})
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
			mediaItem.RetentionValueReasons = append(mediaItem.RetentionValueReasons, model.Reason{Label: label, Value: fmt.Sprintf("%.0f days ago", requestAge.Hours()/24), Points: points})
		}
		rating, voteCount := effectiveRating(*mediaItem)
		if rating > 0 {
			points := clamp(rating/10, 0, 1) * configuration.Valuation.Weights.Rating
			value += points
			mediaItem.RetentionValueReasons = append(mediaItem.RetentionValueReasons, model.Reason{Label: "Rating", Value: fmt.Sprintf("%.1f/10", rating), Points: points})
		}
		if mediaItem.Views == 0 {
			value += configuration.Valuation.Weights.NeverWatched
			mediaItem.RetentionValueReasons = append(mediaItem.RetentionValueReasons, model.Reason{Label: "Never watched", Value: "0 views", Points: configuration.Valuation.Weights.NeverWatched})
		} else if mediaItem.LastWatched != nil {
			points := (1 - clamp(daysSince(*mediaItem.LastWatched)/730, 0, 1)) * configuration.Valuation.Weights.LastWatchedAge
			value += points
			mediaItem.RetentionValueReasons = append(mediaItem.RetentionValueReasons, model.Reason{Label: "Last watched", Value: fmt.Sprintf("%.0f days ago", daysSince(*mediaItem.LastWatched)), Points: points})
		}
		if !mediaItem.AddedAt.IsZero() {
			points := (1 - clamp(daysSince(mediaItem.AddedAt)/1095, 0, 1)) * configuration.Valuation.Weights.LibraryAge
			value += points
			mediaItem.RetentionValueReasons = append(mediaItem.RetentionValueReasons, model.Reason{Label: "Library age", Value: fmt.Sprintf("%.0f days", daysSince(mediaItem.AddedAt)), Points: points})
		}
		if voteCount > 0 {
			points := clamp(math.Log10(float64(voteCount)+1)/5, 0, 1) * configuration.Valuation.Weights.LowPopularity
			value += points
			mediaItem.RetentionValueReasons = append(mediaItem.RetentionValueReasons, model.Reason{Label: "Vote count", Value: fmt.Sprintf("%d votes", voteCount), Points: points})
		}
		if mediaItem.Popularity > 0 {
			points := clamp(math.Log10(mediaItem.Popularity+1)/3, 0, 1) * configuration.Valuation.Weights.Popularity
			value += points
			mediaItem.RetentionValueReasons = append(mediaItem.RetentionValueReasons, model.Reason{Label: "Popularity", Value: fmt.Sprintf("%.1f", mediaItem.Popularity), Points: points, Note: "TMDB's own popularity score."})
		}
		// Everything above this point (protection, rating, watch state,
		// popularity) describes the whole series and applies identically to
		// every season; LibraryAge and TorrentActivity below are whole-series
		// scoped and are replaced, per season, by SeasonRecency and
		// season-scoped torrent activity in applySeasonValues.
		seriesWideValue := value
		seriesWideReasons := append([]model.Reason(nil), mediaItem.RetentionValueReasons...)
		if len(mediaItem.Torrents) > 0 && configuration.Valuation.Weights.TorrentActivity != 0 {
			leechers := 0
			contributingTorrentCount := 0
			var uploadBytesPerSecond int64
			for _, torrent := range mediaItem.Torrents {
				// Only a current relationship physically proven to be hardlinked to
				// this media can transfer torrent health into media Retention Value.
				// Mere import provenance and unrelated hardlinks are insufficient.
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
				// popularity can keep adding Retention Value without one huge swarm
				// dominating everything.
				uploadMiBPerSecond := float64(uploadBytesPerSecond) / (1024 * 1024)
				points := configuration.Valuation.Weights.TorrentActivity * (math.Log2(1+float64(leechers)) + math.Log2(1+uploadMiBPerSecond))
				value += points
				mediaItem.RetentionValueReasons = append(mediaItem.RetentionValueReasons, model.Reason{
					Label:  "Associated torrent",
					Value:  fmt.Sprintf("%d hardlinked torrent(s), %d swarm leechers, %.2f MiB/s up", contributingTorrentCount, leechers, uploadMiBPerSecond),
					Points: points,
					Note:   "The torrent health contributes to the media Retention Value because their files are hardlinked.",
				})
			}
		}
		mediaItem.RetentionValue = value
		if mediaItem.Type == model.Series && len(mediaItem.Seasons) > 0 {
			applySeasonValues(mediaItem, seriesWideValue, seriesWideReasons, configuration, now)
		}
	}
	sort.SliceStable(mediaItems, func(leftIndex, rightIndex int) bool {
		leftMedia, rightMedia := mediaItems[leftIndex], mediaItems[rightIndex]
		if leftMedia.RetentionValue != rightMedia.RetentionValue {
			return leftMedia.RetentionValue < rightMedia.RetentionValue
		}
		return leftMedia.SizeBytes > rightMedia.SizeBytes
	})
}

// applySeasonValues assigns each Series season its own Retention Value:
// series-wide factors (protection, rating, watch state, popularity) apply
// identically to every season via seriesWideValue/seriesWideReasons, plus
// one season-specific factor (recency of that season's most recently aired
// episode — the content's own age, not when it was imported) and torrent
// activity scoped to torrents proven hardlinked to that specific season
// rather than the whole series.
func applySeasonValues(mediaItem *model.Media, seriesWideValue float64, seriesWideReasons []model.Reason, configuration config.Config, now time.Time) {
	for seasonIndex := range mediaItem.Seasons {
		season := &mediaItem.Seasons[seasonIndex]
		season.Protected = mediaItem.Protected
		season.ProtectionReason = mediaItem.ProtectionReason
		season.RemovalRestricted = mediaItem.RemovalRestricted
		value := seriesWideValue
		reasons := append([]model.Reason(nil), seriesWideReasons...)
		if !season.LastAiredAt.IsZero() {
			points := (1 - clamp(daysSince(season.LastAiredAt)/1095, 0, 1)) * configuration.Valuation.Weights.SeasonRecency
			value += points
			reasons = append(reasons, model.Reason{Label: "Season recency", Value: fmt.Sprintf("%.0f days since last aired", daysSince(season.LastAiredAt)), Points: points})
		}
		if configuration.Valuation.Weights.TorrentActivity != 0 {
			leechers := 0
			contributingTorrentCount := 0
			var uploadBytesPerSecond int64
			for _, torrent := range mediaItem.Torrents {
				if model.NormalizeTorrentStatus(torrent.AssociationStatus) != model.TorrentCurrent || !torrent.MediaHardlinked {
					continue
				}
				inSeason := false
				for _, s := range torrent.HardlinkedSeasons {
					if s == season.Number {
						inSeason = true
						break
					}
				}
				if !inSeason {
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
				uploadMiBPerSecond := float64(uploadBytesPerSecond) / (1024 * 1024)
				points := configuration.Valuation.Weights.TorrentActivity * (math.Log2(1+float64(leechers)) + math.Log2(1+uploadMiBPerSecond))
				value += points
				reasons = append(reasons, model.Reason{
					Label:  "Associated torrent",
					Value:  fmt.Sprintf("%d hardlinked torrent(s), %d swarm leechers, %.2f MiB/s up", contributingTorrentCount, leechers, uploadMiBPerSecond),
					Points: points,
					Note:   "The torrent health contributes to this season's Retention Value because their files are hardlinked to this season specifically.",
				})
			}
		}
		season.RetentionValue = value
		season.RetentionValueReasons = reasons
	}
}

// ApplyTorrents assigns an independent Swarm Value to torrents. It uses
// current swarm facts only; media Retention Value and storage cost
// deliberately do not leak into torrent Swarm Value.
func ApplyTorrents(torrents []model.Torrent, configuration config.Config) {
	for torrentIndex := range torrents {
		torrent := &torrents[torrentIndex]
		torrent.SwarmValue = 0
		torrent.SwarmValueReasons = nil
		torrent.Protected = false
		torrent.ProtectionReason = ""
		torrent.RemovalRestricted = !serviceAllowsRemoval(configuration, torrent.ServiceID)
		if configuration.Protection.MinTorrentRatio > 0 && torrent.Ratio < configuration.Protection.MinTorrentRatio {
			torrent.Protected = true
			torrent.ProtectionReason = "Below minimum ratio"
		}
		if hasTag(splitTags(torrent.Tags), configuration.Protection.KeepTorrentTags) {
			torrent.Protected = true
			if torrent.ProtectionReason == "" {
				torrent.ProtectionReason = "Keep tag"
			}
		}
		if torrent.SeedsSwarm > 0 && configuration.Valuation.TorrentWeights.Seeds != 0 {
			points := math.Log2(1+float64(torrent.SeedsSwarm)) * configuration.Valuation.TorrentWeights.Seeds
			torrent.SwarmValue += points
			torrent.SwarmValueReasons = append(torrent.SwarmValueReasons, model.Reason{Label: "Swarm seeds", Value: fmt.Sprintf("%d seeds", torrent.SeedsSwarm), Points: points})
		}
		if torrent.LeechersSwarm > 0 && configuration.Valuation.TorrentWeights.Leechers != 0 {
			points := math.Log2(1+float64(torrent.LeechersSwarm)) * configuration.Valuation.TorrentWeights.Leechers
			torrent.SwarmValue += points
			torrent.SwarmValueReasons = append(torrent.SwarmValueReasons, model.Reason{Label: "Swarm demand", Value: fmt.Sprintf("%d leechers", torrent.LeechersSwarm), Points: points})
		}
		if torrent.UploadSpeed > 0 && configuration.Valuation.TorrentWeights.UploadRate != 0 {
			uploadMiBPerSecond := float64(torrent.UploadSpeed) / (1024 * 1024)
			points := math.Log2(1+uploadMiBPerSecond) * configuration.Valuation.TorrentWeights.UploadRate
			torrent.SwarmValue += points
			torrent.SwarmValueReasons = append(torrent.SwarmValueReasons, model.Reason{Label: "Live upload", Value: fmt.Sprintf("%.2f MiB/s", uploadMiBPerSecond), Points: points})
		}
	}
}
