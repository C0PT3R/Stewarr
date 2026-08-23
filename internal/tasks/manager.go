package tasks

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

type Runner func(context.Context) error

type Definition struct {
	ID          string
	Name        string
	Description string
	Interval    time.Duration
	Preflight   Runner
	Runner      Runner
}

type Status struct {
	ID           string
	Name         string
	Description  string
	Interval     time.Duration
	Running      bool
	LastStarted  time.Time
	LastFinished time.Time
	LastDuration time.Duration
	LastError    string
	NextRun      time.Time
}

type entry struct {
	def    Definition
	status Status
}

type Manager struct {
	mu      sync.RWMutex
	entries map[string]*entry
}

func New(defs ...Definition) *Manager {
	m := &Manager{entries: make(map[string]*entry, len(defs))}
	now := time.Now()
	for _, d := range defs {
		if d.Interval <= 0 {
			continue
		}
		e := &entry{def: d}
		e.status = Status{ID: d.ID, Name: d.Name, Description: d.Description, Interval: d.Interval, NextRun: now.Add(d.Interval)}
		m.entries[d.ID] = e
	}
	return m
}

func (m *Manager) Snapshot() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Status, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, e.status)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (m *Manager) Run(ctx context.Context, id string) error {
	m.mu.RLock()
	e, ok := m.entries[id]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("unknown task %q", id)
	}
	if e.status.Running {
		m.mu.RUnlock()
		return nil
	}
	preflight := e.def.Preflight
	m.mu.RUnlock()
	// Validate prerequisites before the task is marked running or any task work
	// begins. This keeps a failed integration preflight from looking like a scan
	// that started and then discovered its topology was invalid.
	if preflight != nil {
		started := time.Now()
		err := preflight(ctx)
		if err != nil {
			finished := time.Now()
			m.mu.Lock()
			e = m.entries[id]
			e.status.LastStarted = started
			e.status.LastFinished = finished
			e.status.LastDuration = finished.Sub(started)
			e.status.LastError = err.Error()
			e.status.NextRun = finished.Add(e.def.Interval)
			m.mu.Unlock()
			return err
		}
	}
	m.mu.Lock()
	e = m.entries[id]
	if e.status.Running {
		m.mu.Unlock()
		return nil
	}
	e.status.Running = true
	e.status.LastStarted = time.Now()
	e.status.LastError = ""
	runner := e.def.Runner
	interval := e.def.Interval
	m.mu.Unlock()
	started := time.Now()
	err := runner(ctx)
	finished := time.Now()
	m.mu.Lock()
	e.status.Running = false
	e.status.LastFinished = finished
	e.status.LastDuration = finished.Sub(started)
	if err != nil {
		e.status.LastError = err.Error()
	}
	e.status.NextRun = finished.Add(interval)
	m.mu.Unlock()
	return err
}

func (m *Manager) RunAsync(ctx context.Context, id string) {
	go func() { _ = m.Run(ctx, id) }()
}

func (m *Manager) Start(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			m.mu.RLock()
			ids := make([]string, 0, len(m.entries))
			for id, e := range m.entries {
				if !e.status.Running && !e.status.NextRun.IsZero() && !now.Before(e.status.NextRun) {
					ids = append(ids, id)
				}
			}
			m.mu.RUnlock()
			for _, id := range ids {
				m.RunAsync(ctx, id)
			}
		}
	}
}
