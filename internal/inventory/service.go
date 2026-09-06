package inventory

import (
	"connarr/internal/config"
	"connarr/internal/filetopology"
	"connarr/internal/integrations/jellyfin"
	"connarr/internal/integrations/qbittorrent"
	"connarr/internal/integrations/radarr"
	"connarr/internal/integrations/seerr"
	"connarr/internal/integrations/sonarr"
	"connarr/internal/model"
	"connarr/internal/store"
	"connarr/internal/valuation"
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type ServiceStatus struct {
	ID         string
	Name       string
	Configured bool
	OK         bool
	Message    string
	CheckedAt  time.Time
}

type Reliability struct {
	Inventory bool   `json:"inventory"`
	Jellyfin  string `json:"jellyfin"`
	Seerr     string `json:"seerr"`
	Valuation bool   `json:"valuation"`
	FileModel string `json:"fileModel"`
	Message   string `json:"message"`
}

type Service struct {
	cfg              config.Config
	mu               sync.RWMutex
	items            []model.Media
	torrents         []model.Torrent
	unmanaged        []model.UnmanagedFile
	unmanagedUpdated time.Time
	unmanagedErr     error
	files            []model.File
	mediaFileRefs    []model.MediaFileRef
	torrentFileRefs  []model.TorrentFileRef
	storageRoots     []storageRoot
	filesUpdated     time.Time
	filesErr         error
	updated          time.Time
	lastErr          error
	refreshing       bool
	reliability      Reliability
	baseReady        chan struct{}
	baseReadyOnce    sync.Once
	stageTimings     map[string]time.Duration
	validatedBaseAt  time.Time
	generation       uint64
	fileGeneration   uint64
	// fileTopologyVersion increments every time files/mediaFileRefs/
	// torrentFileRefs/unmanaged is published, by whichever of the three
	// writers (full reconcileFiles, reconcileTargeted, or Refresh's inline
	// delta reconciliation) does it. A writer that read the old topology
	// before doing its own I/O checks this hasn't moved before publishing,
	// so a concurrent writer's work is never silently clobbered.
	fileTopologyVersion      uint64
	baseFingerprint          [32]byte
	hasBaseFingerprint       bool
	enrichmentFingerprint    [32]byte
	hasEnrichmentFingerprint bool
	statuses                 map[string]ServiceStatus
	reconciliationMu         sync.Mutex
	// publishMu serializes concurrent reconciliation publishers (full, delta,
	// and targeted can all run from different goroutines) against each
	// other, WITHOUT being held during the database write itself — mu (the
	// RWMutex nearly every page load needs just to read cached state) must
	// never be held across I/O, or a single slow write stalls the entire
	// app. publishMu only prevents two publishers from interleaving; readers
	// never touch it.
	publishMu sync.Mutex
	changed   chan struct{}
	// rad/son/qb are keyed by config.Service.ID: Radarr, Sonarr, and
	// qBittorrent are commonly run as more than one instance (separate
	// quality-tier libraries, a seedbox alongside a local client). Jellyfin
	// and Seerr are each a single centralized service in every known
	// real-world deployment, so they keep the simpler single-client shape.
	rad   map[string]*radarr.Client
	son   map[string]*sonarr.Client
	jf    *jellyfin.Client
	seerr *seerr.Client
	qb    map[string]*qbittorrent.Client
	db    *store.Store
	// configPath is where a live config mutation (AddService) persists
	// the updated Config. Empty means live editing is unavailable (e.g. a
	// Service built directly in tests, with no file backing it at all).
	configPath string
	// configMu serializes AddService calls: validate, persist, then swap
	// service.cfg/rebuild the affected client as one sequence, so concurrent
	// calls can't race each other's read-modify-write of Services.
	configMu sync.Mutex
}

// SetConfigPath records where live config mutations should be persisted.
// Called once from main() after New(); left unset in tests that construct a
// Service directly, where AddService is expected to report an error
// rather than silently write nowhere.
func (service *Service) SetConfigPath(path string) {
	service.mu.Lock()
	service.configPath = path
	service.mu.Unlock()
}

func New(configuration config.Config, database *store.Store) *Service {
	service := &Service{cfg: configuration, db: database, statuses: map[string]ServiceStatus{}, stageTimings: map[string]time.Duration{}, baseReady: make(chan struct{}), changed: make(chan struct{}), rad: buildRadarrClients(configuration), son: buildSonarrClients(configuration), jf: jellyfin.New(configuration.Jellyfin.URL, configuration.Jellyfin.APIKey), seerr: seerr.New(configuration.Seerr.URL, configuration.Seerr.APIKey), qb: buildQBittorrentClients(configuration)}
	loadStarted := time.Now()
	if database != nil {
		if items, updated, err := database.LoadMedia(); err == nil {
			service.items, service.updated = items, updated
			if !updated.IsZero() {
				service.reliability.Inventory = true
				service.reliability.Jellyfin = enrichmentInitialState(configuration.Jellyfin.URL != "")
				service.reliability.Seerr = enrichmentInitialState(configuration.Seerr.URL != "")
				service.reliability.Valuation = configuration.Jellyfin.URL == "" && configuration.Seerr.URL == ""
				service.baseReadyOnce.Do(func() { close(service.baseReady) })
			}
		} else {
			log.Printf("[inventory] load cached media: %v", err)
		}
		if torrents, err := database.LoadTorrents(); err == nil {
			service.torrents = torrents
			for torrentIndex := range service.torrents {
				service.torrents[torrentIndex].AssociationStatus = model.NormalizeTorrentStatus(service.torrents[torrentIndex].AssociationStatus)
			}
		} else {
			log.Printf("[inventory] load cached torrents: %v", err)
		}
		if files, updated, err := database.LoadUnmanagedFiles(); err == nil {
			service.unmanaged, service.unmanagedUpdated = files, updated
		} else {
			log.Printf("[inventory] load cached unmanaged files: %v", err)
		}
		if files, mediaRefs, torrentRefs, updated, err := database.LoadFiles(); err == nil {
			nameByID := map[string]string{}
			for _, svc := range configuration.Services {
				nameByID[svc.ID] = svc.Name
			}
			for fileIndex := range files {
				for contextIndex := range files[fileIndex].StorageContexts {
					if serviceName := nameByID[files[fileIndex].StorageContexts[contextIndex].ServiceID]; serviceName != "" {
						files[fileIndex].StorageContexts[contextIndex].ServiceName = serviceName
					}
				}
			}
			for refIndex := range mediaRefs {
				if mediaRefs[refIndex].ServiceName == "" {
					mediaRefs[refIndex].ServiceName = serviceName(configuration, mediaRefs[refIndex].Source, strings.Title(mediaRefs[refIndex].Source))
				}
				if mediaRefs[refIndex].ServiceID == "" {
					mediaRefs[refIndex].ServiceID = svcID(configuration, mediaRefs[refIndex].Source)
				}
			}
			for refIndex := range torrentRefs {
				if torrentRefs[refIndex].ServiceName == "" {
					torrentRefs[refIndex].ServiceName = serviceName(configuration, "qbittorrent", torrentRefs[refIndex].Client)
				}
				if torrentRefs[refIndex].ServiceID == "" {
					torrentRefs[refIndex].ServiceID = svcID(configuration, "qbittorrent")
				}
			}
			service.files, service.mediaFileRefs, service.torrentFileRefs, service.filesUpdated = files, mediaRefs, torrentRefs, updated
			if !updated.IsZero() {
				service.reliability.FileModel = "stale"
			}
			applyTorrentFileEstimates(service.torrents, files, torrentRefs)
			applyTorrentMediaHardlinks(service.torrents, service.items, files, mediaRefs, torrentRefs)
			projectTorrentRelations(service.items, service.torrents)
			valuation.ApplyMedia(service.items, service.cfg)
		} else {
			log.Printf("[inventory] load cached File topology: %v", err)
		}
		log.Printf("[inventory] timing startup cache load and relationships=%s", time.Since(loadStarted).Round(time.Millisecond))
	}
	if service.reliability.Jellyfin == "" {
		service.reliability.Jellyfin = enrichmentInitialState(configuration.Jellyfin.URL != "")
	}
	if service.reliability.Seerr == "" {
		service.reliability.Seerr = enrichmentInitialState(configuration.Seerr.URL != "")
	}
	if service.reliability.FileModel == "" {
		service.reliability.FileModel = "pending"
	}
	if service.reliability.Inventory {
		for mediaIndex := range service.items {
			for torrentIndex := range service.items[mediaIndex].Torrents {
				service.items[mediaIndex].Torrents[torrentIndex].AssociationStatus = model.NormalizeTorrentStatus(service.items[mediaIndex].Torrents[torrentIndex].AssociationStatus)
			}
		}
		valuation.ApplyMedia(service.items, service.cfg)
		service.generation = 1
		service.baseFingerprint = inventoryFingerprint(service.items, service.torrents)
		service.hasBaseFingerprint = true
		service.enrichmentFingerprint = mediaEnrichmentFingerprint(service.items)
		service.hasEnrichmentFingerprint = true
	}
	return service
}

func mediaEnrichmentFingerprint(items []model.Media) [32]byte {
	media := append([]model.Media(nil), items...)
	sort.Slice(media, func(i, j int) bool {
		if media[i].Type != media[j].Type {
			return media[i].Type < media[j].Type
		}
		return media[i].SourceID < media[j].SourceID
	})
	var b strings.Builder
	for _, m := range media {
		fmt.Fprintf(&b, "%s\x00%d\x00%d\x00%d\x00%s\n", m.Type, m.SourceID, m.TMDBID, m.TVDBID, m.IMDBID)
	}
	return sha256.Sum256([]byte(b.String()))
}

func inventoryFingerprint(items []model.Media, torrents []model.Torrent) [32]byte {
	media := append([]model.Media(nil), items...)
	tors := append([]model.Torrent(nil), torrents...)
	sort.Slice(media, func(i, j int) bool {
		if media[i].Type != media[j].Type {
			return media[i].Type < media[j].Type
		}
		return media[i].SourceID < media[j].SourceID
	})
	sort.Slice(tors, func(i, j int) bool { return strings.ToLower(tors[i].Hash) < strings.ToLower(tors[j].Hash) })
	var b strings.Builder
	for _, m := range media {
		fmt.Fprintf(&b, "m\x00%s\x00%d\x00%s\x00%d\n", m.Type, m.SourceID, filepath.Clean(m.Path), m.SizeBytes)
	}
	for _, t := range tors {
		fmt.Fprintf(&b, "t\x00%s\x00%s\n", strings.ToLower(t.Hash), filepath.Clean(t.SavePath))
	}
	return sha256.Sum256([]byte(b.String()))
}

func enrichmentInitialState(configured bool) string {
	if configured {
		return "stale"
	}
	return "not configured"
}

func (service *Service) ReliabilitySnapshot() Reliability {
	service.mu.RLock()
	defer service.mu.RUnlock()
	return service.reliability
}

func (service *Service) RefreshAdvisory() string {
	service.mu.RLock()
	defer service.mu.RUnlock()
	parts := []string{}
	if service.reliability.Jellyfin == "stale" {
		parts = append(parts, "Jellyfin enrichment stale")
	}
	if service.reliability.Seerr == "stale" {
		parts = append(parts, "Seerr enrichment stale")
	}
	return strings.Join(parts, "; ")
}

func (service *Service) ValidateJellyfin(ctx context.Context) error {
	if service.cfg.Jellyfin.URL == "" {
		return nil
	}
	err := service.jf.WithContext(ctx).Validate()
	service.setStatus(serviceName(service.cfg, "jellyfin", "Jellyfin"), true, err == nil, err)
	return err
}

func (service *Service) ValidateSeerr(ctx context.Context) error {
	if service.cfg.Seerr.URL == "" {
		return nil
	}
	err := service.seerr.WithContext(ctx).Validate()
	service.setStatus(serviceName(service.cfg, "seerr", "Seerr"), true, err == nil, err)
	return err
}

func (service *Service) StageTimings() map[string]time.Duration {
	service.mu.RLock()
	defer service.mu.RUnlock()
	out := make(map[string]time.Duration, len(service.stageTimings))
	for k, v := range service.stageTimings {
		out[k] = v
	}
	return out
}

func (service *Service) setStageTiming(name string, started time.Time) {
	service.setStageDuration(name, time.Since(started))
}

func (service *Service) setStageDuration(name string, d time.Duration) {
	service.mu.Lock()
	service.stageTimings[name] = d
	service.mu.Unlock()
}

func (service *Service) logStageTimings() {
	timings := service.StageTimings()
	names := make([]string, 0, len(timings))
	for name := range timings {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		log.Printf("[inventory] timing %s=%s", name, timings[name].Round(time.Millisecond))
	}
}

func (service *Service) WaitForBase(ctx context.Context) bool {
	select {
	case <-service.baseReady:
		return true
	case <-ctx.Done():
		return false
	}
}

func (service *Service) setStatus(name string, configured, ok bool, err error) {
	service.mu.Lock()
	service.setStatusLocked(name, configured, ok, err)
	service.mu.Unlock()
}

func (service *Service) setStatusLocked(name string, configured, ok bool, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	service.statuses[name] = ServiceStatus{Name: name, Configured: configured, OK: ok, Message: msg, CheckedAt: time.Now()}
}

func (service *Service) StatusSnapshot() []ServiceStatus {
	service.mu.RLock()
	defer service.mu.RUnlock()
	out := make([]ServiceStatus, 0, len(service.cfg.Services))
	for _, i := range service.cfg.Services {
		if st, ok := service.statuses[i.Name]; ok {
			st.ID = i.ID
			out = append(out, st)
			continue
		}
		out = append(out, ServiceStatus{ID: i.ID, Name: i.Name, Configured: i.Enabled(), OK: false, Message: "Not checked yet"})
	}
	return out
}

func (service *Service) Store() *store.Store { return service.db }

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
	baseTorrents := make([]model.Torrent, 0, len(torrentMap))
	for _, t := range torrentMap {
		baseTorrents = append(baseTorrents, t)
	}
	sort.Slice(baseTorrents, func(i, j int) bool {
		return strings.ToLower(baseTorrents[i].Hash) < strings.ToLower(baseTorrents[j].Hash)
	})
	fingerprint := inventoryFingerprint(all, baseTorrents)
	enrichmentFingerprint := mediaEnrichmentFingerprint(all)

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
	if !service.hasEnrichmentFingerprint || enrichmentFingerprint != service.enrichmentFingerprint {
		service.enrichmentFingerprint = enrichmentFingerprint
		service.hasEnrichmentFingerprint = true
		service.reliability.Jellyfin = enrichmentInitialState(service.cfg.Jellyfin.URL != "")
		service.reliability.Seerr = enrichmentInitialState(service.cfg.Seerr.URL != "")
		service.reliability.Valuation = service.cfg.Jellyfin.URL == "" && service.cfg.Seerr.URL == ""
		service.reliability.Message = "Media inventory changed; enrichment must complete before automatic removal planning."
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
		service.reliability.Valuation = enrichmentReliable(service.reliability.Jellyfin) && enrichmentReliable(service.reliability.Seerr)
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
		// have been deleted. Keep that media in Connarr, but do not let its
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
			t.AssociationStatus = model.TorrentUnassociated
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
	valuation.ApplyTorrents(torrentList, service.cfg)
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
	valuation.ApplyTorrents(torrentList, service.cfg)
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
	service.reliability.Valuation = enrichmentReliable(service.reliability.Jellyfin) && enrichmentReliable(service.reliability.Seerr)
	if service.reliability.Valuation {
		service.reliability.Message = "Media valuation is reliable."
	}
	service.mu.Unlock()
	return nil
}

// RefreshJellyfin updates only playback and favorite facts. It never refreshes
// authoritative inventory, torrent state, history, or filesystem topology.
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
	generation := service.generation
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
	service.mu.Lock()
	defer service.mu.Unlock()
	if generation != service.generation {
		service.reliability.Jellyfin = "stale"
		service.reliability.Valuation = false
		service.reliability.Message = "Jellyfin enrichment no longer matches base inventory; automatic removal planning is paused."
		return fmt.Errorf("inventory changed during Jellyfin enrichment; result discarded")
	}
	items := cloneMedia(service.items)
	mergeJellyfinFacts(items, base)
	valuation.ApplyMedia(items, service.cfg)
	if service.db != nil {
		if err := service.db.PublishEnrichment("jellyfin", generation, items); err != nil {
			service.reliability.Jellyfin = "stale"
			service.reliability.Valuation = false
			return err
		}
	}
	service.items = items
	service.reliability.Jellyfin = "reliable"
	service.reliability.Valuation = enrichmentReliable(service.reliability.Seerr)
	if service.reliability.Valuation {
		service.reliability.Message = "Media valuation is reliable."
	} else {
		service.reliability.Message = "Seerr enrichment is stale; automatic removal planning is paused."
	}
	service.setStatusLocked(serviceName(service.cfg, "jellyfin", "Jellyfin"), true, true, nil)
	return nil
}

// RefreshSeerr updates only request facts and follows the same generation
// boundary as Jellyfin enrichment.
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
	generation := service.generation
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
	service.mu.Lock()
	defer service.mu.Unlock()
	if generation != service.generation {
		service.reliability.Seerr = "stale"
		service.reliability.Valuation = false
		service.reliability.Message = "Seerr enrichment no longer matches base inventory; automatic removal planning is paused."
		return fmt.Errorf("inventory changed during Seerr enrichment; result discarded")
	}
	items := cloneMedia(service.items)
	mergeSeerrFacts(items, base)
	valuation.ApplyMedia(items, service.cfg)
	if service.db != nil {
		if err := service.db.PublishEnrichment("seerr", generation, items); err != nil {
			service.reliability.Seerr = "stale"
			service.reliability.Valuation = false
			return err
		}
	}
	service.items = items
	service.reliability.Seerr = "reliable"
	service.reliability.Valuation = enrichmentReliable(service.reliability.Jellyfin)
	if service.reliability.Valuation {
		service.reliability.Message = "Media valuation is reliable."
	} else {
		service.reliability.Message = "Jellyfin enrichment is stale; automatic removal planning is paused."
	}
	service.setStatusLocked(serviceName(service.cfg, "seerr", "Seerr"), true, true, nil)
	return nil
}

func enrichmentReliable(state string) bool {
	return state == "reliable" || state == "not configured"
}

func (service *Service) ScanUnmanaged(ctx context.Context) error { return service.ReconcileFiles(ctx) }

func (service *Service) setFilesError(err error) error {
	service.mu.Lock()
	service.filesErr = err
	service.reliability.FileModel = "stale"
	service.mu.Unlock()
	return err
}

func (service *Service) FileSnapshot() ([]model.File, []model.MediaFileRef, []model.TorrentFileRef, time.Time, error) {
	service.mu.RLock()
	defer service.mu.RUnlock()
	files := append([]model.File(nil), service.files...)
	mr := append([]model.MediaFileRef(nil), service.mediaFileRefs...)
	tr := append([]model.TorrentFileRef(nil), service.torrentFileRefs...)
	return files, mr, tr, service.filesUpdated, service.filesErr
}

// ManagedFileRefs returns the files claimed by one specific media item.
// svcID disambiguates SourceID across multiple configured instances
// of the same service type (Radarr's own movie IDs, like Sonarr's series
// IDs, are unique only within one instance) — pass "" only when the caller
// has already independently proven (kind, id) can't collide, e.g. it was
// resolved from a single already-identified model.Media.
func (service *Service) ManagedFileRefs(kind model.MediaType, id int, svcID string) ([]model.MediaFileRef, time.Time, error) {
	_, refs, _, updated, err := service.FileSnapshot()
	out := []model.MediaFileRef{}
	for _, r := range refs {
		if r.MediaType != kind || r.MediaID != id {
			continue
		}
		if svcID != "" && r.ServiceID != svcID {
			continue
		}
		r.Parts = append([]model.MediaFilePart(nil), r.Parts...)
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	return out, updated, err
}

func (service *Service) TorrentFiles(hash string) ([]model.File, time.Time, error) {
	files, _, refs, updated, err := service.FileSnapshot()
	byPath := map[string]model.File{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	out := []model.File{}
	for _, r := range refs {
		if strings.EqualFold(r.Hash, hash) {
			if f, ok := byPath[r.Path]; ok {
				out = append(out, f)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	return out, updated, err
}

type TorrentFileOwner struct {
	Hash   string `json:"hash"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type FilePeer struct {
	Path     string             `json:"path"`
	Media    []model.MediaRef   `json:"media,omitempty"`
	Torrents []TorrentFileOwner `json:"torrents,omitempty"`
}

type FileView struct {
	File       model.File `json:"file"`
	SharedWith []FilePeer `json:"sharedWith,omitempty"`
}

type RemovalEstimate = filetopology.Estimate

type MediaStorageView struct {
	Files             []FileView      `json:"files"`
	RemoveMedia       RemovalEstimate `json:"removeMedia"`
	RemoveWithCurrent RemovalEstimate `json:"removeWithCurrentTorrents"`
}

func applyTorrentFileEstimates(torrents []model.Torrent, files []model.File, refs []model.TorrentFileRef) {
	x := filetopology.New(files, nil, refs)
	for i := range torrents {
		t := &torrents[i]
		t.ReclaimableKnown = false
		t.ReclaimableBytes = 0
		t.SharedBytes = 0
		t.InspectedBytes = 0
		t.InspectedFiles = 0
		t.SharedFiles = 0
		t.StorageError = ""
		paths := x.TorrentPaths(t.Hash)
		if len(paths) == 0 {
			continue
		}
		e := x.Estimate(paths)
		t.ReclaimableKnown = e.Known
		t.ReclaimableBytes = e.ReclaimableBytes
		t.SharedBytes = e.SharedBytes
		t.InspectedBytes = e.TotalBytes
		t.InspectedFiles = e.Files
		t.SharedFiles = e.SharedFiles
		if !e.Known && e.UnknownFiles > 0 {
			t.StorageError = fmt.Sprintf("file identity incomplete for %d torrent file(s); run File reconciliation", e.UnknownFiles)
		}
	}
}

func sameMediaRef(left, right model.MediaRef) bool {
	return left.Type == right.Type && left.ServiceID == right.ServiceID && left.SourceID == right.SourceID
}

func containsMediaRef(items []model.MediaRef, candidate model.MediaRef) bool {
	for _, item := range items {
		if sameMediaRef(item, candidate) {
			return true
		}
	}
	return false
}

// applyTorrentMediaHardlinks publishes relationship-specific topology facts.
// A torrent being shared somewhere is insufficient: the physical identity must
// specifically join that current torrent to that current media item.
func applyTorrentMediaHardlinks(torrents []model.Torrent, media []model.Media, files []model.File, mediaRefs []model.MediaFileRef, torrentRefs []model.TorrentFileRef) {
	index := filetopology.New(files, mediaRefs, torrentRefs)
	seasonPaths := seasonPathsByKey(mediaRefs)
	currentMediaByKey := make(map[string]model.MediaRef, len(media))
	for _, item := range media {
		currentMediaByKey[fmt.Sprintf("%s:%s:%d", item.Type, item.ServiceID, item.SourceID)] = model.MediaRef{ServiceID: item.ServiceID, Type: item.Type, SourceID: item.SourceID, Title: item.Title, Year: item.Year}
	}
	for torrentIndex := range torrents {
		torrent := &torrents[torrentIndex]
		torrent.AssociationStatus = model.NormalizeTorrentStatus(torrent.AssociationStatus)
		torrent.HardlinkKnownMediaItems = nil
		torrent.HardlinkedMediaItems = nil
		torrent.HardlinkedSeasons = nil
		torrent.MediaHardlinkKnown = false
		torrent.MediaHardlinked = false
		// Only media that physically shares an inode with one of this torrent's
		// paths can ever match; narrowing to that candidate set avoids scanning
		// the entire library for every torrent (O(torrents) instead of
		// O(torrents x media), which dominates refresh time on large libraries).
		for _, mediaItem := range candidateMediaRefs(index, torrent.Hash, currentMediaByKey) {
			matched, _ := index.TorrentMediaPhysicalMatch(torrent.Hash, mediaItem.Type, mediaItem.ServiceID, mediaItem.SourceID)
			if !matched {
				continue
			}
			if !containsMediaRef(torrent.MediaItems, mediaItem) {
				torrent.MediaItems = append(torrent.MediaItems, mediaItem)
			}
			former := torrent.FormerMediaItems[:0]
			for _, historical := range torrent.FormerMediaItems {
				if !sameMediaRef(historical, mediaItem) {
					former = append(former, historical)
				}
			}
			torrent.FormerMediaItems = former
			torrent.AssociationStatus = model.TorrentCurrent
			torrent.AssociationReason = "Current filesystem topology proves this torrent physically backs managed media."
		}
		for _, mediaItem := range torrent.MediaItems {
			hardlinked, known := index.TorrentMediaHardlink(torrent.Hash, mediaItem.Type, mediaItem.ServiceID, mediaItem.SourceID)
			if known {
				torrent.HardlinkKnownMediaItems = append(torrent.HardlinkKnownMediaItems, mediaItem)
			}
			if !hardlinked {
				continue
			}
			torrent.HardlinkedMediaItems = append(torrent.HardlinkedMediaItems, mediaItem)
			if mediaItem.Type != model.Series {
				continue
			}
			torrentPaths := index.TorrentPaths(torrent.Hash)
			for key, paths := range seasonPaths {
				if key.seriesID != mediaItem.SourceID || key.svcID != mediaItem.ServiceID {
					continue
				}
				if hl, _ := index.PathsHardlinked(paths, torrentPaths); hl {
					torrent.HardlinkedSeasons = append(torrent.HardlinkedSeasons, key.season)
				}
			}
			sort.Ints(torrent.HardlinkedSeasons)
		}
	}
}

// candidateMediaRefs returns the current media that physically share an inode
// with at least one of the torrent's paths, using the same identity data
// TorrentMediaPhysicalMatch itself relies on. Any media outside this set is
// guaranteed to not match, so this only skips calls that would return false.
func candidateMediaRefs(index *filetopology.Index, hash string, currentMediaByKey map[string]model.MediaRef) []model.MediaRef {
	seen := map[string]bool{}
	var candidates []model.MediaRef
	for _, torrentPath := range index.TorrentPaths(hash) {
		physicalPaths := append([]string{torrentPath}, index.SamePhysicalPaths(torrentPath)...)
		for _, path := range physicalPaths {
			for _, ref := range index.MediaOwners(path) {
				key := fmt.Sprintf("%s:%s:%d", ref.MediaType, ref.ServiceID, ref.MediaID)
				if seen[key] {
					continue
				}
				seen[key] = true
				if mediaItem, ok := currentMediaByKey[key]; ok {
					candidates = append(candidates, mediaItem)
				}
			}
		}
	}
	return candidates
}

func applyMediaFileEstimates(items []model.Media, files []model.File, refs []model.MediaFileRef) {
	x := filetopology.New(files, refs, nil)
	for i := range items {
		e := x.Estimate(x.MediaPaths(items[i].Type, items[i].ServiceID, items[i].SourceID))
		items[i].ReclaimableKnown = e.Known
		items[i].ReclaimableBytes = e.ReclaimableBytes
	}
}

// applyMediaBundleEstimates computes, for every media item, the bytes that
// would become reclaimable if the media and every torrent physically
// hardlinked to it (per HardlinkedMediaItems, already published by
// applyTorrentMediaHardlinks) were unlinked together. This is deliberately
// narrower than a "media plus every Current torrent" preview: a Current
// torrent that is merely a separate copy must never be folded into this
// figure, since removing it does not require also removing the media.
func applyMediaBundleEstimates(items []model.Media, torrents []model.Torrent, files []model.File, mediaRefs []model.MediaFileRef, torrentRefs []model.TorrentFileRef) {
	x := filetopology.New(files, mediaRefs, torrentRefs)
	hardlinkPathsByMediaKey := map[string][]string{}
	for _, t := range torrents {
		if model.NormalizeTorrentStatus(t.AssociationStatus) != model.TorrentCurrent {
			continue
		}
		for _, ref := range t.HardlinkedMediaItems {
			key := fmt.Sprintf("%s:%s:%d", ref.Type, ref.ServiceID, ref.SourceID)
			hardlinkPathsByMediaKey[key] = append(hardlinkPathsByMediaKey[key], x.TorrentPaths(t.Hash)...)
		}
	}
	for i := range items {
		key := fmt.Sprintf("%s:%s:%d", items[i].Type, items[i].ServiceID, items[i].SourceID)
		mediaPaths := x.MediaPaths(items[i].Type, items[i].ServiceID, items[i].SourceID)
		e := x.Estimate(filetopology.Union(mediaPaths, hardlinkPathsByMediaKey[key]))
		items[i].BundleReclaimableKnown = e.Known
		items[i].BundleReclaimableBytes = e.ReclaimableBytes
	}
}

func mediaRefMap(items []model.Media) map[string]model.MediaRef {
	out := map[string]model.MediaRef{}
	for _, m := range items {
		out[fmt.Sprintf("%s:%s:%d", m.Type, m.ServiceID, m.SourceID)] = model.MediaRef{ServiceID: m.ServiceID, Type: m.Type, SourceID: m.SourceID, Title: m.Title, Year: m.Year}
	}
	return out
}

func torrentOwnerMap(items []model.Torrent) map[string]TorrentFileOwner {
	out := map[string]TorrentFileOwner{}
	for _, t := range items {
		out[strings.ToLower(t.Hash)] = TorrentFileOwner{Hash: t.Hash, Name: t.Name, Status: t.AssociationStatus}
	}
	return out
}

func buildFileViews(paths []string, x *filetopology.Index, mediaRefs map[string]model.MediaRef, torrentOwners map[string]TorrentFileOwner) []FileView {
	views := make([]FileView, 0, len(paths))
	for _, path := range paths {
		f, ok := x.File(path)
		if !ok {
			continue
		}
		v := FileView{File: f}
		for _, peerPath := range x.SamePhysicalPaths(path) {
			peer := FilePeer{Path: peerPath}
			seenMedia := map[string]bool{}
			for _, r := range x.MediaOwners(peerPath) {
				key := fmt.Sprintf("%s:%s:%d", r.MediaType, r.ServiceID, r.MediaID)
				if ref, ok := mediaRefs[key]; ok && !seenMedia[key] {
					peer.Media = append(peer.Media, ref)
					seenMedia[key] = true
				}
			}
			seenTorrent := map[string]bool{}
			for _, r := range x.TorrentOwners(peerPath) {
				h := strings.ToLower(r.Hash)
				if owner, ok := torrentOwners[h]; ok && !seenTorrent[h] {
					peer.Torrents = append(peer.Torrents, owner)
					seenTorrent[h] = true
				}
			}
			v.SharedWith = append(v.SharedWith, peer)
		}
		views = append(views, v)
	}
	sort.Slice(views, func(i, j int) bool { return strings.ToLower(views[i].File.Path) < strings.ToLower(views[j].File.Path) })
	return views
}

func (service *Service) MediaStorage(kind model.MediaType, svcID string, id int) (MediaStorageView, time.Time, error) {
	files, mr, tr, updated, err := service.FileSnapshot()
	items, _, _ := service.Snapshot()
	torrents := service.TorrentSnapshot()
	x := filetopology.New(files, mr, tr)
	paths := x.MediaPaths(kind, svcID, id)
	view := MediaStorageView{Files: buildFileViews(paths, x, mediaRefMap(items), torrentOwnerMap(torrents))}
	view.RemoveMedia = x.Estimate(paths)
	var currentPaths []string
	for _, t := range torrents {
		if model.NormalizeTorrentStatus(t.AssociationStatus) != model.TorrentCurrent {
			continue
		}
		matched := false
		for _, ref := range t.MediaItems {
			if ref.Type == kind && ref.ServiceID == svcID && ref.SourceID == id {
				matched = true
				break
			}
		}
		if matched {
			currentPaths = append(currentPaths, x.TorrentPaths(t.Hash)...)
		}
	}
	view.RemoveWithCurrent = x.Estimate(filetopology.Union(paths, currentPaths))
	return view, updated, err
}

type TorrentStorageView struct {
	Files         []FileView      `json:"files"`
	RemoveTorrent RemovalEstimate `json:"removeTorrent"`
}

func (service *Service) TorrentStorage(hash string) (TorrentStorageView, time.Time, error) {
	files, mr, tr, updated, err := service.FileSnapshot()
	items, _, _ := service.Snapshot()
	torrents := service.TorrentSnapshot()
	x := filetopology.New(files, mr, tr)
	paths := x.TorrentPaths(hash)
	return TorrentStorageView{Files: buildFileViews(paths, x, mediaRefMap(items), torrentOwnerMap(torrents)), RemoveTorrent: x.Estimate(paths)}, updated, err
}

func projectTorrentRelations(media []model.Media, torrents []model.Torrent) {
	mediaIndex := map[string]int{}
	for i := range media {
		media[i].Torrents = nil
		mediaIndex[fmt.Sprintf("%s:%s:%d", media[i].Type, media[i].ServiceID, media[i].SourceID)] = i
	}
	seen := map[string]map[string]bool{}
	attach := func(ref model.MediaRef, t model.Torrent, current bool) {
		key := fmt.Sprintf("%s:%s:%d", ref.Type, ref.ServiceID, ref.SourceID)
		i, ok := mediaIndex[key]
		if !ok {
			return
		}
		if seen[key] == nil {
			seen[key] = map[string]bool{}
		}
		h := strings.ToLower(t.Hash)
		if seen[key][h] {
			return
		}
		t.MediaHardlinkKnown = current && containsMediaRef(t.HardlinkKnownMediaItems, ref)
		t.MediaHardlinked = current && containsMediaRef(t.HardlinkedMediaItems, ref)
		media[i].Torrents = append(media[i].Torrents, t)
		seen[key][h] = true
	}
	for _, t := range torrents {
		for _, ref := range t.MediaItems {
			attach(ref, t, true)
		}
		for _, ref := range t.FormerMediaItems {
			attach(ref, t, false)
		}
	}
}

func mediaHasCurrentFiles(m model.Media) bool {
	return m.SizeBytes > 0
}

func preserveJellyfinFacts(dst, previous []model.Media) {
	byKey := make(map[string]model.Media, len(previous))
	for _, m := range previous {
		byKey[fmt.Sprintf("%s:%s:%d", m.Type, m.ServiceID, m.SourceID)] = m
	}
	for i := range dst {
		if old, ok := byKey[fmt.Sprintf("%s:%s:%d", dst[i].Type, dst[i].ServiceID, dst[i].SourceID)]; ok {
			dst[i].Views = old.Views
			dst[i].UniqueViewers = old.UniqueViewers
			dst[i].LastWatched = old.LastWatched
			dst[i].Favorite = old.Favorite
		}
	}
}

func preserveSeerrFacts(dst, previous []model.Media) {
	byKey := make(map[string]model.Media, len(previous))
	for _, m := range previous {
		byKey[fmt.Sprintf("%s:%s:%d", m.Type, m.ServiceID, m.SourceID)] = m
	}
	for i := range dst {
		if old, ok := byKey[fmt.Sprintf("%s:%s:%d", dst[i].Type, dst[i].ServiceID, dst[i].SourceID)]; ok {
			dst[i].Requested = old.Requested
			dst[i].RequestedAt = old.RequestedAt
		}
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
		}
	}
}

func clearJellyfinFacts(items []model.Media) {
	for i := range items {
		items[i].Views = 0
		items[i].UniqueViewers = 0
		items[i].LastWatched = nil
		items[i].Favorite = false
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
		}
	}
}

func clearSeerrFacts(items []model.Media) {
	for i := range items {
		items[i].Requested = false
		items[i].RequestedAt = nil
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
		out[i].SwarmValueReasons = append([]model.Reason(nil), in[i].SwarmValueReasons...)
		out[i].MediaItems = append([]model.MediaRef(nil), in[i].MediaItems...)
		out[i].FormerMediaItems = append([]model.MediaRef(nil), in[i].FormerMediaItems...)
		out[i].HardlinkKnownMediaItems = append([]model.MediaRef(nil), in[i].HardlinkKnownMediaItems...)
		out[i].HardlinkedMediaItems = append([]model.MediaRef(nil), in[i].HardlinkedMediaItems...)
		out[i].HardlinkedSeasons = append([]int(nil), in[i].HardlinkedSeasons...)
	}
	return out
}
func (service *Service) fail(e error) error {
	service.mu.Lock()
	service.lastErr = e
	service.mu.Unlock()
	return e
}
func (service *Service) persistenceFail(e error) error {
	service.mu.Lock()
	service.lastErr = e
	service.reliability.Inventory = false
	service.reliability.Valuation = false
	service.reliability.Message = "The current generation was not durably saved; automatic removal planning is paused."
	service.mu.Unlock()
	return e
}
func (service *Service) Snapshot() ([]model.Media, time.Time, error) {
	service.mu.RLock()
	defer service.mu.RUnlock()
	return cloneMedia(service.items), service.updated, service.lastErr
}
func (service *Service) TorrentSnapshot() []model.Torrent {
	service.mu.RLock()
	defer service.mu.RUnlock()
	return cloneTorrents(service.torrents)
}

// TorrentDetail enriches the locally indexed relationship/storage facts with
// live client-owned details. Nothing fetched here is persisted. svcID
// disambiguates hash across multiple configured qBittorrent instances (the
// same infohash can legitimately exist in more than one, e.g. cross-seeding
// or a seedbox mirrored to a local client); when empty, a match is only
// accepted if it is unique.
func (service *Service) TorrentDetail(hash, svcID string) (model.Torrent, error) {
	var indexed model.Torrent
	found := false
	for _, t := range service.TorrentSnapshot() {
		if !strings.EqualFold(t.Hash, hash) {
			continue
		}
		if svcID != "" {
			if t.ServiceID == svcID {
				indexed, found = t, true
				break
			}
			continue
		}
		if found {
			return model.Torrent{}, fmt.Errorf("torrent hash %q exists in more than one configured instance; an service_id is required", hash)
		}
		indexed, found = t, true
	}
	if !found {
		return model.Torrent{}, fmt.Errorf("torrent not found")
	}
	service.mu.RLock()
	client, ok := service.qb[indexed.ServiceID]
	service.mu.RUnlock()
	if !ok {
		return indexed, fmt.Errorf("qbittorrent client for service %q not found", indexed.ServiceID)
	}
	live, err := client.Detail(hash)
	if err != nil {
		return indexed, err
	}
	// Preserve Connarr-owned interpretations and expensive reconciliation facts.
	live.ServiceID = indexed.ServiceID
	live.SwarmValue = indexed.SwarmValue
	live.SwarmValueReasons = indexed.SwarmValueReasons
	live.AssociationStatus = indexed.AssociationStatus
	live.AssociationReason = indexed.AssociationReason
	live.MediaItems = indexed.MediaItems
	live.FormerMediaItems = indexed.FormerMediaItems
	live.HardlinkKnownMediaItems = indexed.HardlinkKnownMediaItems
	live.HardlinkedMediaItems = indexed.HardlinkedMediaItems
	live.SupersededByHash = indexed.SupersededByHash
	live.ReclaimableKnown = indexed.ReclaimableKnown
	live.ReclaimableBytes = indexed.ReclaimableBytes
	live.SharedBytes = indexed.SharedBytes
	live.InspectedBytes = indexed.InspectedBytes
	live.InspectedFiles = indexed.InspectedFiles
	live.SharedFiles = indexed.SharedFiles
	live.StorageError = indexed.StorageError
	return live, nil
}

func (service *Service) IsRefreshing() bool {
	service.mu.RLock()
	defer service.mu.RUnlock()
	return service.refreshing
}

func (service *Service) publishChange() {
	service.mu.Lock()
	defer service.mu.Unlock()
	close(service.changed)
	service.changed = make(chan struct{})
}

// Changes returns a one-shot notification channel for the next published
// inventory, enrichment, topology, status, or reliability change.
func (service *Service) Changes() <-chan struct{} {
	service.mu.RLock()
	defer service.mu.RUnlock()
	return service.changed
}
func (service *Service) Config() config.Config {
	service.mu.RLock()
	defer service.mu.RUnlock()
	return service.cfg
}

func (service *Service) UnmanagedSnapshot() ([]model.UnmanagedFile, time.Time, error) {
	service.mu.RLock()
	defer service.mu.RUnlock()
	out := make([]model.UnmanagedFile, len(service.unmanaged))
	copy(out, service.unmanaged)
	return out, service.unmanagedUpdated, service.unmanagedErr
}

// RemoveManagedFile delegates removal of one managed file to the specific
// application instance that owns that file (ref.ServiceID). The Media
// object itself remains in the owner application.
func (service *Service) RemoveManagedFile(ctx context.Context, ref model.MediaFileRef) error {
	switch strings.ToLower(ref.Source) {
	case "radarr":
		if ref.SourceFileID <= 0 {
			return fmt.Errorf("radarr managed file id is missing")
		}
		client, err := service.radarrClient(ref.ServiceID)
		if err != nil {
			return err
		}
		return client.WithContext(ctx).DeleteFile(ref.SourceFileID)
	case "sonarr":
		if ref.SourceFileID <= 0 {
			return fmt.Errorf("sonarr managed file id is missing")
		}
		client, err := service.sonarrClient(ref.ServiceID)
		if err != nil {
			return err
		}
		return client.WithContext(ctx).DeleteFile(ref.SourceFileID)
	default:
		return fmt.Errorf("unsupported managed-file owner %q", ref.Source)
	}
}

// RemoveTorrent delegates torrent and torrent-owned data removal to the
// specific qBittorrent instance identified by svcID — the same
// infohash can legitimately exist in more than one configured instance.
func (service *Service) RemoveTorrent(ctx context.Context, hash, svcID string) error {
	client, err := service.qbittorrentClient(svcID)
	if err != nil {
		return err
	}
	return client.WithContext(ctx).Delete(hash)
}

func (service *Service) SetMovieMonitored(ctx context.Context, svcID string, id int, monitored bool) error {
	client, err := service.radarrClient(svcID)
	if err != nil {
		return err
	}
	return client.WithContext(ctx).SetMonitored(id, monitored)
}

func (service *Service) SetEpisodesMonitored(ctx context.Context, svcID string, ids []int, monitored bool) error {
	client, err := service.sonarrClient(svcID)
	if err != nil {
		return err
	}
	return client.WithContext(ctx).SetEpisodesMonitored(ids, monitored)
}

func (service *Service) AddMovieImportListExclusion(ctx context.Context, mediaItem model.Media) error {
	if mediaItem.Type != model.Movie {
		return fmt.Errorf("import-list exclusion target is not a movie")
	}
	client, err := service.radarrClient(mediaItem.ServiceID)
	if err != nil {
		return err
	}
	return client.WithContext(ctx).AddImportListExclusion(mediaItem.Title, mediaItem.Year, mediaItem.TMDBID)
}

func (service *Service) AddSeriesImportListExclusion(ctx context.Context, mediaItem model.Media) error {
	if mediaItem.Type != model.Series {
		return fmt.Errorf("import-list exclusion target is not a series")
	}
	client, err := service.sonarrClient(mediaItem.ServiceID)
	if err != nil {
		return err
	}
	return client.WithContext(ctx).AddImportListExclusion(mediaItem.Title, mediaItem.TVDBID)
}

func (service *Service) radarrClient(svcID string) (*radarr.Client, error) {
	service.mu.RLock()
	defer service.mu.RUnlock()
	client, ok := service.rad[svcID]
	if !ok {
		return nil, fmt.Errorf("no radarr client for service %q", svcID)
	}
	return client, nil
}

func (service *Service) sonarrClient(svcID string) (*sonarr.Client, error) {
	service.mu.RLock()
	defer service.mu.RUnlock()
	client, ok := service.son[svcID]
	if !ok {
		return nil, fmt.Errorf("no sonarr client for service %q", svcID)
	}
	return client, nil
}

func (service *Service) qbittorrentClient(svcID string) (*qbittorrent.Client, error) {
	service.mu.RLock()
	defer service.mu.RUnlock()
	client, ok := service.qb[svcID]
	if !ok {
		return nil, fmt.Errorf("no qbittorrent client for service %q", svcID)
	}
	return client, nil
}

// VerifyUnmanaged fails closed before direct filesystem removal. It refreshes
// qBittorrent's authoritative file ownership and also rejects paths currently
// indexed as managed Media files. This is intentionally heavier than routine
// browsing because direct OS removal must never rely on stale absence alone.
func (service *Service) VerifyUnmanaged(paths []string) error {
	return service.VerifyUnmanagedContext(context.Background(), paths)
}

func (service *Service) VerifyUnmanagedContext(ctx context.Context, paths []string) error {
	wanted := map[string]bool{}
	for _, p := range paths {
		wanted[filepath.Clean(p)] = true
	}
	service.mu.RLock()
	radInstances := service.cfg.ServicesOfType("radarr")
	sonInstances := service.cfg.ServicesOfType("sonarr")
	qbInstances := service.cfg.ServicesOfType("qbittorrent")
	radClients := service.rad
	sonClients := service.son
	qbClients := service.qb
	service.mu.RUnlock()

	var wg sync.WaitGroup
	qbErrs := make([]error, len(qbInstances))
	radarrMediaByInstance := make([][]model.Media, len(radInstances))
	sonarrMediaByInstance := make([][]model.Media, len(sonInstances))
	radarrErrs := make([]error, len(radInstances))
	sonarrErrs := make([]error, len(sonInstances))
	wg.Add(len(qbInstances) + len(radInstances) + len(sonInstances))
	for i, svc := range qbInstances {
		go func(i int, svc config.Service) {
			defer wg.Done()
			qbErrs[i] = qbClients[svc.ID].WithContext(ctx).VerifyPathsUnmanaged(paths)
		}(i, svc)
	}
	for i, svc := range radInstances {
		go func(i int, svc config.Service) {
			defer wg.Done()
			media, err := radClients[svc.ID].WithContext(ctx).Inventory()
			for mediaIndex := range media {
				media[mediaIndex].ServiceID = svc.ID
			}
			radarrMediaByInstance[i] = media
			radarrErrs[i] = err
		}(i, svc)
	}
	for i, svc := range sonInstances {
		go func(i int, svc config.Service) {
			defer wg.Done()
			media, err := sonClients[svc.ID].WithContext(ctx).Inventory()
			for mediaIndex := range media {
				media[mediaIndex].ServiceID = svc.ID
			}
			sonarrMediaByInstance[i] = media
			sonarrErrs[i] = err
		}(i, svc)
	}
	wg.Wait()
	for i, err := range qbErrs {
		if err != nil {
			return fmt.Errorf("cannot verify current qBittorrent (%s) ownership: %w", qbInstances[i].Name, err)
		}
	}
	for i, err := range radarrErrs {
		if err != nil {
			return fmt.Errorf("cannot verify current Radarr (%s) ownership: %w", radInstances[i].Name, err)
		}
	}
	for i, err := range sonarrErrs {
		if err != nil {
			return fmt.Errorf("cannot verify current Sonarr (%s) ownership: %w", sonInstances[i].Name, err)
		}
	}
	var radarrMedia, sonarrMedia []model.Media
	for _, media := range radarrMediaByInstance {
		radarrMedia = append(radarrMedia, media...)
	}
	for _, media := range sonarrMediaByInstance {
		sonarrMedia = append(sonarrMedia, media...)
	}

	movieIDsByInstance := map[string][]int{}
	seriesIDsByInstance := map[string][]int{}
	mediaRoots := map[ownerKey]string{}
	for _, mediaItem := range radarrMedia {
		possible := false
		for path := range wanted {
			if under(mediaItem.Path, path) {
				possible = true
				break
			}
		}
		if possible {
			movieIDsByInstance[mediaItem.ServiceID] = append(movieIDsByInstance[mediaItem.ServiceID], mediaItem.SourceID)
			mediaRoots[ownerKey{ServiceID: mediaItem.ServiceID, OwnerID: mediaItem.SourceID}] = mediaItem.Path
		}
	}
	for _, mediaItem := range sonarrMedia {
		possible := false
		for path := range wanted {
			if under(mediaItem.Path, path) {
				possible = true
				break
			}
		}
		if possible {
			seriesIDsByInstance[mediaItem.ServiceID] = append(seriesIDsByInstance[mediaItem.ServiceID], mediaItem.SourceID)
			mediaRoots[ownerKey{ServiceID: mediaItem.ServiceID, OwnerID: mediaItem.SourceID}] = mediaItem.Path
		}
	}

	type radarrFilesFetch struct {
		svcID string
		files []radarr.FileRecord
		err   error
	}
	type sonarrFilesFetch struct {
		svcID string
		files []sonarr.FileRecord
		err   error
	}
	radFilesFetches := make([]radarrFilesFetch, 0, len(movieIDsByInstance))
	for id := range movieIDsByInstance {
		radFilesFetches = append(radFilesFetches, radarrFilesFetch{svcID: id})
	}
	sonFilesFetches := make([]sonarrFilesFetch, 0, len(seriesIDsByInstance))
	for id := range seriesIDsByInstance {
		sonFilesFetches = append(sonFilesFetches, sonarrFilesFetch{svcID: id})
	}
	wg = sync.WaitGroup{}
	wg.Add(len(radFilesFetches) + len(sonFilesFetches))
	for i := range radFilesFetches {
		go func(i int) {
			defer wg.Done()
			id := radFilesFetches[i].svcID
			radFilesFetches[i].files, radFilesFetches[i].err = radClients[id].WithContext(ctx).Files(movieIDsByInstance[id])
		}(i)
	}
	for i := range sonFilesFetches {
		go func(i int) {
			defer wg.Done()
			id := sonFilesFetches[i].svcID
			sonFilesFetches[i].files, sonFilesFetches[i].err = sonClients[id].WithContext(ctx).Files(seriesIDsByInstance[id])
		}(i)
	}
	wg.Wait()
	for _, f := range radFilesFetches {
		if f.err != nil {
			return fmt.Errorf("cannot verify current Radarr file claims: %w", f.err)
		}
	}
	for _, f := range sonFilesFetches {
		if f.err != nil {
			return fmt.Errorf("cannot verify current Sonarr file claims: %w", f.err)
		}
	}
	for _, f := range radFilesFetches {
		for _, file := range f.files {
			path := filepath.Clean(filepath.Join(mediaRoots[ownerKey{ServiceID: f.svcID, OwnerID: file.MovieID}], file.Relative))
			if wanted[path] {
				return fmt.Errorf("file is now claimed by Radarr: %s", path)
			}
		}
	}
	for _, f := range sonFilesFetches {
		for _, file := range f.files {
			path := filepath.Clean(filepath.Join(mediaRoots[ownerKey{ServiceID: f.svcID, OwnerID: file.SeriesID}], file.Relative))
			if wanted[path] {
				return fmt.Errorf("file is now claimed by Sonarr: %s", path)
			}
		}
	}
	return nil
}
