package qbittorrent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCurrentClaimedFilesDiscoversTorrentNotPresentInCachedState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"hash": "new", "name": "New", "save_path": "/data/downloads"}})
		case "/api/v2/torrents/files":
			if r.URL.Query().Get("hash") != "new" {
				t.Errorf("unexpected hash: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"index": 0, "name": "new/file.mkv", "size": 123}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	claimed, err := New("qBittorrent", srv.URL, "", "", "token").CurrentClaimedFiles()
	if err != nil {
		t.Fatal(err)
	}
	if !claimed["/data/downloads/new/file.mkv"] {
		t.Fatalf("fresh torrent claim was missed: %#v", claimed)
	}
}

func TestVerifyPathsUnclaimedUsesFreshContentPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"hash": "new", "name": "New", "save_path": "/data/downloads", "content_path": "/data/downloads/new"}})
		case "/api/v2/torrents/files":
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"index": 0, "name": "new/file.mkv", "size": 123}})
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	err := New("qBittorrent", srv.URL, "", "", "token").VerifyPathsUnclaimed([]string{"/data/downloads/new/file.mkv"})
	if err == nil {
		t.Fatal("fresh torrent content path was not treated as an owner")
	}
}
