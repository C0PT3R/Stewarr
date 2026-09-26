package radarr

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAddKeepTagCreatesTagWhenMissing guards the resolve-or-create path:
// no matching tag exists yet, so it must be created before the movie is
// tagged with the id that creation returns.
func TestAddKeepTagCreatesTagWhenMissing(t *testing.T) {
	tagCreated := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/tag":
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"id": 1, "label": "keep"}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/tag":
			body, _ := io.ReadAll(r.Body)
			var payload map[string]string
			_ = json.Unmarshal(body, &payload)
			if payload["label"] != "stewarr_keep" {
				t.Fatalf("unexpected create-tag payload: %#v", payload)
			}
			tagCreated = true
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "label": "stewarr_keep"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie/42":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 42, "tags": []any{1}})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v3/movie/42":
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			tags, _ := payload["tags"].([]any)
			if len(tags) != 2 || tags[0] != float64(1) || tags[1] != float64(7) {
				t.Fatalf("expected tags [1,7], got %#v", tags)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	if err := New(server.URL, "key").AddKeepTag(42, "stewarr_keep"); err != nil {
		t.Fatal(err)
	}
	if !tagCreated {
		t.Fatal("expected the missing tag to be created")
	}
}

// TestAddKeepTagReusesExistingTagAndSkipsDuplicate guards both remaining
// cases in one pass: an existing tag is reused (no create call), and a
// movie that already carries it is left untouched (no PUT at all).
func TestAddKeepTagReusesExistingTagAndSkipsDuplicate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/tag":
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"id": 7, "label": "stewarr_keep"}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/tag":
			t.Fatal("expected no tag creation when one already exists")
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie/42":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 42, "tags": []any{7}})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v3/movie/42":
			t.Fatal("expected no update when the movie already carries the tag")
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	if err := New(server.URL, "key").AddKeepTag(42, "stewarr_keep"); err != nil {
		t.Fatal(err)
	}
}

func TestAddImportListExclusion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v3/exclusions" {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["movieTitle"] != "Movie" || payload["tmdbId"] != float64(42) || payload["movieYear"] != float64(2026) {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		response.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	if err := New(server.URL, "key").AddImportListExclusion("Movie", 2026, 42); err != nil {
		t.Fatal(err)
	}
}

func TestFindQueueItemMatchesByDownloadID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/queue" {
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pageSize":     250,
			"totalRecords": 1,
			"records":      []any{map[string]any{"id": 7, "downloadId": "ABC123"}},
		})
	}))
	defer server.Close()
	id, ok, err := New(server.URL, "key").FindQueueItem("abc123")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || id != 7 {
		t.Fatalf("expected match id 7, got id=%d ok=%v", id, ok)
	}
}

func TestFindQueueItemNoMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pageSize":     250,
			"totalRecords": 1,
			"records":      []any{map[string]any{"id": 7, "downloadId": "OTHER"}},
		})
	}))
	defer server.Close()
	_, ok, err := New(server.URL, "key").FindQueueItem("abc123")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected no match")
	}
}

func TestRemoveQueueItemRemovesFromClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v3/queue/7" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("removeFromClient") != "true" {
			t.Fatalf("expected removeFromClient=true, got %q", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if err := New(server.URL, "key").RemoveQueueItem(7); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryFailsClosedWhenTagsAreUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/tag" {
			http.Error(w, "broken", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode([]any{})
	}))
	defer srv.Close()
	if _, err := New(srv.URL, "key").Inventory(); err == nil {
		t.Fatal("missing protection tags must make inventory unreliable")
	}
}
