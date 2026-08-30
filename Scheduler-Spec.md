# Connarr Scheduler Specification

Status: design target for the scheduler overhaul; core policy confirmed
2026-08-29; not implemented.

Confirmed policy decisions:

- Run now joins a sufficiently fresh active execution; newer facts require one
  coalesced successor.
- At the five-minute dirty deadline, consistency restoration temporarily
  precedes new ordinary removals.
- After an interrupted removal, Connarr verifies external state before resuming
  only demonstrably incomplete, safe/idempotent actions; unknowable outcomes
  require attention.
- Downtime produces at most one catch-up execution per periodic task.

## 1. Purpose and boundary

The scheduler coordinates when application work may run. It does not know what
Radarr, Sonarr, Jellyfin, torrents, files, inventory, or removal mean.

Domain code defines tasks, resource requirements, workflows, and durability.
Task runners remain responsible for validating prerequisites, performing work,
publishing facts atomically, and returning an explicit outcome.

The scheduler must never contain hardcoded task IDs or domain workflows.

## 2. Required guarantees

1. Every accepted trigger is executed, explicitly coalesced into identified
   work, or visibly rejected. It is never silently discarded.
2. At most one execution of a given task/coalescing key runs at once.
3. New facts arriving after an execution's coverage boundary require a
   successor execution; the running execution cannot accidentally satisfy them.
4. Conflicting resource claims never overlap.
5. Cancellation never releases resources until the runner has actually exited.
6. Once a durable mutation is accepted, browser disconnection has no effect on
   its execution.
7. Correctness-required work survives restart.
8. A caller can observe or wait for the exact trigger/execution it received.
9. All waiting, coalescing, retry, cancellation, and interruption decisions are
   explainable through durable state.
10. Scheduler behavior is deterministic under an injected clock in tests.

These are scheduling guarantees, not an impossible promise of exactly-once
effects across third-party HTTP APIs. Irreversible workflows require durable
domain journals and idempotent recovery.

## 3. Core model

### 3.1 Task definition

A task definition is registered by application code and has:

- stable task ID, display name, and description;
- runner and optional preflight validation;
- static resource claims;
- interruptibility policy;
- retry policy;
- default priority and trigger/coalescing policy;
- optional default periodic schedule;
- outcome advisory hook.

Task definitions are code, not database rows. A task may be event-only and does
not require a periodic interval. Runtime schedule configuration may override a
definition's default without changing the task's identity or runner.

### 3.2 Trigger

A trigger is a request that work become true. It has:

- unique trigger ID;
- task ID and optional coalescing key;
- kind: `startup`, `periodic`, `manual`, `event`, `workflow`, or `retry`;
- creation time and optional `not_before`/deadline;
- priority class;
- durability: `ephemeral` or `durable`;
- cause/reference for explainability;
- required coverage token, when freshness matters;
- disposition: pending, attached, coalesced, fulfilled, cancelled, or rejected.

Submission returns the trigger ID and its disposition. Coalescing names the
surviving trigger or execution.

Connarr's default durability is:

- mutation, consistency-workflow, and accepted manual triggers are durable;
- startup and periodic ticks are ephemeral and reconstructible;
- event triggers declare durability according to whether losing the event could
  violate correctness.

### 3.3 Execution

An execution is one invocation of a runner. It has:

- unique execution ID;
- task ID and coalescing key;
- the triggers it satisfies;
- coverage token captured at admission;
- state and wait reason;
- created, admitted, started, and finished timestamps;
- attempt number;
- outcome, warning/error, and duration.

One execution can satisfy many compatible triggers. A trigger can belong to
only one active or completed execution.

### 3.4 Coverage token

A coverage token is an opaque, monotonic value supplied by domain code. It may
represent an inventory generation, dirty revision, event sequence, or other
freshness boundary. The scheduler compares tokens but does not interpret them.

An execution satisfies only triggers whose required token is less than or equal
to the token captured by that execution. A later token creates or advances a
successor execution.

Tasks that do not require freshness tokens may attach compatible triggers to an
already queued or running execution.

### 3.5 Workflow

A workflow is a durable, declarative set of dependent task steps. Version 1
supports a linear sequence; the representation may allow a future DAG without
changing trigger or execution semantics.

Each step declares:

- task ID;
- coverage requirement derived from the workflow instance;
- success condition;
- optional delay/cooldown;
- failure/retry policy.

An equivalent successful execution may satisfy a pending step only when its
coverage token and timing satisfy that step. The scheduler does not identify
equivalence through hardcoded task names.

## 4. State model

### 4.1 Execution states

`scheduled → ready → waiting → running → terminal`

`waiting` always includes one concrete reason:

- not-before time;
- conflicting resource;
- higher-priority work;
- workflow dependency;
- retry backoff;
- concurrency limit;
- cancellation in progress.

Terminal outcomes are:

- `succeeded`;
- `degraded` — usable result with an advisory;
- `failed` — attempt failed and no retry is currently pending;
- `cancelled` — never started;
- `interrupted` — started but cooperatively stopped;
- `rejected` — could not be accepted.

Task-page states such as Idle, Scheduled, Waiting, Running, Cancelling, Backoff,
Failed, and Degraded are projections of triggers and executions, not mutable
flags maintained independently.

### 4.2 Runner outcome

Runners return a structured outcome rather than relying on an unclassified
error:

- success;
- degraded with advisory;
- retryable failure;
- permanent failure;
- cooperative interruption.

Panics are recovered at the execution boundary and recorded as failures.

## 5. Trigger and coalescing rules

1. Coalescing is opt-in per task and key.
2. Compatible pending triggers merge into one pending execution intent.
3. The merged intent retains the highest priority, earliest deadline, latest
   required coverage token, and every contributing trigger ID/cause.
4. A compatible trigger may attach to a running execution only when that
   execution's captured coverage token satisfies it.
5. A later coverage token creates or advances exactly one pending successor.
6. Multiple later triggers coalesce into that successor.
7. Periodic ticks missed while work is queued/running coalesce; they do not form
   an unbounded backlog.
8. A manual Run now request normally attaches to a compatible current execution.
   A future explicit Force another run operation would create a successor; it is
   outside version 1.
9. Coalescing is recorded and visible. Returning success while dropping the
   trigger is forbidden.

The normal bound is therefore one running execution plus one coalesced
successor per task/key.

## 6. Priority and fairness

Priority classes, highest first:

1. `emergency` — alarms and protective action;
2. `mutation` — user-confirmed or policy-confirmed state changes;
3. `consistency` — correctness-required post-mutation work;
4. `manual` — user-requested maintenance;
5. `periodic` — routine maintenance.

Priority selects among ready work that can acquire its resources. It never
violates resource safety or forcibly kills a runner.

Emergency or mutation work may request cooperative interruption of conflicting
executions that explicitly declare themselves interruptible. Non-interruptible
work finishes at its next natural safe boundary.

FIFO ordering applies within a priority class. Bounded priority aging prevents
ordinary maintenance starvation, but emergency and mutation safety cannot be
aged below routine work.

A consistency workflow reaching its maximum dirty deadline is promoted ahead
of new ordinary mutations until its required consistency boundary is restored.

## 7. Resource arbitration

Tasks declare named resource claims in one of two modes:

- `shared` — may overlap other shared claims;
- `exclusive` — conflicts with every claim on the same resource.

Claims are acquired atomically in canonical name order before runner preflight
and retained until the runner exits. Partial acquisition is forbidden.

Resource names and meanings belong to application composition. Example claims
might describe inventory publication, file-topology publication, integration
API pressure, or the owner/filesystem mutation boundary. The scheduler treats
them as opaque strings.

Version 1 uses static claims declared by the task. Dynamic per-item locking and
distributed locking are out of scope.

## 8. Cancellation and preemption

- Cancellation is cooperative through context cancellation.
- Only definitions marked interruptible may be preempted.
- Requesting cancellation changes state to `cancelling`; it does not mark the
  execution finished.
- Resources remain held until the runner returns.
- Interrupted work retains or creates a successor when its triggers still
  require completion.
- Shutdown cancels interruptible executions and allows a configurable grace
  period for non-interruptible executions.
- The scheduler never kills goroutines or assumes an external side effect was
  rolled back.

## 9. Time, periodic schedules, and retry

- The engine is event/timer driven; it does not poll status every second or
  every 25 milliseconds.
- All time comes from an injected clock.
- Periodic schedules are fixed-rate from a stable schedule anchor, avoiding
  drift caused by task duration.
- After downtime, periodic tasks create at most one catch-up trigger; missed
  intervals do not replay individually.
- `not_before` represents cooldown and debounce without blocking a worker.
- Retry policy declares backoff type, initial/max delay, attempt limit, and
  jitter policy.
- Retryable failures create visible retry triggers retaining original coverage
  and cause.
- Successful execution resets retry state.
- Permanent failures require a new external/manual trigger unless their
  workflow declares another recovery path.

## 10. Persistence and restart

SQLite persists:

- durable triggers and their dispositions;
- workflow instances and step state;
- execution history and current outcomes;
- periodic schedule anchors/last satisfied occurrence;
- retry state;
- task-status projection inputs.

Periodic definitions are reconstructed from code at startup. At most one
catch-up trigger is produced.

Executions left `running` at process death become `interrupted` during startup.

- Idempotent maintenance work may be requeued when its trigger remains required.
- Correctness workflows resume from the first unsatisfied step.
- Irreversible mutations are never blindly replayed. Their domain operation
  journal determines resume, verification, compensation, or manual attention.

Durable work is committed before the caller is told it was accepted. After
that commit, its lifetime belongs to the application context, never to the HTTP
request context.

## 11. Connarr composition requirements

These are application declarations built on the generic engine, not scheduler
special cases.

### 11.1 Initial maintenance

- Base inventory is requested at startup.
- File reconciliation may begin only under its declared generation/resource
  rules.
- Enrichment tasks use their own schedules and publication claims.

### 11.2 Removal

1. Confirmation durably records the removal operation and mutation trigger.
2. The HTTP request returns/observes its trigger or operation ID.
3. Mutation priority waits for conflicting non-interruptible work and
   interrupts only declared interruptible work.
4. The RemovalPlan is rebuilt/revalidated inside the acquired mutation
   boundary.
5. Domain execution and its journal own recovery.
6. Successful or uncertain mutation increments a durable dirty revision and
   emits the post-removal workflow event.

### 11.3 Post-removal consistency workflow

- Triggered by the durable dirty revision.
- Its ready time is the earlier of 60 seconds after the latest removal and five
  minutes after the first unsatisfied revision.
- Repeated removals advance the required revision without creating parallel
  workflows.
- Sequence: Base inventory, then File reconciliation.
- A qualifying scheduled/manual execution may satisfy either step.
- A removal after Base inventory success invalidates that step for the newer
  revision.
- Automatic planning remains inhibited until the workflow is satisfied.
- Jellyfin and Seerr enrichment are not implicitly part of this workflow.

### 11.4 Enrichment cooldown

Jellyfin's post-removal cooldown is represented by a delayed trigger or policy,
not by removal-specific fields in the scheduler. If interrupted for mutation,
its still-required trigger is rescheduled after the cooldown.

### 11.5 Emergency work

Future critical-storage behavior uses emergency triggers. The scheduler can
prioritize and cooperatively preempt, but domain policy decides when an alarm is
critical and which protective actions—such as stopping downloads—are required.

## 12. Observability and API contract

The scheduler exposes:

- registered tasks and schedules;
- next due time;
- pending triggers and causes;
- execution ID, state, attempt, priority, and coverage;
- exact waiting reason and conflicting resource/execution when applicable;
- last success, degradation, failure, interruption, and duration;
- retry/backoff time;
- workflow progress;
- cancellation capability.

Run now returns a trigger ID immediately. Callers may query that trigger or
subscribe to state changes. Internal workflow code awaits execution completion
through notifications/futures, never status polling.

The UI must distinguish Scheduled, Queued, Waiting, Running, Cancelling,
Backoff, Degraded, Failed, and Interrupted rather than compressing them into a
single boolean pair.

## 13. Explicit non-goals for version 1

- Distributed/multi-node scheduling;
- arbitrary cron expressions;
- dynamic per-file resource locks;
- forcibly terminating goroutines;
- exactly-once third-party side effects;
- a general-purpose event bus;
- arbitrary user-authored workflow DAGs;
- automatic cleanup policy itself.

## 14. Mandatory acceptance tests

The replacement is incomplete unless deterministic tests prove:

1. no accepted trigger disappears;
2. compatible triggers coalesce and preserve every disposition;
3. a later coverage token produces one successor while a task runs;
4. conflicting resources never overlap, while compatible work can overlap;
5. priority ordering and FIFO behavior are stable;
6. cancellation retains resources until runner exit;
7. non-interruptible work is never preempted;
8. retries follow policy and retain cause/coverage;
9. periodic downtime produces only one catch-up run;
10. workflow dependencies and equivalent-step satisfaction are correct;
11. the 60-second debounce and five-minute maximum dirty window are exact;
12. a newer removal revision invalidates stale workflow progress;
13. browser cancellation cannot cancel an accepted durable mutation;
14. restart marks interrupted executions and resumes only permitted work;
15. mutation journals, not the scheduler, decide irreversible recovery;
16. task status and wait reasons match the underlying trigger/execution state;
17. all timing tests run without real sleeps.

## 15. Replacement strategy

1. Introduce the new engine, injected clock, persistence schema, and tests with
   no task migration.
2. Migrate periodic/manual maintenance tasks and the Tasks UI.
3. Express startup ordering and post-removal synchronization as workflows.
4. Move removal admission to durable application-owned mutation triggers.
5. Verify behavior under shutdown/restart and remove the legacy manager,
   dirty loop, polling waits, and hardcoded task IDs.

No compatibility layer should preserve the legacy manager's silent-trigger or
browser-lifetime behavior.
