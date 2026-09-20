package inventory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"stewarr/internal/config"
	"stewarr/internal/model"
)

// TestRemoveTorrentUsesRadarrQueueForIncompleteCurrentDownload guards the
// preferred removal path for an incomplete torrent Radarr is still waiting
// to import: RemoveTorrent should delete it through Radarr's queue (which
// itself instructs qBittorrent to remove the client-side download) instead
// of calling qBittorrent directly and leaving Radarr's queue entry stale.
func TestRemoveTorrentUsesRadarrQueueForIncompleteCurrentDownload(t *testing.T) {
	queueDeleted := false
	radarrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/queue":
			_, _ = w.Write([]byte(`{"pageSize":250,"totalRecords":1,"records":[{"id":42,"downloadId":"ABCHASH"}]}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v3/queue/42":
			if r.URL.Query().Get("removeFromClient") != "true" {
				t.Fatalf("expected removeFromClient=true, got %q", r.URL.RawQuery)
			}
			queueDeleted = true
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected radarr request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer radarrSrv.Close()

	qbDeleteCalled := false
	qbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/torrents/delete" {
			qbDeleteCalled = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer qbSrv.Close()

	cfg := config.Config{Services: []config.Service{
		{ID: "r1", Type: "radarr", Name: "Movies", URL: radarrSrv.URL},
		{ID: "q1", Type: "qbittorrent", Name: "qB", URL: qbSrv.URL},
	}}
	service := New(cfg, nil)
	service.torrents = []model.Torrent{{
		ServiceID:       "q1",
		Hash:            "abchash",
		AmountLeftBytes: 123,
		MediaItems:      []model.MediaRef{{Type: model.Movie, ServiceID: "r1", ServiceName: "Movies", SourceID: 1}},
	}}

	if err := service.RemoveTorrent(context.Background(), "abchash", "q1"); err != nil {
		t.Fatal(err)
	}
	if !queueDeleted {
		t.Fatal("expected the torrent to be removed through Radarr's queue")
	}
	if qbDeleteCalled {
		t.Fatal("expected qBittorrent not to be called directly when the queue path applies")
	}
}

// TestRemoveTorrentFallsBackToQBittorrentWithoutQueueEntry guards the other
// half: when there's no matching Radarr queue entry (already dropped, or
// the torrent has no current owning media item), removal must still fall
// back to deleting directly from qBittorrent rather than doing nothing.
func TestRemoveTorrentFallsBackToQBittorrentWithoutQueueEntry(t *testing.T) {
	qbDeleteCalled := false
	qbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/torrents/delete" {
			qbDeleteCalled = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer qbSrv.Close()

	cfg := config.Config{Services: []config.Service{
		{ID: "q1", Type: "qbittorrent", Name: "qB", URL: qbSrv.URL},
	}}
	service := New(cfg, nil)
	service.torrents = []model.Torrent{{
		ServiceID:       "q1",
		Hash:            "abchash",
		AmountLeftBytes: 123,
	}}

	if err := service.RemoveTorrent(context.Background(), "abchash", "q1"); err != nil {
		t.Fatal(err)
	}
	if !qbDeleteCalled {
		t.Fatal("expected fallback removal directly through qBittorrent")
	}
}

// TestRemoveTorrentSkipsQueuePathForCompleteTorrents guards that a
// completed torrent (no longer in anyone's download queue) goes straight
// to qBittorrent, without spending a request probing a queue that can't
// have anything relevant in it.
func TestRemoveTorrentSkipsQueuePathForCompleteTorrents(t *testing.T) {
	radarrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected radarr request for a complete torrent: %s", r.URL.Path)
	}))
	defer radarrSrv.Close()

	qbDeleteCalled := false
	qbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/torrents/delete" {
			qbDeleteCalled = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer qbSrv.Close()

	cfg := config.Config{Services: []config.Service{
		{ID: "r1", Type: "radarr", Name: "Movies", URL: radarrSrv.URL},
		{ID: "q1", Type: "qbittorrent", Name: "qB", URL: qbSrv.URL},
	}}
	service := New(cfg, nil)
	service.torrents = []model.Torrent{{
		ServiceID:       "q1",
		Hash:            "abchash",
		AmountLeftBytes: 0,
		MediaItems:      []model.MediaRef{{Type: model.Movie, ServiceID: "r1", ServiceName: "Movies", SourceID: 1}},
	}}

	if err := service.RemoveTorrent(context.Background(), "abchash", "q1"); err != nil {
		t.Fatal(err)
	}
	if !qbDeleteCalled {
		t.Fatal("expected removal directly through qBittorrent for a complete torrent")
	}
}
