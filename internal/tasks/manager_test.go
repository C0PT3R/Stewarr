package tasks

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunnerReceivesTriggerContextAndEmitsOneSummary(t *testing.T) {
	previousOutput := log.Writer()
	previousFlags := log.Flags()
	var output bytes.Buffer
	log.SetOutput(&output)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(previousOutput); log.SetFlags(previousFlags) })

	manager := New(Definition{ID: "files", Name: "Files", Runner: func(ctx context.Context) error {
		if !TriggeredOnlyBy(ctx, TriggerWorkflow) {
			t.Fatal("workflow trigger context was not propagated")
		}
		AddMetric(ctx, "mode", "incremental")
		AddMetric(ctx, "torrents_cached", 12)
		return nil
	}})
	startManager(t, manager)
	receipt, err := manager.Submit(Request{TaskID: "files", Kind: TriggerWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Await(context.Background(), receipt.TriggerID); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "task=files") || !strings.Contains(lines[0], "triggers=workflow") || !strings.Contains(lines[0], "mode=incremental") || !strings.Contains(lines[0], "torrents_cached=12") {
		t.Fatalf("summary=%q", output.String())
	}
}

func TestDegradedStatusReflectsCurrentAdvisoryNotAFrozenOne(t *testing.T) {
	var warning atomic.Value
	warning.Store("Jellyfin enrichment stale; Seerr enrichment stale")
	manager := New(Definition{ID: "inventory", Name: "Base inventory", Runner: func(context.Context) error { return nil }, Advisory: func() string { return warning.Load().(string) }})
	startManager(t, manager)
	receipt, err := manager.Submit(Request{TaskID: "inventory", Kind: TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Await(context.Background(), receipt.TriggerID); err != nil {
		t.Fatal(err)
	}
	status := manager.Snapshot()[0]
	if status.State != "Degraded" || status.LastWarning != "Jellyfin enrichment stale; Seerr enrichment stale" {
		t.Fatalf("expected degraded status right after the run, got %#v", status)
	}
	// The underlying condition resolves without the task running again.
	warning.Store("")
	status = manager.Snapshot()[0]
	if status.State == "Degraded" || status.LastWarning != "" {
		t.Fatalf("expected the display to reflect the now-resolved advisory instead of the frozen one, got %#v", status)
	}
	// And if it becomes degraded again for a different reason, the display
	// must show the fresh text, not the original run's frozen warning.
	warning.Store("Seerr enrichment stale")
	status = manager.Snapshot()[0]
	if status.State != "Degraded" || status.LastWarning != "Seerr enrichment stale" {
		t.Fatalf("expected the fresh advisory text, got %#v", status)
	}
}

func TestIncompatibleTriggerCreatesSuccessorInsteadOfJoining(t *testing.T) {
	started := make(chan TriggerKind, 2)
	release := make(chan struct{}, 2)
	manager := New(Definition{ID: "files", Name: "Files", AttachCompatible: func(active []TriggerKind, requested TriggerKind) bool {
		return requested == TriggerWorkflow
	}, Runner: func(ctx context.Context) error {
		execution, _ := FromContext(ctx)
		started <- execution.TriggerKinds[0]
		<-release
		return nil
	}})
	startManager(t, manager)
	workflow, _ := manager.Submit(Request{TaskID: "files", Kind: TriggerWorkflow})
	if receive(t, started) != TriggerWorkflow {
		t.Fatal("workflow execution did not start")
	}
	manual, _ := manager.Submit(Request{TaskID: "files", Kind: TriggerManual})
	if manual.Disposition == "attached" {
		t.Fatalf("manual full scan attached to incremental execution: %#v", manual)
	}
	release <- struct{}{}
	_, _ = manager.Await(context.Background(), workflow.TriggerID)
	if receive(t, started) != TriggerManual {
		t.Fatal("manual successor did not start")
	}
	release <- struct{}{}
	_, _ = manager.Await(context.Background(), manual.TriggerID)
}

func startManager(t *testing.T, manager *Manager) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go manager.Start(ctx)
	t.Cleanup(cancel)
	return cancel
}

func receive[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for scheduler event")
		var zero T
		return zero
	}
}

func assertNotReceived[T any](t *testing.T, channel <-chan T) {
	t.Helper()
	select {
	case value := <-channel:
		t.Fatalf("unexpected scheduler event: %v", value)
	default:
	}
}

func waitUntil(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not reached")
		}
		runtime.Gosched()
	}
}

func TestAcceptedTriggersCoalesceWithoutDisappearing(t *testing.T) {
	var runs atomic.Int32
	manager := New(Definition{ID: "inventory", Name: "Inventory", Runner: func(context.Context) error { runs.Add(1); return nil }})
	first, err := manager.Submit(Request{TaskID: "inventory", Kind: TriggerManual, Durable: true, Cause: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Submit(Request{TaskID: "inventory", Kind: TriggerManual, Durable: true, Cause: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Disposition != "coalesced" || second.TargetID != first.TargetID {
		t.Fatalf("unexpected coalescing receipt: first=%#v second=%#v", first, second)
	}
	startManager(t, manager)
	if _, err := manager.Await(context.Background(), first.TriggerID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Await(context.Background(), second.TriggerID); err != nil {
		t.Fatal(err)
	}
	if runs.Load() != 1 {
		t.Fatalf("runs=%d", runs.Load())
	}
}

func TestLaterCoverageCreatesExactlyOneSuccessor(t *testing.T) {
	started := make(chan int, 2)
	release := make(chan struct{}, 2)
	var runNumber atomic.Int32
	manager := New(Definition{ID: "inventory", Name: "Inventory", Runner: func(context.Context) error {
		number := int(runNumber.Add(1))
		started <- number
		<-release
		return nil
	}})
	startManager(t, manager)
	first, _ := manager.Submit(Request{TaskID: "inventory", Kind: TriggerEvent, Durable: true, RequiredCoverage: 1})
	if receive(t, started) != 1 {
		t.Fatal("first execution did not start")
	}
	second, _ := manager.Submit(Request{TaskID: "inventory", Kind: TriggerEvent, Durable: true, RequiredCoverage: 2})
	third, _ := manager.Submit(Request{TaskID: "inventory", Kind: TriggerEvent, Durable: true, RequiredCoverage: 3})
	if second.Disposition != "pending" || third.Disposition != "coalesced" {
		t.Fatalf("successor receipts: %#v %#v", second, third)
	}
	release <- struct{}{}
	if _, err := manager.Await(context.Background(), first.TriggerID); err != nil {
		t.Fatal(err)
	}
	if receive(t, started) != 2 {
		t.Fatal("successor execution did not start")
	}
	release <- struct{}{}
	_, _ = manager.Await(context.Background(), second.TriggerID)
	_, _ = manager.Await(context.Background(), third.TriggerID)
	if runNumber.Load() != 2 {
		t.Fatalf("runs=%d", runNumber.Load())
	}
}

func TestRunNowJoinsSufficientlyFreshExecution(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	manager := New(Definition{ID: "x", Name: "X", Runner: func(context.Context) error {
		close(started)
		<-release
		return nil
	}})
	startManager(t, manager)
	first, _ := manager.Submit(Request{TaskID: "x", Kind: TriggerEvent, Durable: true, RequiredCoverage: 8})
	<-started
	manual, _ := manager.Submit(Request{TaskID: "x", Kind: TriggerManual, Durable: true})
	if manual.Disposition != "attached" || manual.ExecutionID == "" {
		t.Fatalf("manual trigger did not join execution: %#v", manual)
	}
	close(release)
	_, _ = manager.Await(context.Background(), first.TriggerID)
	_, _ = manager.Await(context.Background(), manual.TriggerID)
}

func TestCallerCancellationDoesNotCancelAcceptedExecution(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	manager := New(Definition{ID: "mutation", Name: "Mutation", Recovery: RecoveryAttention, Runner: func(context.Context) error {
		close(started)
		<-release
		close(finished)
		return nil
	}})
	startManager(t, manager)
	receipt, _ := manager.Submit(Request{TaskID: "mutation", Kind: TriggerEvent, Priority: PriorityMutation, Durable: true})
	<-started
	waitContext, cancelWait := context.WithCancel(context.Background())
	cancelWait()
	if _, err := manager.Await(waitContext, receipt.TriggerID); err == nil {
		t.Fatal("cancelled caller unexpectedly kept waiting")
	}
	assertNotReceived(t, finished)
	close(release)
	<-finished
	if _, err := manager.Await(context.Background(), receipt.TriggerID); err != nil {
		t.Fatal(err)
	}
}

func TestConflictingResourcesNeverOverlap(t *testing.T) {
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	claim := []ResourceClaim{{Resource: "publication", Mode: ClaimExclusive}}
	manager := New(
		Definition{ID: "first", Name: "First", Resources: claim, Runner: func(context.Context) error { close(firstStarted); <-releaseFirst; return nil }},
		Definition{ID: "second", Name: "Second", Resources: claim, Runner: func(context.Context) error { close(secondStarted); return nil }},
	)
	startManager(t, manager)
	first, _ := manager.Submit(Request{TaskID: "first", Durable: true})
	<-firstStarted
	second, _ := manager.Submit(Request{TaskID: "second", Durable: true})
	waitUntil(t, func() bool {
		for _, status := range manager.Snapshot() {
			if status.ID == "second" {
				return status.State == "Waiting" && status.Waiting == "Resource: publication held by First"
			}
		}
		return false
	})
	assertNotReceived(t, secondStarted)
	close(releaseFirst)
	_, _ = manager.Await(context.Background(), first.TriggerID)
	<-secondStarted
	_, _ = manager.Await(context.Background(), second.TriggerID)
}

func TestCompatibleSharedResourcesCanOverlap(t *testing.T) {
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	release := make(chan struct{})
	claim := []ResourceClaim{{Resource: "publication", Mode: ClaimShared}}
	manager := New(
		Definition{ID: "first", Name: "First", Resources: claim, Runner: func(context.Context) error { close(firstStarted); <-release; return nil }},
		Definition{ID: "second", Name: "Second", Resources: claim, Runner: func(context.Context) error { close(secondStarted); <-release; return nil }},
	)
	startManager(t, manager)
	first, _ := manager.Submit(Request{TaskID: "first", Durable: true})
	second, _ := manager.Submit(Request{TaskID: "second", Durable: true})
	receive(t, firstStarted)
	receive(t, secondStarted)
	close(release)
	_, _ = manager.Await(context.Background(), first.TriggerID)
	_, _ = manager.Await(context.Background(), second.TriggerID)
}

func TestEqualPriorityUsesStableFIFOOrder(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC))
	order := make(chan string, 3)
	claim := []ResourceClaim{{Resource: "serial", Mode: ClaimExclusive}}
	manager := NewWithClock(clock,
		Definition{ID: "first", Name: "First", Resources: claim, Runner: func(context.Context) error { order <- "first"; return nil }},
		Definition{ID: "second", Name: "Second", Resources: claim, Runner: func(context.Context) error { order <- "second"; return nil }},
		Definition{ID: "third", Name: "Third", Resources: claim, Runner: func(context.Context) error { order <- "third"; return nil }},
	)
	_, _ = manager.Submit(Request{TaskID: "first", Kind: TriggerManual, Durable: true})
	_, _ = manager.Submit(Request{TaskID: "second", Kind: TriggerManual, Durable: true})
	_, _ = manager.Submit(Request{TaskID: "third", Kind: TriggerManual, Durable: true})
	startManager(t, manager)
	for _, expected := range []string{"first", "second", "third"} {
		if actual := receive(t, order); actual != expected {
			t.Fatalf("expected %s, got %s", expected, actual)
		}
	}
}

func TestCancellationRetainsResourceUntilRunnerExits(t *testing.T) {
	lowStarted := make(chan struct{})
	lowCancelled := make(chan struct{})
	allowLowExit := make(chan struct{})
	highStarted := make(chan struct{})
	claim := []ResourceClaim{{Resource: "mutation", Mode: ClaimExclusive}}
	manager := New(
		Definition{ID: "low", Name: "Low", Priority: PriorityPeriodic, Resources: claim, Interruptible: true, InterruptionDelay: time.Hour, Runner: func(ctx context.Context) error {
			close(lowStarted)
			<-ctx.Done()
			close(lowCancelled)
			<-allowLowExit
			return ctx.Err()
		}},
		Definition{ID: "high", Name: "High", Priority: PriorityMutation, Resources: claim, Runner: func(context.Context) error { close(highStarted); return nil }},
	)
	startManager(t, manager)
	_, _ = manager.Submit(Request{TaskID: "low", Kind: TriggerPeriodic, Priority: PriorityPeriodic})
	<-lowStarted
	high, _ := manager.Submit(Request{TaskID: "high", Kind: TriggerEvent, Priority: PriorityMutation, Durable: true})
	<-lowCancelled
	assertNotReceived(t, highStarted)
	close(allowLowExit)
	<-highStarted
	_, _ = manager.Await(context.Background(), high.TriggerID)
}

func TestNonInterruptibleWorkReachesSafeBoundary(t *testing.T) {
	lowStarted := make(chan struct{})
	releaseLow := make(chan struct{})
	highStarted := make(chan struct{})
	claim := []ResourceClaim{{Resource: "mutation", Mode: ClaimExclusive}}
	manager := New(
		Definition{ID: "low", Name: "Low", Resources: claim, Runner: func(context.Context) error { close(lowStarted); <-releaseLow; return nil }},
		Definition{ID: "high", Name: "High", Priority: PriorityMutation, Resources: claim, Runner: func(context.Context) error { close(highStarted); return nil }},
	)
	startManager(t, manager)
	_, _ = manager.Submit(Request{TaskID: "low", Kind: TriggerPeriodic})
	<-lowStarted
	_, _ = manager.Submit(Request{TaskID: "high", Kind: TriggerEvent, Priority: PriorityMutation, Durable: true})
	assertNotReceived(t, highStarted)
	close(releaseLow)
	<-highStarted
}

func TestBoundedAgingPreventsRoutineWorkStarvation(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC))
	order := make(chan string, 2)
	claim := []ResourceClaim{{Resource: "one-at-a-time", Mode: ClaimExclusive}}
	manager := NewWithClock(clock,
		Definition{ID: "periodic", Name: "Periodic", Resources: claim, Runner: func(context.Context) error { order <- "periodic"; return nil }},
		Definition{ID: "manual", Name: "Manual", Resources: claim, Runner: func(context.Context) error { order <- "manual"; return nil }},
	)
	_, _ = manager.Submit(Request{TaskID: "periodic", Kind: TriggerPeriodic, Priority: PriorityPeriodic})
	clock.Advance(35 * time.Minute)
	_, _ = manager.Submit(Request{TaskID: "manual", Kind: TriggerManual, Priority: PriorityManual, Durable: true})
	clock.Advance(5 * time.Minute)
	startManager(t, manager)
	if first := receive(t, order); first != "periodic" {
		t.Fatalf("aged periodic work remained starved behind %s", first)
	}
	if second := receive(t, order); second != "manual" {
		t.Fatalf("unexpected second execution %s", second)
	}
}

func TestRetryUsesInjectedClock(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC))
	started := make(chan int, 2)
	var attempts atomic.Int32
	manager := NewWithClock(clock, Definition{ID: "retry", Name: "Retry", Retry: RetryPolicy{MaxAttempts: 2, InitialDelay: time.Minute}, Runner: func(context.Context) error {
		attempt := int(attempts.Add(1))
		started <- attempt
		if attempt == 1 {
			return Retryable(fmt.Errorf("temporary"))
		}
		return nil
	}})
	startManager(t, manager)
	receipt, _ := manager.Submit(Request{TaskID: "retry", Durable: true, Cause: "service unavailable", RequiredCoverage: 17})
	if receive(t, started) != 1 {
		t.Fatal("first attempt missing")
	}
	waitUntil(t, func() bool {
		status := manager.Snapshot()[0]
		return status.Queued && status.State == "Backoff"
	})
	clock.Advance(59 * time.Second)
	assertNotReceived(t, started)
	clock.Advance(time.Second)
	if receive(t, started) != 2 {
		t.Fatal("retry attempt missing")
	}
	result, err := manager.Await(context.Background(), receipt.TriggerID)
	if err != nil {
		t.Fatal(err)
	}
	trigger, _ := manager.Trigger(receipt.TriggerID)
	execution, _ := manager.Execution(result.ExecutionID)
	if trigger.Cause != "service unavailable" || trigger.RequiredCoverage != 17 || execution.Coverage != 17 || execution.Attempt != 2 {
		t.Fatalf("retry lost scheduling facts: trigger=%#v execution=%#v", trigger, execution)
	}
}

func TestRunnerPanicBecomesVisibleFailure(t *testing.T) {
	manager := New(Definition{ID: "panic", Name: "Panic", Runner: func(context.Context) error { panic("broken runner") }})
	startManager(t, manager)
	receipt, _ := manager.Submit(Request{TaskID: "panic", Durable: true})
	result, err := manager.Await(context.Background(), receipt.TriggerID)
	if err == nil || result.State != "failed" || result.Error != "task panic: broken runner" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestPeriodicDowntimeProducesOneCatchup(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC))
	runs := make(chan struct{}, 2)
	manager := NewWithClock(clock, Definition{ID: "periodic", Name: "Periodic", Interval: time.Hour, Runner: func(context.Context) error { runs <- struct{}{}; return nil }})
	startManager(t, manager)
	waitUntil(t, func() bool { return !manager.Snapshot()[0].NextRun.IsZero() })
	clock.Advance(5 * time.Hour)
	receive(t, runs)
	assertNotReceived(t, runs)
	waitUntil(t, func() bool { return manager.Snapshot()[0].NextRun.Equal(clock.Now().Add(time.Hour)) })
}

func TestPostRemovalWorkflowDebounceAndOrder(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC))
	steps := make(chan string, 2)
	manager := NewWithClock(clock,
		Definition{ID: "inventory", Name: "Inventory", Runner: func(context.Context) error { steps <- "inventory"; return nil }},
		Definition{ID: "files", Name: "Files", Runner: func(context.Context) error { steps <- "files"; return nil }},
	)
	if err := manager.RegisterWorkflow(WorkflowDefinition{ID: "consistency", Steps: []string{"inventory", "files"}}); err != nil {
		t.Fatal(err)
	}
	startManager(t, manager)
	if _, err := manager.AdvanceWorkflow("consistency", "global", time.Minute, 5*time.Minute, "removal"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(59 * time.Second)
	assertNotReceived(t, steps)
	clock.Advance(time.Second)
	if receive(t, steps) != "inventory" {
		t.Fatal("workflow did not start with inventory")
	}
	if receive(t, steps) != "files" {
		t.Fatal("workflow did not continue with files")
	}
	waitUntil(t, func() bool { return !manager.ConsistencyPending() })
}

func TestRepeatedWorkflowRevisionKeepsFirstMaximumDeadline(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC))
	runs := make(chan struct{}, 1)
	manager := NewWithClock(clock, Definition{ID: "inventory", Name: "Inventory", Runner: func(context.Context) error { runs <- struct{}{}; return nil }})
	_ = manager.RegisterWorkflow(WorkflowDefinition{ID: "consistency", Steps: []string{"inventory"}})
	startManager(t, manager)
	_, _ = manager.AdvanceWorkflow("consistency", "global", time.Minute, 5*time.Minute, "first")
	firstDeadline := manager.WorkflowSnapshot()[0].Deadline
	clock.Advance(30 * time.Second)
	_, _ = manager.AdvanceWorkflow("consistency", "global", time.Minute, 5*time.Minute, "second")
	workflow := manager.WorkflowSnapshot()[0]
	if workflow.Revision != 2 || !workflow.Deadline.Equal(firstDeadline) {
		t.Fatalf("workflow=%#v", workflow)
	}
	clock.Advance(4*time.Minute + 30*time.Second)
	receive(t, runs)
}

func TestNewMutationInvalidatesCompletedWorkflowStep(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC))
	baseStarted := make(chan int, 2)
	releaseFirstBase := make(chan struct{})
	filesStarted := make(chan struct{}, 1)
	mutationFinished := make(chan struct{})
	var baseRuns atomic.Int32
	shared := []ResourceClaim{{Resource: "mutation-boundary", Mode: ClaimShared}}
	exclusive := []ResourceClaim{{Resource: "mutation-boundary", Mode: ClaimExclusive}}
	var manager *Manager
	manager = NewWithClock(clock,
		Definition{ID: "inventory", Name: "Inventory", Resources: shared, Runner: func(context.Context) error {
			run := int(baseRuns.Add(1))
			baseStarted <- run
			if run == 1 {
				<-releaseFirstBase
			}
			return nil
		}},
		Definition{ID: "files", Name: "Files", Resources: shared, Runner: func(context.Context) error { filesStarted <- struct{}{}; return nil }},
		Definition{ID: "mutation", Name: "Mutation", Priority: PriorityMutation, Resources: exclusive, Runner: func(context.Context) error {
			_, err := manager.AdvanceWorkflow("consistency", "global", time.Minute, 5*time.Minute, "new removal")
			close(mutationFinished)
			return err
		}},
	)
	_ = manager.RegisterWorkflow(WorkflowDefinition{ID: "consistency", Steps: []string{"inventory", "files"}})
	startManager(t, manager)
	_, _ = manager.AdvanceWorkflow("consistency", "global", 0, 5*time.Minute, "first removal")
	if receive(t, baseStarted) != 1 {
		t.Fatal("first inventory step missing")
	}
	_, _ = manager.Submit(Request{TaskID: "mutation", Kind: TriggerEvent, Priority: PriorityMutation, Durable: true})
	close(releaseFirstBase)
	<-mutationFinished
	assertNotReceived(t, filesStarted)
	clock.Advance(time.Minute)
	if receive(t, baseStarted) != 2 {
		t.Fatal("new revision did not restart inventory")
	}
	receive(t, filesStarted)
}

type memoryStateStore struct {
	mu    sync.Mutex
	value string
	fail  bool
}

func (store *memoryStateStore) Meta(string) (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.value, nil
}

func (store *memoryStateStore) SetMeta(_ string, value string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.fail {
		return fmt.Errorf("persistence unavailable")
	}
	store.value = value
	return nil
}

func TestSubmissionRollsBackWhenDurabilityFails(t *testing.T) {
	stateStore := &memoryStateStore{}
	manager, err := newManager(stateStore, realClock{}, Definition{ID: "x", Name: "X", Runner: func(context.Context) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	stateStore.mu.Lock()
	stateStore.fail = true
	stateStore.mu.Unlock()
	if _, err := manager.Submit(Request{TaskID: "x", Durable: true}); err == nil {
		t.Fatal("submission unexpectedly succeeded")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.state.Triggers) != 0 || len(manager.state.Pending) != 0 {
		t.Fatalf("failed submission leaked scheduler state: %#v", manager.state)
	}
}

func TestOnlyDurablePendingWorkSurvivesRestart(t *testing.T) {
	stateStore := &memoryStateStore{}
	definition := Definition{ID: "x", Name: "X", Runner: func(context.Context) error { return nil }}
	manager, err := newManager(stateStore, realClock{}, definition)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = manager.Submit(Request{TaskID: "x", Kind: TriggerPeriodic, Durable: false, CoalescingKey: "ephemeral"})
	durable, _ := manager.Submit(Request{TaskID: "x", Kind: TriggerEvent, Durable: true, CoalescingKey: "durable"})
	restarted, err := newManager(stateStore, realClock{}, definition)
	if err != nil {
		t.Fatal(err)
	}
	restarted.mu.Lock()
	defer restarted.mu.Unlock()
	if len(restarted.state.Pending) != 1 || restarted.state.Triggers[durable.TriggerID] == nil {
		t.Fatalf("unexpected recovered state: %#v", restarted.state)
	}
}

func TestRestartMarksNonReplayableMutationForAttention(t *testing.T) {
	stateStore := &memoryStateStore{}
	started := make(chan struct{})
	manager, err := newManager(stateStore, realClock{}, Definition{ID: "mutation", Name: "Mutation", Recovery: RecoveryAttention, Runner: func(ctx context.Context) error { close(started); <-ctx.Done(); return ctx.Err() }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go manager.Start(ctx)
	receipt, _ := manager.Submit(Request{TaskID: "mutation", Kind: TriggerEvent, Priority: PriorityMutation, Durable: true})
	<-started
	manager.mu.Lock()
	if err := manager.persistLocked(); err != nil {
		t.Fatal(err)
	}
	manager.mu.Unlock()
	restarted, err := newManager(stateStore, realClock{}, Definition{ID: "mutation", Name: "Mutation", Recovery: RecoveryAttention, Runner: func(context.Context) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := restarted.Await(context.Background(), receipt.TriggerID)
	if err == nil || result.State != "attention" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	cancel()
}

func TestRemovedTaskDefinitionRequiresAttentionWithoutSpinning(t *testing.T) {
	manager := New(Definition{ID: "known", Name: "Known", Runner: func(context.Context) error { return nil }})
	now := time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)
	manager.state.Triggers["trigger-1"] = &Trigger{ID: "trigger-1", TaskID: "removed", Durable: true, State: "pending", CreatedAt: now}
	manager.state.Pending[intentKey("removed", "")] = &pendingIntent{ID: "intent-2", TaskID: "removed", TriggerIDs: []string{"trigger-1"}, CreatedAt: now}
	startManager(t, manager)
	result, err := manager.Await(context.Background(), "trigger-1")
	if err == nil || result.State != "attention" || result.Error != "registered task definition no longer exists" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestShutdownKeepsStateOwnershipUntilRunnerExits(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC))
	runnerStarted := make(chan struct{})
	runnerCancelled := make(chan struct{})
	allowRunnerExit := make(chan struct{})
	manager := NewWithClock(clock, Definition{ID: "safe-boundary", Name: "Safe boundary", Runner: func(ctx context.Context) error {
		close(runnerStarted)
		<-ctx.Done()
		close(runnerCancelled)
		<-allowRunnerExit
		return ctx.Err()
	}})
	manager.shutdownGrace = time.Minute
	applicationContext, stopApplication := context.WithCancel(context.Background())
	go manager.Start(applicationContext)
	_, _ = manager.Submit(Request{TaskID: "safe-boundary", Durable: true})
	receive(t, runnerStarted)
	stopApplication()
	waitUntil(t, func() bool {
		clock.mu.Lock()
		defer clock.mu.Unlock()
		return len(clock.waiters) == 1
	})
	clock.Advance(time.Minute)
	receive(t, runnerCancelled)
	assertNotReceived(t, manager.Done())
	close(allowRunnerExit)
	receive(t, manager.Done())
}
