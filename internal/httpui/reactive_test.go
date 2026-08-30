package httpui

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"connarr/internal/config"
	"connarr/internal/inventory"
	"connarr/internal/model"
	"connarr/internal/store"
	"connarr/internal/tasks"
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

func TestRemovalAdmissionReturnsImmediatelyAndIsIdempotent(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.SaveTorrents([]model.Torrent{{Hash: "ABC123", Name: "Release", AssociationStatus: model.TorrentUnassociated}}); err != nil {
		t.Fatal(err)
	}
	var configuration config.Config
	configuration.Storage.Path = t.TempDir()
	service := inventory.New(configuration, database)
	manager, err := tasks.NewPersistent(database)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(service, manager)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()

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

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes atomic.Int32
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (recorder *flushRecorder) Flush() { recorder.flushes.Add(1) }
