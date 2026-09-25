package inventory

import (
	"context"
	"errors"
	"fmt"
	"stewarr/internal/model"
	"stewarr/internal/valuation"
	"sync"
	"time"
)

// RefreshJellyfin updates only playback and favorite facts. It never refreshes
// authoritative inventory, torrent state, history, or filesystem topology.
// RefreshJellyfin reconciles Views/Favorite/etc. for the whole catalog
// against current Jellyfin state. It does not discard its results if the
// base generation moves on mid-pass — see RefreshTMDB's doc comment for
// why (the same reasoning applies here): mergeJellyfinFacts matches by
// stable key onto whatever items are current at merge time, so merging a
// pass computed against a slightly older generation is safe regardless of
// what changed in between. A newly discovered or re-identified item is
// never waiting on this pass in the first place — see EnrichNewMedia.
func (service *Service) RefreshJellyfin(ctx context.Context) error {
	defer service.publishChange()
	if service.cfg.Jellyfin.URL == "" {
		return nil
	}
	service.mu.Lock()
	if !service.reliability.Inventory || service.generation == 0 {
		service.mu.Unlock()
		return fmt.Errorf("base inventory is not available")
	}
	base := cloneMedia(service.items)
	service.reliability.Jellyfin = "pending"
	service.reliability.Valuation = false
	service.reliability.Message = "Jellyfin enrichment is running; automatic removal planning is paused."
	service.mu.Unlock()
	clearJellyfinFacts(base)
	if err := service.jf.WithContext(ctx).Apply(base); err != nil {
		service.setStatus(serviceName(service.cfg, "jellyfin", "Jellyfin"), true, false, err)
		service.mu.Lock()
		service.reliability.Jellyfin = "stale"
		service.reliability.Valuation = false
		service.reliability.Message = "Jellyfin enrichment failed; automatic removal planning is paused."
		service.mu.Unlock()
		return err
	}
	now := time.Now()
	for i := range base {
		base[i].JellyfinEnrichedAt = now
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	items := cloneMedia(service.items)
	mergeJellyfinFacts(items, base)
	valuation.ApplyMedia(items, service.cfg, service.imdbRatings)
	if service.db != nil {
		if err := service.db.PublishEnrichment("jellyfin", service.generation, items); err != nil {
			service.reliability.Jellyfin = "stale"
			service.reliability.Valuation = false
			return err
		}
	}
	service.items = items
	service.reliability.Jellyfin = "reliable"
	service.reliability.Valuation = enrichmentReliable(service.reliability.Seerr) && enrichmentReliable(service.reliability.TMDB)
	if service.reliability.Valuation {
		service.reliability.Message = "Media valuation is reliable."
	} else {
		service.reliability.Message = "Other enrichment is stale; automatic removal planning is paused."
	}
	service.setStatusLocked(serviceName(service.cfg, "jellyfin", "Jellyfin"), true, true, nil)
	return nil
}

// RefreshSeerr updates only request facts. It does not discard its
// results if the base generation moves on mid-pass — see RefreshTMDB's
// doc comment for why the same reasoning applies here.
func (service *Service) RefreshSeerr(ctx context.Context) error {
	defer service.publishChange()
	if service.cfg.Seerr.URL == "" {
		return nil
	}
	service.mu.Lock()
	if !service.reliability.Inventory || service.generation == 0 {
		service.mu.Unlock()
		return fmt.Errorf("base inventory is not available")
	}
	base := cloneMedia(service.items)
	service.reliability.Seerr = "pending"
	service.reliability.Valuation = false
	service.reliability.Message = "Seerr enrichment is running; automatic removal planning is paused."
	service.mu.Unlock()
	clearSeerrFacts(base)
	if err := service.seerr.WithContext(ctx).Apply(base); err != nil {
		service.setStatus(serviceName(service.cfg, "seerr", "Seerr"), true, false, err)
		service.mu.Lock()
		service.reliability.Seerr = "stale"
		service.reliability.Valuation = false
		service.reliability.Message = "Seerr enrichment failed; automatic removal planning is paused."
		service.mu.Unlock()
		return err
	}
	now := time.Now()
	for i := range base {
		base[i].SeerrEnrichedAt = now
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	items := cloneMedia(service.items)
	mergeSeerrFacts(items, base)
	valuation.ApplyMedia(items, service.cfg, service.imdbRatings)
	if service.db != nil {
		if err := service.db.PublishEnrichment("seerr", service.generation, items); err != nil {
			service.reliability.Seerr = "stale"
			service.reliability.Valuation = false
			return err
		}
	}
	service.items = items
	service.reliability.Seerr = "reliable"
	service.reliability.Valuation = enrichmentReliable(service.reliability.Jellyfin) && enrichmentReliable(service.reliability.TMDB)
	if service.reliability.Valuation {
		service.reliability.Message = "Media valuation is reliable."
	} else {
		service.reliability.Message = "Other enrichment is stale; automatic removal planning is paused."
	}
	service.setStatusLocked(serviceName(service.cfg, "seerr", "Seerr"), true, true, nil)
	return nil
}

// RefreshTMDB updates TMDB's own rating/vote/popularity facts. Unlike
// Jellyfin/Seerr, it does not discard its results if the base generation
// moves on mid-pass: TMDB fetches one item at a time (this pass can run
// for minutes across a real library), so a base refresh ticking in the
// meantime — every RefreshInterval, or immediately after any removal — is
// routine, not exceptional. Discarding a fully-successful pass over that
// would have meant TMDB data could go indefinitely stale despite running
// on schedule, since EnrichNewMedia only ever catches up items that were
// never checked by this specific source at all, not ones whose last
// refresh got thrown away.
// mergeTMDBFacts matches by stable key onto whatever items are current at
// merge time, so merging a pass computed against an older generation is
// safe regardless of what changed in between. TMDB also has no
// service-page status to report to (it isn't a config.Service — see
// Config.TMDB), so it never calls setStatus.
func (service *Service) RefreshTMDB(ctx context.Context) error {
	defer service.publishChange()
	if service.cfg.TMDB.APIKey == "" {
		return nil
	}
	service.mu.Lock()
	if !service.reliability.Inventory || service.generation == 0 {
		service.mu.Unlock()
		return fmt.Errorf("base inventory is not available")
	}
	base := cloneMedia(service.items)
	service.reliability.TMDB = "pending"
	service.reliability.Valuation = false
	service.reliability.Message = "TMDB enrichment is running; automatic removal planning is paused."
	service.mu.Unlock()
	// Unlike Jellyfin/Seerr, TMDB fetches one item at a time — a transient
	// per-item failure must not blank that item's data (base starts from
	// the current live values, not cleared first), or a single flaky
	// fetch would erase yesterday's good data immediately instead of
	// leaving it in place until it's actually gone stale (see
	// mediaTMDBDataStale in internal/httpui/auto_removal.go). Apply only
	// returns an error when this whole pass was cancelled out from under
	// it (e.g. yielding to a higher-priority removal, since this task is
	// Interruptible) — see its doc comment.
	if err := service.tmdb.WithContext(ctx).Apply(base); err != nil {
		service.mu.Lock()
		service.reliability.TMDB = "stale"
		service.reliability.Valuation = false
		if errors.Is(err, context.Canceled) {
			service.reliability.Message = "TMDB enrichment yielded to higher-priority work; retrying shortly."
		} else {
			service.reliability.Message = "TMDB enrichment failed; automatic removal planning is paused."
		}
		service.mu.Unlock()
		return err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	items := cloneMedia(service.items)
	mergeTMDBFacts(items, base)
	valuation.ApplyMedia(items, service.cfg, service.imdbRatings)
	if service.db != nil {
		if err := service.db.PublishEnrichment("tmdb", service.generation, items); err != nil {
			service.reliability.TMDB = "stale"
			service.reliability.Valuation = false
			return err
		}
	}
	service.items = items
	service.reliability.TMDB = "reliable"
	service.reliability.Valuation = enrichmentReliable(service.reliability.Jellyfin) && enrichmentReliable(service.reliability.Seerr)
	if service.reliability.Valuation {
		service.reliability.Message = "Media valuation is reliable."
	} else {
		service.reliability.Message = "Other enrichment is stale; automatic removal planning is paused."
	}
	return nil
}

// needsTMDBEnrichment reports whether a media item has never been checked
// by TMDB at all yet, but has an external id TMDB could use to try — see
// EnrichNewMedia. needsJellyfinEnrichment/needsSeerrEnrichment mirror this
// for their own sources; those two never require an external id, since
// Jellyfin/Seerr matching itself is what determines whether one applies.
func needsTMDBEnrichment(m model.Media) bool {
	return m.TMDBEnrichedAt.IsZero() && (m.TMDBID != 0 || m.TVDBID != 0)
}

func needsJellyfinEnrichment(m model.Media) bool { return m.JellyfinEnrichedAt.IsZero() }

func needsSeerrEnrichment(m model.Media) bool { return m.SeerrEnrichedAt.IsZero() }

// EnrichNewMedia immediately fetches Jellyfin/Seerr/TMDB facts — whichever
// of the three a given item has never been checked by yet — rather than
// leaving it to wait for that source's next scheduled reconcile pass. A
// newly discovered item (or one re-identified, e.g. Radarr re-matching a
// movie to a different TMDB entry — see preserveTMDBFacts) gets every
// applicable source's data right away instead of sitting unscored until
// that source's own periodic run, which for TMDB is up to 24h away.
//
// This is deliberately the *only* place a new item's enrichment ever gets
// fetched: RefreshJellyfin/RefreshSeerr/RefreshTMDB exist purely to catch
// facts drifting for items already known (a watch state changing, a
// rating moving, a request being fulfilled), never to discover new items
// — there is no other path that would ever clear one of the *EnrichedAt
// fields this function checks. Meant to be triggered right after a base
// (Radarr/Sonarr) refresh (see cmd/stewarr/main.go); a no-op, cheap call
// when nothing needs any source's attention.
//
// Unlike the three Refresh* methods this never touches Reliability.* or
// requires a generation match against the *whole* snapshot — it's a
// best-effort catch-up for specific items, not an authoritative full
// pass, so a base refresh racing ahead of it just means whatever's still
// unmet is picked up again next cycle (by this trigger, or by that
// source's own next scheduled run, whichever comes first).
func (service *Service) EnrichNewMedia(ctx context.Context) error {
	service.mu.RLock()
	base := cloneMedia(service.items)
	generation := service.generation
	cfg := service.cfg
	service.mu.RUnlock()

	var tmdbPending, jellyfinPending, seerrPending []model.Media
	for _, m := range base {
		if cfg.TMDB.APIKey != "" && needsTMDBEnrichment(m) {
			tmdbPending = append(tmdbPending, m)
		}
		if cfg.Jellyfin.URL != "" && needsJellyfinEnrichment(m) {
			jellyfinPending = append(jellyfinPending, m)
		}
		if cfg.Seerr.URL != "" && needsSeerrEnrichment(m) {
			seerrPending = append(seerrPending, m)
		}
	}
	if len(tmdbPending) == 0 && len(jellyfinPending) == 0 && len(seerrPending) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	var tmdbErr, jellyfinErr, seerrErr error
	now := time.Now()
	if len(tmdbPending) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// TMDB fetches one item at a time — a cancellation partway
			// through still leaves earlier items in this batch genuinely
			// enriched, so tmdbErr below only ever affects the return
			// value, never whether tmdbPending gets merged.
			tmdbErr = service.tmdb.WithContext(ctx).Apply(tmdbPending)
		}()
	}
	if len(jellyfinPending) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if jellyfinErr = service.jf.WithContext(ctx).Apply(jellyfinPending); jellyfinErr == nil {
				for i := range jellyfinPending {
					jellyfinPending[i].JellyfinEnrichedAt = now
				}
			}
		}()
	}
	if len(seerrPending) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if seerrErr = service.seerr.WithContext(ctx).Apply(seerrPending); seerrErr == nil {
				for i := range seerrPending {
					seerrPending[i].SeerrEnrichedAt = now
				}
			}
		}()
	}
	wg.Wait()

	service.mu.Lock()
	defer service.mu.Unlock()
	if generation != service.generation {
		// Base inventory moved on while this ran; the next base refresh's
		// own trigger (or that source's next scheduled run) will catch
		// whatever's still unmet instead of publishing against a snapshot
		// that's no longer current.
		return nil
	}
	items := cloneMedia(service.items)
	if len(tmdbPending) > 0 {
		mergeTMDBFacts(items, tmdbPending)
	}
	if len(jellyfinPending) > 0 && jellyfinErr == nil {
		mergeJellyfinFacts(items, jellyfinPending)
	}
	if len(seerrPending) > 0 && seerrErr == nil {
		mergeSeerrFacts(items, seerrPending)
	}
	valuation.ApplyMedia(items, service.cfg, service.imdbRatings)
	if service.db != nil {
		if err := service.db.PublishEnrichment("new-media", generation, items); err != nil {
			return err
		}
	}
	service.items = items
	if tmdbErr != nil {
		return tmdbErr
	}
	if jellyfinErr != nil {
		return jellyfinErr
	}
	return seerrErr
}

func enrichmentReliable(state string) bool {
	return state == "reliable" || state == "not configured"
}

// preserveJellyfinFacts carries forward Jellyfin-sourced facts across a
// base (Radarr/Sonarr) refresh that has nothing to do with Jellyfin — see
// preserveTMDBFacts for why a genuine external-id change (not just this
// item being new) skips preservation instead: Jellyfin's own match was
// found using the *old* ids, so it may no longer even be correct.
func preserveJellyfinFacts(dst, previous []model.Media) {
	byKey := make(map[string]model.Media, len(previous))
	for _, m := range previous {
		byKey[fmt.Sprintf("%s:%s:%d", m.Type, m.ServiceID, m.SourceID)] = m
	}
	for i := range dst {
		old, ok := byKey[fmt.Sprintf("%s:%s:%d", dst[i].Type, dst[i].ServiceID, dst[i].SourceID)]
		if !ok || externalIDsChanged(dst[i], old) {
			continue
		}
		dst[i].Views = old.Views
		dst[i].UniqueViewers = old.UniqueViewers
		dst[i].LastWatched = old.LastWatched
		dst[i].Favorite = old.Favorite
		dst[i].JellyfinEnrichedAt = old.JellyfinEnrichedAt
	}
}

// preserveSeerrFacts mirrors preserveJellyfinFacts for Requested/RequestedAt.
func preserveSeerrFacts(dst, previous []model.Media) {
	byKey := make(map[string]model.Media, len(previous))
	for _, m := range previous {
		byKey[fmt.Sprintf("%s:%s:%d", m.Type, m.ServiceID, m.SourceID)] = m
	}
	for i := range dst {
		old, ok := byKey[fmt.Sprintf("%s:%s:%d", dst[i].Type, dst[i].ServiceID, dst[i].SourceID)]
		if !ok || externalIDsChanged(dst[i], old) {
			continue
		}
		dst[i].Requested = old.Requested
		dst[i].RequestedAt = old.RequestedAt
		dst[i].SeerrEnrichedAt = old.SeerrEnrichedAt
	}
}

func mergeJellyfinFacts(dst, enriched []model.Media) {
	byKey := make(map[string]model.Media, len(enriched))
	for _, m := range enriched {
		byKey[fmt.Sprintf("%s:%s:%d", m.Type, m.ServiceID, m.SourceID)] = m
	}
	for i := range dst {
		if source, ok := byKey[fmt.Sprintf("%s:%s:%d", dst[i].Type, dst[i].ServiceID, dst[i].SourceID)]; ok {
			dst[i].Views = source.Views
			dst[i].UniqueViewers = source.UniqueViewers
			dst[i].LastWatched = source.LastWatched
			dst[i].Favorite = source.Favorite
			dst[i].JellyfinEnrichedAt = source.JellyfinEnrichedAt
		}
	}
}

func clearJellyfinFacts(items []model.Media) {
	for i := range items {
		items[i].Views = 0
		items[i].UniqueViewers = 0
		items[i].LastWatched = nil
		items[i].Favorite = false
		items[i].JellyfinEnrichedAt = time.Time{}
	}
}

func mergeSeerrFacts(dst, enriched []model.Media) {
	byKey := make(map[string]model.Media, len(enriched))
	for _, m := range enriched {
		byKey[fmt.Sprintf("%s:%s:%d", m.Type, m.ServiceID, m.SourceID)] = m
	}
	for i := range dst {
		if source, ok := byKey[fmt.Sprintf("%s:%s:%d", dst[i].Type, dst[i].ServiceID, dst[i].SourceID)]; ok {
			dst[i].Requested = source.Requested
			dst[i].RequestedAt = source.RequestedAt
			dst[i].SeerrEnrichedAt = source.SeerrEnrichedAt
		}
	}
}

func clearSeerrFacts(items []model.Media) {
	for i := range items {
		items[i].Requested = false
		items[i].RequestedAt = nil
		items[i].SeerrEnrichedAt = time.Time{}
	}
}

// preserveTMDBFacts carries forward TMDB-sourced facts across a base
// (Radarr/Sonarr) refresh that has nothing to do with TMDB, the same as
// preserveJellyfinFacts/preserveSeerrFacts. It also backfills a resolved
// series TMDBID (see tmdb.Client.Apply) so that one-time /find lookup is
// never repeated — but only when the base refresh didn't already supply
// one, since Radarr's own TMDBID for a movie is always the freshest truth
// and must never be overridden by a stale cached value.
//
// If a movie's TMDBID genuinely changed (Radarr re-matched it to a
// different TMDB entry), the old Rating/VoteCount/Popularity were computed
// for the *previous* entry and must not be carried forward onto the new
// one — leaving them zeroed (TMDBEnrichedAt included) is what makes
// EnrichNewMedia pick this item up for an immediate re-fetch instead of it
// silently keeping data that now describes the wrong title.
func preserveTMDBFacts(dst, previous []model.Media) {
	byKey := make(map[string]model.Media, len(previous))
	for _, m := range previous {
		byKey[fmt.Sprintf("%s:%s:%d", m.Type, m.ServiceID, m.SourceID)] = m
	}
	for i := range dst {
		old, ok := byKey[fmt.Sprintf("%s:%s:%d", dst[i].Type, dst[i].ServiceID, dst[i].SourceID)]
		if !ok {
			continue
		}
		if dst[i].TMDBID == 0 {
			dst[i].TMDBID = old.TMDBID
		}
		if dst[i].TMDBID != old.TMDBID {
			continue
		}
		dst[i].TMDBRating = old.TMDBRating
		dst[i].TMDBVoteCount = old.TMDBVoteCount
		dst[i].Popularity = old.Popularity
		dst[i].TMDBEnrichedAt = old.TMDBEnrichedAt
	}
}

func mergeTMDBFacts(dst, enriched []model.Media) {
	byKey := make(map[string]model.Media, len(enriched))
	for _, m := range enriched {
		byKey[fmt.Sprintf("%s:%s:%d", m.Type, m.ServiceID, m.SourceID)] = m
	}
	for i := range dst {
		if source, ok := byKey[fmt.Sprintf("%s:%s:%d", dst[i].Type, dst[i].ServiceID, dst[i].SourceID)]; ok {
			dst[i].TMDBRating = source.TMDBRating
			dst[i].TMDBVoteCount = source.TMDBVoteCount
			dst[i].Popularity = source.Popularity
			dst[i].TMDBEnrichedAt = source.TMDBEnrichedAt
			if source.TMDBID != 0 {
				dst[i].TMDBID = source.TMDBID
			}
		}
	}
}

// clearTMDBFacts resets the enrichment-only facts (rating/votes/popularity
// have a fallback — see model.Media — so "cleared" means valuation falls
// back to Radarr/Sonarr's own Rating/VoteCount, not that the signal
// vanishes) and the per-item staleness timestamp. Used only when TMDB
// enrichment is explicitly turned off (SetTMDBAPIKey with an empty key) —
// a deliberate opt-out should stop influencing valuation immediately
// rather than leaving last-known values to linger indefinitely. A
// transient per-item fetch failure during a normal refresh is a different
// case entirely and must not call this — see RefreshTMDB. A resolved
// series TMDBID is deliberately left alone even here: it's just cached
// plumbing, not a valuation input, and re-resolving it would waste an API
// call for no benefit if TMDB is reconfigured later.
func clearTMDBFacts(items []model.Media) {
	for i := range items {
		items[i].TMDBRating = 0
		items[i].TMDBVoteCount = 0
		items[i].Popularity = 0
		items[i].TMDBEnrichedAt = time.Time{}
	}
}

func cloneMedia(in []model.Media) []model.Media {
	out := make([]model.Media, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Tags = append([]string(nil), in[i].Tags...)
		out[i].DownloadIDs = append([]string(nil), in[i].DownloadIDs...)
		out[i].Torrents = cloneTorrents(in[i].Torrents)
		out[i].RetentionValueReasons = append([]model.Reason(nil), in[i].RetentionValueReasons...)
		out[i].Seasons = cloneSeasons(in[i].Seasons)
	}
	return out
}

func cloneSeasons(in []model.Season) []model.Season {
	out := make([]model.Season, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].RetentionValueReasons = append([]model.Reason(nil), in[i].RetentionValueReasons...)
	}
	return out
}

func cloneTorrents(in []model.Torrent) []model.Torrent {
	out := make([]model.Torrent, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].TorrentValueReasons = append([]model.Reason(nil), in[i].TorrentValueReasons...)
		out[i].MediaItems = append([]model.MediaRef(nil), in[i].MediaItems...)
		out[i].FormerMediaItems = append([]model.MediaRef(nil), in[i].FormerMediaItems...)
		out[i].HardlinkKnownMediaItems = append([]model.MediaRef(nil), in[i].HardlinkKnownMediaItems...)
		out[i].HardlinkedMediaItems = append([]model.MediaRef(nil), in[i].HardlinkedMediaItems...)
		out[i].HardlinkedSeasons = append([]int(nil), in[i].HardlinkedSeasons...)
	}
	return out
}
