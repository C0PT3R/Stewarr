package httpui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"connarr/internal/model"
)

type uiRevision struct {
	Revision uint64 `json:"revision"`
	Kind     string `json:"kind"`
}

type revisionHub struct {
	mu          sync.Mutex
	revision    atomic.Uint64
	latest      uiRevision
	subscribers map[chan uiRevision]struct{}
}

func newRevisionHub() *revisionHub {
	hub := &revisionHub{subscribers: map[chan uiRevision]struct{}{}}
	hub.latest = uiRevision{Revision: 1, Kind: "startup"}
	hub.revision.Store(1)
	return hub
}

func (hub *revisionHub) current() uiRevision {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return hub.latest
}

func (hub *revisionHub) publish(kind string) uiRevision {
	if strings.TrimSpace(kind) == "" {
		kind = "background"
	}
	revision := uiRevision{Revision: hub.revision.Add(1), Kind: kind}
	hub.mu.Lock()
	hub.latest = revision
	for subscriber := range hub.subscribers {
		select {
		case subscriber <- revision:
		default:
			// Invalidation is revision-based, so a slow browser only needs the
			// newest revision. Replace its buffered obsolete notification.
			select {
			case <-subscriber:
			default:
			}
			select {
			case subscriber <- revision:
			default:
			}
		}
	}
	hub.mu.Unlock()
	return revision
}

func (hub *revisionHub) subscribe() (<-chan uiRevision, func(), uiRevision) {
	updates := make(chan uiRevision, 1)
	hub.mu.Lock()
	hub.subscribers[updates] = struct{}{}
	latest := hub.latest
	hub.mu.Unlock()
	return updates, func() {
		hub.mu.Lock()
		delete(hub.subscribers, updates)
		hub.mu.Unlock()
	}, latest
}

func (server *Server) publishUIChange(kind string) uiRevision {
	if server.revisions == nil {
		return uiRevision{}
	}
	return server.revisions.publish(kind)
}

// Start connects published application state to browser invalidations. It is
// intentionally separate from New so isolated handler tests do not leak
// watcher goroutines.
func (server *Server) Start(ctx context.Context) {
	server.startOnce.Do(func() {
		if server.inv != nil {
			go server.watchInventoryChanges(ctx)
			go server.watchStorageChanges(ctx)
		}
		if server.tasks != nil {
			go server.watchTaskChanges(ctx)
		}
	})
}

func (server *Server) watchInventoryChanges(ctx context.Context) {
	for {
		changes := server.inv.Changes()
		select {
		case <-ctx.Done():
			return
		case <-changes:
			server.publishUIChange("inventory")
		}
	}
}

func (server *Server) watchTaskChanges(ctx context.Context) {
	for {
		changes := server.tasks.Changes()
		select {
		case <-ctx.Done():
			return
		case <-changes:
			server.publishUIChange("tasks")
		}
	}
}

type storageFingerprint struct {
	blocks uint64
	free   uint64
}

func sampleStorage(path string) (storageFingerprint, bool) {
	var statistics syscall.Statfs_t
	if strings.TrimSpace(path) == "" || syscall.Statfs(path, &statistics) != nil {
		return storageFingerprint{}, false
	}
	return storageFingerprint{blocks: statistics.Blocks, free: statistics.Bavail}, true
}

// sampleAllStorage stats every currently known storage device's
// representative path. It never calls an integration or walks a
// filesystem, so it is safe on a short polling interval.
func (server *Server) sampleAllStorage() map[string]storageFingerprint {
	fingerprints := map[string]storageFingerprint{}
	for _, path := range server.inv.KnownStorageDevicePaths() {
		if fingerprint, available := sampleStorage(path); available {
			fingerprints[path] = fingerprint
		}
	}
	return fingerprints
}

func storageFingerprintsEqual(a, b map[string]storageFingerprint) bool {
	if len(a) != len(b) {
		return false
	}
	for path, fingerprint := range a {
		if b[path] != fingerprint {
			return false
		}
	}
	return true
}

func (server *Server) watchStorageChanges(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	last := server.sampleAllStorage()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current := server.sampleAllStorage()
			if !storageFingerprintsEqual(current, last) {
				last = current
				server.publishUIChange("storage")
			}
		}
	}
}

func writeSSERevision(response http.ResponseWriter, revision uiRevision) error {
	encoded, err := json.Marshal(revision)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(response, "id: %d\nevent: revision\ndata: %s\n\n", revision.Revision, encoded)
	return err
}

func (server *Server) uiEvents(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(response, "GET only", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := response.(http.Flusher)
	if !ok {
		http.Error(response, "streaming unavailable", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("X-Accel-Buffering", "no")
	updates, unsubscribe, current := server.revisions.subscribe()
	defer unsubscribe()
	if err := writeSSERevision(response, current); err != nil {
		return
	}
	flusher.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case revision := <-updates:
			if writeSSERevision(response, revision) != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(response, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

type operationNotice struct {
	ID      int64  `json:"id"`
	Label   string `json:"label"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type uiStatus struct {
	Revision          uint64            `json:"revision"`
	Kind              string            `json:"kind"`
	PendingOperations int               `json:"pendingOperations"`
	Notices           []operationNotice `json:"notices,omitempty"`
}

func (server *Server) uiStatus(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(response, "GET only", http.StatusMethodNotAllowed)
		return
	}
	revision := server.revisions.current()
	etag := `"ui-` + strconv.FormatUint(revision.Revision, 10) + `"`
	response.Header().Set("ETag", etag)
	response.Header().Set("Cache-Control", "no-cache")
	if request.Header.Get("If-None-Match") == etag {
		response.WriteHeader(http.StatusNotModified)
		return
	}
	projection := server.pendingProjection()
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(uiStatus{Revision: revision.Revision, Kind: revision.Kind, PendingOperations: projection.PendingCount, Notices: projection.Notices})
}

type pendingProjection struct {
	Media        map[string]operationNotice
	Torrents     map[string]operationNotice
	Unmanaged    map[string]operationNotice
	ManagedFiles map[string]operationNotice
	PendingCount int
	Notices      []operationNotice
}

// mediaOperationKey identifies one media item across pending-removal
// projections and history lookups. integrationID disambiguates SourceID
// across multiple configured instances of the same integration type.
func mediaOperationKey(kind model.MediaType, id int, integrationID string) string {
	return fmt.Sprintf("%s:%d:%s", kind, id, integrationID)
}

func emptyPendingProjection() pendingProjection {
	return pendingProjection{Media: map[string]operationNotice{}, Torrents: map[string]operationNotice{}, Unmanaged: map[string]operationNotice{}, ManagedFiles: map[string]operationNotice{}}
}

func (server *Server) pendingProjection() pendingProjection {
	projection := emptyPendingProjection()
	if server.inv == nil || server.inv.Store() == nil {
		return projection
	}
	events, err := server.inv.Store().HistoryEvents(200)
	if err != nil {
		return projection
	}
	managedOwners := map[string]string{}
	managedTotals := map[string]int{}
	_, mediaRefs, _, _, _ := server.inv.FileSnapshot()
	for _, mediaRef := range mediaRefs {
		owner := mediaOperationKey(mediaRef.MediaType, mediaRef.MediaID, mediaRef.IntegrationID)
		managedOwners[managedFileKey(mediaRef)] = owner
		managedTotals[owner]++
	}
	for _, event := range events {
		status := strings.ToLower(strings.TrimSpace(event.Status))
		notice := operationNotice{ID: event.ID, Label: event.RequestedLabel, Status: status, Message: event.Error}
		if (status == "failed" || status == "partial" || status == "interrupted" || status == "attention") && len(projection.Notices) < 5 {
			projection.Notices = append(projection.Notices, notice)
		}
		projecting := status == "queued" || status == "started"
		if (status == "interrupted" || status == "attention") && server.tasks != nil && server.tasks.ConsistencyPending() {
			projecting = true
		}
		if status == "success" && server.tasks != nil && server.tasks.ConsistencyPending() {
			projecting = true
		}
		if event.DryRun || !projecting {
			continue
		}
		projection.PendingCount++
		var queued queuedRemovalPayload
		if json.Unmarshal(event.Payload, &queued) == nil && len(queued.Command.Form) > 0 {
			form := queued.Command.Form
			selectedByOwner := map[string]int{}
			for _, torrentHash := range form["torrent"] {
				projection.Torrents[strings.ToLower(strings.TrimSpace(torrentHash))] = notice
			}
			for _, path := range append(append([]string(nil), form["path"]...), form["unmanaged_path"]...) {
				projection.Unmanaged[filepath.Clean(path)] = notice
			}
			for _, managedKey := range form["managed_file"] {
				key := strings.ToLower(strings.TrimSpace(managedKey))
				projection.ManagedFiles[key] = notice
				if mediaKey := managedOwners[key]; mediaKey != "" {
					selectedByOwner[mediaKey]++
				}
			}
			if form.Get("kind") == "torrent" && form.Get("target") == "1" {
				projection.Torrents[strings.ToLower(strings.TrimSpace(form.Get("hash")))] = notice
			}
			if form.Get("kind") == "media" {
				id, _ := strconv.Atoi(form.Get("media_id"))
				key := mediaOperationKey(model.MediaType(form.Get("media_type")), id, form.Get("integration_id"))
				if managedTotals[key] > 0 && selectedByOwner[key] >= managedTotals[key] {
					projection.Media[key] = notice
				}
			}
			continue
		}
		// Once execution has started, its journal payload contains the
		// authoritative plan rather than the admission form. Keep the requested
		// root suppressed until execution/reconciliation reaches a known result.
		switch event.RequestedKind {
		case "media":
			projection.Media[event.RequestedKey] = notice
		case "torrent":
			projection.Torrents[strings.ToLower(event.RequestedKey)] = notice
		}
	}
	return projection
}

func (projection pendingProjection) filterMedia(items []model.Media) []model.Media {
	filtered := make([]model.Media, 0, len(items))
	for _, item := range items {
		if _, pending := projection.Media[mediaOperationKey(item.Type, item.SourceID, item.IntegrationID)]; !pending {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (projection pendingProjection) filterTorrents(items []model.Torrent) []model.Torrent {
	filtered := make([]model.Torrent, 0, len(items))
	for _, item := range items {
		if _, pending := projection.Torrents[strings.ToLower(item.Hash)]; !pending {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (projection pendingProjection) filterUnmanaged(items []model.UnmanagedFile) []model.UnmanagedFile {
	filtered := make([]model.UnmanagedFile, 0, len(items))
	for _, item := range items {
		if _, pending := projection.Unmanaged[filepath.Clean(item.Path)]; !pending {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (projection pendingProjection) mediaOperation(kind model.MediaType, id int, integrationID string) (operationNotice, bool) {
	notice, found := projection.Media[mediaOperationKey(kind, id, integrationID)]
	return notice, found
}

func (projection pendingProjection) torrentOperation(hash string) (operationNotice, bool) {
	notice, found := projection.Torrents[strings.ToLower(hash)]
	return notice, found
}

func (server *Server) removalHistory(requestedKind, requestedKey string) (operationNotice, bool) {
	if server.inv == nil || server.inv.Store() == nil {
		return operationNotice{}, false
	}
	events, err := server.inv.Store().HistoryEvents(200)
	if err != nil {
		return operationNotice{}, false
	}
	for _, event := range events {
		if event.EventType == "removal" && event.RequestedKind == requestedKind && strings.EqualFold(event.RequestedKey, requestedKey) {
			return operationNotice{ID: event.ID, Label: event.RequestedLabel, Status: strings.ToLower(event.Status), Message: event.Error}, true
		}
	}
	return operationNotice{}, false
}
