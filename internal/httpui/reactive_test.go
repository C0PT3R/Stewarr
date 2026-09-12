package httpui

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"stewarr/internal/config"
	"stewarr/internal/inventory"
	"stewarr/internal/model"
	"stewarr/internal/store"
	"stewarr/internal/tasks"
)

func TestRevisionHubDeliversNewestInvalidation(t *testing.T) {
	hub := newRevisionHub()
	updates, unsubscribe, initial := hub.subscribe()
	defer unsubscribe()
	if initial.Revision != 1 || initial.Kind != "startup" {
		t.Fatalf("initial revision = %#v", initial)
	}
	hub.publish("inventory")
	want := hub.publish("storage")
	select {
	case got := <-updates:
		if got != want {
			t.Fatalf("slow subscriber received %#v, want newest %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("revision was not delivered")
	}
}

func TestDashboardStatusUsesConditionalETag(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	first := httptest.NewRecorder()
	server.uiStatus(first, httptest.NewRequest(http.MethodGet, "/ui/status", nil))
	if first.Code != http.StatusOK || first.Header().Get("ETag") == "" {
		t.Fatalf("first response status=%d etag=%q", first.Code, first.Header().Get("ETag"))
	}
	secondRequest := httptest.NewRequest(http.MethodGet, "/ui/status", nil)
	secondRequest.Header.Set("If-None-Match", first.Header().Get("ETag"))
	second := httptest.NewRecorder()
	server.uiStatus(second, secondRequest)
	if second.Code != http.StatusNotModified || second.Body.Len() != 0 {
		t.Fatalf("conditional response status=%d body=%q", second.Code, second.Body.String())
	}
}

// TestUIStatusSurfacesRunningServiceConsistencyThenClears guards step 3's
// global-chrome counterpart: while the inventory-and-files-consistency
// workflow a service add/edit/remove triggers is still in flight,
// #operation-indicator (driven by uiStatus's PendingOperations) must reflect
// it, exactly like a pending removal already does — and stop once no such
// instance exists.
func TestUIStatusSurfacesRunningServiceConsistencyThenClears(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	baseline := httptest.NewRecorder()
	server.uiStatus(baseline, httptest.NewRequest(http.MethodGet, "/ui/status", nil))
	var baselineBody struct {
		PendingOperations int `json:"pendingOperations"`
	}
	if err := json.Unmarshal(baseline.Body.Bytes(), &baselineBody); err != nil {
		t.Fatal(err)
	}
	if baselineBody.PendingOperations != 0 {
		t.Fatalf("expected no pending operations with no task manager, got %d", baselineBody.PendingOperations)
	}

	manager := tasks.New(
		tasks.Definition{ID: "inventory", Name: "Base inventory", Runner: func(context.Context) error { return nil }},
		tasks.Definition{ID: "files", Name: "File reconciliation", Runner: func(context.Context) error { return nil }},
	)
	if err := manager.RegisterWorkflow(tasks.WorkflowDefinition{ID: "inventory-and-files-consistency", Steps: []string{"inventory", "files"}}); err != nil {
		t.Fatal(err)
	}
	withWorkflow, err := New(nil, manager)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AdvanceWorkflow("inventory-and-files-consistency", "global", 0, 5*time.Minute, "test"); err != nil {
		t.Fatal(err)
	}

	running := httptest.NewRecorder()
	withWorkflow.uiStatus(running, httptest.NewRequest(http.MethodGet, "/ui/status", nil))
	var runningBody struct {
		PendingOperations int `json:"pendingOperations"`
	}
	if err := json.Unmarshal(running.Body.Bytes(), &runningBody); err != nil {
		t.Fatal(err)
	}
	if runningBody.PendingOperations != 1 {
		t.Fatalf("expected the running consistency workflow to count as 1 pending operation, got %d", runningBody.PendingOperations)
	}

	// No instance at all (a server with a task manager but nothing ever
	// scheduled) must not falsely report anything running.
	freshManager := tasks.New(
		tasks.Definition{ID: "inventory", Runner: func(context.Context) error { return nil }},
		tasks.Definition{ID: "files", Runner: func(context.Context) error { return nil }},
	)
	if err := freshManager.RegisterWorkflow(tasks.WorkflowDefinition{ID: "inventory-and-files-consistency", Steps: []string{"inventory", "files"}}); err != nil {
		t.Fatal(err)
	}
	idle, err := New(nil, freshManager)
	if err != nil {
		t.Fatal(err)
	}
	idleRecorder := httptest.NewRecorder()
	idle.uiStatus(idleRecorder, httptest.NewRequest(http.MethodGet, "/ui/status", nil))
	var idleBody struct {
		PendingOperations int `json:"pendingOperations"`
	}
	if err := json.Unmarshal(idleRecorder.Body.Bytes(), &idleBody); err != nil {
		t.Fatal(err)
	}
	if idleBody.PendingOperations != 0 {
		t.Fatalf("expected no pending operations with no consistency instance ever scheduled, got %d", idleBody.PendingOperations)
	}
}

// TestServiceConsistencyStatusReportsStepAndTotal guards step 3's own
// polling endpoint: it must report the real step/total from the workflow
// definition, not a hardcoded guess.
func TestServiceConsistencyStatusReportsStepAndTotal(t *testing.T) {
	manager := tasks.New(
		tasks.Definition{ID: "inventory", Name: "Base inventory", Runner: func(context.Context) error { return nil }},
		tasks.Definition{ID: "files", Name: "File reconciliation", Runner: func(context.Context) error { return nil }},
	)
	if err := manager.RegisterWorkflow(tasks.WorkflowDefinition{ID: "inventory-and-files-consistency", Steps: []string{"inventory", "files"}}); err != nil {
		t.Fatal(err)
	}
	server, err := New(nil, manager)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AdvanceWorkflow("inventory-and-files-consistency", "global", 0, 5*time.Minute, "test"); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	server.serviceConsistencyStatus(recorder, httptest.NewRequest(http.MethodGet, "/services/consistency-status", nil))
	var status consistencyStatusResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.TotalSteps != 2 {
		t.Fatalf("expected 2 total steps, got %#v", status)
	}
	if status.State == "" {
		t.Fatalf("expected a non-empty state, got %#v", status)
	}
}

func TestRemovalAdmissionReturnsImmediatelyAndIsIdempotent(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.SaveTorrents([]model.Torrent{{Hash: "ABC123", Name: "Release", AssociationStatus: model.TorrentUnassociated}}); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	// removal.dry_run=false: this test exercises a real (non-dry-run)
	// removal actually reaching admission/history — config.Load otherwise
	// defaults dry_run to true, which pendingProjection deliberately ignores.
	if err := os.WriteFile(configPath, []byte(`{"storage":{},"removal":{"dry_run":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	configuration, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	service := inventory.New(configuration, database)
	service.SetConfigPath(configPath)
	manager, err := tasks.NewPersistent(database)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(service, manager)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	sessionCookie := loginForTest(t, handler)

	post := func() *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for name, value := range map[string]string{
			"kind": "torrent", "hash": "ABC123", "target": "1", "operation_token": "stable-browser-token",
		} {
			if err := writer.WriteField(name, value); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/removal/execute", &body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		request.Header.Set("Accept", "application/json")
		request.AddCookie(sessionCookie)
		response := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			handler.ServeHTTP(response, request)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("admission waited for scheduler execution")
		}
		return response
	}

	first := post()
	if first.Code != http.StatusAccepted {
		t.Fatalf("first admission status=%d body=%q", first.Code, first.Body.String())
	}
	var firstResult struct {
		OperationID int64 `json:"operationId"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstResult); err != nil || firstResult.OperationID <= 0 {
		t.Fatalf("first result=%q err=%v", first.Body.String(), err)
	}
	second := post()
	var secondResult struct {
		OperationID int64 `json:"operationId"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondResult); err != nil || secondResult.OperationID != firstResult.OperationID {
		t.Fatalf("duplicate result=%q err=%v", second.Body.String(), err)
	}
	events, err := database.HistoryEvents(10)
	if err != nil || len(events) != 1 {
		t.Fatalf("history events=%#v err=%v", events, err)
	}
	projection := server.pendingProjection()
	if _, hidden := projection.Torrents["abc123"]; !hidden || projection.PendingCount != 1 {
		t.Fatalf("pending projection=%#v", projection)
	}
	if _, found := manager.LatestTriggerForKey(removalTaskID, "operation:"+jsonNumber(firstResult.OperationID)); !found {
		t.Fatal("durable scheduler trigger was not admitted")
	}
}

func jsonNumber(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func TestPendingProjectionRestoresFailedTorrent(t *testing.T) {
	projection := emptyPendingProjection()
	projection.Torrents["abc"] = operationNotice{Status: "started"}
	items := []model.Torrent{{Hash: "ABC"}, {Hash: "DEF"}}
	filtered := projection.filterTorrents(items)
	if len(filtered) != 1 || filtered[0].Hash != "DEF" {
		t.Fatalf("filtered torrents=%#v", filtered)
	}
	delete(projection.Torrents, "abc")
	if restored := projection.filterTorrents(items); len(restored) != 2 {
		t.Fatalf("failed removal did not restore projection: %#v", restored)
	}
}

func TestSSEDisconnectDoesNotLeakSubscriber(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/ui/events", nil).WithContext(ctx)
	response := newFlushRecorder()
	done := make(chan struct{})
	go func() {
		server.uiEvents(response, request)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for response.flushes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not stop after disconnect")
	}
	server.revisions.mu.Lock()
	subscribers := len(server.revisions.subscribers)
	server.revisions.mu.Unlock()
	if subscribers != 0 {
		t.Fatalf("SSE subscribers still registered: %d", subscribers)
	}
}

// TestSSEEndsOnItsOwnAfterMaxLifetime guards the actual fix for a real
// production incident: a connection can go silently dead (a NAT mapping
// timing out, a network path change) without either side ever seeing an
// error, leaving it open indefinitely from both ends' point of view. The
// server must not rely on detecting that failure — it has to voluntarily
// end the stream on a bounded schedule regardless, so the client's
// EventSource auto-reconnects into a fresh connection either way.
func TestSSEEndsOnItsOwnAfterMaxLifetime(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	server.sseMaxLifetime = 20 * time.Millisecond
	request := httptest.NewRequest(http.MethodGet, "/ui/events", nil)
	response := newFlushRecorder()
	done := make(chan struct{})
	go func() {
		server.uiEvents(response, request)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not end on its own after sseMaxLifetime, even with no client disconnect and no revision update — a silently dead connection would never be replaced")
	}
	server.revisions.mu.Lock()
	subscribers := len(server.revisions.subscribers)
	server.revisions.mu.Unlock()
	if subscribers != 0 {
		t.Fatalf("SSE subscribers still registered after the lifetime-based close: %d", subscribers)
	}
}

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes atomic.Int32
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (recorder *flushRecorder) Flush() { recorder.flushes.Add(1) }
