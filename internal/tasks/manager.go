package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const persistedStateKey = "scheduler.state.v1"

type pendingIntent struct {
	ID               string          `json:"id"`
	TaskID           string          `json:"taskId"`
	Key              string          `json:"key"`
	TriggerIDs       []string        `json:"triggerIds"`
	Priority         Priority        `json:"priority"`
	Coverage         uint64          `json:"coverage"`
	Payload          json.RawMessage `json:"payload,omitempty"`
	NotBefore        time.Time       `json:"notBefore,omitempty"`
	Deadline         time.Time       `json:"deadline,omitempty"`
	CreatedAt        time.Time       `json:"createdAt"`
	Attempt          int             `json:"attempt"`
	WorkflowInstance string          `json:"workflowInstance,omitempty"`
}

type persistedState struct {
	Sequence     uint64                       `json:"sequence"`
	Triggers     map[string]*Trigger          `json:"triggers"`
	Executions   map[string]*Execution        `json:"executions"`
	Pending      map[string]*pendingIntent    `json:"pending"`
	Workflows    map[string]*WorkflowInstance `json:"workflows"`
	PeriodicNext map[string]time.Time         `json:"periodicNext"`
}

type resourceUse struct {
	shared    map[string]bool
	exclusive string
}

type runtimeExecution struct {
	cancel context.CancelFunc
	claims []ResourceClaim
}

type Manager struct {
	mu sync.Mutex

	definitions         map[string]Definition
	workflowDefinitions map[string]WorkflowDefinition
	state               persistedState
	store               StateStore
	clock               Clock

	running       map[string]*runtimeExecution
	resources     map[string]*resourceUse
	wake          chan struct{}
	changed       chan struct{}
	done          chan struct{}
	started       bool
	appCtx        context.Context
	sequence      atomic.Uint64
	shutdownGrace time.Duration
}

func New(definitions ...Definition) *Manager {
	manager, err := newManager(nil, realClock{}, definitions...)
	if err != nil {
		panic(err)
	}
	return manager
}

func NewPersistent(store StateStore, definitions ...Definition) (*Manager, error) {
	return newManager(store, realClock{}, definitions...)
}

func NewWithClock(clock Clock, definitions ...Definition) *Manager {
	manager, err := newManager(nil, clock, definitions...)
	if err != nil {
		panic(err)
	}
	return manager
}

func newManager(store StateStore, clock Clock, definitions ...Definition) (*Manager, error) {
	if clock == nil {
		clock = realClock{}
	}
	manager := &Manager{
		definitions:         map[string]Definition{},
		workflowDefinitions: map[string]WorkflowDefinition{},
		store:               store,
		clock:               clock,
		running:             map[string]*runtimeExecution{},
		resources:           map[string]*resourceUse{},
		wake:                make(chan struct{}, 1),
		changed:             make(chan struct{}),
		done:                make(chan struct{}),
		state: persistedState{
			Triggers: map[string]*Trigger{}, Executions: map[string]*Execution{},
			Pending: map[string]*pendingIntent{}, Workflows: map[string]*WorkflowInstance{},
			PeriodicNext: map[string]time.Time{},
		},
		shutdownGrace: 10 * time.Second,
	}
	if store != nil {
		encoded, err := store.Meta(persistedStateKey)
		if err != nil {
			return nil, fmt.Errorf("load scheduler state: %w", err)
		}
		if encoded != "" {
			if err := json.Unmarshal([]byte(encoded), &manager.state); err != nil {
				return nil, fmt.Errorf("decode scheduler state: %w", err)
			}
			manager.normalizeState()
		}
	}
	manager.sequence.Store(manager.state.Sequence)
	for _, definition := range definitions {
		if err := manager.registerLocked(definition); err != nil {
			return nil, err
		}
	}
	manager.recoverInterruptedLocked()
	if err := manager.persistLocked(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (manager *Manager) normalizeState() {
	if manager.state.Triggers == nil {
		manager.state.Triggers = map[string]*Trigger{}
	}
	if manager.state.Executions == nil {
		manager.state.Executions = map[string]*Execution{}
	}
	if manager.state.Pending == nil {
		manager.state.Pending = map[string]*pendingIntent{}
	}
	if manager.state.Workflows == nil {
		manager.state.Workflows = map[string]*WorkflowInstance{}
	}
	if manager.state.PeriodicNext == nil {
		manager.state.PeriodicNext = map[string]time.Time{}
	}
}

func (manager *Manager) Register(definition Definition) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.registerLocked(definition)
}

func (manager *Manager) registerLocked(definition Definition) error {
	definition.ID = strings.TrimSpace(definition.ID)
	if definition.ID == "" {
		return fmt.Errorf("task id is required")
	}
	if definition.Runner == nil && definition.PayloadRunner == nil {
		return fmt.Errorf("task %q has no runner", definition.ID)
	}
	if _, exists := manager.definitions[definition.ID]; exists {
		return fmt.Errorf("task %q already registered", definition.ID)
	}
	if definition.Priority == 0 {
		definition.Priority = PriorityPeriodic
	}
	// Version 1 intentionally coalesces all registered task definitions. Force
	// another run is not part of the public contract yet.
	definition.Coalesce = true
	if definition.Recovery == "" {
		definition.Recovery = RecoveryRetry
	}
	if definition.Runner != nil {
		definition.AllowManual = true
	}
	for _, claim := range definition.Resources {
		if strings.TrimSpace(claim.Resource) == "" {
			return fmt.Errorf("task %q has an empty resource claim", definition.ID)
		}
		if claim.Mode != ClaimShared && claim.Mode != ClaimExclusive {
			return fmt.Errorf("task %q has invalid claim mode %q", definition.ID, claim.Mode)
		}
	}
	manager.definitions[definition.ID] = definition
	if definition.Interval > 0 && manager.state.PeriodicNext[definition.ID].IsZero() {
		manager.state.PeriodicNext[definition.ID] = manager.clock.Now().Add(definition.Interval)
	}
	return nil
}

func (manager *Manager) RegisterWorkflow(definition WorkflowDefinition) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if strings.TrimSpace(definition.ID) == "" || len(definition.Steps) == 0 {
		return fmt.Errorf("workflow id and steps are required")
	}
	if _, exists := manager.workflowDefinitions[definition.ID]; exists {
		return fmt.Errorf("workflow %q already registered", definition.ID)
	}
	manager.workflowDefinitions[definition.ID] = definition
	manager.signalLocked()
	return nil
}

func (manager *Manager) nextID(prefix string) string {
	sequence := manager.sequence.Add(1)
	manager.state.Sequence = sequence
	return fmt.Sprintf("%s-%d", prefix, sequence)
}

func intentKey(taskID, key string) string {
	if key == "" {
		key = "default"
	}
	return taskID + "\x00" + key
}

func (manager *Manager) Submit(request Request) (Receipt, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	var previousState persistedState
	var previousSequence uint64
	if manager.store != nil {
		var err error
		previousState, previousSequence, err = manager.copyStateLocked()
		if err != nil {
			return Receipt{}, err
		}
	}
	receipt, err := manager.submitLocked(request)
	if err != nil {
		return Receipt{}, err
	}
	if err := manager.persistLocked(); err != nil {
		if manager.store != nil {
			manager.state = previousState
			manager.sequence.Store(previousSequence)
		}
		return Receipt{}, fmt.Errorf("persist accepted trigger: %w", err)
	}
	manager.signalLocked()
	manager.signalChangedLocked()
	return receipt, nil
}

func (manager *Manager) submitLocked(request Request) (Receipt, error) {
	definition, exists := manager.definitions[request.TaskID]
	if !exists {
		return Receipt{}, fmt.Errorf("unknown task %q", request.TaskID)
	}
	if request.Kind == "" {
		request.Kind = TriggerManual
	}
	if request.Kind == TriggerManual && !definition.AllowManual {
		return Receipt{}, fmt.Errorf("task %q cannot be run without an operation payload", request.TaskID)
	}
	if request.Priority == 0 {
		request.Priority = definition.Priority
		if request.Kind == TriggerManual && request.Priority < PriorityManual {
			request.Priority = PriorityManual
		}
	}
	now := manager.clock.Now()
	trigger := &Trigger{
		ID: manager.nextID("trigger"), TaskID: request.TaskID, Kind: request.Kind,
		Priority: request.Priority, Durable: request.Durable, CoalescingKey: request.CoalescingKey,
		Cause: request.Cause, RequiredCoverage: request.RequiredCoverage, NotBefore: request.NotBefore,
		Deadline: request.Deadline, Payload: append(json.RawMessage(nil), request.Payload...),
		WorkflowInstance: request.WorkflowInstance, State: "pending", Disposition: "pending", CreatedAt: now,
	}
	manager.state.Triggers[trigger.ID] = trigger
	key := intentKey(request.TaskID, request.CoalescingKey)
	if definition.Coalesce {
		for executionID := range manager.running {
			execution := manager.state.Executions[executionID]
			activeKinds := make([]TriggerKind, 0, len(execution.TriggerIDs))
			for _, triggerID := range execution.TriggerIDs {
				if activeTrigger := manager.state.Triggers[triggerID]; activeTrigger != nil {
					activeKinds = append(activeKinds, activeTrigger.Kind)
				}
			}
			compatible := definition.AttachCompatible == nil || definition.AttachCompatible(activeKinds, request.Kind)
			if compatible && execution.TaskID == request.TaskID && intentKey(execution.TaskID, execution.CoalescingKey) == key && execution.Coverage >= request.RequiredCoverage {
				execution.TriggerIDs = append(execution.TriggerIDs, trigger.ID)
				trigger.State, trigger.Disposition, trigger.ExecutionID, trigger.TargetID = "attached", "attached", execution.ID, execution.ID
				return Receipt{TriggerID: trigger.ID, ExecutionID: execution.ID, Disposition: "attached", TargetID: execution.ID}, nil
			}
		}
		if pending := manager.state.Pending[key]; pending != nil {
			pending.TriggerIDs = append(pending.TriggerIDs, trigger.ID)
			if request.Priority > pending.Priority {
				pending.Priority = request.Priority
			}
			if request.RequiredCoverage > pending.Coverage {
				pending.Coverage = request.RequiredCoverage
			}
			pending.NotBefore = laterTime(pending.NotBefore, request.NotBefore)
			pending.Deadline = earlierNonZero(pending.Deadline, request.Deadline)
			if len(request.Payload) > 0 {
				pending.Payload = append(json.RawMessage(nil), request.Payload...)
			}
			trigger.Disposition, trigger.TargetID = "coalesced", pending.ID
			return Receipt{TriggerID: trigger.ID, Disposition: "coalesced", TargetID: pending.ID}, nil
		}
	}
	intent := &pendingIntent{
		ID: manager.nextID("intent"), TaskID: request.TaskID, Key: request.CoalescingKey,
		TriggerIDs: []string{trigger.ID}, Priority: request.Priority, Coverage: request.RequiredCoverage,
		Payload: append(json.RawMessage(nil), request.Payload...), NotBefore: request.NotBefore,
		Deadline: request.Deadline, CreatedAt: now, Attempt: 1, WorkflowInstance: request.WorkflowInstance,
	}
	manager.state.Pending[key] = intent
	trigger.TargetID = intent.ID
	return Receipt{TriggerID: trigger.ID, Disposition: "pending", TargetID: intent.ID}, nil
}

func (manager *Manager) Await(ctx context.Context, triggerID string) (Result, error) {
	for {
		manager.mu.Lock()
		trigger := manager.state.Triggers[triggerID]
		if trigger == nil {
			manager.mu.Unlock()
			return Result{}, fmt.Errorf("unknown trigger %q", triggerID)
		}
		if terminalTrigger(trigger.State) {
			result := Result{TriggerID: trigger.ID, ExecutionID: trigger.ExecutionID, State: trigger.State, Warning: trigger.Warning, Error: trigger.Error, FinishedAt: trigger.FinishedAt}
			manager.mu.Unlock()
			if trigger.State == "failed" || trigger.State == "rejected" || trigger.State == "attention" {
				return result, errors.New(trigger.Error)
			}
			return result, nil
		}
		changed := manager.changed
		manager.mu.Unlock()
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-changed:
		}
	}
}

// Changes returns a one-shot notification channel for scheduler-visible state.
// Callers must obtain a new channel after each notification. The scheduler
// publishes no domain payload here; consumers read an authoritative snapshot.
func (manager *Manager) Changes() <-chan struct{} {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.changed
}

func terminalTrigger(state string) bool {
	switch state {
	case "fulfilled", "failed", "cancelled", "interrupted", "rejected", "attention":
		return true
	}
	return false
}

func (manager *Manager) Run(ctx context.Context, taskID string) error {
	receipt, err := manager.Submit(Request{TaskID: taskID, Kind: TriggerManual, Priority: PriorityManual, Durable: true, Cause: "Run now"})
	if err != nil {
		return err
	}
	_, err = manager.Await(ctx, receipt.TriggerID)
	return err
}

func (manager *Manager) RunAsyncApp(taskID string) (Receipt, error) {
	return manager.Submit(Request{TaskID: taskID, Kind: TriggerManual, Priority: PriorityManual, Durable: true, Cause: "Run now"})
}

func (manager *Manager) Start(ctx context.Context) {
	manager.mu.Lock()
	if manager.started {
		manager.mu.Unlock()
		return
	}
	manager.started, manager.appCtx = true, ctx
	manager.signalLocked()
	manager.mu.Unlock()
	for {
		manager.mu.Lock()
		manager.createPeriodicTriggersLocked()
		manager.createWorkflowTriggersLocked()
		manager.preemptForPriorityLocked()
		manager.admitReadyLocked(ctx)
		delay := manager.nextWakeDelayLocked()
		if err := manager.persistLocked(); err != nil {
			log.Printf("[scheduler] state: %v", err)
		}
		manager.mu.Unlock()
		var timer <-chan time.Time
		if delay >= 0 {
			timer = manager.clock.After(delay)
		}
		select {
		case <-ctx.Done():
			manager.stop()
			return
		case <-manager.wake:
		case <-timer:
		}
	}
}

func (manager *Manager) stop() {
	manager.mu.Lock()
	for executionID, runtime := range manager.running {
		definition := manager.definitions[manager.state.Executions[executionID].TaskID]
		if definition.Interruptible {
			runtime.cancel()
		}
	}
	manager.mu.Unlock()
	graceTimer := manager.clock.After(manager.shutdownGrace)
	graceExpired := false
	for {
		manager.mu.Lock()
		if len(manager.running) == 0 {
			_ = manager.persistLocked()
			manager.signalChangedLocked()
			close(manager.done)
			manager.mu.Unlock()
			return
		}
		changed := manager.changed
		manager.mu.Unlock()
		select {
		case <-changed:
		case <-graceTimer:
			manager.mu.Lock()
			for _, runtime := range manager.running {
				runtime.cancel()
			}
			manager.mu.Unlock()
			graceExpired = true
		}
		if graceExpired {
			// Go cannot safely terminate a runner goroutine. Keep ownership of its
			// resources and the state store until it actually returns.
			continue
		}
	}
}

func (manager *Manager) Done() <-chan struct{} { return manager.done }

func (manager *Manager) createPeriodicTriggersLocked() {
	now := manager.clock.Now()
	for taskID, definition := range manager.definitions {
		if definition.Interval <= 0 {
			continue
		}
		next := manager.state.PeriodicNext[taskID]
		if next.IsZero() {
			manager.state.PeriodicNext[taskID] = now.Add(definition.Interval)
			continue
		}
		if now.Before(next) {
			continue
		}
		_, _ = manager.submitLocked(Request{TaskID: taskID, Kind: TriggerPeriodic, Priority: PriorityPeriodic, Durable: false, Cause: "Periodic schedule", RequiredCoverage: uint64(next.UnixNano())})
		elapsed := now.Sub(next)
		steps := elapsed/definition.Interval + 1
		manager.state.PeriodicNext[taskID] = next.Add(steps * definition.Interval)
	}
}

func (manager *Manager) admitReadyLocked(appContext context.Context) {
	for {
		intent := manager.nextReadyIntentLocked()
		if intent == nil {
			return
		}
		definition, exists := manager.definitions[intent.TaskID]
		if !exists {
			return
		}
		delete(manager.state.Pending, intentKey(intent.TaskID, intent.Key))
		now := manager.clock.Now()
		execution := &Execution{
			ID: manager.nextID("execution"), TaskID: intent.TaskID, CoalescingKey: intent.Key,
			TriggerIDs: append([]string(nil), intent.TriggerIDs...), Priority: intent.Priority,
			Coverage: intent.Coverage, Payload: append(json.RawMessage(nil), intent.Payload...),
			State: "running", Attempt: intent.Attempt, CreatedAt: intent.CreatedAt,
			AdmittedAt: now, StartedAt: now,
		}
		manager.state.Executions[execution.ID] = execution
		for _, triggerID := range intent.TriggerIDs {
			trigger := manager.state.Triggers[triggerID]
			trigger.State, trigger.ExecutionID, trigger.TargetID = "running", execution.ID, execution.ID
		}
		manager.acquireResourcesLocked(execution.ID, definition.Resources)
		runParent := appContext
		if !definition.Interruptible {
			runParent = context.WithoutCancel(appContext)
		}
		kinds := make([]TriggerKind, 0, len(intent.TriggerIDs))
		for _, triggerID := range intent.TriggerIDs {
			if trigger := manager.state.Triggers[triggerID]; trigger != nil {
				kinds = append(kinds, trigger.Kind)
			}
		}
		executionContext := &ExecutionContext{ID: execution.ID, TaskID: execution.TaskID, TriggerKinds: kinds}
		runParent = context.WithValue(runParent, executionContextKey{}, executionContext)
		runContext, cancel := context.WithCancel(runParent)
		manager.running[execution.ID] = &runtimeExecution{cancel: cancel, claims: append([]ResourceClaim(nil), definition.Resources...)}
		manager.signalChangedLocked()
		go manager.execute(runContext, execution.ID, definition)
	}
}

func (manager *Manager) nextReadyIntentLocked() *pendingIntent {
	now := manager.clock.Now()
	ready := []*pendingIntent{}
	for key, intent := range manager.state.Pending {
		if _, exists := manager.definitions[intent.TaskID]; !exists {
			manager.requireAttentionLocked(intent, "registered task definition no longer exists")
			delete(manager.state.Pending, key)
			continue
		}
		readyAt := intent.NotBefore
		if !intent.Deadline.IsZero() && (readyAt.IsZero() || intent.Deadline.Before(readyAt)) {
			readyAt = intent.Deadline
		}
		if !readyAt.IsZero() && now.Before(readyAt) {
			continue
		}
		ready = append(ready, intent)
	}
	sort.Slice(ready, func(first, second int) bool {
		firstPriority := effectivePriority(ready[first], now)
		secondPriority := effectivePriority(ready[second], now)
		if firstPriority != secondPriority {
			return firstPriority > secondPriority
		}
		if !ready[first].CreatedAt.Equal(ready[second].CreatedAt) {
			return ready[first].CreatedAt.Before(ready[second].CreatedAt)
		}
		return idSequence(ready[first].ID) < idSequence(ready[second].ID)
	})
	for _, intent := range ready {
		definition := manager.definitions[intent.TaskID]
		if conflict := manager.resourceConflictLocked(definition.Resources); conflict == "" {
			return intent
		} else {
			for _, triggerID := range intent.TriggerIDs {
				trigger := manager.state.Triggers[triggerID]
				trigger.State = "waiting"
				trigger.Error = "Waiting for " + conflict
			}
		}
	}
	return nil
}

func (manager *Manager) requireAttentionLocked(intent *pendingIntent, reason string) {
	now := manager.clock.Now()
	for _, triggerID := range intent.TriggerIDs {
		if trigger := manager.state.Triggers[triggerID]; trigger != nil {
			trigger.State = "attention"
			trigger.Error = reason
			trigger.FinishedAt = now
		}
	}
	manager.signalChangedLocked()
}

func effectivePriority(intent *pendingIntent, now time.Time) Priority {
	if intent.Priority >= PriorityMutation || !now.After(intent.CreatedAt) {
		return intent.Priority
	}
	aged := intent.Priority + Priority(now.Sub(intent.CreatedAt)/time.Minute)
	if aged >= PriorityMutation {
		return PriorityMutation - 1
	}
	return aged
}

func (manager *Manager) execute(ctx context.Context, executionID string, definition Definition) {
	var err error
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("task panic: %v", recovered)
		}
		executionContext, _ := FromContext(ctx)
		manager.finishExecution(executionID, definition, executionContext, err)
	}()
	if definition.Preflight != nil {
		err = definition.Preflight(ctx)
	}
	if err == nil {
		manager.mu.Lock()
		payload := append(json.RawMessage(nil), manager.state.Executions[executionID].Payload...)
		manager.mu.Unlock()
		if definition.PayloadRunner != nil {
			err = definition.PayloadRunner(ctx, payload)
		} else {
			err = definition.Runner(ctx)
		}
	}
}

func (manager *Manager) finishExecution(executionID string, definition Definition, executionContext *ExecutionContext, runError error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	execution := manager.state.Executions[executionID]
	if execution == nil {
		return
	}
	now := manager.clock.Now()
	execution.FinishedAt = now
	interrupted := errors.Is(runError, context.Canceled)
	if interrupted {
		execution.State, execution.Warning = "interrupted", "Yielded to higher-priority work"
	} else if runError != nil {
		execution.State, execution.Error = "failed", runError.Error()
	} else {
		execution.State = "succeeded"
		if definition.Advisory != nil {
			execution.Warning = definition.Advisory()
			if execution.Warning != "" {
				execution.State = "degraded"
			}
		}
	}
	runtime := manager.running[executionID]
	if runtime != nil {
		runtime.cancel()
		manager.releaseResourcesLocked(executionID, runtime.claims)
		delete(manager.running, executionID)
	}
	if interrupted && definition.Recovery == RecoveryRetry {
		manager.requeueExecutionLocked(execution, definition.InterruptionDelay)
	} else if interrupted {
		for _, triggerID := range execution.TriggerIDs {
			trigger := manager.state.Triggers[triggerID]
			trigger.FinishedAt, trigger.Warning, trigger.Error = now, execution.Warning, "interrupted work requires verification"
			trigger.State = "attention"
		}
	} else if runError != nil && IsRetryable(runError) && execution.Attempt < definition.Retry.MaxAttempts {
		manager.requeueExecutionLocked(execution, retryDelay(definition.Retry, execution.Attempt))
	} else {
		for _, triggerID := range execution.TriggerIDs {
			trigger := manager.state.Triggers[triggerID]
			trigger.FinishedAt, trigger.Warning, trigger.Error = now, execution.Warning, execution.Error
			if runError != nil {
				trigger.State = "failed"
				if trigger.WorkflowInstance != "" {
					if workflow := manager.state.Workflows[trigger.WorkflowInstance]; workflow != nil {
						workflow.State = "attention"
						workflow.TriggerID = ""
					}
				}
			} else {
				trigger.State = "fulfilled"
			}
		}
		if runError == nil {
			manager.satisfyWorkflowsLocked(execution)
		}
	}
	manager.trimHistoryLocked(500)
	if err := manager.persistLocked(); err != nil {
		log.Printf("[scheduler] state: %v", err)
	}
	kinds := []string{}
	metrics := ""
	for _, triggerID := range execution.TriggerIDs {
		if trigger := manager.state.Triggers[triggerID]; trigger != nil {
			kinds = append(kinds, string(trigger.Kind))
		}
	}
	if executionContext != nil {
		metrics = executionContext.metricSummary()
	}
	sort.Strings(kinds)
	summary := fmt.Sprintf("[scheduler] execution=%s task=%s triggers=%s status=%s attempt=%d duration=%s", execution.ID, execution.TaskID, strings.Join(kinds, ","), execution.State, execution.Attempt, execution.FinishedAt.Sub(execution.StartedAt).Round(time.Millisecond))
	if metrics != "" {
		summary += " " + metrics
	}
	if execution.Warning != "" {
		summary += fmt.Sprintf(" warning=%q", execution.Warning)
	}
	if execution.Error != "" {
		summary += fmt.Sprintf(" error=%q", execution.Error)
	}
	log.Print(summary)
	manager.signalLocked()
	manager.signalChangedLocked()
}

func retryDelay(policy RetryPolicy, attempt int) time.Duration {
	if policy.InitialDelay <= 0 {
		return time.Minute
	}
	delay := policy.InitialDelay
	for index := 1; index < attempt; index++ {
		delay *= 2
		if policy.MaxDelay > 0 && delay >= policy.MaxDelay {
			return policy.MaxDelay
		}
	}
	return delay
}

func (manager *Manager) requeueExecutionLocked(execution *Execution, delay time.Duration) {
	key := intentKey(execution.TaskID, execution.CoalescingKey)
	intent := manager.state.Pending[key]
	if intent == nil {
		intent = &pendingIntent{
			ID: manager.nextID("intent"), TaskID: execution.TaskID, Key: execution.CoalescingKey,
			Priority: execution.Priority, Coverage: execution.Coverage,
			Payload: append(json.RawMessage(nil), execution.Payload...), CreatedAt: manager.clock.Now(),
			Attempt: execution.Attempt + 1,
		}
		manager.state.Pending[key] = intent
	} else if execution.Attempt+1 > intent.Attempt {
		intent.Attempt = execution.Attempt + 1
	}
	intent.TriggerIDs = appendUnique(intent.TriggerIDs, execution.TriggerIDs...)
	if execution.Priority > intent.Priority {
		intent.Priority = execution.Priority
	}
	if execution.Coverage > intent.Coverage {
		intent.Coverage = execution.Coverage
	}
	if delay > 0 {
		intent.NotBefore = laterTime(intent.NotBefore, manager.clock.Now().Add(delay))
	}
	for _, triggerID := range execution.TriggerIDs {
		trigger := manager.state.Triggers[triggerID]
		trigger.State, trigger.ExecutionID, trigger.Error = "pending", "", ""
		trigger.TargetID = intent.ID
		trigger.NotBefore = intent.NotBefore
	}
}

func (manager *Manager) resourceConflictLocked(claims []ResourceClaim) string {
	for _, claim := range claims {
		use := manager.resources[claim.Resource]
		if use == nil {
			continue
		}
		if claim.Mode == ClaimExclusive && (len(use.shared) > 0 || use.exclusive != "") {
			return manager.resourceWaitDescriptionLocked(claim.Resource, use)
		}
		if claim.Mode == ClaimShared && use.exclusive != "" {
			return manager.resourceWaitDescriptionLocked(claim.Resource, use)
		}
	}
	return ""
}

func (manager *Manager) acquireResourcesLocked(executionID string, claims []ResourceClaim) {
	for _, claim := range claims {
		use := manager.resources[claim.Resource]
		if use == nil {
			use = &resourceUse{shared: map[string]bool{}}
			manager.resources[claim.Resource] = use
		}
		if claim.Mode == ClaimExclusive {
			use.exclusive = executionID
		} else {
			use.shared[executionID] = true
		}
	}
}

func (manager *Manager) releaseResourcesLocked(executionID string, claims []ResourceClaim) {
	for _, claim := range claims {
		use := manager.resources[claim.Resource]
		if use == nil {
			continue
		}
		if claim.Mode == ClaimExclusive && use.exclusive == executionID {
			use.exclusive = ""
		} else if claim.Mode == ClaimShared {
			delete(use.shared, executionID)
		}
		if len(use.shared) == 0 && use.exclusive == "" {
			delete(manager.resources, claim.Resource)
		}
	}
}

func (manager *Manager) resourceWaitDescriptionLocked(resource string, use *resourceUse) string {
	holderID := use.exclusive
	if holderID == "" {
		for executionID := range use.shared {
			holderID = executionID
			break
		}
	}
	if execution := manager.state.Executions[holderID]; execution != nil {
		if definition, exists := manager.definitions[execution.TaskID]; exists {
			return resource + " held by " + definition.Name
		}
	}
	return resource
}

func claimsConflict(first, second []ResourceClaim) bool {
	for _, left := range first {
		for _, right := range second {
			if left.Resource == right.Resource && (left.Mode == ClaimExclusive || right.Mode == ClaimExclusive) {
				return true
			}
		}
	}
	return false
}

func (manager *Manager) preemptForPriorityLocked() {
	for _, intent := range manager.state.Pending {
		if intent.Priority < PriorityMutation {
			continue
		}
		definition := manager.definitions[intent.TaskID]
		for executionID, runtime := range manager.running {
			runningExecution := manager.state.Executions[executionID]
			runningDefinition := manager.definitions[runningExecution.TaskID]
			if runningDefinition.Interruptible && claimsConflict(definition.Resources, runtime.claims) && intent.Priority > runningExecution.Priority {
				runningExecution.State, runningExecution.Waiting = "cancelling", "Yielding to "+intent.Priority.String()
				runtime.cancel()
			}
		}
	}
}

func (manager *Manager) nextWakeDelayLocked() time.Duration {
	now := manager.clock.Now()
	var next time.Time
	for _, intent := range manager.state.Pending {
		definition, exists := manager.definitions[intent.TaskID]
		if !exists {
			return 0
		}
		candidate := intent.NotBefore
		if !intent.Deadline.IsZero() && (candidate.IsZero() || intent.Deadline.Before(candidate)) {
			candidate = intent.Deadline
		}
		if candidate.IsZero() || !candidate.After(now) {
			if manager.resourceConflictLocked(definition.Resources) == "" {
				return 0
			}
			continue
		}
		next = earlierNonZero(next, candidate)
	}
	for taskID, due := range manager.state.PeriodicNext {
		if manager.definitions[taskID].Interval <= 0 {
			continue
		}
		if !due.After(now) {
			return 0
		}
		next = earlierNonZero(next, due)
	}
	for _, instance := range manager.state.Workflows {
		if instance.State == "succeeded" || instance.State == "attention" || instance.TriggerID != "" {
			continue
		}
		if _, exists := manager.workflowDefinitions[instance.DefinitionID]; !exists {
			continue
		}
		candidate := instance.NotBefore
		if instance.CurrentStep > 0 {
			candidate = instance.StepReadyAfter
		}
		if !instance.Deadline.IsZero() && (candidate.IsZero() || instance.Deadline.Before(candidate)) {
			candidate = instance.Deadline
		}
		if candidate.IsZero() || !candidate.After(now) {
			return 0
		}
		next = earlierNonZero(next, candidate)
	}
	if next.IsZero() {
		return -1
	}
	return next.Sub(now)
}

func (manager *Manager) Snapshot() []Status {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	now := manager.clock.Now()
	statuses := make([]Status, 0, len(manager.definitions))
	for taskID, definition := range manager.definitions {
		status := Status{ID: taskID, Name: definition.Name, Description: definition.Description, Interval: definition.Interval, State: "Idle", NextRun: manager.state.PeriodicNext[taskID], CanRun: definition.AllowManual}
		for _, intent := range manager.state.Pending {
			if intent.TaskID != taskID {
				continue
			}
			status.Pending += len(intent.TriggerIDs)
			status.Queued, status.State, status.Priority = true, "Queued", intent.Priority.String()
			readyAt := intent.NotBefore
			if !intent.Deadline.IsZero() && (readyAt.IsZero() || intent.Deadline.Before(readyAt)) {
				readyAt = intent.Deadline
			}
			if !readyAt.IsZero() && now.Before(readyAt) {
				status.Waiting = "Until " + readyAt.Local().Format("2006-01-02 15:04:05")
				if intent.Attempt > 1 {
					status.State = "Backoff"
				} else {
					status.State = "Scheduled"
				}
			}
			if conflict := manager.resourceConflictLocked(definition.Resources); conflict != "" {
				status.Waiting, status.State = "Resource: "+conflict, "Waiting"
			}
		}
		var latest *Execution
		for executionID := range manager.running {
			execution := manager.state.Executions[executionID]
			if execution.TaskID == taskID {
				latest = execution
				status.Running, status.Priority = true, execution.Priority.String()
				if execution.State == "cancelling" {
					status.State = "Cancelling"
				} else {
					status.State = "Running"
				}
				status.Waiting = execution.Waiting
			}
		}
		for _, execution := range manager.state.Executions {
			if execution.TaskID != taskID {
				continue
			}
			if latest == nil || execution.FinishedAt.After(latest.FinishedAt) {
				latest = execution
			}
		}
		if latest != nil {
			status.LastStarted, status.LastFinished = latest.StartedAt, latest.FinishedAt
			status.LastError, status.LastWarning = latest.Error, latest.Warning
			if !latest.FinishedAt.IsZero() {
				status.LastDuration = latest.FinishedAt.Sub(latest.StartedAt)
			}
			if !status.Running && !status.Queued {
				switch latest.State {
				case "failed":
					status.State = "Failed"
				case "degraded":
					// The advisory that caused this execution to finish
					// degraded may have resolved since (e.g. Jellyfin/Seerr
					// enrichment has since completed) — re-check live rather
					// than showing a frozen warning from whenever this task
					// last ran.
					if definition.Advisory != nil {
						if warning := definition.Advisory(); warning != "" {
							status.State, status.LastWarning = "Degraded", warning
						} else {
							status.LastWarning = ""
						}
					} else {
						status.State = "Degraded"
					}
				case "interrupted":
					status.State = "Interrupted"
				}
			}
		}
		statuses = append(statuses, status)
	}
	sort.Slice(statuses, func(first, second int) bool { return statuses[first].Name < statuses[second].Name })
	return statuses
}

func (manager *Manager) Trigger(triggerID string) (Trigger, bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	trigger := manager.state.Triggers[triggerID]
	if trigger == nil {
		return Trigger{}, false
	}
	copy := *trigger
	copy.Payload = append(json.RawMessage(nil), trigger.Payload...)
	return copy, true
}

// LatestTriggerForKey lets domain recovery reconcile its journal with durable
// scheduler admission without interpreting scheduler persistence directly.
func (manager *Manager) LatestTriggerForKey(taskID, coalescingKey string) (Trigger, bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	var latest *Trigger
	for _, trigger := range manager.state.Triggers {
		if trigger.TaskID != taskID || trigger.CoalescingKey != coalescingKey {
			continue
		}
		if latest == nil || trigger.CreatedAt.After(latest.CreatedAt) || (trigger.CreatedAt.Equal(latest.CreatedAt) && idSequence(trigger.ID) > idSequence(latest.ID)) {
			latest = trigger
		}
	}
	if latest == nil {
		return Trigger{}, false
	}
	copy := *latest
	copy.Payload = append(json.RawMessage(nil), latest.Payload...)
	return copy, true
}

func idSequence(identifier string) uint64 {
	separator := strings.LastIndexByte(identifier, '-')
	if separator < 0 || separator == len(identifier)-1 {
		return 0
	}
	sequence, _ := strconv.ParseUint(identifier[separator+1:], 10, 64)
	return sequence
}

func (manager *Manager) Execution(executionID string) (Execution, bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	execution := manager.state.Executions[executionID]
	if execution == nil {
		return Execution{}, false
	}
	copy := *execution
	copy.TriggerIDs = append([]string(nil), execution.TriggerIDs...)
	copy.Payload = append(json.RawMessage(nil), execution.Payload...)
	return copy, true
}

func (manager *Manager) AdvanceWorkflow(definitionID, key string, quietPeriod, maximumDelay time.Duration, cause string) (string, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	previousState, previousSequence, copyErr := manager.copyStateLocked()
	if copyErr != nil {
		return "", copyErr
	}
	definition, exists := manager.workflowDefinitions[definitionID]
	if !exists {
		return "", fmt.Errorf("unknown workflow %q", definitionID)
	}
	instanceID := definition.ID + ":" + key
	now := manager.clock.Now()
	instance := manager.state.Workflows[instanceID]
	if instance == nil {
		instance = &WorkflowInstance{ID: instanceID, DefinitionID: definition.ID, Key: key, Revision: 1, State: "pending", UpdatedAt: now, StepReadyAfter: now, NotBefore: now.Add(quietPeriod), Deadline: now.Add(maximumDelay)}
		manager.state.Workflows[instanceID] = instance
	} else {
		wasTerminal := instance.State == "succeeded" || instance.State == "attention"
		manager.cancelWorkflowTriggerLocked(instance)
		instance.Revision++
		instance.CurrentStep, instance.State, instance.UpdatedAt, instance.StepReadyAfter = 0, "pending", now, now
		instance.NotBefore = now.Add(quietPeriod)
		if instance.Deadline.IsZero() || wasTerminal {
			instance.Deadline = now.Add(maximumDelay)
		}
		instance.TriggerID = ""
	}
	instance.Cause = cause
	if err := manager.persistLocked(); err != nil {
		manager.state = previousState
		manager.sequence.Store(previousSequence)
		return "", err
	}
	manager.signalLocked()
	manager.signalChangedLocked()
	return instanceID, nil
}

func (manager *Manager) createWorkflowTriggersLocked() {
	now := manager.clock.Now()
	for _, instance := range manager.state.Workflows {
		if instance.State == "succeeded" || instance.State == "attention" {
			continue
		}
		definition, exists := manager.workflowDefinitions[instance.DefinitionID]
		if !exists {
			instance.State = "attention"
			instance.UpdatedAt = now
			continue
		}
		if instance.CurrentStep >= len(definition.Steps) {
			continue
		}
		if instance.TriggerID != "" {
			trigger := manager.state.Triggers[instance.TriggerID]
			if trigger != nil && !terminalTrigger(trigger.State) {
				continue
			}
			instance.TriggerID = ""
		}
		readyAt := instance.NotBefore
		if instance.CurrentStep > 0 {
			readyAt = instance.StepReadyAfter
		}
		if !instance.Deadline.IsZero() && (readyAt.IsZero() || instance.Deadline.Before(readyAt)) {
			readyAt = instance.Deadline
		}
		if !readyAt.IsZero() && now.Before(readyAt) {
			continue
		}
		priority := PriorityConsistency
		if !instance.Deadline.IsZero() && !now.Before(instance.Deadline) {
			priority = PriorityConsistencyDeadline
		}
		receipt, err := manager.submitLocked(Request{
			TaskID: definition.Steps[instance.CurrentStep], Kind: TriggerWorkflow,
			Priority: priority, Durable: true, Cause: instance.Cause, WorkflowInstance: instance.ID,
		})
		if err != nil {
			instance.State = "attention"
			continue
		}
		instance.TriggerID = receipt.TriggerID
		instance.State = "running"
	}
}

func (manager *Manager) satisfyWorkflowsLocked(execution *Execution) {
	for _, instance := range manager.state.Workflows {
		if instance.State == "succeeded" {
			continue
		}
		definition, exists := manager.workflowDefinitions[instance.DefinitionID]
		if !exists || instance.CurrentStep >= len(definition.Steps) || definition.Steps[instance.CurrentStep] != execution.TaskID {
			continue
		}
		if execution.StartedAt.Before(instance.StepReadyAfter) {
			continue
		}
		instance.CurrentStep++
		manager.cancelWorkflowTriggerLocked(instance)
		instance.TriggerID = ""
		instance.StepReadyAfter = execution.FinishedAt
		if instance.CurrentStep >= len(definition.Steps) {
			instance.State = "succeeded"
		} else {
			instance.State = "pending"
		}
	}
}

func (manager *Manager) ConsistencyPending() bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for _, instance := range manager.state.Workflows {
		if instance.State != "succeeded" {
			return true
		}
	}
	return false
}

func (manager *Manager) WorkflowSnapshot() []WorkflowInstance {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	result := make([]WorkflowInstance, 0, len(manager.state.Workflows))
	for _, instance := range manager.state.Workflows {
		result = append(result, *instance)
	}
	sort.Slice(result, func(first, second int) bool { return result[first].ID < result[second].ID })
	return result
}

func (manager *Manager) WorkflowStatuses() []WorkflowStatus {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	result := make([]WorkflowStatus, 0, len(manager.state.Workflows))
	for _, instance := range manager.state.Workflows {
		definition := manager.workflowDefinitions[instance.DefinitionID]
		status := WorkflowStatus{ID: instance.ID, Name: definition.Name, Description: definition.Description, State: instance.State, Revision: instance.Revision, CurrentStep: instance.CurrentStep, TotalSteps: len(definition.Steps), UpdatedAt: instance.UpdatedAt, ReadyAt: instance.NotBefore}
		if instance.CurrentStep < len(definition.Steps) {
			if task, exists := manager.definitions[definition.Steps[instance.CurrentStep]]; exists {
				status.StepName = task.Name
			} else {
				status.StepName = definition.Steps[instance.CurrentStep]
			}
		}
		result = append(result, status)
	}
	sort.Slice(result, func(first, second int) bool { return result[first].Name < result[second].Name })
	return result
}

func (manager *Manager) recoverInterruptedLocked() {
	now := manager.clock.Now()
	for _, execution := range manager.state.Executions {
		if execution.State != "running" && execution.State != "cancelling" {
			continue
		}
		execution.State, execution.FinishedAt, execution.Error = "interrupted", now, "application stopped before execution completed"
		definition, registered := manager.definitions[execution.TaskID]
		hasDurableTrigger := false
		for _, triggerID := range execution.TriggerIDs {
			if trigger := manager.state.Triggers[triggerID]; trigger != nil && trigger.Durable {
				hasDurableTrigger = true
				break
			}
		}
		if registered && definition.Recovery == RecoveryRetry && hasDurableTrigger {
			manager.requeueExecutionLocked(execution, 0)
			continue
		}
		for _, triggerID := range execution.TriggerIDs {
			if trigger := manager.state.Triggers[triggerID]; trigger != nil {
				if trigger.Durable {
					trigger.State, trigger.Error = "attention", "interrupted work requires verification"
				} else {
					trigger.State, trigger.Error = "interrupted", execution.Error
				}
				trigger.FinishedAt = now
			}
		}
	}
}

func (manager *Manager) trimHistoryLocked(limit int) {
	if len(manager.state.Executions) <= limit {
		return
	}
	completed := []*Execution{}
	for executionID, execution := range manager.state.Executions {
		if manager.running[executionID] == nil {
			completed = append(completed, execution)
		}
	}
	sort.Slice(completed, func(first, second int) bool { return completed[first].FinishedAt.Before(completed[second].FinishedAt) })
	for len(manager.state.Executions) > limit && len(completed) > 0 {
		removed := completed[0]
		delete(manager.state.Executions, removed.ID)
		for _, triggerID := range removed.TriggerIDs {
			if trigger := manager.state.Triggers[triggerID]; trigger != nil && terminalTrigger(trigger.State) {
				delete(manager.state.Triggers, triggerID)
			}
		}
		completed = completed[1:]
	}
}

func (manager *Manager) persistLocked() error {
	if manager.store == nil {
		return nil
	}
	manager.state.Sequence = manager.sequence.Load()
	persisted := manager.persistenceViewLocked()
	encoded, err := json.Marshal(persisted)
	if err != nil {
		return err
	}
	return manager.store.SetMeta(persistedStateKey, string(encoded))
}

func (manager *Manager) copyStateLocked() (persistedState, uint64, error) {
	encoded, err := json.Marshal(manager.state)
	if err != nil {
		return persistedState{}, 0, err
	}
	var copied persistedState
	if err := json.Unmarshal(encoded, &copied); err != nil {
		return persistedState{}, 0, err
	}
	return copied, manager.sequence.Load(), nil
}

func (manager *Manager) persistenceViewLocked() persistedState {
	view := manager.state
	view.Triggers = make(map[string]*Trigger, len(manager.state.Triggers))
	for triggerID, trigger := range manager.state.Triggers {
		if trigger.Durable || terminalTrigger(trigger.State) {
			view.Triggers[triggerID] = trigger
		}
	}
	view.Pending = map[string]*pendingIntent{}
	for key, intent := range manager.state.Pending {
		durable := false
		for _, triggerID := range intent.TriggerIDs {
			if trigger := manager.state.Triggers[triggerID]; trigger != nil && trigger.Durable {
				durable = true
				break
			}
		}
		if durable {
			view.Pending[key] = intent
		}
	}
	return view
}

func (manager *Manager) cancelWorkflowTriggerLocked(instance *WorkflowInstance) {
	if instance == nil || instance.TriggerID == "" {
		return
	}
	trigger := manager.state.Triggers[instance.TriggerID]
	if trigger == nil || terminalTrigger(trigger.State) || trigger.State == "running" || trigger.State == "attached" {
		return
	}
	key := intentKey(trigger.TaskID, trigger.CoalescingKey)
	if intent := manager.state.Pending[key]; intent != nil {
		remaining := intent.TriggerIDs[:0]
		for _, triggerID := range intent.TriggerIDs {
			if triggerID != trigger.ID {
				remaining = append(remaining, triggerID)
			}
		}
		intent.TriggerIDs = remaining
		if len(intent.TriggerIDs) == 0 {
			delete(manager.state.Pending, key)
		}
	}
	trigger.State, trigger.FinishedAt = "cancelled", manager.clock.Now()
}

func (manager *Manager) signalLocked() {
	select {
	case manager.wake <- struct{}{}:
	default:
	}
}

func (manager *Manager) signalChangedLocked() {
	close(manager.changed)
	manager.changed = make(chan struct{})
}

func appendUnique(existing []string, values ...string) []string {
	seen := map[string]bool{}
	for _, value := range existing {
		seen[value] = true
	}
	for _, value := range values {
		if !seen[value] {
			existing = append(existing, value)
			seen[value] = true
		}
	}
	return existing
}

func laterTime(first, second time.Time) time.Time {
	if first.IsZero() || second.After(first) {
		return second
	}
	return first
}

func earlierNonZero(first, second time.Time) time.Time {
	if first.IsZero() || (!second.IsZero() && second.Before(first)) {
		return second
	}
	return first
}
