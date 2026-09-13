package inventory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"stewarr/internal/config"
	"stewarr/internal/model"
	"stewarr/internal/services/jellyfin"
	"stewarr/internal/services/seerr"
)

// TestRefreshJellyfinDoesNotDiscardResultsWhenBaseGenerationMovesOnMidPass
// mirrors the same TMDB fix: a base refresh ticking while Jellyfin's own
// (potentially slow, multi-user, paginated) pass is still in flight is
// routine, not exceptional, and must not throw away a fully successful
// result.
func TestRefreshJellyfinDoesNotDiscardResultsWhenBaseGenerationMovesOnMidPass(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/Users" {
			_, _ = w.Write([]byte(`[{"Id":"u1","Policy":{"IsDisabled":false}}]`))
			return
		}
		<-release // held open until the test has moved the generation on
		_, _ = w.Write([]byte(`{"Items":[]}`))
	}))
	defer server.Close()

	service := New(config.Config{}, nil)
	service.cfg.Jellyfin.URL = server.URL
	service.jf = jellyfin.New(server.URL, "fake-key")
	service.reliability.Inventory = true
	service.generation = 1
	service.items = []model.Media{{Type: model.Movie, SourceID: 1}}

	done := make(chan error, 1)
	go func() { done <- service.RefreshJellyfin(context.Background()) }()

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
			if m.JellyfinEnrichedAt.IsZero() {
				t.Fatalf("expected the successfully-checked item to be merged despite the generation change, got %#v", m)
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

// TestRefreshSeerrDoesNotDiscardResultsWhenBaseGenerationMovesOnMidPass
// mirrors the same fix for Seerr's paginated request-list fetch.
func TestRefreshSeerrDoesNotDiscardResultsWhenBaseGenerationMovesOnMidPass(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/request") {
			<-release // held open until the test has moved the generation on
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	service := New(config.Config{}, nil)
	service.cfg.Seerr.URL = server.URL
	service.seerr = seerr.New(server.URL, "fake-key")
	service.reliability.Inventory = true
	service.generation = 1
	service.items = []model.Media{{Type: model.Movie, SourceID: 1}}

	done := make(chan error, 1)
	go func() { done <- service.RefreshSeerr(context.Background()) }()

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
			if m.SeerrEnrichedAt.IsZero() {
				t.Fatalf("expected the successfully-checked item to be merged despite the generation change, got %#v", m)
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
