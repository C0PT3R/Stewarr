package inventory

import (
	"context"
	"time"

	"stewarr/internal/services/imdb"
	"stewarr/internal/valuation"
)

// imdbRatingsFetchHour is the local wall-clock hour RefreshIMDbRatings
// becomes due each day — see imdbRatingsDue. Chosen as an off-peak,
// low-traffic hour; simple to reason about.
const imdbRatingsFetchHour = 3

// imdbRatingsDue decides whether a new daily fetch should run: local
// wall-clock time has passed the configured hour, and the last
// successful fetch wasn't already today. internal/tasks only supports
// plain intervals, not wall-clock alignment, so RefreshIMDbRatings is
// registered on a short, cheap-to-check interval and self-gates using
// this function instead — every check is nearly free (no network call)
// except on the one invocation per day that's actually due.
func imdbRatingsDue(now, lastFetch time.Time, hour int) bool {
	nowLocal := now.Local()
	if nowLocal.Hour() < hour {
		return false
	}
	if lastFetch.IsZero() {
		return true
	}
	ly, lm, ld := lastFetch.Local().Date()
	ny, nm, nd := nowLocal.Date()
	return ly != ny || lm != nm || ld != nd
}

// RefreshIMDbRatings downloads and replaces IMDb's own ratings dataset
// once a day, at imdbRatingsFetchHour local time. Unlike RefreshTMDB this
// is one atomic bulk download, not per-item network calls — a failure
// here just means valuation keeps using whatever snapshot is already
// cached (possibly a day or more stale, never blocking cleanup planning
// the way an entirely-unenriched item would), so there's no
// pending/stale/reliable state to track the way TMDB enrichment has.
func (service *Service) RefreshIMDbRatings(ctx context.Context) error {
	defer service.publishChange()
	service.mu.RLock()
	enabled := !service.cfg.IMDb.Disabled
	service.mu.RUnlock()
	if !enabled || service.db == nil {
		return nil
	}
	_, lastFetch, err := service.db.LoadIMDbRatings()
	if err != nil {
		return err
	}
	if !imdbRatingsDue(service.nowFunc(), lastFetch, imdbRatingsFetchHour) {
		return nil
	}
	ratings, err := service.imdbClient.FetchRatings(ctx)
	if err != nil {
		return err
	}
	if err := service.db.ReplaceIMDbRatings(ratings); err != nil {
		return err
	}
	byTconst := make(map[string]imdb.Rating, len(ratings))
	for _, r := range ratings {
		byTconst[r.Tconst] = r
	}

	// Held for the whole re-score, unlike RefreshTMDB (which unlocks
	// around its own per-item network calls): the only slow I/O here —
	// the dataset download and the SQLite replace — already happened
	// above, so there's no reason to open a window for service.items to
	// drift underneath a wholesale reassignment the way a merge-by-key
	// function (mergeTMDBFacts) is specifically built to tolerate.
	// Re-cloning here rather than reusing an earlier snapshot means this
	// always scores whatever is actually current.
	service.mu.Lock()
	defer service.mu.Unlock()
	service.imdbRatings = byTconst
	items := cloneMedia(service.items)
	valuation.ApplyMedia(items, service.cfg, byTconst)
	service.items = items
	if service.db != nil {
		return service.db.PublishEnrichment("imdb", service.generation, items)
	}
	return nil
}
