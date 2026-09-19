package httpui

import (
	"net/http/httptest"
	"strings"
	"testing"

	"stewarr/internal/inventory"
)

func TestHomeTemplateRendersStorageSummaryOnly(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := homeData{
		Devices: []deviceView{
			{
				Storage: inventory.StorageDevice{
					RootLabels: []string{"downloads", "movies"}, RepresentativePath: "/data/movies", Filesystem: "ext2/ext3/ext4",
					Available: true, TotalBytes: 1000, FreeBytes: 400, UsedBytes: 600,
					Claimed:        []inventory.ClaimedSegment{{Service: "Movies", Bytes: 500}},
					UnmanagedBytes: 50, OtherBytes: 50,
				},
			},
			{
				Storage: inventory.StorageDevice{RepresentativePath: "/data/broken", Available: false, Error: "permission denied"},
			},
		},
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.homeTpl, data); err != nil {
		t.Fatalf("render home template: %v", err)
	}
	body := recorder.Body.String()
	for _, want := range []string{"downloads, movies", "ext2/ext3/ext4", "550.0 B used of 950.0 B usable", "/data/broken", "permission denied", "storage-bar", `href="/storage"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected home output to contain %q, got:\n%s", want, body)
		}
	}
	// The full per-service/cleanup-plan detail now lives on the dedicated
	// Storage page, not on this quick-glance card.
	for _, mustNotContain := range []string{"Movies: 500.0 B", "storage-legend", "Cleanup active", "Cleanup inactive"} {
		if strings.Contains(body, mustNotContain) {
			t.Fatalf("expected home's Storage card to no longer contain %q (that detail moved to /storage), got:\n%s", mustNotContain, body)
		}
	}
}

func TestHomeTemplateRendersServicesSummaryAndCardLinks(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := homeData{
		Services: []inventory.ServiceStatus{{Name: "Movies", Configured: true, OK: true}},
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.homeTpl, data); err != nil {
		t.Fatalf("render home template: %v", err)
	}
	body := recorder.Body.String()
	for _, want := range []string{"Movies", "Connected", `href="/services"`, `href="/library"`, `href="/torrents"`, `href="/history"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected home output to contain %q, got:\n%s", want, body)
		}
	}
}

func TestHomeTemplateRendersNoKnownDevices(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.homeTpl, homeData{}); err != nil {
		t.Fatalf("render home template: %v", err)
	}
	if !strings.Contains(recorder.Body.String(), "No storage devices are known yet") {
		t.Fatalf("expected empty-state message, got:\n%s", recorder.Body.String())
	}
}
