package inventory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"stewarr/internal/config"
	"stewarr/internal/integrations/jellyfin"
	"stewarr/internal/integrations/seerr"
	"stewarr/internal/integrations/tmdb"
	"stewarr/internal/model"
)

// TestEnrichNewMediaFetchesAllApplicableSourcesForANewItem guards the
// actual point of the redesign: a genuinely new item gets Jellyfin, Seerr,
// and TMDB facts fetched atomically, in one immediate pass, rather than
// waiting for each source's own periodic reconcile task — the fix for
// data that could otherwise stay reported "stale" for up to a full day
// (TMDB's interval) after nothing more than an ordinary new import.
func TestEnrichNewMediaFetchesAllApplicableSourcesForANewItem(t *testing.T) {
	jellyfinServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/Users" {
			_, _ = w.Write([]byte(`[{"Id":"u1","Policy":{"IsDisabled":false}}]`))
			return
		}
		// Every /Users/{id}/Items page: report nothing played/favorited.
		_, _ = w.Write([]byte(`{"Items":[]}`))
	}))
	defer jellyfinServer.Close()

	seerrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer seerrServer.Close()

	tmdbServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/movie/") {
			_, _ = w.Write([]byte(`{"popularity":12.5,"vote_average":7.0,"vote_count":200}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer tmdbServer.Close()

	var cfg config.Config
	cfg.Jellyfin.URL = jellyfinServer.URL
	cfg.Jellyfin.APIKey = "key"
	cfg.Seerr.URL = seerrServer.URL
	cfg.Seerr.APIKey = "key"
	cfg.TMDB.APIKey = "key"

	service := New(cfg, nil)
	service.jf = jellyfin.New(jellyfinServer.URL, "key")
	service.seerr = seerr.New(seerrServer.URL, "key")
	service.tmdb = tmdb.NewWithBaseURL("key", tmdbServer.URL)
	service.reliability.Inventory = true
	service.generation = 1
	service.items = []model.Media{{Type: model.Movie, SourceID: 1, TMDBID: 42}}

	if err := service.EnrichNewMedia(context.Background()); err != nil {
		t.Fatal(err)
	}

	service.mu.RLock()
	defer service.mu.RUnlock()
	item := service.items[0]
	if item.TMDBRating != 7.0 || item.Popularity != 12.5 || item.TMDBEnrichedAt.IsZero() {
		t.Fatalf("expected TMDB facts to be fetched immediately, got %+v", item)
	}
	if item.JellyfinEnrichedAt.IsZero() {
		t.Fatalf("expected Jellyfin to be checked immediately (even with no match), got %+v", item)
	}
	if item.SeerrEnrichedAt.IsZero() {
		t.Fatalf("expected Seerr to be checked immediately (even with no match), got %+v", item)
	}
}

// TestEnrichNewMediaSkipsSourcesAlreadyChecked guards the selection logic
// itself: an item that's already been checked by every source must
// trigger no work at all.
func TestEnrichNewMediaSkipsSourcesAlreadyChecked(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	var cfg config.Config
	cfg.TMDB.APIKey = "key"

	service := New(cfg, nil)
	service.tmdb = tmdb.NewWithBaseURL("key", server.URL)
	service.reliability.Inventory = true
	service.generation = 1
	service.items = []model.Media{{Type: model.Movie, SourceID: 1, TMDBID: 42, TMDBEnrichedAt: time.Now()}}

	if err := service.EnrichNewMedia(context.Background()); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("expected no request at all for an item already checked by every configured source")
	}
}
