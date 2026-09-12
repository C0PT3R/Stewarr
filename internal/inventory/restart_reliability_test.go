package inventory

import (
	"path/filepath"
	"testing"
	"time"

	"stewarr/internal/config"
	"stewarr/internal/model"
	"stewarr/internal/store"
)

// TestNewTrustsCachedEnrichmentAfterRestart guards a real observability bug:
// a process restart that successfully loads persisted media used to mark
// every configured enrichment source (Jellyfin/Seerr/TMDB) "stale"
// unconditionally, via enrichmentInitialState, the same helper used for a
// genuine cold start with no cache at all. That state only self-corrects
// once that source's own periodic task completes again from scratch —
// Jellyfin/Seerr's hourly interval hid this quickly, but TMDB's 24h interval
// meant "automatic removal planning is paused" for a full day after every
// single restart, even though the just-loaded facts were perfectly good.
func TestNewTrustsCachedEnrichmentAfterRestart(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	media := []model.Media{{Type: model.Movie, SourceID: 1, Title: "Old Movie", TMDBID: 42, TMDBEnrichedAt: time.Now().Add(-time.Hour)}}
	if err := database.SaveMedia(media); err != nil {
		t.Fatal(err)
	}

	var cfg config.Config
	cfg.TMDB.APIKey = "a-real-key"
	cfg.Jellyfin.URL = "http://jellyfin.local"
	cfg.Seerr.URL = "http://seerr.local"

	service := New(cfg, database)

	if service.reliability.TMDB != "reliable" {
		t.Fatalf("expected TMDB reliability to trust the just-loaded cache as reliable, got %q", service.reliability.TMDB)
	}
	if service.reliability.Jellyfin != "reliable" || service.reliability.Seerr != "reliable" {
		t.Fatalf("expected Jellyfin/Seerr to trust the just-loaded cache too, got jellyfin=%q seerr=%q", service.reliability.Jellyfin, service.reliability.Seerr)
	}
	if !service.reliability.Valuation {
		t.Fatalf("expected Valuation to be reliable immediately after a successful cache load, got false (message=%q)", service.reliability.Message)
	}
}
