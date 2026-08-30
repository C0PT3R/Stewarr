package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type Runner func(context.Context) error
type PayloadRunner func(context.Context, json.RawMessage) error

type executionContextKey struct{}

// ExecutionContext describes the scheduler execution currently invoking a
// runner. Metrics are accumulated by the runner and emitted in the scheduler's
// single terminal execution summary.
type ExecutionContext struct {
	ID           string
	TaskID       string
	TriggerKinds []TriggerKind

	mu      sync.Mutex
	metrics map[string]string
}

func FromContext(ctx context.Context) (*ExecutionContext, bool) {
	value, ok := ctx.Value(executionContextKey{}).(*ExecutionContext)
	return value, ok && value != nil
}

func TriggeredOnlyBy(ctx context.Context, kind TriggerKind) bool {
	execution, ok := FromContext(ctx)
	if !ok || len(execution.TriggerKinds) == 0 {
		return false
	}
	for _, actual := range execution.TriggerKinds {
		if actual != kind {
			return false
		}
	}
	return true
}

func AddMetric(ctx context.Context, name string, value any) {
	execution, ok := FromContext(ctx)
	if !ok || strings.TrimSpace(name) == "" {
		return
	}
	execution.mu.Lock()
	defer execution.mu.Unlock()
	if execution.metrics == nil {
		execution.metrics = map[string]string{}
	}
	execution.metrics[name] = fmt.Sprint(value)
}

func (execution *ExecutionContext) metricSummary() string {
	execution.mu.Lock()
	defer execution.mu.Unlock()
	keys := make([]string, 0, len(execution.metrics))
	for key := range execution.metrics {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+execution.metrics[key])
	}
	return strings.Join(parts, " ")
}

type taskFailure struct {
	err       error
	retryable bool
}

func (failure taskFailure) Error() string { return failure.err.Error() }
func (failure taskFailure) Unwrap() error { return failure.err }

func Retryable(err error) error {
	if err == nil {
		return nil
	}
	return taskFailure{err: err, retryable: true}
}

func IsRetryable(err error) bool {
	var failure taskFailure
	return errors.As(err, &failure) && failure.retryable
}

type Priority int

const (
	PriorityPeriodic            Priority = 10
	PriorityManual              Priority = 20
	PriorityConsistency         Priority = 30
	PriorityMutation            Priority = 40
	PriorityConsistencyDeadline Priority = 45
	PriorityEmergency           Priority = 50
)

func (priority Priority) String() string {
	switch priority {
	case PriorityEmergency:
		return "Emergency"
	case PriorityMutation:
		return "Mutation"
	case PriorityConsistencyDeadline:
		return "Consistency deadline"
	case PriorityConsistency:
		return "Consistency"
	case PriorityManual:
		return "Manual"
	default:
		return "Periodic"
	}
}

type TriggerKind string

const (
	TriggerStartup  TriggerKind = "startup"
	TriggerPeriodic TriggerKind = "periodic"
	TriggerManual   TriggerKind = "manual"
	TriggerEvent    TriggerKind = "event"
	TriggerWorkflow TriggerKind = "workflow"
	TriggerRetry    TriggerKind = "retry"
)

type ClaimMode string

const (
	ClaimShared    ClaimMode = "shared"
	ClaimExclusive ClaimMode = "exclusive"
)

type ResourceClaim struct {
	Resource string    `json:"resource"`
	Mode     ClaimMode `json:"mode"`
}

type RecoveryPolicy string

const (
	RecoveryRetry     RecoveryPolicy = "retry"
	RecoveryAttention RecoveryPolicy = "attention"
)

type RetryPolicy struct {
	MaxAttempts  int           `json:"maxAttempts"`
	InitialDelay time.Duration `json:"initialDelay"`
	MaxDelay     time.Duration `json:"maxDelay"`
}

type Definition struct {
	ID                string
	Name              string
	Description       string
	Interval          time.Duration
	Preflight         Runner
	Runner            Runner
	PayloadRunner     PayloadRunner
	Advisory          func() string
	AttachCompatible  func(active []TriggerKind, requested TriggerKind) bool
	Resources         []ResourceClaim
	Interruptible     bool
	InterruptionDelay time.Duration
	Coalesce          bool
	Priority          Priority
	Retry             RetryPolicy
	Recovery          RecoveryPolicy
	AllowManual       bool
}

type Request struct {
	TaskID           string          `json:"taskId"`
	Kind             TriggerKind     `json:"kind"`
	Priority         Priority        `json:"priority"`
	Durable          bool            `json:"durable"`
	CoalescingKey    string          `json:"coalescingKey,omitempty"`
	Cause            string          `json:"cause,omitempty"`
	RequiredCoverage uint64          `json:"requiredCoverage,omitempty"`
	NotBefore        time.Time       `json:"notBefore,omitempty"`
	Deadline         time.Time       `json:"deadline,omitempty"`
	Payload          json.RawMessage `json:"payload,omitempty"`
	WorkflowInstance string          `json:"workflowInstance,omitempty"`
}

type Receipt struct {
	TriggerID   string `json:"triggerId"`
	ExecutionID string `json:"executionId,omitempty"`
	Disposition string `json:"disposition"`
	TargetID    string `json:"targetId,omitempty"`
}

type Result struct {
	TriggerID   string    `json:"triggerId"`
	ExecutionID string    `json:"executionId,omitempty"`
	State       string    `json:"state"`
	Warning     string    `json:"warning,omitempty"`
	Error       string    `json:"error,omitempty"`
	FinishedAt  time.Time `json:"finishedAt,omitempty"`
}

type Trigger struct {
	ID               string          `json:"id"`
	TaskID           string          `json:"taskId"`
	Kind             TriggerKind     `json:"kind"`
	Priority         Priority        `json:"priority"`
	Durable          bool            `json:"durable"`
	CoalescingKey    string          `json:"coalescingKey,omitempty"`
	Cause            string          `json:"cause,omitempty"`
	RequiredCoverage uint64          `json:"requiredCoverage,omitempty"`
	NotBefore        time.Time       `json:"notBefore,omitempty"`
	Deadline         time.Time       `json:"deadline,omitempty"`
	Payload          json.RawMessage `json:"payload,omitempty"`
	WorkflowInstance string          `json:"workflowInstance,omitempty"`
	State            string          `json:"state"`
	Disposition      string          `json:"disposition"`
	TargetID         string          `json:"targetId,omitempty"`
	ExecutionID      string          `json:"executionId,omitempty"`
	CreatedAt        time.Time       `json:"createdAt"`
	FinishedAt       time.Time       `json:"finishedAt,omitempty"`
	Warning          string          `json:"warning,omitempty"`
	Error            string          `json:"error,omitempty"`
}

type Execution struct {
	ID            string          `json:"id"`
	TaskID        string          `json:"taskId"`
	CoalescingKey string          `json:"coalescingKey,omitempty"`
	TriggerIDs    []string        `json:"triggerIds"`
	Priority      Priority        `json:"priority"`
	Coverage      uint64          `json:"coverage,omitempty"`
	Payload       json.RawMessage `json:"payload,omitempty"`
	State         string          `json:"state"`
	Waiting       string          `json:"waiting,omitempty"`
	Attempt       int             `json:"attempt"`
	CreatedAt     time.Time       `json:"createdAt"`
	AdmittedAt    time.Time       `json:"admittedAt,omitempty"`
	StartedAt     time.Time       `json:"startedAt,omitempty"`
	FinishedAt    time.Time       `json:"finishedAt,omitempty"`
	Warning       string          `json:"warning,omitempty"`
	Error         string          `json:"error,omitempty"`
}

type Status struct {
	ID           string
	Name         string
	Description  string
	Interval     time.Duration
	State        string
	Priority     string
	Running      bool
	Queued       bool
	Waiting      string
	LastStarted  time.Time
	LastFinished time.Time
	LastDuration time.Duration
	LastError    string
	LastWarning  string
	NextRun      time.Time
	Pending      int
	CanRun       bool
}

type WorkflowDefinition struct {
	ID          string
	Name        string
	Description string
	Steps       []string
}

type WorkflowInstance struct {
	ID             string    `json:"id"`
	DefinitionID   string    `json:"definitionId"`
	Key            string    `json:"key"`
	Revision       uint64    `json:"revision"`
	CurrentStep    int       `json:"currentStep"`
	State          string    `json:"state"`
	Cause          string    `json:"cause,omitempty"`
	UpdatedAt      time.Time `json:"updatedAt"`
	StepReadyAfter time.Time `json:"stepReadyAfter"`
	NotBefore      time.Time `json:"notBefore"`
	Deadline       time.Time `json:"deadline"`
	TriggerID      string    `json:"triggerId,omitempty"`
}

type WorkflowStatus struct {
	ID          string
	Name        string
	Description string
	State       string
	Revision    uint64
	CurrentStep int
	TotalSteps  int
	StepName    string
	UpdatedAt   time.Time
	ReadyAt     time.Time
}

type StateStore interface {
	Meta(string) (string, error)
	SetMeta(string, string) error
}
