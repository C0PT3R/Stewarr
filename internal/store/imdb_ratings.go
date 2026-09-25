package store

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>
*/
import "C"

import (
	"time"

	"stewarr/internal/services/imdb"
)

// ReplaceIMDbRatings atomically swaps in a full new snapshot of IMDb's
// ratings dataset — mirrors saveMedia's exact shape (one write
// transaction: delete everything, then batch-insert) since this is the
// same kind of full-refresh bulk data, not incremental per-row updates.
func (s *Store) ReplaceIMDbRatings(ratings []imdb.Rating) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error { return s.replaceIMDbRatings(ratings) })
}

func (s *Store) replaceIMDbRatings(ratings []imdb.Rating) error {
	if err := s.exec("DELETE FROM imdb_ratings"); err != nil {
		return err
	}
	st, e := s.prepare(`INSERT INTO imdb_ratings(tconst,rating,votes) VALUES(?,?,?)`)
	if e != nil {
		return e
	}
	defer C.sqlite3_finalize(st)
	for _, r := range ratings {
		C.sqlite3_reset(st)
		C.sqlite3_clear_bindings(st)
		bindText(st, 1, r.Tconst)
		C.sqlite3_bind_double(st, 2, C.double(r.Value))
		bindInt(st, 3, int64(r.Votes))
		if e := stepDone(s, st); e != nil {
			return e
		}
	}
	return s.setMeta("imdb_ratings.updated_at", time.Now().UTC().Format(time.RFC3339Nano))
}

// LoadIMDbRatings returns the full current snapshot, keyed by IMDb id
// (tconst), along with when it was last successfully replaced — loaded
// once at startup and cached in memory by the caller rather than
// re-queried on every valuation pass (this table can hold well over a
// million rows).
func (s *Store) LoadIMDbRatings() (map[string]imdb.Rating, time.Time, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	st, e := s.prepare(`SELECT tconst,rating,votes FROM imdb_ratings`)
	if e != nil {
		return nil, time.Time{}, e
	}
	defer C.sqlite3_finalize(st)
	out := map[string]imdb.Rating{}
	for {
		rc := C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, time.Time{}, s.err(rc)
		}
		tconst := colText(st, 0)
		out[tconst] = imdb.Rating{
			Tconst: tconst,
			Value:  float64(C.sqlite3_column_double(st, 1)),
			Votes:  int(C.sqlite3_column_int64(st, 2)),
		}
	}
	ts, _ := s.meta("imdb_ratings.updated_at")
	t, _ := time.Parse(time.RFC3339Nano, ts)
	return out, t, nil
}
