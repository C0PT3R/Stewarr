package qbittorrent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"connarr/internal/model"
)

func TestVerifyPathsUnmanagedUsesFreshContentPath(t *testing.T) {
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
	err := New("qBittorrent", srv.URL, "", "", "token").VerifyPathsUnmanaged([]string{"/data/downloads/new/file.mkv"})
	if err == nil {
		t.Fatal("fresh torrent content path was not treated as an owner")
	}
}

func TestAllFilesToleratesOneTorrentRemovedDirectlyInQBittorrent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/files" {
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
		if r.URL.Query().Get("hash") == "gone" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("Not Found"))
			return
		}
		_ = json.NewEncoder(w).Encode([]any{map[string]any{"index": 0, "name": "present/file.mkv", "size": 123}})
	}))
	defer srv.Close()
	client := New("qBittorrent", srv.URL, "", "", "token")
	out, err := client.AllFiles(map[string]model.Torrent{
		"gone":    {Hash: "gone"},
		"present": {Hash: "present"},
	})
	if err != nil {
		t.Fatalf("a single removed torrent must not fail the whole batch: %v", err)
	}
	if _, ok := out["gone"]; ok {
		t.Fatalf("expected no entry for the removed torrent, got %#v", out["gone"])
	}
	if len(out["present"]) != 1 || out["present"][0].Name != "present/file.mkv" {
		t.Fatalf("expected the still-present torrent's files to be returned, got %#v", out["present"])
	}
}
