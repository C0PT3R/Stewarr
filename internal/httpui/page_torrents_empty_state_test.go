package httpui

import (
	"net/http/httptest"
	"strings"
	"testing"

	"connarr/internal/model"
)

// TestTorrentsTemplateShowsEmptyStateWithoutTorrentClient guards the fix
// for a real gap: with no torrent-client-type service configured, the
// Torrents page rendered an empty table with a generic "no torrents match
// these filters" row instead of telling the user why — there's nothing to
// filter because nothing produces torrents at all.
func TestTorrentsTemplateShowsEmptyStateWithoutTorrentClient(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := torrentData{HasTorrentClient: false}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.torrentTpl, data); err != nil {
		t.Fatalf("render torrents template: %v", err)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "No torrent client registered") {
		t.Fatalf("expected the empty-state message, got:\n%s", body)
	}
	if !strings.Contains(body, `data-overlay-url="/services/add?category=torrentclient"`) || !strings.Contains(body, "Add torrent client") {
		t.Fatalf("expected an Add torrent client button scoped to the torrentclient category, got:\n%s", body)
	}
	for _, mustNotContain := range []string{"<table>", "No torrents match these filters"} {
		if strings.Contains(body, mustNotContain) {
			t.Fatalf("expected no table/filter-empty markup when no torrent client is configured, found %q in:\n%s", mustNotContain, body)
		}
	}
}

func TestTorrentsTemplateShowsListWithTorrentClient(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := torrentData{
		HasTorrentClient: true,
		Torrents:         []model.Torrent{{Hash: "abc", Name: "Torrent", AssociationStatus: "ASSOCIATED"}},
		TotalItems:       1, Page: 1, PageSize: 50, TotalPages: 1, Sort: "status", Order: "asc",
		SortURLs:  map[string]string{"status": "/torrents", "name": "/torrents", "media": "/torrents", "state": "/torrents", "size": "/torrents", "ratio": "/torrents", "upload": "/torrents", "seeds": "/torrents", "leechers": "/torrents", "activity": "/torrents"},
		SizeLinks: []navLink{{Value: 50, URL: "/torrents"}},
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.torrentTpl, data); err != nil {
		t.Fatalf("render torrents template: %v", err)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "No torrent client registered") {
		t.Fatalf("expected no empty-state message when a torrent client is configured, got:\n%s", body)
	}
	if !strings.Contains(body, "<table>") || !strings.Contains(body, "Torrent") {
		t.Fatalf("expected the normal torrent table, got:\n%s", body)
	}
}
