package tasks

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunRecordsStatus(t *testing.T) {
	var runs atomic.Int32
	m := New(Definition{ID: "x", Name: "X", Interval: time.Hour, Runner: func(context.Context) error { runs.Add(1); return nil }})
	if err := m.Run(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if runs.Load() != 1 {
		t.Fatalf("runs=%d", runs.Load())
	}
	s := m.Snapshot()
	if len(s) != 1 || s[0].Running || s[0].LastFinished.IsZero() || s[0].NextRun.IsZero() {
		t.Fatalf("bad status: %#v", s)
	}
}

func TestPreflightRunsBeforeTaskStarts(t *testing.T) {
	var ran bool
	m := New(Definition{ID: "x", Name: "x", Interval: time.Hour, Preflight: func(context.Context) error { return fmt.Errorf("bad integration") }, Runner: func(context.Context) error { ran = true; return nil }})
	if err := m.Run(context.Background(), "x"); err == nil {
		t.Fatal("expected preflight failure")
	}
	if ran {
		t.Fatal("runner must not start when preflight fails")
	}
	s := m.Snapshot()[0]
	if s.Running {
		t.Fatal("task must not be marked running on preflight failure")
	}
	if s.LastError == "" {
		t.Fatal("preflight error should be recorded")
	}
}
