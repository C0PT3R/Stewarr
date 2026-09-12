package tmdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"stewarr/internal/model"
)

func TestApplyEnrichesMovieDirectlyByTMDBID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/movie/42" {
			t.Fatalf("unexpected request: %s", request.URL.Path)
		}
		if request.URL.Query().Get("api_key") != "key" {
			t.Fatalf("missing api_key: %s", request.URL.RawQuery)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"popularity":123.4,"vote_average":7.8,"vote_count":900}`))
	}))
	defer server.Close()

	client := &Client{apiKey: "key", base: server.URL, hc: server.Client()}
	items := []model.Media{{Type: model.Movie, TMDBID: 42}}
	if err := client.Apply(items); err != nil {
		t.Fatal(err)
	}
	if items[0].TMDBRating != 7.8 || items[0].TMDBVoteCount != 900 || items[0].Popularity != 123.4 {
		t.Fatalf("unexpected enrichment: %#v", items[0])
	}
	if items[0].TMDBEnrichedAt.IsZero() || time.Since(items[0].TMDBEnrichedAt) > time.Minute {
		t.Fatalf("expected TMDBEnrichedAt to be set to roughly now, got %v", items[0].TMDBEnrichedAt)
	}
}

// TestApplyResolvesSeriesTMDBIDFromTVDBID guards the one extra step a series
// needs: Sonarr identifies it by TVDB id, not TMDB id, so Apply must resolve
// that through TMDB's own /find endpoint before it can fetch TV details —
// and it must cache the resolved id back onto the item (TMDBID) so this
// lookup only ever needs to happen once per series.
func TestApplyResolvesSeriesTMDBIDFromTVDBID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/find/99":
			if request.URL.Query().Get("external_source") != "tvdb_id" {
				t.Fatalf("expected external_source=tvdb_id, got %s", request.URL.RawQuery)
			}
			_, _ = response.Write([]byte(`{"tv_results":[{"id":55}]}`))
		case "/tv/55":
			_, _ = response.Write([]byte(`{"popularity":50.0,"vote_average":8.1,"vote_count":300}`))
		default:
			t.Fatalf("unexpected request: %s", request.URL.Path)
		}
	}))
	defer server.Close()

	client := &Client{apiKey: "key", base: server.URL, hc: server.Client()}
	items := []model.Media{{Type: model.Series, TVDBID: 99}}
	if err := client.Apply(items); err != nil {
		t.Fatal(err)
	}
	if items[0].TMDBID != 55 {
		t.Fatalf("expected the resolved TMDB id to be cached onto the item, got %d", items[0].TMDBID)
	}
	if items[0].TMDBRating != 8.1 || items[0].TMDBVoteCount != 300 || items[0].Popularity != 50.0 {
		t.Fatalf("unexpected enrichment: %#v", items[0])
	}
}

// TestApplySkipsResolutionWhenTMDBIDAlreadyKnown guards the actual point
// of caching a series' resolved TMDBID (see preserveTMDBFacts in
// internal/inventory/service.go, which carries it forward across every
// base refresh): once known, Apply must never repeat the TVDB->TMDB
// /find lookup — a series needing two TMDB queries every single pass,
// forever, would be pure waste. The server here fails the test outright
// on any /find request at all.
func TestApplySkipsResolutionWhenTMDBIDAlreadyKnown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/tv/55" {
			t.Fatalf("expected only /tv/55, no re-resolution, got: %s", request.URL.Path)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"popularity":50.0,"vote_average":8.1,"vote_count":300}`))
	}))
	defer server.Close()

	client := &Client{apiKey: "key", base: server.URL, hc: server.Client()}
	items := []model.Media{{Type: model.Series, TVDBID: 99, TMDBID: 55}}
	if err := client.Apply(items); err != nil {
		t.Fatal(err)
	}
	if items[0].TMDBID != 55 {
		t.Fatalf("expected the already-known id to be left as-is, got %d", items[0].TMDBID)
	}
	if items[0].TMDBRating != 8.1 || items[0].TMDBVoteCount != 300 || items[0].Popularity != 50.0 {
		t.Fatalf("unexpected enrichment: %#v", items[0])
	}
}

// TestApplyLeavesItemUnenrichedWithoutFailingTheWholePass guards a real
// resilience requirement: one item having no TMDB match (or a transient
// error) must not abort enrichment for every other item in the batch.
func TestApplyLeavesItemUnenrichedWithoutFailingTheWholePass(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/find/1":
			_, _ = response.Write([]byte(`{"tv_results":[]}`))
		case "/movie/7":
			_, _ = response.Write([]byte(`{"popularity":10,"vote_average":5,"vote_count":20}`))
		default:
			t.Fatalf("unexpected request: %s", request.URL.Path)
		}
	}))
	defer server.Close()

	client := &Client{apiKey: "key", base: server.URL, hc: server.Client()}
	items := []model.Media{
		{Type: model.Series, TVDBID: 1},
		{Type: model.Movie, TMDBID: 7},
	}
	if err := client.Apply(items); err != nil {
		t.Fatal(err)
	}
	if items[0].TMDBRating != 0 || items[0].Popularity != 0 {
		t.Fatalf("expected the unmatched series to stay unenriched, got %#v", items[0])
	}
	if items[1].TMDBRating != 5 || items[1].Popularity != 10 {
		t.Fatalf("expected the matched movie to still be enriched, got %#v", items[1])
	}
}

func TestApplyIsNoOpWithoutAnAPIKey(t *testing.T) {
	client := &Client{}
	items := []model.Media{{Type: model.Movie, TMDBID: 42}}
	if err := client.Apply(items); err != nil {
		t.Fatal(err)
	}
	if items[0].TMDBRating != 0 {
		t.Fatalf("expected no enrichment without an api key, got %#v", items[0])
	}
}

// TestApplySurfacesContextCancellationInsteadOfSwallowingIt guards a real
// bug: a per-item fetch failure (bad id, transient network error) is
// deliberately tolerated and never fails the whole pass — but the whole
// pass being cancelled out from under it (e.g. the caller's task yielding
// to higher-priority work) is a different situation entirely, and must be
// surfaced rather than swallowed the same way. Callers (RefreshTMDB, and
// beyond it the task scheduler) rely on errors.Is(err, context.Canceled)
// to tell an interruption apart from a genuine failure and react
// differently — a plain nil here would silently misreport a cut-short
// pass as a full success.
func TestApplySurfacesContextCancellationInsteadOfSwallowingIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"popularity":1,"vote_average":1,"vote_count":1}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := (&Client{apiKey: "key", base: server.URL, hc: server.Client()}).WithContext(ctx)
	items := []model.Media{{Type: model.Movie, TMDBID: 42}}
	err := client.Apply(items)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected Apply to surface context.Canceled instead of swallowing it, got %v", err)
	}
}

func TestValidateIsNoOpWithoutAnAPIKey(t *testing.T) {
	client := &Client{}
	if err := client.Validate(); err != nil {
		t.Fatal(err)
	}
}
