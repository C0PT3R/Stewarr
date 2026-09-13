package inventory

import (
	"context"
	"fmt"
	"log"
	"sort"
	"stewarr/internal/config"
	"stewarr/internal/model"
	"stewarr/internal/services/radarr"
	"stewarr/internal/services/sonarr"
	"stewarr/internal/store"
	"stewarr/internal/valuation"
	"strings"
	"sync"
	"time"
)

func parseCursor(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, v)
	return t
}

type torrentProvenance struct {
	Source       string
	ServiceID    string
	OwnerID      int
	SubID        int
	ImportedAt   time.Time
	SupersededBy string
}

// ownerKey identifies one Radarr movie or Sonarr series across multiple
// configured instances of the same type — OwnerID (Radarr's/Sonarr's own
// numeric ID) is unique only within one instance.
type ownerKey struct {
	ServiceID string
	OwnerID   int
}

// torrentKey identifies one torrent across multiple configured qBittorrent
// instances — the same infohash can legitimately exist in more than one
// client (cross-seeding, a seedbox mirrored to a local client).
func torrentKey(svcID, hash string) string {
	return svcID + "\x00" + strings.ToLower(strings.TrimSpace(hash))
}

func addProvenance(m map[string][]torrentProvenance, hash string, p torrentProvenance) {
	h := strings.ToLower(strings.TrimSpace(hash))
	if h == "" {
		return
	}
	m[h] = append(m[h], p)
}

// syncImportHistory fans out import-history sync across every configured
// Radarr and Sonarr instance independently — each gets its own durable
// cursor (radarr.<id>.history.cursor / sonarr.<id>.history.cursor) and its
// own rows in arr_imports (tagged with service_id), since movie/series
// IDs are only unique within one instance. The returned maps are keyed by
// (ServiceID, OwnerID) for the same reason.
func (service *Service) syncImportHistory(ctx context.Context) (map[ownerKey][]string, map[ownerKey][]string, map[string][]torrentProvenance, error) {
	if service.db == nil {
		return nil, nil, nil, fmt.Errorf("database is unavailable")
	}
	service.mu.RLock()
	radInstances := service.cfg.ServicesOfType("radarr")
	sonInstances := service.cfg.ServicesOfType("sonarr")
	radClients := service.rad
	sonClients := service.son
	service.mu.RUnlock()

	type radarrFetch struct {
		svc    config.Service
		events []radarr.ImportEvent
		newest time.Time
		err    error
	}
	type sonarrFetch struct {
		svc    config.Service
		events []sonarr.ImportEvent
		newest time.Time
		err    error
	}
	radFetches := make([]radarrFetch, len(radInstances))
	sonFetches := make([]sonarrFetch, len(sonInstances))
	var wg sync.WaitGroup
	for i, svc := range radInstances {
		wg.Add(1)
		go func(i int, svc config.Service) {
			defer wg.Done()
			cursor, _ := service.db.Meta("radarr." + svc.ID + ".history.cursor")
			client := radClients[svc.ID]
			events, newest, err := client.WithContext(ctx).ImportEventsSince(parseCursor(cursor))
			radFetches[i] = radarrFetch{svc: svc, events: events, newest: newest, err: err}
		}(i, svc)
	}
	for i, svc := range sonInstances {
		wg.Add(1)
		go func(i int, svc config.Service) {
			defer wg.Done()
			cursor, _ := service.db.Meta("sonarr." + svc.ID + ".history.cursor")
			client := sonClients[svc.ID]
			events, newest, err := client.WithContext(ctx).ImportEventsSince(parseCursor(cursor))
			sonFetches[i] = sonarrFetch{svc: svc, events: events, newest: newest, err: err}
		}(i, svc)
	}
	wg.Wait()
	for _, f := range radFetches {
		if f.err != nil {
			return nil, nil, nil, fmt.Errorf("radarr (%s) history: %w", f.svc.Name, f.err)
		}
	}
	for _, f := range sonFetches {
		if f.err != nil {
			return nil, nil, nil, fmt.Errorf("sonarr (%s) history: %w", f.svc.Name, f.err)
		}
	}

	batch := []store.ImportEvent{}
	for _, f := range radFetches {
		for _, x := range f.events {
			batch = append(batch, store.ImportEvent{Source: "radarr", ServiceID: f.svc.ID, OwnerID: x.MovieID, DownloadID: x.DownloadID, ImportedAt: x.Date})
		}
	}
	for _, f := range sonFetches {
		for _, x := range f.events {
			batch = append(batch, store.ImportEvent{Source: "sonarr", ServiceID: f.svc.ID, OwnerID: x.SeriesID, SubID: x.EpisodeID, DownloadID: x.DownloadID, ImportedAt: x.Date})
		}
	}
	if err := service.db.AddImportEvents(batch); err != nil {
		return nil, nil, nil, err
	}
	for _, f := range radFetches {
		if !f.newest.IsZero() {
			if err := service.db.SetMeta("radarr."+f.svc.ID+".history.cursor", f.newest.UTC().Format(time.RFC3339Nano)); err != nil {
				return nil, nil, nil, fmt.Errorf("persist radarr (%s) history cursor: %w", f.svc.Name, err)
			}
		}
	}
	for _, f := range sonFetches {
		if !f.newest.IsZero() {
			if err := service.db.SetMeta("sonarr."+f.svc.ID+".history.cursor", f.newest.UTC().Format(time.RFC3339Nano)); err != nil {
				return nil, nil, nil, fmt.Errorf("persist sonarr (%s) history cursor: %w", f.svc.Name, err)
			}
		}
	}

	provenance := map[string][]torrentProvenance{}
	rids := map[ownerKey][]string{}
	for _, svc := range radInstances {
		rall, err := service.db.ImportEvents("radarr", svc.ID)
		if err != nil {
			return nil, nil, nil, err
		}
		latestMovieHash := map[int]string{}
		for _, x := range rall { // newest first
			h := strings.ToLower(x.DownloadID)
			key := ownerKey{ServiceID: svc.ID, OwnerID: x.OwnerID}
			latest := latestMovieHash[x.OwnerID]
			if latest == "" {
				latestMovieHash[x.OwnerID] = h
				rids[key] = []string{h}
				addProvenance(provenance, h, torrentProvenance{Source: "radarr", ServiceID: svc.ID, OwnerID: x.OwnerID, ImportedAt: x.ImportedAt})
				continue
			}
			if h == latest {
				addProvenance(provenance, h, torrentProvenance{Source: "radarr", ServiceID: svc.ID, OwnerID: x.OwnerID, ImportedAt: x.ImportedAt})
				continue
			}
			addProvenance(provenance, h, torrentProvenance{Source: "radarr", ServiceID: svc.ID, OwnerID: x.OwnerID, ImportedAt: x.ImportedAt, SupersededBy: latest})
		}
	}

	sids := map[ownerKey][]string{}
	for _, svc := range sonInstances {
		sall, err := service.db.ImportEvents("sonarr", svc.ID)
		if err != nil {
			return nil, nil, nil, err
		}
		bySeries := map[int]map[string]bool{}
		latestEpisodeHash := map[int]string{}
		for _, x := range sall { // newest first, one current hash per episode
			h := strings.ToLower(x.DownloadID)
			latest := latestEpisodeHash[x.SubID]
			if latest == "" {
				latestEpisodeHash[x.SubID] = h
				if bySeries[x.OwnerID] == nil {
					bySeries[x.OwnerID] = map[string]bool{}
				}
				bySeries[x.OwnerID][h] = true
				addProvenance(provenance, h, torrentProvenance{Source: "sonarr", ServiceID: svc.ID, OwnerID: x.OwnerID, SubID: x.SubID, ImportedAt: x.ImportedAt})
				continue
			}
			if h == latest {
				addProvenance(provenance, h, torrentProvenance{Source: "sonarr", ServiceID: svc.ID, OwnerID: x.OwnerID, SubID: x.SubID, ImportedAt: x.ImportedAt})
				continue
			}
			addProvenance(provenance, h, torrentProvenance{Source: "sonarr", ServiceID: svc.ID, OwnerID: x.OwnerID, SubID: x.SubID, ImportedAt: x.ImportedAt, SupersededBy: latest})
		}
		for sid, set := range bySeries {
			key := ownerKey{ServiceID: svc.ID, OwnerID: sid}
			for h := range set {
				sids[key] = append(sids[key], h)
			}
			sort.Strings(sids[key])
		}
	}
	return rids, sids, provenance, nil
}

// syncTorrents fans out an incremental sync across every configured
// qBittorrent instance independently, each with its own durable RID cursor.
// The same infohash can legitimately exist in more than one instance
// (cross-seeding, a seedbox mirrored to a local client), so the merged
// result is keyed by torrentKey(instanceID, hash), not hash alone.
func (service *Service) syncTorrents(ctx context.Context) (map[string]model.Torrent, map[string]int64, error) {
	service.mu.RLock()
	qbInstances := service.cfg.ServicesOfType("qbittorrent")
	qbClients := service.qb
	service.mu.RUnlock()

	previousByInstance := map[string]map[string]model.Torrent{}
	if service.db != nil {
		ps, err := service.db.LoadTorrents()
		if err != nil {
			return nil, nil, err
		}
		for _, p := range ps {
			if previousByInstance[p.ServiceID] == nil {
				previousByInstance[p.ServiceID] = map[string]model.Torrent{}
			}
			previousByInstance[p.ServiceID][strings.ToLower(p.Hash)] = p
		}
	}

	out := map[string]model.Torrent{}
	rids := map[string]int64{}
	for _, svc := range qbInstances {
		rid := int64(0)
		if service.db != nil {
			var err error
			rid, err = service.db.MetaInt64("qbittorrent." + svc.ID + ".rid")
			if err != nil {
				return nil, nil, fmt.Errorf("load qbittorrent (%s) sync cursor: %w", svc.Name, err)
			}
		}
		next, newRID, err := qbClients[svc.ID].WithContext(ctx).Sync(previousByInstance[svc.ID], rid)
		if err != nil {
			return nil, nil, fmt.Errorf("qbittorrent (%s): %w", svc.Name, err)
		}
		rids[svc.ID] = newRID
		for hash, t := range next {
			t.ServiceID = svc.ID
			out[torrentKey(svc.ID, hash)] = t
		}
	}
	return out, rids, nil
}

func (service *Service) Refresh(ctx context.Context) error {
	defer service.publishChange()
	refreshStarted := time.Now()
	validationStarted := time.Now()
	if err := service.ValidateBaseServices(ctx); err != nil {
		return service.fail(err)
	}
	validationDuration := time.Since(validationStarted)
	service.mu.Lock()
	if service.refreshing {
		service.mu.Unlock()
		return nil
	}
	service.refreshing = true
	service.stageTimings = map[string]time.Duration{}
	service.stageTimings["validation"] = validationDuration
	service.mu.Unlock()
	defer func() { service.mu.Lock(); service.refreshing = false; service.mu.Unlock() }()
	defer service.logStageTimings()
	defer service.setStageTiming("total refresh", refreshStarted)

	// Stage 1: current application state. Publish this quickly; cached database
	// state was already available from startup while these requests were running.
	service.mu.RLock()
	radInstances := service.cfg.ServicesOfType("radarr")
	sonInstances := service.cfg.ServicesOfType("sonarr")
	radClients := service.rad
	sonClients := service.son
	service.mu.RUnlock()
	var wg sync.WaitGroup
	radarrMediaByInstance := make([][]model.Media, len(radInstances))
	sonarrMediaByInstance := make([][]model.Media, len(sonInstances))
	radarrErrs := make([]error, len(radInstances))
	sonarrErrs := make([]error, len(sonInstances))
	var torrentMap map[string]model.Torrent
	var torrentRIDs map[string]int64
	var qbittorrentErr error
	baseStarted := time.Now()
	wg.Add(len(radInstances) + len(sonInstances) + 1)
	for i, svc := range radInstances {
		go func(i int, svc config.Service) {
			defer wg.Done()
			media, err := radClients[svc.ID].WithContext(ctx).Inventory()
			for mediaIndex := range media {
				media[mediaIndex].ServiceID = svc.ID
				media[mediaIndex].ServiceName = svc.Name
			}
			radarrMediaByInstance[i] = media
			radarrErrs[i] = err
			service.setStatus(svc.Name, true, err == nil, err)
		}(i, svc)
	}
	for i, svc := range sonInstances {
		go func(i int, svc config.Service) {
			defer wg.Done()
			media, err := sonClients[svc.ID].WithContext(ctx).Inventory()
			for mediaIndex := range media {
				media[mediaIndex].ServiceID = svc.ID
				media[mediaIndex].ServiceName = svc.Name
			}
			sonarrMediaByInstance[i] = media
			sonarrErrs[i] = err
			service.setStatus(svc.Name, true, err == nil, err)
		}(i, svc)
	}
	go func() { defer wg.Done(); torrentMap, torrentRIDs, qbittorrentErr = service.syncTorrents(ctx) }()
	wg.Wait()
	service.setStageTiming("base inventory", baseStarted)
	for i, err := range radarrErrs {
		if err != nil {
			return service.fail(fmt.Errorf("radarr (%s): %w", radInstances[i].Name, err))
		}
	}
	for i, err := range sonarrErrs {
		if err != nil {
			return service.fail(fmt.Errorf("sonarr (%s): %w", sonInstances[i].Name, err))
		}
	}
	if qbittorrentErr != nil {
		return service.fail(fmt.Errorf("qbittorrent: %w", qbittorrentErr))
	}
	var radarrMedia, sonarrMedia []model.Media
	for _, media := range radarrMediaByInstance {
		radarrMedia = append(radarrMedia, media...)
	}
	for _, media := range sonarrMediaByInstance {
		sonarrMedia = append(sonarrMedia, media...)
	}
	all := append(radarrMedia, sonarrMedia...)
	// Jellyfin and Seerr are enrichment, not inventory authorities. Base refresh
	// carries forward their last published facts; their independent tasks replace
	// those facts only after a complete generation-matched response.
	previousMedia, _, _ := service.Snapshot()
	preserveJellyfinFacts(all, previousMedia)
	preserveSeerrFacts(all, previousMedia)
	preserveTMDBFacts(all, previousMedia)
	baseTorrents := make([]model.Torrent, 0, len(torrentMap))
	for _, t := range torrentMap {
		baseTorrents = append(baseTorrents, t)
	}
	sort.Slice(baseTorrents, func(i, j int) bool {
		return strings.ToLower(baseTorrents[i].Hash) < strings.ToLower(baseTorrents[j].Hash)
	})
	fingerprint := inventoryFingerprint(all, baseTorrents)

	service.mu.RLock()
	baseDrift := !service.hasBaseFingerprint || fingerprint != service.baseFingerprint
	hasFileModel := !service.filesUpdated.IsZero()
	oldTorrents := append([]model.Torrent(nil), service.torrents...)
	startTopologyVersion := service.fileTopologyVersion
	service.mu.RUnlock()

	// Ordinary content activity (a new import, a torrent completing and
	// moving out of its incomplete directory, a file's size settling) changes
	// the base-catalog fingerprint just as a removal would. Rather than mark
	// the file topology stale and wait for the next full reconciliation (up
	// to 12h away), reconcile exactly what changed inline, before publishing,
	// so file topology never has to pass through "stale" for this — only a
	// genuine reconciliation error still falls back to that.
	var topology *deltaFileTopology
	if baseDrift && hasFileModel {
		delta := computeInventoryDelta(previousMedia, all, oldTorrents, baseTorrents)
		if !delta.empty() {
			if result, err := service.reconcileInventoryDelta(ctx, delta, previousMedia, all, oldTorrents, baseTorrents); err != nil {
				log.Printf("[inventory] inline file topology reconciliation failed, file topology will report stale until the next full reconciliation: %v", err)
			} else {
				topology = &result
			}
		}
	}

	service.mu.Lock()
	if topology != nil && service.fileTopologyVersion != startTopologyVersion {
		// A concurrent files reconciliation (full or targeted) published in
		// the time it took to compute this delta. Discard it rather than
		// publish file-topology facts derived from a base state that is no
		// longer current; the concurrent publish's own facts stand instead.
		topology = nil
	}
	if !service.hasBaseFingerprint || fingerprint != service.baseFingerprint {
		service.generation++
		service.baseFingerprint = fingerprint
		service.hasBaseFingerprint = true
		if !service.filesUpdated.IsZero() && topology == nil {
			service.reliability.FileModel = "stale"
		}
	}
	publishGeneration := service.generation
	preWriteTopologyVersion := service.fileTopologyVersion
	service.mu.Unlock()

	// The database write (when there's a file-topology delta to persist)
	// happens outside mu — a slow write must never stall every other page's
	// reads. publishMu only serializes this against other reconciliation
	// publishers (full/targeted can run concurrently in other goroutines).
	dbWriteFailed := false
	if topology != nil && service.db != nil {
		service.publishMu.Lock()
		if e := service.db.PublishReconciliationDelta(store.ReconciliationDelta{
			Paths: topology.affectedPaths, Files: topology.filesToInsert, MediaOwners: topology.mediaOwners, MediaRefs: topology.mediaRefsToInsert,
			RemovedTorrentHashes: topology.removedTorrentHashes, TorrentRefs: topology.torrentRefsToInsert, Unmanaged: topology.unmanagedToInsert,
			Torrents: append([]model.Torrent(nil), baseTorrents...), Media: cloneMedia(all), Generation: publishGeneration,
		}); e != nil {
			log.Printf("[inventory] persist inline file topology reconciliation: %v; file topology will report stale until the next full reconciliation", e)
			topology = nil
			dbWriteFailed = true
		}
		service.publishMu.Unlock()
	}

	service.mu.Lock()
	if topology != nil && service.fileTopologyVersion != preWriteTopologyVersion {
		// A concurrent files reconciliation published while our write was in
		// flight. Its facts are newer than ours; discard rather than overwrite.
		topology = nil
	}
	if topology != nil {
		service.files, service.mediaFileRefs, service.torrentFileRefs = topology.files, topology.mediaRefs, topology.torrentRefs
		now := time.Now()
		service.unmanaged, service.unmanagedUpdated, service.unmanagedErr = topology.unmanaged, now, nil
		service.filesUpdated, service.filesErr = now, nil
		service.reliability.FileModel = "reliable"
		service.fileGeneration = service.generation
		service.fileTopologyVersion++
	} else if dbWriteFailed {
		service.reliability.FileModel = "stale"
	}
	service.items = cloneMedia(all)
	service.torrents = append([]model.Torrent(nil), baseTorrents...)
	service.updated = time.Now()
	service.lastErr = nil
	service.reliability.Inventory = true
	service.mu.Unlock()
	service.baseReadyOnce.Do(func() { close(service.baseReady) })
	var rids, sids map[ownerKey][]string
	var provenance map[string][]torrentProvenance
	var he error
	historyStarted := time.Now()
	historyDone := make(chan struct{})
	go func() {
		defer close(historyDone)
		rids, sids, provenance, he = service.syncImportHistory(ctx)
	}()

	valuation.ApplyMedia(all, service.cfg)
	service.mu.Lock()
	service.items = cloneMedia(all)
	service.updated = time.Now()
	service.lastErr = nil
	service.mu.Unlock()
	<-historyDone
	service.setStageTiming("import history", historyStarted)
	if he != nil {
		return service.fail(he)
	}

	if len(service.cfg.ServicesOfType("qbittorrent")) == 0 {
		service.mu.RLock()
		files, mediaFileRefs, cfg := service.files, service.mediaFileRefs, service.cfg
		generation := service.generation
		service.mu.RUnlock()
		applyMediaFileEstimates(all, files, mediaFileRefs)
		attachSeasons(all, mediaFileRefs, files)
		applySeasonFileEstimates(all, files, mediaFileRefs)
		valuation.ApplyMedia(all, cfg)
		if service.db != nil {
			service.publishMu.Lock()
			err := service.db.PublishInventory(generation, nil, nil, all)
			service.publishMu.Unlock()
			if err != nil {
				return service.persistenceFail(fmt.Errorf("persist inventory generation: %w", err))
			}
		}
		service.mu.Lock()
		service.items = cloneMedia(all)
		service.torrents = nil
		service.reliability.Valuation = enrichmentReliable(service.reliability.Jellyfin) && enrichmentReliable(service.reliability.Seerr) && enrichmentReliable(service.reliability.TMDB)
		if service.reliability.Valuation {
			service.reliability.Message = "Media valuation is reliable."
		}
		service.mu.Unlock()
		return nil
	}

	// Stage 2: durable, incremental import provenance. The first-ever run
	// bootstraps *arr history; later runs stop at the saved cursor.
	// hashIndex maps a bare torrent hash to every composite torrentMap key
	// that currently carries it — the same infohash can legitimately be
	// present in more than one configured qBittorrent instance.
	hashIndex := map[string][]string{}
	for key, t := range torrentMap {
		h := strings.ToLower(t.Hash)
		hashIndex[h] = append(hashIndex[h], key)
	}
	torrentMediaItems := map[string][]model.MediaRef{}
	for i := range all {
		m := &all[i]
		if m.Type == model.Movie {
			m.DownloadIDs = rids[ownerKey{ServiceID: m.ServiceID, OwnerID: m.SourceID}]
		} else {
			m.DownloadIDs = sids[ownerKey{ServiceID: m.ServiceID, OwnerID: m.SourceID}]
		}
		// A logical media item may remain in Radarr/Sonarr after all of its files
		// have been deleted. Keep that media in Stewarr, but do not let its
		// historical latest download hash claim a torrent as currently associated.
		// Until first-class media files are indexed, SizeBytes > 0 is our current
		// authoritative summary that this media has file data in the library.
		if !mediaHasCurrentFiles(*m) {
			continue
		}
		seen := map[string]bool{}
		for _, id := range m.DownloadIDs {
			h := strings.ToLower(id)
			for _, key := range hashIndex[h] {
				if !seen[key] {
					torrentMediaItems[key] = append(torrentMediaItems[key], model.MediaRef{ServiceID: m.ServiceID, Type: m.Type, SourceID: m.SourceID, Title: m.Title, Year: m.Year})
					seen[key] = true
				}
			}
		}
	}
	currentRefs := map[string]model.MediaRef{}
	for _, m := range all {
		currentRefs[fmt.Sprintf("%s:%s:%d", m.Type, m.ServiceID, m.SourceID)] = model.MediaRef{ServiceID: m.ServiceID, Type: m.Type, SourceID: m.SourceID, Title: m.Title, Year: m.Year}
	}

	torrentList := make([]model.Torrent, 0, len(torrentMap))
	for h, t := range torrentMap {
		t.MediaItems = torrentMediaItems[h]
		t.FormerMediaItems = nil
		t.SupersededByHash = ""
		t.AssociationReason = ""
		t.StorageError = ""
		t.ReclaimableKnown = false
		t.ReclaimableBytes = 0
		t.SharedBytes = 0
		t.InspectedBytes = 0
		t.InspectedFiles = 0
		t.SharedFiles = 0

		prov := provenance[strings.ToLower(t.Hash)]
		superseded := false
		seenFormer := map[string]bool{}
		for _, p := range prov {
			if p.SupersededBy != "" {
				superseded = true
				if t.SupersededByHash == "" {
					t.SupersededByHash = p.SupersededBy
				}
			}
			kind := model.Movie
			if p.Source == "sonarr" {
				kind = model.Series
			}
			key := fmt.Sprintf("%s:%s:%d", kind, p.ServiceID, p.OwnerID)
			ref, ok := currentRefs[key]
			if !ok {
				// Import history remains meaningful after media disappears from
				// Radarr/Sonarr. Its stable owner identity survives even when the
				// current title and year can no longer be resolved.
				ref = model.MediaRef{ServiceID: p.ServiceID, Type: kind, SourceID: p.OwnerID}
			}
			if !seenFormer[key] {
				t.FormerMediaItems = append(t.FormerMediaItems, ref)
				seenFormer[key] = true
			}
		}
		switch {
		case len(t.MediaItems) > 0:
			t.AssociationStatus = model.TorrentCurrent
			t.AssociationReason = "Torrent hash matches the latest imported release for current library media."
		case superseded:
			t.AssociationStatus = model.TorrentSuperseded
			t.AssociationReason = "A later release was imported for the same Radarr movie or Sonarr episode."
		case len(prov) > 0:
			t.AssociationStatus = model.TorrentOrphaned
			t.AssociationReason = "No current media relationship exists; import history records a former relationship."
		default:
			t.AssociationStatus = model.TorrentUnassociated
			t.AssociationReason = "No authoritative Radarr/Sonarr import association is known."
		}

		torrentList = append(torrentList, t)
		torrentMap[h] = t
	}

	// Storage estimates come from the reconciled File model. Normal inventory
	// refreshes never walk torrent directories; they reuse the latest sparse file
	// reconciliation snapshot and therefore stay cheap.
	files, mediaFileRefs, torrentFileRefs, _, _ := service.FileSnapshot()
	relationshipsStarted := time.Now()
	applyTorrentFileEstimates(torrentList, files, torrentFileRefs)
	applyTorrentMediaHardlinks(torrentList, all, files, mediaFileRefs, torrentFileRefs)
	applyMediaFileEstimates(all, files, mediaFileRefs)
	applyMediaBundleEstimates(all, torrentList, files, mediaFileRefs, torrentFileRefs)
	attachSeasons(all, mediaFileRefs, files)
	applySeasonFileEstimates(all, files, mediaFileRefs)
	applySeasonBundleEstimates(all, torrentList, files, mediaFileRefs, torrentFileRefs)
	valuation.ApplyTorrentValue(torrentList, service.cfg, service.recentTorrentHistory())
	service.setStageTiming("relationships", relationshipsStarted)

	sort.SliceStable(torrentList, func(i, j int) bool {
		if torrentList[i].AssociationStatus != torrentList[j].AssociationStatus {
			return torrentList[i].AssociationStatus < torrentList[j].AssociationStatus
		}
		return strings.ToLower(torrentList[i].Name) < strings.ToLower(torrentList[j].Name)
	})
	projectTorrentRelations(all, torrentList)
	valuation.ApplyMedia(all, service.cfg)
	service.mu.Lock()
	// Re-merge the newest topology while holding the generation lock. A file
	// reconciliation may have completed after the earlier snapshot was read;
	// publishing its stale projection here would otherwise undo that work.
	relationshipsMergeStarted := time.Now()
	applyTorrentFileEstimates(torrentList, service.files, service.torrentFileRefs)
	applyTorrentMediaHardlinks(torrentList, all, service.files, service.mediaFileRefs, service.torrentFileRefs)
	applyMediaFileEstimates(all, service.files, service.mediaFileRefs)
	applyMediaBundleEstimates(all, torrentList, service.files, service.mediaFileRefs, service.torrentFileRefs)
	attachSeasons(all, service.mediaFileRefs, service.files)
	applySeasonFileEstimates(all, service.files, service.mediaFileRefs)
	applySeasonBundleEstimates(all, torrentList, service.files, service.mediaFileRefs, service.torrentFileRefs)
	projectTorrentRelations(all, torrentList)
	valuation.ApplyTorrentValue(torrentList, service.cfg, service.recentTorrentHistory())
	valuation.ApplyMedia(all, service.cfg)
	// setStageTiming locks service.mu itself, and it is already held here.
	service.stageTimings["relationships merge"] = time.Since(relationshipsMergeStarted)
	generation := service.generation
	service.mu.Unlock()

	// The database write happens outside mu — a slow write must never stall
	// every other page's reads. publishMu only serializes this against other
	// reconciliation publishers (full/targeted/delta can run concurrently in
	// other goroutines); it is never held by a reader.
	if service.db != nil {
		service.publishMu.Lock()
		err := service.db.PublishInventory(generation, torrentRIDs, torrentList, all)
		service.publishMu.Unlock()
		if err != nil {
			return service.persistenceFail(fmt.Errorf("persist inventory generation: %w", err))
		}
	}

	service.mu.Lock()
	service.items = cloneMedia(all)
	service.torrents = torrentList
	service.updated = time.Now()
	service.lastErr = nil
	service.reliability.Valuation = enrichmentReliable(service.reliability.Jellyfin) && enrichmentReliable(service.reliability.Seerr) && enrichmentReliable(service.reliability.TMDB)
	if service.reliability.Valuation {
		service.reliability.Message = "Media valuation is reliable."
	}
	service.mu.Unlock()
	return nil
}
