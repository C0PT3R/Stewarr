package inventory

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"stewarr/internal/config"
	"stewarr/internal/model"
	"stewarr/internal/services/imdb"
	"stewarr/internal/services/jellyfin"
	"stewarr/internal/services/qbittorrent"
	"stewarr/internal/services/radarr"
	"stewarr/internal/services/seerr"
	"stewarr/internal/services/sonarr"
	"stewarr/internal/services/tmdb"
	"stewarr/internal/store"
	"stewarr/internal/valuation"
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
	TMDB      string `json:"tmdb"`
	Valuation bool   `json:"valuation"`
	FileModel string `json:"fileModel"`
	Message   string `json:"message"`
}

type Service struct {
	cfg      config.Config
	mu       sync.RWMutex
	items    []model.Media
	torrents []model.Torrent
	// imdbRatings is IMDb's own dataset (see internal/services/imdb),
	// loaded at startup and replaced wholesale after each successful daily
	// refresh — never mutated in place, so callers that copy the map
	// reference under a read lock don't need to hold the lock while using
	// it afterward.
	imdbRatings      map[string]imdb.Rating
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
	fileTopologyVersion uint64
	baseFingerprint     [32]byte
	hasBaseFingerprint  bool
	statuses            map[string]ServiceStatus
	reconciliationMu    sync.Mutex
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
	rad        map[string]*radarr.Client
	son        map[string]*sonarr.Client
	jf         *jellyfin.Client
	seerr      *seerr.Client
	tmdb       *tmdb.Client
	imdbClient *imdb.Client
	qb         map[string]*qbittorrent.Client
	db         *store.Store
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
	service := &Service{cfg: configuration, db: database, statuses: map[string]ServiceStatus{}, stageTimings: map[string]time.Duration{}, baseReady: make(chan struct{}), changed: make(chan struct{}), rad: buildRadarrClients(configuration), son: buildSonarrClients(configuration), jf: jellyfin.New(configuration.Jellyfin.URL, configuration.Jellyfin.APIKey), seerr: seerr.New(configuration.Seerr.URL, configuration.Seerr.APIKey), tmdb: tmdb.New(configuration.TMDB.APIKey), imdbClient: imdb.New(), qb: buildQBittorrentClients(configuration)}
	loadStarted := time.Now()
	if database != nil {
		if items, updated, err := database.LoadMedia(); err == nil {
			service.items, service.updated = items, updated
			if !updated.IsZero() {
				service.reliability.Inventory = true
				// Trust facts a database load just restored, rather than
				// enrichmentInitialState's "stale" — that's correct for a
				// genuine cold start (no cache) or a fingerprint-changed
				// reset (see Refresh), but here the persisted values were
				// already reliable the moment they were saved. Marking them
				// stale regardless would pause automatic removal planning
				// for up to that source's own refresh interval after every
				// single restart — TMDB's is 24h, so this was highly
				// visible there even though nothing about the data actually
				// changed. The genuinely precise per-item staleness check
				// (mediaTMDBDataStale, internal/httpui/auto_removal.go)
				// still independently excludes any item whose own
				// TMDBEnrichedAt really is too old, regardless of this
				// coarser reliability flag.
				service.reliability.Jellyfin = enrichmentInitialStateFromCache(configuration.Jellyfin.URL != "")
				service.reliability.Seerr = enrichmentInitialStateFromCache(configuration.Seerr.URL != "")
				service.reliability.TMDB = enrichmentInitialStateFromCache(configuration.TMDB.APIKey != "")
				service.reliability.Valuation = enrichmentReliable(service.reliability.Jellyfin) && enrichmentReliable(service.reliability.Seerr) && enrichmentReliable(service.reliability.TMDB)
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
		if imdbRatings, _, err := database.LoadIMDbRatings(); err == nil {
			service.imdbRatings = imdbRatings
		} else {
			log.Printf("[inventory] load cached IMDb ratings: %v", err)
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
			valuation.ApplyMedia(service.items, service.cfg, service.imdbRatings)
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
	if service.reliability.TMDB == "" {
		service.reliability.TMDB = enrichmentInitialState(configuration.TMDB.APIKey != "")
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
		valuation.ApplyMedia(service.items, service.cfg, service.imdbRatings)
		service.generation = 1
		service.baseFingerprint = inventoryFingerprint(service.items, service.torrents)
		service.hasBaseFingerprint = true
	}
	return service
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

// enrichmentInitialStateFromCache is enrichmentInitialState's counterpart
// for the one case where "stale" is wrong: a process restart that just
// successfully loaded persisted media from the database. See its call site
// in New() for why.
func enrichmentInitialStateFromCache(configured bool) string {
	if configured {
		return "reliable"
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
	if service.reliability.TMDB == "stale" {
		parts = append(parts, "TMDB enrichment stale")
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

// ValidateTMDB has no service-page status to report to (TMDB is not a
// config.Service — see Config.TMDB), so unlike ValidateJellyfin/
// ValidateSeerr it doesn't call setStatus; its result is only used as a
// task Preflight check gating RefreshTMDB.
func (service *Service) ValidateTMDB(ctx context.Context) error {
	if service.cfg.TMDB.APIKey == "" {
		return nil
	}
	return service.tmdb.WithContext(ctx).Validate()
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

// imdbRatingsSnapshot returns the current IMDb ratings map reference for a
// caller that isn't already holding service.mu — safe without cloning
// since imdbRatings is only ever replaced wholesale (see the field's own
// comment), never mutated in place.
func (service *Service) imdbRatingsSnapshot() map[string]imdb.Rating {
	service.mu.RLock()
	defer service.mu.RUnlock()
	return service.imdbRatings
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
	// Preserve Stewarr-owned interpretations and expensive reconciliation facts.
	live.ServiceID = indexed.ServiceID
	live.TorrentValue = indexed.TorrentValue
	live.TorrentValueReasons = indexed.TorrentValueReasons
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
//
// An incomplete torrent still linked to the media item it was grabbed for
// is removed through that owning Radarr/Sonarr instance's queue instead,
// when a matching queue entry still exists: one call there both drops the
// stale queue entry and tells qBittorrent to delete the download, instead
// of leaving Radarr/Sonarr still waiting on a download Stewarr just deleted
// out from under it. Falls back to removing directly from qBittorrent when
// there's no matching queue entry — already imported, already dropped from
// the queue, or no current owning media item at all.
func (service *Service) RemoveTorrent(ctx context.Context, hash, svcID string) error {
	removedViaQueue, err := service.removeIncompleteTorrentViaQueue(ctx, hash, svcID)
	if err != nil {
		return err
	}
	if removedViaQueue {
		return nil
	}
	client, err := service.qbittorrentClient(svcID)
	if err != nil {
		return err
	}
	return client.WithContext(ctx).Delete(hash)
}

// removeIncompleteTorrentViaQueue attempts the Radarr/Sonarr queue removal
// path described on RemoveTorrent. The bool return reports whether it
// actually removed the torrent; a nil error with false means there was no
// applicable queue entry to use, so the caller should fall back to
// qBittorrent directly rather than treat this as a failure.
func (service *Service) removeIncompleteTorrentViaQueue(ctx context.Context, hash, svcID string) (bool, error) {
	var indexed model.Torrent
	found := false
	for _, t := range service.TorrentSnapshot() {
		if strings.EqualFold(t.Hash, hash) && t.ServiceID == svcID {
			indexed, found = t, true
			break
		}
	}
	if !found || indexed.AmountLeftBytes <= 0 || len(indexed.MediaItems) == 0 {
		return false, nil
	}
	owner := indexed.MediaItems[0]
	var id int
	var ok bool
	var err error
	switch owner.Type {
	case model.Movie:
		client, clientErr := service.radarrClient(owner.ServiceID)
		if clientErr != nil {
			return false, nil
		}
		id, ok, err = client.WithContext(ctx).FindQueueItem(hash)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		return true, client.WithContext(ctx).RemoveQueueItem(id)
	case model.Series:
		client, clientErr := service.sonarrClient(owner.ServiceID)
		if clientErr != nil {
			return false, nil
		}
		id, ok, err = client.WithContext(ctx).FindQueueItem(hash)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		return true, client.WithContext(ctx).RemoveQueueItem(id)
	default:
		return false, nil
	}
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
