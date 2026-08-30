package httpui

import (
	"net/http/httptest"
	"strings"
	"testing"

	"connarr/internal/cleanup"
	"connarr/internal/inventory"
)

func TestHomeTemplateRendersStorageDevices(t *testing.T) {
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
					Claimed:        []inventory.ClaimedSegment{{Integration: "Movies", Bytes: 500}},
					UnmanagedBytes: 50, OtherBytes: 50,
				},
				Plan: cleanup.Plan{Available: true, UsagePercent: 60, TargetUsagePercent: 90, Message: "No cleanup: 60.00% used (target 90.0%)"},
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
	for _, want := range []string{"downloads, movies", "ext2/ext3/ext4", "Movies: 500.0 B", "No cleanup", "/data/broken", "permission denied", "storage-bar"} {
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
