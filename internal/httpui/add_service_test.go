package httpui

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"stewarr/internal/config"
	"stewarr/internal/inventory"
	"stewarr/internal/tasks"
)

// multipartServiceForm builds a request body shaped exactly like the
// real browser's fetch(url, {body: new FormData(form)}) — multipart, not
// urlencoded. A urlencoded body would mask the real bug this once shipped
// with: r.ParseForm alone never reads a multipart body, so every field came
// back empty and every add attempt failed with "type is required" even
// though the browser form was filled in correctly.
func multipartServiceForm(t *testing.T, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, writer.FormDataContentType()
}

func TestAddServiceEndToEndOverHTTP(t *testing.T) {
	radarrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"5.0.0"}`))
	}))
	defer radarrSrv.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	inv := inventory.New(cfg, nil)
	inv.SetConfigPath(configPath)
	server, err := New(inv, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()

	// The overlay fragment loads via GET.
	getRecorder := httptest.NewRecorder()
	getRequest := httptest.NewRequest(http.MethodGet, "/services/add", nil)
	handler.ServeHTTP(getRecorder, getRequest)
	if getRecorder.Code != http.StatusOK || !strings.Contains(getRecorder.Body.String(), "Add service") {
		t.Fatalf("expected the add-service overlay to render, got status=%d body=%q", getRecorder.Code, getRecorder.Body.String())
	}

	// Submitting the form (as a real browser would: multipart, via
	// fetch+FormData) adds, validates, and persists the service.
	body, contentType := multipartServiceForm(t, map[string]string{"type": "radarr", "name": "Movies", "url": radarrSrv.URL, "api_key": "key"})
	postRecorder := httptest.NewRecorder()
	postRequest := httptest.NewRequest(http.MethodPost, "/services/create", body)
	postRequest.Header.Set("Content-Type", contentType)
	handler.ServeHTTP(postRecorder, postRequest)
	if postRecorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204 on success, got status=%d body=%q", postRecorder.Code, postRecorder.Body.String())
	}

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Services) != 1 || reloaded.Services[0].Name != "Movies" {
		t.Fatalf("expected the service to be persisted, got %#v", reloaded.Services)
	}

	// The Services page now reflects it, without needing a restart.
	servicesRecorder := httptest.NewRecorder()
	servicesRequest := httptest.NewRequest(http.MethodGet, "/services", nil)
	handler.ServeHTTP(servicesRecorder, servicesRequest)
	if !strings.Contains(servicesRecorder.Body.String(), "Movies") {
		t.Fatalf("expected the Services page to show the newly added service live, got:\n%s", servicesRecorder.Body.String())
	}
}

// TestAddServiceSchedulesStorageDiscovery guards the whole reason live
// config editing exists: a newly added service's storage paths must be
// discovered promptly, not left to wait for the next periodic file
// reconciliation (which can be hours away). Adding a service must
// schedule the same inventory-then-files workflow a removal does.
func TestAddServiceSchedulesStorageDiscovery(t *testing.T) {
	radarrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"5.0.0"}`))
	}))
	defer radarrSrv.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	inv := inventory.New(cfg, nil)
	inv.SetConfigPath(configPath)

	manager := tasks.New(
		tasks.Definition{ID: "inventory", Name: "Inventory", Runner: func(context.Context) error { return nil }},
		tasks.Definition{ID: "files", Name: "Files", Runner: func(context.Context) error { return nil }},
	)
	if err := manager.RegisterWorkflow(tasks.WorkflowDefinition{ID: "inventory-and-files-consistency", Steps: []string{"inventory", "files"}}); err != nil {
		t.Fatal(err)
	}
	server, err := New(inv, manager)
	if err != nil {
		t.Fatal(err)
	}

	body, contentType := multipartServiceForm(t, map[string]string{"type": "radarr", "name": "Movies", "url": radarrSrv.URL, "api_key": "key"})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/services/create", body)
	request.Header.Set("Content-Type", contentType)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204 on success, got status=%d body=%q", recorder.Code, recorder.Body.String())
	}

	workflows := manager.WorkflowSnapshot()
	if len(workflows) != 1 {
		t.Fatalf("expected adding a service to schedule exactly one inventory-and-files-consistency run, got %#v", workflows)
	}
	if workflows[0].DefinitionID != "inventory-and-files-consistency" {
		t.Fatalf("unexpected workflow definition scheduled: %#v", workflows[0])
	}
}

func TestAddServiceEndToEndRejectsBadConnection(t *testing.T) {
	brokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer brokenSrv.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	inv := inventory.New(cfg, nil)
	inv.SetConfigPath(configPath)
	server, err := New(inv, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()

	body, contentType := multipartServiceForm(t, map[string]string{"type": "radarr", "name": "Movies", "url": brokenSrv.URL, "api_key": "bad"})
	postRecorder := httptest.NewRecorder()
	postRequest := httptest.NewRequest(http.MethodPost, "/services/create", body)
	postRequest.Header.Set("Content-Type", contentType)
	handler.ServeHTTP(postRecorder, postRequest)
	if postRecorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on a failed connection check, got status=%d body=%q", postRecorder.Code, postRecorder.Body.String())
	}

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Services) != 0 {
		t.Fatalf("expected nothing persisted after a failed connection check, got %#v", reloaded.Services)
	}
}

// TestTestServiceConnectionPersistsNothing guards step 1 of the service
// setup overlay: a connection check must be a pure dry run, since step 2
// (root path, device thresholds) is only shown after this succeeds, and an
// abandoned step 2 must never leave a half-configured service behind.
func TestTestServiceConnectionPersistsNothing(t *testing.T) {
	radarrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"5.0.0"}`))
	}))
	defer radarrSrv.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	inv := inventory.New(cfg, nil)
	inv.SetConfigPath(configPath)
	server, err := New(inv, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()

	body, contentType := multipartServiceForm(t, map[string]string{"type": "radarr", "name": "Movies", "url": radarrSrv.URL, "api_key": "key"})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/services/test", body)
	request.Header.Set("Content-Type", contentType)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for a successful test, got status=%d body=%q", recorder.Code, recorder.Body.String())
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("expected /services/test to write nothing, config changed:\nbefore=%s\nafter=%s", before, after)
	}
	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Services) != 0 {
		t.Fatalf("expected no service to be persisted by a connection test, got %#v", reloaded.Services)
	}
}

// TestAddServiceSetsAllowAutomaticRemoval guards the add-service form's new
// checkbox actually reaching config.Service.AllowAutomaticRemoval, the flag
// runAutoRemovalEvaluation checks per service before touching anything it
// owns.
func TestAddServiceSetsAllowAutomaticRemoval(t *testing.T) {
	radarrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"5.0.0"}`))
	}))
	defer radarrSrv.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	inv := inventory.New(cfg, nil)
	inv.SetConfigPath(configPath)
	server, err := New(inv, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()

	body, contentType := multipartServiceForm(t, map[string]string{"type": "radarr", "name": "Movies", "url": radarrSrv.URL, "api_key": "key", "allow_automatic_removal": "on"})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/services/create", body)
	request.Header.Set("Content-Type", contentType)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got status=%d body=%q", recorder.Code, recorder.Body.String())
	}

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Services) != 1 || !reloaded.Services[0].AllowAutomaticRemoval {
		t.Fatalf("expected the checked checkbox to persist as AllowAutomaticRemoval=true, got %#v", reloaded.Services)
	}
}

// TestAddServiceFormNarrowsTypeOptionsByCategory guards the Torrents/Library
// empty-state buttons: each opens the same overlay with a category query
// param, which must narrow the Type select and change the heading, rather
// than always showing every adapter type regardless of where it was opened
// from.
func TestAddServiceFormNarrowsTypeOptionsByCategory(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()

	cases := []struct {
		category    string
		wantTitle   string
		wantTypes   []string
		unwantTypes []string
	}{
		{"torrentclient", "Add torrent client", []string{"qbittorrent"}, []string{"radarr", "sonarr", "jellyfin", "seerr"}},
		{"medialibrary", "Add media library", []string{"radarr", "sonarr"}, []string{"qbittorrent", "jellyfin", "seerr"}},
		{"", "Add service", []string{"radarr", "sonarr", "qbittorrent", "jellyfin", "seerr"}, nil},
		{"unknown", "Add service", []string{"radarr", "sonarr", "qbittorrent", "jellyfin", "seerr"}, nil},
	}
	for _, c := range cases {
		url := "/services/add"
		if c.category != "" {
			url += "?category=" + c.category
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, url, nil))
		body := recorder.Body.String()
		if !strings.Contains(body, c.wantTitle) {
			t.Fatalf("category=%q: expected title %q, got:\n%s", c.category, c.wantTitle, body)
		}
		for _, want := range c.wantTypes {
			if !strings.Contains(body, `value="`+want+`"`) {
				t.Fatalf("category=%q: expected type option %q, got:\n%s", c.category, want, body)
			}
		}
		for _, unwant := range c.unwantTypes {
			if strings.Contains(body, `value="`+unwant+`"`) {
				t.Fatalf("category=%q: expected type option %q to be absent, got:\n%s", c.category, unwant, body)
			}
		}
	}
}
