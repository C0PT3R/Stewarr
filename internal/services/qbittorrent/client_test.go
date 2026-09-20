package qbittorrent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"stewarr/internal/model"
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

func TestTorrentFileRoot(t *testing.T) {
	// Still downloading with a separate incomplete-downloads path
	// configured: the real files sit under ContentPath's parent, not
	// SavePath (the eventual final destination once complete).
	incomplete := model.Torrent{SavePath: "/data/complete", ContentPath: "/data/incomplete/Movie"}
	if got := TorrentFileRoot(incomplete); got != "/data/incomplete" {
		t.Fatalf("expected ContentPath's parent for an incomplete download, got %q", got)
	}
	// Complete, or single-file torrent where ContentPath already agrees
	// with SavePath: either source gives the same answer.
	complete := model.Torrent{SavePath: "/data/complete", ContentPath: "/data/complete/Movie"}
	if got := TorrentFileRoot(complete); got != "/data/complete" {
		t.Fatalf("expected SavePath-equivalent when ContentPath agrees, got %q", got)
	}
	// No ContentPath at all (stale/partial metadata) falls back to SavePath.
	noContentPath := model.Torrent{SavePath: "/data/complete"}
	if got := TorrentFileRoot(noContentPath); got != "/data/complete" {
		t.Fatalf("expected SavePath fallback when ContentPath is empty, got %q", got)
	}
}

// TestClaimedFilesUsesContentPathForIncompleteDownloads guards the actual
// bug: a torrent still downloading into a separate incomplete-downloads
// directory has its real files there, not under SavePath — using SavePath
// unconditionally meant those real, in-progress files never matched the
// reconstructed claim path, so Stewarr silently treated live download data
// as Unmanaged (and VerifyPathsUnmanaged's ownership safety check missed
// it too).
func TestClaimedFilesUsesContentPathForIncompleteDownloads(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/files" {
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]any{map[string]any{"index": 0, "name": "Movie/file.mkv", "size": 123}})
	}))
	defer srv.Close()
	client := New("qBittorrent", srv.URL, "", "", "token")
	claimed, roots, err := client.ClaimedFiles(map[string]model.Torrent{
		"abc": {Hash: "abc", SavePath: "/data/complete", ContentPath: "/data/incomplete/Movie"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !claimed["/data/incomplete/Movie/file.mkv"] {
		t.Fatalf("expected the file's real (incomplete-download) path to be claimed, got %#v", claimed)
	}
	if claimed["/data/complete/Movie/file.mkv"] {
		t.Fatalf("expected the never-reached final SavePath not to be claimed, got %#v", claimed)
	}
	if len(roots) != 1 || roots[0] != "/data/incomplete" {
		t.Fatalf("expected the incomplete-downloads directory reported as the root, got %#v", roots)
	}
}

// TestValidateAcceptsA204LoginResponseWithSIDCookie guards a real
// production failure: some qBittorrent deployments respond to a successful
// /api/v2/auth/login with 204 and an empty body instead of the documented
// 200 "Ok." — login() must accept this since the actual authentication
// signal is the SID session cookie, not one specific body string.
func TestValidateAcceptsA204EmptyBodyLoginResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/auth/login" {
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
		http.SetCookie(w, &http.Cookie{Name: "SID", Value: "abc123"})
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	if err := New("qBittorrent", srv.URL, "user", "pass", "").Validate(); err != nil {
		t.Fatalf("expected a 204 login with a SID cookie to be accepted, got %v", err)
	}
}

// TestValidateAcceptsA204EmptyBodyNoCookieLoginResponse guards a real
// production case the SID-cookie-based version of this fix still missed:
// a qBittorrent instance with "bypass authentication for whitelisted IPs"
// enabled never hands back a session cookie at all — every request from
// that IP is auto-authorized regardless — yet a successful login attempt
// still isn't an error.
func TestValidateAcceptsA204EmptyBodyNoCookieLoginResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/auth/login" {
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	if err := New("qBittorrent", srv.URL, "user", "pass", "").Validate(); err != nil {
		t.Fatalf("expected a 204 login with no cookie (auth bypass) to be accepted, got %v", err)
	}
}

// TestValidateRejectsFailedLogin guards against the above permissive
// 2xx-is-success rule accidentally accepting wrong credentials: the one
// case qBittorrent's API actually documents as a failure is a 200 response
// with body exactly "Fails.", which must still be rejected.
func TestValidateRejectsFailedLogin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			_, _ = w.Write([]byte("Fails."))
			return
		}
		t.Fatalf("unexpected request: %s", r.URL.Path)
	}))
	defer srv.Close()
	if err := New("qBittorrent", srv.URL, "user", "wrong", "").Validate(); err == nil {
		t.Fatal("expected a failed login (body \"Fails.\") to be rejected")
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

func TestStorageRootsIncludesIncompleteDownloadsFromPreferences(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/app/preferences" {
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"save_path":         "/data/complete",
			"temp_path":         "/data/incomplete",
			"temp_path_enabled": true,
		})
	}))
	defer srv.Close()
	client := New("qBittorrent", srv.URL, "", "", "token")
	roots, err := client.StorageRoots(nil)
	if err != nil {
		t.Fatal(err)
	}
	var foundIncomplete, foundSave bool
	for _, r := range roots {
		if r.Path == "/data/incomplete" {
			foundIncomplete = true
			if r.Purpose != RootPurposeIncompleteDownloads {
				t.Fatalf("expected the temp_path root to carry RootPurposeIncompleteDownloads, got %#v", r)
			}
		}
		if r.Path == "/data/complete" {
			foundSave = true
			if r.Purpose != "" {
				t.Fatalf("expected the save_path root to have no purpose, got %#v", r)
			}
		}
	}
	if !foundIncomplete || !foundSave {
		t.Fatalf("expected both save_path and temp_path roots, got %#v", roots)
	}
}

func TestStorageRootsOmitsIncompleteDownloadsWhenDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"save_path":         "/data/complete",
			"temp_path":         "/data/incomplete",
			"temp_path_enabled": false,
		})
	}))
	defer srv.Close()
	client := New("qBittorrent", srv.URL, "", "", "token")
	roots, err := client.StorageRoots(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range roots {
		if r.Path == "/data/incomplete" {
			t.Fatalf("expected no incomplete-downloads root when temp_path_enabled is false, got %#v", roots)
		}
	}
}

func TestStorageRootsReactivelyPicksUpContentPathEvenWithoutTempPathPreference(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"save_path": "/data/complete"})
	}))
	defer srv.Close()
	client := New("qBittorrent", srv.URL, "", "", "token")
	roots, err := client.StorageRoots(map[string]model.Torrent{
		"abc": {Hash: "abc", SavePath: "/data/complete", ContentPath: "/data/incomplete/abc-folder"},
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range roots {
		if r.Path == "/data/incomplete/abc-folder" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the torrent's live ContentPath to be discovered as a root, got %#v", roots)
	}
}
