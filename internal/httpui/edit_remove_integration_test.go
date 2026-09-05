package httpui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connarr/internal/config"
	"connarr/internal/inventory"
)

func TestEditIntegrationEndToEndOverHTTP(t *testing.T) {
	oldRadarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer oldRadarr.Close()
	newRadarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer newRadarr.Close()

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

	addBody, addContentType := multipartIntegrationForm(t, map[string]string{"type": "radarr", "name": "Movies", "url": oldRadarr.URL, "api_key": "key"})
	addRecorder := httptest.NewRecorder()
	addRequest := httptest.NewRequest(http.MethodPost, "/services/integrations", addBody)
	addRequest.Header.Set("Content-Type", addContentType)
	handler.ServeHTTP(addRecorder, addRequest)
	if addRecorder.Code != http.StatusNoContent {
		t.Fatalf("setup: expected 204 adding Movies, got status=%d body=%q", addRecorder.Code, addRecorder.Body.String())
	}
	added, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	id := added.Integrations[0].ID

	// The pre-filled edit overlay loads via GET, with the existing URL baked in.
	getRecorder := httptest.NewRecorder()
	getRequest := httptest.NewRequest(http.MethodGet, "/services/edit?id="+id, nil)
	handler.ServeHTTP(getRecorder, getRequest)
	if getRecorder.Code != http.StatusOK || !strings.Contains(getRecorder.Body.String(), oldRadarr.URL) {
		t.Fatalf("expected the edit overlay to render pre-filled with the current URL, got status=%d body=%q", getRecorder.Code, getRecorder.Body.String())
	}

	// Moving it to a brand new URL (real browser shape: multipart) keeps the same ID.
	editBody, editContentType := multipartIntegrationForm(t, map[string]string{"id": id, "name": "Movies", "url": newRadarr.URL, "api_key": "newkey"})
	editRecorder := httptest.NewRecorder()
	editRequest := httptest.NewRequest(http.MethodPost, "/services/edit", editBody)
	editRequest.Header.Set("Content-Type", editContentType)
	handler.ServeHTTP(editRecorder, editRequest)
	if editRecorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204 on a successful edit, got status=%d body=%q", editRecorder.Code, editRecorder.Body.String())
	}

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Integrations) != 1 || reloaded.Integrations[0].ID != id || reloaded.Integrations[0].URL != newRadarr.URL {
		t.Fatalf("expected the same ID with the new URL persisted, got %#v (want id %q)", reloaded.Integrations, id)
	}
}

func TestRemoveIntegrationEndToEndOverHTTP(t *testing.T) {
	radarrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
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

	addBody, addContentType := multipartIntegrationForm(t, map[string]string{"type": "radarr", "name": "Movies", "url": radarrSrv.URL, "api_key": "key"})
	addRecorder := httptest.NewRecorder()
	addRequest := httptest.NewRequest(http.MethodPost, "/services/integrations", addBody)
	addRequest.Header.Set("Content-Type", addContentType)
	handler.ServeHTTP(addRecorder, addRequest)
	if addRecorder.Code != http.StatusNoContent {
		t.Fatalf("setup: expected 204 adding Movies, got status=%d body=%q", addRecorder.Code, addRecorder.Body.String())
	}
	added, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	id := added.Integrations[0].ID

	removeBody, removeContentType := multipartIntegrationForm(t, map[string]string{"id": id})
	removeRecorder := httptest.NewRecorder()
	removeRequest := httptest.NewRequest(http.MethodPost, "/services/remove", removeBody)
	removeRequest.Header.Set("Content-Type", removeContentType)
	handler.ServeHTTP(removeRecorder, removeRequest)
	if removeRecorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204 on a successful remove, got status=%d body=%q", removeRecorder.Code, removeRecorder.Body.String())
	}

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Integrations) != 0 {
		t.Fatalf("expected the integration to be gone, got %#v", reloaded.Integrations)
	}

	servicesRecorder := httptest.NewRecorder()
	servicesRequest := httptest.NewRequest(http.MethodGet, "/services", nil)
	handler.ServeHTTP(servicesRecorder, servicesRequest)
	if strings.Contains(servicesRecorder.Body.String(), "Movies") {
		t.Fatalf("expected the Services page to no longer show the removed integration, got:\n%s", servicesRecorder.Body.String())
	}
}
