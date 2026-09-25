package store

import (
	"path/filepath"
	"testing"
	"time"

	"stewarr/internal/services/imdb"
)

func TestIMDbRatingsRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "stewarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	before := time.Now().UTC()
	if err := db.ReplaceIMDbRatings([]imdb.Rating{
		{Tconst: "tt0000001", Value: 5.7, Votes: 2000},
		{Tconst: "tt0000002", Value: 7.3, Votes: 145000},
	}); err != nil {
		t.Fatal(err)
	}

	got, updatedAt, err := db.LoadIMDbRatings()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 ratings, got %#v", got)
	}
	if got["tt0000002"] != (imdb.Rating{Tconst: "tt0000002", Value: 7.3, Votes: 145000}) {
		t.Fatalf("unexpected rating for tt0000002: %#v", got["tt0000002"])
	}
	if updatedAt.Before(before) {
		t.Fatalf("expected updatedAt to reflect the replace, got %v (before %v)", updatedAt, before)
	}
}

// TestIMDbRatingsReplaceClearsPreviousSnapshot guards the actual point of
// a full-replace refresh: a second daily fetch that no longer includes a
// title (removed/renamed upstream) must not leave that title's stale
// rating behind.
func TestIMDbRatingsReplaceClearsPreviousSnapshot(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "stewarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.ReplaceIMDbRatings([]imdb.Rating{{Tconst: "tt0000001", Value: 5.7, Votes: 2000}}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceIMDbRatings([]imdb.Rating{{Tconst: "tt0000002", Value: 7.3, Votes: 145000}}); err != nil {
		t.Fatal(err)
	}

	got, _, err := db.LoadIMDbRatings()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected only the second snapshot's row to remain, got %#v", got)
	}
	if _, stale := got["tt0000001"]; stale {
		t.Fatalf("expected the first snapshot's row to be gone, got %#v", got)
	}
}
