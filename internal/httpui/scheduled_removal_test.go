package httpui

import (
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"connarr/internal/config"
	"connarr/internal/inventory"
	"connarr/internal/store"
	"connarr/internal/tasks"
)

func TestPostRemovalConsistencyIsReadyImmediately(t *testing.T) {
	now := time.Date(2026, 8, 29, 21, 13, 18, 0, time.UTC)
	clock := tasks.NewFakeClock(now)
	manager := tasks.NewWithClock(clock,
		tasks.Definition{ID: "inventory", Name: "Inventory", Runner: func(context.Context) error { return nil }},
		tasks.Definition{ID: "files", Name: "Files", Runner: func(context.Context) error { return nil }},
	)
	if err := manager.RegisterWorkflow(tasks.WorkflowDefinition{ID: "post-removal-consistency", Steps: []string{"inventory", "files"}}); err != nil {
		t.Fatal(err)
	}
	server := &Server{tasks: manager}
	if err := server.schedulePostRemovalConsistency(12); err != nil {
		t.Fatal(err)
	}
	workflows := manager.WorkflowSnapshot()
	if len(workflows) != 1 {
		t.Fatalf("workflows=%#v", workflows)
	}
	if !workflows[0].NotBefore.Equal(now) {
		t.Fatalf("post-removal workflow ready at %s, want %s", workflows[0].NotBefore, now)
	}
	if !workflows[0].Deadline.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("post-removal deadline=%s", workflows[0].Deadline)
	}
	clock.Advance(250 * time.Millisecond)
	if err := server.schedulePostRemovalConsistency(13); err != nil {
		t.Fatal(err)
	}
	workflows = manager.WorkflowSnapshot()
	if len(workflows) != 1 || workflows[0].Revision != 2 {
		t.Fatalf("overlapping removal workflows=%#v", workflows)
	}
	if !workflows[0].NotBefore.Equal(clock.Now()) {
		t.Fatalf("merged workflow ready at %s, want %s", workflows[0].NotBefore, clock.Now())
	}
	if !workflows[0].Deadline.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("merged workflow moved first consistency deadline to %s", workflows[0].Deadline)
	}
}

func TestRemovalJournalRecoveryResubmitsOnlySafeQueuedWork(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	manager, err := tasks.NewPersistent(database,
		tasks.Definition{ID: "inventory", Name: "Inventory", Runner: func(context.Context) error { return nil }},
		tasks.Definition{ID: "files", Name: "Files", Runner: func(context.Context) error { return nil }},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.RegisterWorkflow(tasks.WorkflowDefinition{ID: "post-removal-consistency", Steps: []string{"inventory", "files"}}); err != nil {
		t.Fatal(err)
	}
	server := &Server{tasks: manager, inv: inventory.New(config.Config{}, database)}
	if err := manager.Register(tasks.Definition{ID: removalTaskID, Name: "Removal", PayloadRunner: func(context.Context, json.RawMessage) error { return nil }, Recovery: tasks.RecoveryAttention}); err != nil {
		t.Fatal(err)
	}
	queuedPayload, _ := json.Marshal(queuedRemovalPayload{Command: scheduledRemovalCommand{Form: url.Values{"kind": {"media"}, "media_id": {"42"}}}})
	queuedID, err := database.SaveHistoryEvent(store.HistoryEvent{EventType: "removal", Status: "queued", RequestedLabel: "Queued", Payload: queuedPayload})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.recoverScheduledRemovals(database); err != nil {
		t.Fatal(err)
	}
	trigger, exists := manager.LatestTriggerForKey(removalTaskID, "operation:"+strconv.FormatInt(queuedID, 10))
	if !exists || trigger.State != "pending" {
		t.Fatalf("recovered trigger=%#v exists=%v", trigger, exists)
	}
	var command scheduledRemovalCommand
	if err := json.Unmarshal(trigger.Payload, &command); err != nil || command.HistoryID != queuedID {
		t.Fatalf("recovered command=%#v err=%v", command, err)
	}

	startedID, err := database.SaveHistoryEvent(store.HistoryEvent{EventType: "removal", Status: "started", RequestedLabel: "Uncertain"})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.recoverScheduledRemovals(database); err != nil {
		t.Fatal(err)
	}
	started, err := database.HistoryEventByID(startedID)
	if err != nil || started.Status != "interrupted" {
		t.Fatalf("started event=%#v err=%v", started, err)
	}
	if !manager.ConsistencyPending() {
		t.Fatal("uncertain removal did not inhibit planning through consistency recovery")
	}
}
