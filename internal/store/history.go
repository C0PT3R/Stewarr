package store

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"time"
)

type HistoryEvent struct {
	ID               int64  `json:"id"`
	EventType        string `json:"eventType"`
	Status           string `json:"status"`
	DryRun           bool   `json:"dryRun"`
	RequestedKind    string `json:"requestedKind"`
	RequestedKey     string `json:"requestedKey"`
	RequestedLabel   string `json:"requestedLabel"`
	ReclaimableBytes int64  `json:"reclaimableBytes"`
	// MediaBytes is the raw sum of every selected file's own size, before
	// hardlink deduplication — "Library bytes removed". It is deliberately
	// tracked separately from ReclaimableBytes ("actual filesystem bytes
	// reclaimed"), since hardlinks mean those are not necessarily equal.
	MediaBytes int64           `json:"mediaBytes"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Error      string          `json:"error,omitempty"`
	CreatedAt  time.Time       `json:"createdAt"`
}

func (s *Store) SaveHistoryEvent(e HistoryEvent) (int64, error) {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage(`{}`)
	}
	st, err := s.prepare(`INSERT INTO history_events(event_type,status,dry_run,requested_kind,requested_key,requested_label,reclaimable_bytes,payload,error,created_at,media_bytes) VALUES(?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer C.sqlite3_finalize(st)
	bindText(st, 1, e.EventType)
	bindText(st, 2, e.Status)
	bindInt(st, 3, boolInt(e.DryRun))
	bindText(st, 4, e.RequestedKind)
	bindText(st, 5, e.RequestedKey)
	bindText(st, 6, e.RequestedLabel)
	bindInt(st, 7, e.ReclaimableBytes)
	bindText(st, 8, string(e.Payload))
	bindText(st, 9, e.Error)
	bindText(st, 10, e.CreatedAt.UTC().Format(time.RFC3339Nano))
	bindInt(st, 11, e.MediaBytes)
	if err := stepDone(s, st); err != nil {
		return 0, err
	}
	return int64(C.sqlite3_last_insert_rowid(s.db)), nil
}

// UpdateHistoryEvent finalizes the durable operation row created before a
// removal begins. The operation identity is preserved across every outcome.
func (s *Store) UpdateHistoryEvent(e HistoryEvent) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	if e.ID <= 0 {
		return fmt.Errorf("history event id is required")
	}
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage(`{}`)
	}
	st, err := s.prepare(`UPDATE history_events SET status=?,dry_run=?,requested_kind=?,requested_key=?,requested_label=?,reclaimable_bytes=?,payload=?,error=?,media_bytes=? WHERE id=?`)
	if err != nil {
		return err
	}
	defer C.sqlite3_finalize(st)
	bindText(st, 1, e.Status)
	bindInt(st, 2, boolInt(e.DryRun))
	bindText(st, 3, e.RequestedKind)
	bindText(st, 4, e.RequestedKey)
	bindText(st, 5, e.RequestedLabel)
	bindInt(st, 6, e.ReclaimableBytes)
	bindText(st, 7, string(e.Payload))
	bindText(st, 8, e.Error)
	bindInt(st, 9, e.MediaBytes)
	bindInt(st, 10, e.ID)
	if err := stepDone(s, st); err != nil {
		return err
	}
	if C.sqlite3_changes(s.db) != 1 {
		return fmt.Errorf("history event %d does not exist", e.ID)
	}
	return nil
}

// InterruptStartedHistoryEvents closes operations left in-flight by a prior
// process exit. Their exact starting plan remains available in payload.
func (s *Store) InterruptStartedHistoryEvents() error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.exec(`UPDATE history_events SET status='interrupted', error=CASE WHEN error='' THEN 'application stopped before the removal operation was finalized' ELSE error END WHERE event_type='removal' AND status='started'`)
}

// InFlightRemovalHistoryEvents returns the domain journal entries that need to
// be reconciled with scheduler state before new work starts after a restart.
func (s *Store) InFlightRemovalHistoryEvents() ([]HistoryEvent, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	statement, err := s.prepare(`SELECT id,event_type,status,dry_run,requested_kind,requested_key,requested_label,reclaimable_bytes,payload,error,created_at,media_bytes FROM history_events WHERE event_type='removal' AND status IN ('queued','started') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(statement)
	var events []HistoryEvent
	for {
		result := C.sqlite3_step(statement)
		if result == C.SQLITE_DONE {
			break
		}
		if result != C.SQLITE_ROW {
			return nil, s.err(result)
		}
		createdAt, _ := time.Parse(time.RFC3339Nano, colText(statement, 10))
		events = append(events, HistoryEvent{ID: int64(C.sqlite3_column_int64(statement, 0)), EventType: colText(statement, 1), Status: colText(statement, 2), DryRun: C.sqlite3_column_int64(statement, 3) != 0, RequestedKind: colText(statement, 4), RequestedKey: colText(statement, 5), RequestedLabel: colText(statement, 6), ReclaimableBytes: int64(C.sqlite3_column_int64(statement, 7)), Payload: json.RawMessage(colText(statement, 8)), Error: colText(statement, 9), CreatedAt: createdAt, MediaBytes: int64(C.sqlite3_column_int64(statement, 11))})
	}
	return events, nil
}

func (s *Store) HistoryEvents(limit int) ([]HistoryEvent, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	st, err := s.prepare(`SELECT id,event_type,status,dry_run,requested_kind,requested_key,requested_label,reclaimable_bytes,payload,error,created_at,media_bytes FROM history_events ORDER BY created_at DESC LIMIT ?`)
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(st)
	bindInt(st, 1, int64(limit))
	var out []HistoryEvent
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, s.err(rc)
		}
		t, _ := time.Parse(time.RFC3339Nano, colText(st, 10))
		out = append(out, HistoryEvent{ID: int64(C.sqlite3_column_int64(st, 0)), EventType: colText(st, 1), Status: colText(st, 2), DryRun: C.sqlite3_column_int64(st, 3) != 0, RequestedKind: colText(st, 4), RequestedKey: colText(st, 5), RequestedLabel: colText(st, 6), ReclaimableBytes: int64(C.sqlite3_column_int64(st, 7)), Payload: json.RawMessage(colText(st, 8)), Error: colText(st, 9), CreatedAt: t, MediaBytes: int64(C.sqlite3_column_int64(st, 11))})
	}
	return out, nil
}

func (s *Store) HistoryEventByID(id int64) (HistoryEvent, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	if id <= 0 {
		return HistoryEvent{}, fmt.Errorf("history event id is required")
	}
	st, err := s.prepare(`SELECT id,event_type,status,dry_run,requested_kind,requested_key,requested_label,reclaimable_bytes,payload,error,created_at,media_bytes FROM history_events WHERE id=?`)
	if err != nil {
		return HistoryEvent{}, err
	}
	defer C.sqlite3_finalize(st)
	bindInt(st, 1, id)
	rc := C.sqlite3_step(st)
	if rc == C.SQLITE_DONE {
		return HistoryEvent{}, fmt.Errorf("history event %d does not exist", id)
	}
	if rc != C.SQLITE_ROW {
		return HistoryEvent{}, s.err(rc)
	}
	createdAt, _ := time.Parse(time.RFC3339Nano, colText(st, 10))
	return HistoryEvent{ID: int64(C.sqlite3_column_int64(st, 0)), EventType: colText(st, 1), Status: colText(st, 2), DryRun: C.sqlite3_column_int64(st, 3) != 0, RequestedKind: colText(st, 4), RequestedKey: colText(st, 5), RequestedLabel: colText(st, 6), ReclaimableBytes: int64(C.sqlite3_column_int64(st, 7)), Payload: json.RawMessage(colText(st, 8)), Error: colText(st, 9), CreatedAt: createdAt, MediaBytes: int64(C.sqlite3_column_int64(st, 11))}, nil
}
