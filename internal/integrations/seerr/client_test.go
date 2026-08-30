package seerr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connarr/internal/model"
)

func TestApplyDoesNotCrossMovieAndSeriesProviderNamespaces(t *testing.T) {
	requested := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pageInfo": map[string]any{"pages": 1},
			"results": []any{map[string]any{
				"createdAt": requested,
				"media":     map[string]any{"mediaType": "movie", "tmdbId": 42},
			}},
		})
	}))
	defer srv.Close()
	items := []model.Media{{Type: model.Movie, TMDBID: 42}, {Type: model.Series, TMDBID: 42}}
	if err := New(srv.URL, "key").Apply(items); err != nil {
		t.Fatal(err)
	}
	if !items[0].Requested || items[1].Requested {
		t.Fatalf("provider namespace collision: %#v", items)
	}
}
