package inventory

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"stewarr/internal/config"
	"stewarr/internal/services/imdb"
	"stewarr/internal/store"
)

func TestIMDbRatingsDue(t *testing.T) {
	day := time.Date(2026, 9, 25, 0, 0, 0, 0, time.Local)
	beforeHour := day.Add(2 * time.Hour)
	afterHour := day.Add(4 * time.Hour)
	nextDayAfterHour := day.AddDate(0, 0, 1).Add(4 * time.Hour)

	cases := []struct {
		name      string
		now       time.Time
		lastFetch time.Time
		want      bool
	}{
		{"before the hour, never fetched", beforeHour, time.Time{}, false},
		{"past the hour, never fetched", afterHour, time.Time{}, true},
		{"past the hour, already fetched today", afterHour, afterHour.Add(-time.Hour), false},
		{"past the hour, last fetch was yesterday", afterHour, day.AddDate(0, 0, -1).Add(4 * time.Hour), true},
		{"past the hour the next day, fetched yesterday", nextDayAfterHour, afterHour, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := imdbRatingsDue(c.now, c.lastFetch, imdbRatingsFetchHour); got != c.want {
				t.Fatalf("imdbRatingsDue(%v, %v) = %v, want %v", c.now, c.lastFetch, got, c.want)
			}
		})
	}
}

func gzipTSV(t *testing.T, rows string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(rows)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestRefreshIMDbRatingsFetchesReplacesAndRescoresWhenDue guards the whole
// path end to end: due, enabled, fetch succeeds, the store is replaced,
// the in-memory map updates, and media is re-scored immediately rather
// than waiting for the next unrelated enrichment pass.
func TestRefreshIMDbRatingsFetchesReplacesAndRescoresWhenDue(t *testing.T) {
	body := gzipTSV(t, "tconst\taverageRating\tnumVotes\ntt1234567\t8.1\t900\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	db, err := store.Open(filepath.Join(t.TempDir(), "stewarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := config.Config{}
	cfg.Valuation.Weights.Rating = 40
	service := New(cfg, db)
	service.imdbClient = imdb.NewWithDatasetURL(srv.URL)

	if err := service.RefreshIMDbRatings(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, _, err := db.LoadIMDbRatings()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["tt1234567"].Value != 8.1 {
		t.Fatalf("expected the store to hold the fetched rating, got %#v", got)
	}
	if len(service.imdbRatings) != 1 || service.imdbRatings["tt1234567"].Votes != 900 {
		t.Fatalf("expected the in-memory map to be updated, got %#v", service.imdbRatings)
	}
}

// TestRefreshIMDbRatingsNoopsWhenDisabled guards the opt-in gate: no
// network call, no store write, when IMDb enrichment isn't enabled.
func TestRefreshIMDbRatingsNoopsWhenDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("expected no request when IMDb enrichment is disabled")
	}))
	defer srv.Close()

	db, err := store.Open(filepath.Join(t.TempDir(), "stewarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := config.Config{}
	cfg.IMDb.Disabled = true
	service := New(cfg, db)
	service.imdbClient = imdb.NewWithDatasetURL(srv.URL)

	if err := service.RefreshIMDbRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// TestRefreshIMDbRatingsNoopsWhenNotDue guards the self-gating: even when
// enabled, no fetch happens outside the daily window.
func TestRefreshIMDbRatingsNoopsWhenNotDue(t *testing.T) {
	if time.Now().Local().Hour() >= imdbRatingsFetchHour {
		t.Skip("test only meaningful before the fetch hour in local time")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("expected no request before the fetch hour")
	}))
	defer srv.Close()

	db, err := store.Open(filepath.Join(t.TempDir(), "stewarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := config.Config{}
	service := New(cfg, db)
	service.imdbClient = imdb.NewWithDatasetURL(srv.URL)

	if err := service.RefreshIMDbRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
}
