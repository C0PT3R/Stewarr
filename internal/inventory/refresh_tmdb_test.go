package inventory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"stewarr/internal/config"
	"stewarr/internal/model"
	"stewarr/internal/services/tmdb"
)

// TestRefreshTMDBDoesNotDiscardResultsWhenBaseGenerationMovesOnMidPass
// guards a real staleness bug: TMDB fetches one item at a time and can run
// for minutes over a real library, so the base Radarr/Sonarr/qBittorrent
// generation ticking in the meantime (every RefreshInterval, or right
// after any removal) is routine, not exceptional. RefreshTMDB used to
// discard the entire pass — including every item that was successfully
// re-fetched — the moment that happened, which meant an item's data could
// go indefinitely stale despite the enrichment task running exactly on
// schedule, since nothing else ever re-fetches an already-enriched item.
func TestRefreshTMDBDoesNotDiscardResultsWhenBaseGenerationMovesOnMidPass(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // held open until the test has moved the generation on
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"popularity":12.5,"vote_average":7.0,"vote_count":200}`))
	}))
	defer server.Close()

	service := New(config.Config{}, nil)
	service.cfg.TMDB.APIKey = "fake-key"
	service.tmdb = tmdb.NewWithBaseURL("fake-key", server.URL)
	service.reliability.Inventory = true
	service.generation = 1
	service.items = []model.Media{{Type: model.Movie, SourceID: 1, TMDBID: 42}}

	done := make(chan error, 1)
	go func() { done <- service.RefreshTMDB(context.Background()) }()

	// Give RefreshTMDB time to snapshot base and start the (blocked) fetch
	// before simulating a concurrent base refresh moving the generation on.
	time.Sleep(50 * time.Millisecond)
	service.mu.Lock()
	service.generation = 2
	service.items = append(cloneMedia(service.items), model.Media{Type: model.Movie, SourceID: 2, Title: "New Arrival"})
	service.mu.Unlock()
	close(release)

	if err := <-done; err != nil {
		t.Fatalf("expected the pass to complete despite the generation moving on, got %v", err)
	}

	service.mu.RLock()
	defer service.mu.RUnlock()
	var foundOriginal, foundNew bool
	for _, m := range service.items {
		switch m.SourceID {
		case 1:
			foundOriginal = true
			if m.Popularity != 12.5 || m.TMDBEnrichedAt.IsZero() {
				t.Fatalf("expected the successfully-fetched item to be merged despite the generation change, got %#v", m)
			}
		case 2:
			if m.Title == "New Arrival" {
				foundNew = true
			}
		}
	}
	if !foundOriginal {
		t.Fatal("expected the original item to still be present")
	}
	if !foundNew {
		t.Fatal("expected the item added during the pass to survive the merge too")
	}
}
