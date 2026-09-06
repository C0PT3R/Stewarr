package inventory

import (
	"connarr/internal/config"
	"testing"
	"time"
)

// TestReadersDoNotBlockBehindSlowPublisher is a regression guard for a real
// production incident: reconcileFiles/Refresh/reconcileTargeted used to hold
// service.mu (the lock every page load needs just to read cached state) for
// the entire duration of a database write. A single slow write (e.g. a large
// first-time import-history backfill for a newly added Radarr/Sonarr
// instance) stalled every page in the app for as long as that write took.
// publishMu now serializes reconciliation publishers against each other
// without ever being held by a reader — this proves that invariant directly,
// rather than trusting it stays true by convention.
func TestReadersDoNotBlockBehindSlowPublisher(t *testing.T) {
	service := New(config.Config{}, nil)

	service.publishMu.Lock()
	release := make(chan struct{})
	go func() {
		<-release
		service.publishMu.Unlock()
	}()
	defer func() { close(release) }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		service.Snapshot()
		service.Config()
		service.ReliabilitySnapshot()
		service.TorrentSnapshot()
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a reader blocked behind publishMu — mu must never be contended by a slow publisher")
	}
}
