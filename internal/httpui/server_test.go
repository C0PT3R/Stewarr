package httpui

import (
	"bytes"
	"spartarr/internal/model"
	"testing"
)

func TestLibraryTemplateRenders(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	data := libraryData{
		Rows: []mediaRow{{Media: model.Media{Type: model.Movie, SourceID: 1, Title: "Test", Year: 2026, Strength: 12.5, SizeBytes: 1024}, Rank: 1}},
		Page: 1, PageSize: 50, TotalPages: 1, TotalItems: 1, Sort: "rank", Order: "asc",
		SortURLs:  map[string]string{"rank": "/library", "strength": "/library", "title": "/library", "type": "/library", "rating": "/library", "votes": "/library", "views": "/library", "lastwatched": "/library", "requested": "/library", "size": "/library", "torrents": "/library"},
		SizeLinks: []navLink{{Value: 25, URL: "/library"}, {Value: 50, URL: "/library"}, {Value: 100, URL: "/library"}, {Value: 250, URL: "/library"}},
	}
	var b bytes.Buffer
	if err := s.libraryTpl.Execute(&b, data); err != nil {
		t.Fatal(err)
	}
}

func TestTorrentTemplateRendersPaged(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	data := torrentData{Torrents: []model.Torrent{{Hash: "abc", Name: "Torrent", AssociationStatus: "ASSOCIATED"}}, TotalItems: 1, Page: 1, PageSize: 50, TotalPages: 1, Sort: "status", Order: "asc", SortURLs: map[string]string{"status": "/torrents", "name": "/torrents", "media": "/torrents", "state": "/torrents", "size": "/torrents", "ratio": "/torrents", "upload": "/torrents", "seeds": "/torrents", "leechers": "/torrents", "activity": "/torrents"}, SizeLinks: []navLink{{Value: 50, URL: "/torrents"}}}
	var b bytes.Buffer
	if err := s.torrentTpl.Execute(&b, data); err != nil {
		t.Fatal(err)
	}
}
