package httpui

import (
	"bytes"
	"errors"
	"log"
	"net/url"
	"strings"
	"testing"

	"connarr/internal/removal"
)

func TestRemovalAuditIsCompleteLineBasedAndSecretFree(t *testing.T) {
	var output bytes.Buffer
	originalWriter := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&output)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(originalWriter)
		log.SetFlags(originalFlags)
	}()
	data := removalData{
		Plan: removal.RemovalPlan{
			Kind: removal.TorrentObject, RequestedKey: "abc", RequestedLabel: "Release", ReclaimableBytes: 0,
			Files: []removal.FileState{
				{Path: "/data/downloads/Release/file.mkv", Owner: removal.TorrentOwner, OwnerKey: "abc", Selected: true, Exists: true, SizeBytes: 100, IdentityKnown: true, Device: 56, Inode: 9, Links: 2},
				{Path: "/data/Films/Movie/file.mkv", Owner: removal.UnmanagedOwner, OwnerKey: "/data/Films/Movie/file.mkv", Exists: true, SizeBytes: 100, IdentityKnown: true, Device: 56, Inode: 9, Links: 2},
			},
		},
		TorrentSelected: true, Hash: "abc", SelectedActions: 1,
	}
	form := url.Values{"kind": {"torrent"}, "hash": {"abc"}, "operation_token": {"must-not-appear"}}
	auditRemovalPlan(12, data, form)
	auditRemovalResult(12, "torrent removed by qBittorrent")
	auditRemovalError(12, "example failure")
	auditRemovalComplete(12, "partial", data, 1, 1)
	auditRemovalRejected(13, form, errors.New("scope rejected"))
	logged := output.String()
	for _, expected := range []string{
		"[removal] [operation=12] plan",
		"action planned owner=qbittorrent",
		`state=selected owner=torrent`,
		`state=preserved owner=unmanaged`,
		`device=56 inode=9 links=2`,
		`result="torrent removed by qBittorrent"`,
		`error="example failure"`,
		"completed status=partial",
		"[removal] [operation=13] rejected",
	} {
		if !strings.Contains(logged, expected) {
			t.Fatalf("audit missing %q:\n%s", expected, logged)
		}
	}
	if strings.Contains(logged, "must-not-appear") || strings.Contains(logged, "operation_token") {
		t.Fatalf("audit leaked operation token:\n%s", logged)
	}
	for _, line := range strings.Split(strings.TrimSpace(logged), "\n") {
		if !strings.HasPrefix(line, "[removal] [operation=") {
			t.Fatalf("audit line has no origin/identity: %q", line)
		}
	}
}
