package imdb

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

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

func TestFetchRatingsParsesRowsAndSkipsHeader(t *testing.T) {
	body := gzipTSV(t, "tconst\taverageRating\tnumVotes\ntt0000001\t5.7\t2000\ntt0000002\t7.3\t145000\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	ratings, err := NewWithDatasetURL(srv.URL).FetchRatings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ratings) != 2 {
		t.Fatalf("expected 2 ratings, got %#v", ratings)
	}
	if ratings[0] != (Rating{Tconst: "tt0000001", Value: 5.7, Votes: 2000}) {
		t.Fatalf("unexpected first rating: %#v", ratings[0])
	}
	if ratings[1] != (Rating{Tconst: "tt0000002", Value: 7.3, Votes: 145000}) {
		t.Fatalf("unexpected second rating: %#v", ratings[1])
	}
}

// TestFetchRatingsSkipsMalformedRowsWithoutFailing guards the actual point
// of per-row tolerance: one bad line (wrong field count, non-numeric
// value) must not discard every other title's real data.
func TestFetchRatingsSkipsMalformedRowsWithoutFailing(t *testing.T) {
	body := gzipTSV(t, "tconst\taverageRating\tnumVotes\ntt0000001\t5.7\t2000\nnot enough fields\ntt0000002\tnot-a-number\t10\ntt0000003\t6.1\t500\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	ratings, err := NewWithDatasetURL(srv.URL).FetchRatings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ratings) != 2 {
		t.Fatalf("expected 2 valid ratings (bad rows skipped), got %#v", ratings)
	}
	if ratings[0].Tconst != "tt0000001" || ratings[1].Tconst != "tt0000003" {
		t.Fatalf("unexpected ratings: %#v", ratings)
	}
}

func TestFetchRatingsFailsOnNon2xxStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if _, err := NewWithDatasetURL(srv.URL).FetchRatings(context.Background()); err == nil {
		t.Fatal("expected an error for a non-2xx response")
	}
}
