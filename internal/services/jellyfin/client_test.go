package jellyfin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"stewarr/internal/model"
)

func TestApplyFetchesSparseUserSignals(t *testing.T) {
	var mu sync.Mutex
	queries := map[string]int{}
	playedAt := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Users":
			_ = json.NewEncoder(w).Encode([]any{
				map[string]any{"Id": "active", "Policy": map[string]any{"IsDisabled": false}},
				map[string]any{"Id": "disabled", "Policy": map[string]any{"IsDisabled": true}},
			})
		case "/Users/active/Items":
			filter := ""
			if r.URL.Query().Get("IsPlayed") == "true" {
				filter = "played"
			}
			if r.URL.Query().Get("IsFavorite") == "true" {
				filter = "favorite"
			}
			if filter == "" {
				t.Errorf("unfiltered catalogue request: %s", r.URL.RawQuery)
				http.Error(w, "missing sparse filter", http.StatusBadRequest)
				return
			}
			mu.Lock()
			queries[filter]++
			mu.Unlock()
			item := map[string]any{"Type": "Movie", "ProviderIds": map[string]string{"Tmdb": "42"}}
			if filter == "played" {
				item["UserData"] = map[string]any{"PlayCount": 3, "LastPlayedDate": playedAt}
			} else {
				item["UserData"] = map[string]any{"IsFavorite": true}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Items": []any{item}, "TotalRecordCount": 1})
		default:
			t.Errorf("unexpected Jellyfin request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	items := []model.Media{{Type: model.Movie, SourceID: 1, TMDBID: 42}}
	if err := New(srv.URL, "key").Apply(items); err != nil {
		t.Fatal(err)
	}
	if queries["played"] != 1 || queries["favorite"] != 1 {
		t.Fatalf("unexpected sparse queries: %#v", queries)
	}
	if items[0].Views != 3 || items[0].UniqueViewers != 1 || !items[0].Favorite || items[0].LastWatched == nil || !items[0].LastWatched.Equal(playedAt) {
		t.Fatalf("unexpected Jellyfin enrichment: %#v", items[0])
	}
}

func TestApplyDoesNotCrossMovieAndSeriesProviderNamespaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Users":
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"Id": "u", "Policy": map[string]any{"IsDisabled": false}}})
		case "/Users/u/Items":
			items := []any{}
			if r.URL.Query().Get("IsPlayed") == "true" {
				items = append(items, map[string]any{"Type": "Movie", "ProviderIds": map[string]string{"Tmdb": "42"}, "UserData": map[string]any{"PlayCount": 2}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Items": items, "TotalRecordCount": len(items)})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	items := []model.Media{{Type: model.Movie, TMDBID: 42}, {Type: model.Series, TMDBID: 42}}
	if err := New(srv.URL, "key").Apply(items); err != nil {
		t.Fatal(err)
	}
	if items[0].Views != 2 || items[1].Views != 0 {
		t.Fatalf("provider namespace collision: %#v", items)
	}
}

func TestApplyHonorsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- New(srv.URL, "key").WithContext(ctx).Apply(nil) }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected cancellation error")
		}
	case <-time.After(time.Second):
		t.Fatal("Jellyfin request ignored context cancellation")
	}
}
