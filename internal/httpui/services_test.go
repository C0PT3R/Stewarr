package httpui

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"stewarr/internal/inventory"
)

func TestServicesTemplateRendersServiceDetailAndRootPaths(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	checked := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	data := servicesData{
		Services: []inventory.ServiceStatus{
			{Name: "Movies", Configured: true, OK: true, CheckedAt: checked},
			{Name: "Seerr", Configured: true, OK: false, Message: "dial tcp: connection refused"},
		},
		ServiceRoots: map[string][]string{
			"Movies": {"/data/movies", "/data/movies-4k"},
		},
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.servicesTpl, data); err != nil {
		t.Fatalf("render services template: %v", err)
	}
	body := recorder.Body.String()
	for _, want := range []string{"Movies", "Connected", "Seerr", "Unavailable", "dial tcp: connection refused", "/data/movies", "/data/movies-4k", "root-icon"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected services output to contain %q, got:\n%s", want, body)
		}
	}
}

// TestAddServiceTemplateDisclosesEnrichmentOnlyLimitation guards the
// add-service overlay's up-front capability disclosure: Jellyfin/Seerr
// never own removable storage, so the warning explaining that must be
// present in the markup (client-side JS shows/hides it per the selected
// type using the same data-service-field mechanism the credential fields
// already rely on).
func TestAddServiceTemplateDisclosesEnrichmentOnlyLimitation(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.addServiceTpl, addServiceFormDataFor("")); err != nil {
		t.Fatalf("render add-service template: %v", err)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `data-service-field="jellyfin seerr"`) || !strings.Contains(body, "never owns removable storage") {
		t.Fatalf("expected the enrichment-only limitation warning, got:\n%s", body)
	}
}

func TestServicesTemplateRendersEmptyState(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.servicesTpl, servicesData{}); err != nil {
		t.Fatalf("render services template: %v", err)
	}
	if !strings.Contains(recorder.Body.String(), "No services are configured yet") {
		t.Fatalf("expected empty-state message, got:\n%s", recorder.Body.String())
	}
}
