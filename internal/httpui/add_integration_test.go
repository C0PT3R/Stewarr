package httpui

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connarr/internal/config"
	"connarr/internal/inventory"
)

// multipartIntegrationForm builds a request body shaped exactly like the
// real browser's fetch(url, {body: new FormData(form)}) — multipart, not
// urlencoded. A urlencoded body would mask the real bug this once shipped
// with: r.ParseForm alone never reads a multipart body, so every field came
// back empty and every add attempt failed with "type is required" even
// though the browser form was filled in correctly.
func multipartIntegrationForm(t *testing.T, fields map[string]string) (*bytes.Buffer, string) {
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

func TestAddIntegrationEndToEndOverHTTP(t *testing.T) {
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
	if getRecorder.Code != http.StatusOK || !strings.Contains(getRecorder.Body.String(), "Add integration") {
		t.Fatalf("expected the add-integration overlay to render, got status=%d body=%q", getRecorder.Code, getRecorder.Body.String())
	}

	// Submitting the form (as a real browser would: multipart, via
	// fetch+FormData) adds, validates, and persists the integration.
	body, contentType := multipartIntegrationForm(t, map[string]string{"type": "radarr", "name": "Movies", "url": radarrSrv.URL, "api_key": "key"})
	postRecorder := httptest.NewRecorder()
	postRequest := httptest.NewRequest(http.MethodPost, "/services/integrations", body)
	postRequest.Header.Set("Content-Type", contentType)
	handler.ServeHTTP(postRecorder, postRequest)
	if postRecorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204 on success, got status=%d body=%q", postRecorder.Code, postRecorder.Body.String())
	}

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Integrations) != 1 || reloaded.Integrations[0].Name != "Movies" {
		t.Fatalf("expected the integration to be persisted, got %#v", reloaded.Integrations)
	}

	// The Services page now reflects it, without needing a restart.
	servicesRecorder := httptest.NewRecorder()
	servicesRequest := httptest.NewRequest(http.MethodGet, "/services", nil)
	handler.ServeHTTP(servicesRecorder, servicesRequest)
	if !strings.Contains(servicesRecorder.Body.String(), "Movies") {
		t.Fatalf("expected the Services page to show the newly added integration live, got:\n%s", servicesRecorder.Body.String())
	}
}

func TestAddIntegrationEndToEndRejectsBadConnection(t *testing.T) {
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

	body, contentType := multipartIntegrationForm(t, map[string]string{"type": "radarr", "name": "Movies", "url": brokenSrv.URL, "api_key": "bad"})
	postRecorder := httptest.NewRecorder()
	postRequest := httptest.NewRequest(http.MethodPost, "/services/integrations", body)
	postRequest.Header.Set("Content-Type", contentType)
	handler.ServeHTTP(postRecorder, postRequest)
	if postRecorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on a failed connection check, got status=%d body=%q", postRecorder.Code, postRecorder.Body.String())
	}

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Integrations) != 0 {
		t.Fatalf("expected nothing persisted after a failed connection check, got %#v", reloaded.Integrations)
	}
}
