package store

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>
*/
import "C"

import (
	"encoding/json"
	"sort"
	"stewarr/internal/model"
	"strings"
	"time"
)

func (s *Store) SaveMedia(items []model.Media) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error { return s.saveMedia(items) })
}

func (s *Store) saveMedia(items []model.Media) error {
	if err := s.exec("DELETE FROM media"); err != nil {
		return err
	}
	st, e := s.prepare(`INSERT INTO media(kind,source_id,service_id,payload,updated_at) VALUES(?,?,?,?,?)`)
	if e != nil {
		return e
	}
	defer C.sqlite3_finalize(st)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, m := range items {
		b, e := json.Marshal(m)
		if e != nil {
			return e
		}
		C.sqlite3_reset(st)
		C.sqlite3_clear_bindings(st)
		bindText(st, 1, string(m.Type))
		bindInt(st, 2, int64(m.SourceID))
		bindText(st, 3, m.ServiceID)
		bindText(st, 4, string(b))
		bindText(st, 5, now)
		if e := stepDone(s, st); e != nil {
			return e
		}
	}
	return s.setMeta("media.updated_at", now)
}

func (s *Store) LoadMedia() ([]model.Media, time.Time, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	st, e := s.prepare(`SELECT payload FROM media`)
	if e != nil {
		return nil, time.Time{}, e
	}
	defer C.sqlite3_finalize(st)
	var out []model.Media
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, time.Time{}, s.err(rc)
		}
		var m model.Media
		if e := json.Unmarshal([]byte(colText(st, 0)), &m); e != nil {
			return nil, time.Time{}, e
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].RetentionValue != out[j].RetentionValue {
			return out[i].RetentionValue < out[j].RetentionValue
		}
		return out[i].SizeBytes > out[j].SizeBytes
	})
	ts, _ := s.meta("media.updated_at")
	t, _ := time.Parse(time.RFC3339Nano, ts)
	return out, t, nil
}

func (s *Store) SaveTorrents(items []model.Torrent) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error { return s.saveTorrents(items) })
}

func (s *Store) saveTorrents(items []model.Torrent) error {
	if err := s.exec("DELETE FROM torrents"); err != nil {
		return err
	}
	st, e := s.prepare(`INSERT INTO torrents(client,hash,payload,updated_at) VALUES(?,?,?,?)`)
	if e != nil {
		return e
	}
	defer C.sqlite3_finalize(st)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, p := range items {
		// Persist only the torrent index Stewarr needs for lists, valuation,
		// relationships and storage work. Client-owned diagnostics are lazy.
		p.Tracker = ""
		p.TotalSizeBytes = 0
		p.CompletedBytes = 0
		p.AmountLeftBytes = 0
		p.DownloadedBytes = 0
		p.UploadedBytes = 0
		p.DownloadedSession = 0
		p.UploadedSession = 0
		p.DownloadLimit = 0
		p.UploadLimit = 0
		p.MaxRatio = 0
		p.Availability = 0
		p.AddedOn = 0
		p.CompletionOn = 0
		p.SeenComplete = 0
		p.TimeActive = 0
		p.SeedingTime = 0
		p.ETA = 0
		p.Reannounce = 0
		p.ForceStart = false
		p.AutoTMM = false
		p.Sequential = false
		p.SuperSeeding = false
		p.Private = false
		b, e := json.Marshal(p)
		if e != nil {
			return e
		}
		C.sqlite3_reset(st)
		C.sqlite3_clear_bindings(st)
		bindText(st, 1, p.Client)
		bindText(st, 2, strings.ToLower(p.Hash))
		bindText(st, 3, string(b))
		bindText(st, 4, now)
		if e := stepDone(s, st); e != nil {
			return e
		}
	}
	return s.setMeta("torrents.updated_at", now)
}

func (s *Store) LoadTorrents() ([]model.Torrent, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	st, e := s.prepare(`SELECT payload FROM torrents`)
	if e != nil {
		return nil, e
	}
	defer C.sqlite3_finalize(st)
	var out []model.Torrent
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, s.err(rc)
		}
		var p model.Torrent
		if e := json.Unmarshal([]byte(colText(st, 0)), &p); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, nil
}
