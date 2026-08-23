package inventory

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"togetharr/internal/config"
	"togetharr/internal/filetopology"
	"togetharr/internal/jellyfin"
	"togetharr/internal/model"
	"togetharr/internal/qbittorrent"
	"togetharr/internal/radarr"
	"togetharr/internal/seerr"
	"togetharr/internal/sonarr"
	"togetharr/internal/store"
	"togetharr/internal/valuation"
)

type ServiceStatus struct {
	Name       string
	Configured bool
	OK         bool
	Message    string
	CheckedAt  time.Time
}

type Service struct {
	cfg              config.Config
	mu               sync.RWMutex
	items            []model.Media
	torrents         []model.Torrent
	unclaimed        []model.UnclaimedFile
	unclaimedUpdated time.Time
	unclaimedErr     error
	files            []model.File
	mediaFileRefs    []model.MediaFileRef
	torrentFileRefs  []model.TorrentFileRef
	filesUpdated     time.Time
	filesErr         error
	updated          time.Time
	lastErr          error
	refreshing       bool
	statuses         map[string]ServiceStatus
	rad              *radarr.Client
	son              *sonarr.Client
	jf               *jellyfin.Client
	seerr            *seerr.Client
	qb               *qbittorrent.Client
	db               *store.Store
}

func New(c config.Config, db *store.Store) *Service {
	s := &Service{cfg: c, db: db, statuses: map[string]ServiceStatus{}, rad: radarr.New(c.Radarr.URL, c.Radarr.APIKey), son: sonarr.New(c.Sonarr.URL, c.Sonarr.APIKey), jf: jellyfin.New(c.Jellyfin.URL, c.Jellyfin.APIKey), seerr: seerr.New(c.Seerr.URL, c.Seerr.APIKey), qb: qbittorrent.New(c.QBittorrent.Name, c.QBittorrent.URL, c.QBittorrent.Username, c.QBittorrent.Password, c.QBittorrent.APIKey)}
	if db != nil {
		if items, updated, err := db.LoadMedia(); err == nil {
			s.items, s.updated = items, updated
		}
		if torrents, err := db.LoadTorrents(); err == nil {
			s.torrents = torrents
		}
		if files, updated, err := db.LoadUnclaimedFiles(); err == nil {
			s.unclaimed, s.unclaimedUpdated = files, updated
		}
		if files, mediaRefs, torrentRefs, updated, err := db.LoadFiles(); err == nil {
			nameByID := map[string]string{}
			for _, i := range c.Integrations {
				nameByID[i.ID] = i.Name
			}
			for fi := range files {
				for ci := range files[fi].StorageContexts {
					if n := nameByID[files[fi].StorageContexts[ci].IntegrationID]; n != "" {
						files[fi].StorageContexts[ci].IntegrationName = n
					}
				}
			}
			for ri := range mediaRefs {
				if mediaRefs[ri].IntegrationName == "" {
					mediaRefs[ri].IntegrationName = integrationName(c, mediaRefs[ri].Source, strings.Title(mediaRefs[ri].Source))
				}
				if mediaRefs[ri].IntegrationID == "" {
					mediaRefs[ri].IntegrationID = integrationID(c, mediaRefs[ri].Source)
				}
			}
			for ri := range torrentRefs {
				if torrentRefs[ri].IntegrationName == "" {
					torrentRefs[ri].IntegrationName = integrationName(c, "qbittorrent", torrentRefs[ri].Client)
				}
				if torrentRefs[ri].IntegrationID == "" {
					torrentRefs[ri].IntegrationID = integrationID(c, "qbittorrent")
				}
			}
			s.files, s.mediaFileRefs, s.torrentFileRefs, s.filesUpdated = files, mediaRefs, torrentRefs, updated
			applyTorrentFileEstimates(s.torrents, files, torrentRefs)
			projectTorrentRelations(s.items, s.torrents)
		}
	}
	return s
}

func (s *Service) setStatus(name string, configured, ok bool, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	s.mu.Lock()
	s.statuses[name] = ServiceStatus{Name: name, Configured: configured, OK: ok, Message: msg, CheckedAt: time.Now()}
	s.mu.Unlock()
}

func (s *Service) StatusSnapshot() []ServiceStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ServiceStatus, 0, len(s.cfg.Integrations))
	for _, i := range s.cfg.Integrations {
		if st, ok := s.statuses[i.Name]; ok {
			out = append(out, st)
			continue
		}
		out = append(out, ServiceStatus{Name: i.Name, Configured: i.Enabled(), OK: false, Message: "Not checked yet"})
	}
	return out
}

func (s *Service) Store() *store.Store { return s.db }

func parseCursor(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, v)
	return t
}

type torrentProvenance struct {
	Source       string
	OwnerID      int
	SubID        int
	ImportedAt   time.Time
	SupersededBy string
}

func addProvenance(m map[string][]torrentProvenance, hash string, p torrentProvenance) {
	h := strings.ToLower(strings.TrimSpace(hash))
	if h == "" {
		return
	}
	m[h] = append(m[h], p)
}

func (s *Service) syncImportHistory() (map[int][]string, map[int][]string, map[string][]torrentProvenance, error) {
	if s.db == nil {
		return nil, nil, nil, fmt.Errorf("database is unavailable")
	}

	rc, _ := s.db.Meta("radarr.history.cursor")
	sc, _ := s.db.Meta("sonarr.history.cursor")
	var re []radarr.ImportEvent
	var se []sonarr.ImportEvent
	var rn, sn time.Time
	var rerr, serr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); re, rn, rerr = s.rad.ImportEventsSince(parseCursor(rc)) }()
	go func() { defer wg.Done(); se, sn, serr = s.son.ImportEventsSince(parseCursor(sc)) }()
	wg.Wait()
	if rerr != nil {
		return nil, nil, nil, fmt.Errorf("radarr history: %w", rerr)
	}
	if serr != nil {
		return nil, nil, nil, fmt.Errorf("sonarr history: %w", serr)
	}

	batch := make([]store.ImportEvent, 0, len(re)+len(se))
	for _, x := range re {
		batch = append(batch, store.ImportEvent{Source: "radarr", OwnerID: x.MovieID, DownloadID: x.DownloadID, ImportedAt: x.Date})
	}
	for _, x := range se {
		batch = append(batch, store.ImportEvent{Source: "sonarr", OwnerID: x.SeriesID, SubID: x.EpisodeID, DownloadID: x.DownloadID, ImportedAt: x.Date})
	}
	if err := s.db.AddImportEvents(batch); err != nil {
		return nil, nil, nil, err
	}
	if !rn.IsZero() {
		_ = s.db.SetMeta("radarr.history.cursor", rn.UTC().Format(time.RFC3339Nano))
	}
	if !sn.IsZero() {
		_ = s.db.SetMeta("sonarr.history.cursor", sn.UTC().Format(time.RFC3339Nano))
	}

	rall, err := s.db.ImportEvents("radarr")
	if err != nil {
		return nil, nil, nil, err
	}
	sall, err := s.db.ImportEvents("sonarr")
	if err != nil {
		return nil, nil, nil, err
	}

	provenance := map[string][]torrentProvenance{}
	rids := map[int][]string{}
	latestMovieHash := map[int]string{}
	for _, x := range rall { // newest first
		h := strings.ToLower(x.DownloadID)
		latest := latestMovieHash[x.OwnerID]
		if latest == "" {
			latestMovieHash[x.OwnerID] = h
			rids[x.OwnerID] = []string{h}
			addProvenance(provenance, h, torrentProvenance{Source: "radarr", OwnerID: x.OwnerID, ImportedAt: x.ImportedAt})
			continue
		}
		if h == latest {
			addProvenance(provenance, h, torrentProvenance{Source: "radarr", OwnerID: x.OwnerID, ImportedAt: x.ImportedAt})
			continue
		}
		addProvenance(provenance, h, torrentProvenance{Source: "radarr", OwnerID: x.OwnerID, ImportedAt: x.ImportedAt, SupersededBy: latest})
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
			addProvenance(provenance, h, torrentProvenance{Source: "sonarr", OwnerID: x.OwnerID, SubID: x.SubID, ImportedAt: x.ImportedAt})
			continue
		}
		if h == latest {
			addProvenance(provenance, h, torrentProvenance{Source: "sonarr", OwnerID: x.OwnerID, SubID: x.SubID, ImportedAt: x.ImportedAt})
			continue
		}
		addProvenance(provenance, h, torrentProvenance{Source: "sonarr", OwnerID: x.OwnerID, SubID: x.SubID, ImportedAt: x.ImportedAt, SupersededBy: latest})
	}
	sids := map[int][]string{}
	for sid, set := range bySeries {
		for h := range set {
			sids[sid] = append(sids[sid], h)
		}
		sort.Strings(sids[sid])
	}
	return rids, sids, provenance, nil
}

func (s *Service) syncTorrents() (map[string]model.Torrent, error) {
	previous := map[string]model.Torrent{}
	if s.db != nil {
		ps, err := s.db.LoadTorrents()
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			previous[strings.ToLower(p.Hash)] = p
		}
	}
	rid := int64(0)
	if s.db != nil {
		rid, _ = s.db.MetaInt64("qbittorrent.rid")
	}
	next, newRID, err := s.qb.Sync(previous, rid)
	if err != nil {
		return nil, err
	}
	if s.db != nil && newRID != 0 {
		_ = s.db.SetMetaInt64("qbittorrent.rid", newRID)
	}
	return next, nil
}

func (s *Service) Refresh(ctx context.Context) error {
	if err := s.ValidateIntegrations(ctx); err != nil {
		return s.fail(err)
	}
	s.mu.Lock()
	if s.refreshing {
		s.mu.Unlock()
		return nil
	}
	s.refreshing = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.refreshing = false; s.mu.Unlock() }()

	// Stage 1: current application state. Publish this quickly; cached database
	// state was already available from startup while these requests were running.
	var wg sync.WaitGroup
	var rm, sm []model.Media
	var re, se error
	wg.Add(2)
	go func() {
		defer wg.Done()
		rm, re = s.rad.Inventory()
		s.setStatus(integrationName(s.cfg, "radarr", "Radarr"), s.cfg.Radarr.URL != "", re == nil, re)
	}()
	go func() {
		defer wg.Done()
		sm, se = s.son.Inventory()
		s.setStatus(integrationName(s.cfg, "sonarr", "Sonarr"), s.cfg.Sonarr.URL != "", se == nil, se)
	}()
	wg.Wait()
	if re != nil {
		return s.fail(re)
	}
	if se != nil {
		return s.fail(se)
	}
	if i, ok := s.cfg.FirstIntegration("radarr"); ok {
		for n := range rm {
			rm[n].IntegrationID = i.ID
			rm[n].IntegrationName = i.Name
		}
	}
	if i, ok := s.cfg.FirstIntegration("sonarr"); ok {
		for n := range sm {
			sm[n].IntegrationID = i.ID
			sm[n].IntegrationName = i.Name
		}
	}
	all := append(rm, sm...)
	// Jellyfin is enrichment, not inventory authority. Seed the new inventory with
	// the last known playback/favorite facts, then apply fresh Jellyfin data to a
	// clone. If Jellyfin is temporarily slow/unavailable, keep those cached facts
	// and continue the refresh instead of failing or zeroing Media Value.
	previousMedia, _, _ := s.Snapshot()
	preserveJellyfinFacts(all, previousMedia)
	jellyfinMedia := cloneMedia(all)
	if e := s.jf.Apply(jellyfinMedia); e != nil {
		s.setStatus(integrationName(s.cfg, "jellyfin", "Jellyfin"), s.cfg.Jellyfin.URL != "", false, e)
		return s.fail(fmt.Errorf("jellyfin: %w", e))
	} else {
		all = jellyfinMedia
		s.setStatus(integrationName(s.cfg, "jellyfin", "Jellyfin"), s.cfg.Jellyfin.URL != "", true, nil)
	}
	if e := s.seerr.Apply(all); e != nil {
		s.setStatus(integrationName(s.cfg, "seerr", "Seerr"), s.cfg.Seerr.URL != "", false, e)
		return s.fail(fmt.Errorf("seerr: %w", e))
	} else {
		s.setStatus(integrationName(s.cfg, "seerr", "Seerr"), s.cfg.Seerr.URL != "", true, nil)
	}
	valuation.ApplyMedia(all, s.cfg)
	s.mu.Lock()
	s.items = cloneMedia(all)
	s.updated = time.Now()
	s.lastErr = nil
	s.mu.Unlock()
	if s.db != nil {
		_ = s.db.SaveMedia(all)
	}

	if s.cfg.QBittorrent.URL == "" {
		s.setStatus(integrationName(s.cfg, "qbittorrent", "qBittorrent"), false, false, nil)
		s.mu.Lock()
		s.torrents = nil
		s.mu.Unlock()
		return nil
	}

	// Stage 2: durable, incremental enrichment. The first-ever run bootstraps *arr
	// import history; later runs stop as soon as they reach the saved cursor.
	var rids, sids map[int][]string
	var provenance map[string][]torrentProvenance
	var torrentMap map[string]model.Torrent
	var he, qe error
	wg.Add(2)
	go func() { defer wg.Done(); rids, sids, provenance, he = s.syncImportHistory() }()
	go func() { defer wg.Done(); torrentMap, qe = s.syncTorrents() }()
	wg.Wait()
	if he != nil {
		return s.fail(he)
	}
	if qe != nil {
		s.setStatus(integrationName(s.cfg, "qbittorrent", "qBittorrent"), true, false, qe)
		return s.fail(fmt.Errorf("qbittorrent: %w", qe))
	}
	s.setStatus(integrationName(s.cfg, "qbittorrent", "qBittorrent"), true, true, nil)

	torrentMediaItems := map[string][]model.MediaRef{}
	for i := range all {
		m := &all[i]
		if m.Type == model.Movie {
			m.DownloadIDs = rids[m.SourceID]
		} else {
			m.DownloadIDs = sids[m.SourceID]
		}
		// A logical media item may remain in Radarr/Sonarr after all of its files
		// have been deleted. Keep that media in Togetharr, but do not let its
		// historical latest download hash claim a torrent as currently associated.
		// Until first-class media files are indexed, SizeBytes > 0 is our current
		// authoritative summary that this media has file data in the library.
		if !mediaHasCurrentFiles(*m) {
			continue
		}
		seen := map[string]bool{}
		for _, id := range m.DownloadIDs {
			h := strings.ToLower(id)
			if _, ok := torrentMap[h]; ok && !seen[h] {
				torrentMediaItems[h] = append(torrentMediaItems[h], model.MediaRef{Type: m.Type, SourceID: m.SourceID, Title: m.Title, Year: m.Year})
				seen[h] = true
			}
		}
	}
	currentRefs := map[string]model.MediaRef{}
	for _, m := range all {
		currentRefs[fmt.Sprintf("%s:%d", m.Type, m.SourceID)] = model.MediaRef{Type: m.Type, SourceID: m.SourceID, Title: m.Title, Year: m.Year}
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

		prov := provenance[h]
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
			key := fmt.Sprintf("%s:%d", kind, p.OwnerID)
			if ref, ok := currentRefs[key]; ok && !seenFormer[key] {
				t.FormerMediaItems = append(t.FormerMediaItems, ref)
				seenFormer[key] = true
			}
		}
		switch {
		case len(t.MediaItems) > 0:
			t.AssociationStatus = "ASSOCIATED"
			t.AssociationReason = "Torrent hash matches the latest imported release for current library media."
		case superseded:
			t.AssociationStatus = "SUPERSEDED"
			t.AssociationReason = "A later release was imported for the same Radarr movie or Sonarr episode."
		case len(prov) > 0:
			t.AssociationStatus = "ORPHANED"
			t.AssociationReason = "The torrent was imported by Radarr/Sonarr, but no current library media claims that import."
		default:
			t.AssociationStatus = "UNASSOCIATED"
			t.AssociationReason = "No authoritative Radarr/Sonarr import association is known."
		}

		torrentList = append(torrentList, t)
		torrentMap[h] = t
	}

	// Storage estimates come from the reconciled File model. Normal inventory
	// refreshes never walk torrent directories; they reuse the latest sparse file
	// reconciliation snapshot and therefore stay cheap.
	files, _, torrentFileRefs, _, _ := s.FileSnapshot()
	applyTorrentFileEstimates(torrentList, files, torrentFileRefs)
	valuation.ApplyTorrents(torrentList, s.cfg)

	sort.SliceStable(torrentList, func(i, j int) bool {
		if torrentList[i].AssociationStatus != torrentList[j].AssociationStatus {
			return torrentList[i].AssociationStatus < torrentList[j].AssociationStatus
		}
		return strings.ToLower(torrentList[i].Name) < strings.ToLower(torrentList[j].Name)
	})
	projectTorrentRelations(all, torrentList)
	valuation.ApplyMedia(all, s.cfg)
	s.mu.Lock()
	s.items = cloneMedia(all)
	s.torrents = torrentList
	s.updated = time.Now()
	s.lastErr = nil
	s.mu.Unlock()
	if s.db != nil {
		_ = s.db.SaveTorrents(torrentList)
		_ = s.db.SaveMedia(all)
	}
	return nil
}

func (s *Service) ScanUnclaimed(ctx context.Context) error { return s.ReconcileFiles(ctx) }

func (s *Service) setFilesError(err error) error {
	s.mu.Lock()
	s.filesErr = err
	s.mu.Unlock()
	return err
}

func (s *Service) FileSnapshot() ([]model.File, []model.MediaFileRef, []model.TorrentFileRef, time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	files := append([]model.File(nil), s.files...)
	mr := append([]model.MediaFileRef(nil), s.mediaFileRefs...)
	tr := append([]model.TorrentFileRef(nil), s.torrentFileRefs...)
	return files, mr, tr, s.filesUpdated, s.filesErr
}

func (s *Service) MediaFiles(kind model.MediaType, id int) ([]model.File, time.Time, error) {
	files, refs, _, updated, err := s.FileSnapshot()
	byPath := map[string]model.File{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	out := []model.File{}
	for _, r := range refs {
		if r.MediaType == kind && r.MediaID == id {
			if f, ok := byPath[r.Path]; ok {
				out = append(out, f)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	return out, updated, err
}

func (s *Service) ManagedFileRefs(kind model.MediaType, id int) ([]model.MediaFileRef, time.Time, error) {
	_, refs, _, updated, err := s.FileSnapshot()
	out := []model.MediaFileRef{}
	for _, r := range refs {
		if r.MediaType == kind && r.MediaID == id {
			r.Parts = append([]model.MediaFilePart(nil), r.Parts...)
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	return out, updated, err
}

func (s *Service) TorrentFiles(hash string) ([]model.File, time.Time, error) {
	files, _, refs, updated, err := s.FileSnapshot()
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

func mediaRefMap(items []model.Media) map[string]model.MediaRef {
	out := map[string]model.MediaRef{}
	for _, m := range items {
		out[fmt.Sprintf("%s:%d", m.Type, m.SourceID)] = model.MediaRef{Type: m.Type, SourceID: m.SourceID, Title: m.Title, Year: m.Year}
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
				key := fmt.Sprintf("%s:%d", r.MediaType, r.MediaID)
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

func (s *Service) MediaStorage(kind model.MediaType, id int) (MediaStorageView, time.Time, error) {
	files, mr, tr, updated, err := s.FileSnapshot()
	items, _, _ := s.Snapshot()
	torrents := s.TorrentSnapshot()
	x := filetopology.New(files, mr, tr)
	paths := x.MediaPaths(kind, id)
	view := MediaStorageView{Files: buildFileViews(paths, x, mediaRefMap(items), torrentOwnerMap(torrents))}
	view.RemoveMedia = x.Estimate(paths)
	var currentPaths []string
	for _, t := range torrents {
		if t.AssociationStatus != "ASSOCIATED" {
			continue
		}
		matched := false
		for _, ref := range t.MediaItems {
			if ref.Type == kind && ref.SourceID == id {
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

func (s *Service) TorrentStorage(hash string) (TorrentStorageView, time.Time, error) {
	files, mr, tr, updated, err := s.FileSnapshot()
	items, _, _ := s.Snapshot()
	torrents := s.TorrentSnapshot()
	x := filetopology.New(files, mr, tr)
	paths := x.TorrentPaths(hash)
	return TorrentStorageView{Files: buildFileViews(paths, x, mediaRefMap(items), torrentOwnerMap(torrents)), RemoveTorrent: x.Estimate(paths)}, updated, err
}

func projectTorrentRelations(media []model.Media, torrents []model.Torrent) {
	mediaIndex := map[string]int{}
	for i := range media {
		media[i].Torrents = nil
		mediaIndex[fmt.Sprintf("%s:%d", media[i].Type, media[i].SourceID)] = i
	}
	seen := map[string]map[string]bool{}
	attach := func(ref model.MediaRef, t model.Torrent) {
		key := fmt.Sprintf("%s:%d", ref.Type, ref.SourceID)
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
		media[i].Torrents = append(media[i].Torrents, t)
		seen[key][h] = true
	}
	for _, t := range torrents {
		for _, ref := range t.MediaItems {
			attach(ref, t)
		}
		for _, ref := range t.FormerMediaItems {
			attach(ref, t)
		}
	}
}

func mediaHasCurrentFiles(m model.Media) bool {
	return m.SizeBytes > 0
}

func preserveJellyfinFacts(dst, previous []model.Media) {
	byKey := make(map[string]model.Media, len(previous))
	for _, m := range previous {
		byKey[fmt.Sprintf("%s:%d", m.Type, m.SourceID)] = m
	}
	for i := range dst {
		if old, ok := byKey[fmt.Sprintf("%s:%d", dst[i].Type, dst[i].SourceID)]; ok {
			dst[i].Views = old.Views
			dst[i].UniqueViewers = old.UniqueViewers
			dst[i].LastWatched = old.LastWatched
			dst[i].Favorite = old.Favorite
		}
	}
}

func cloneMedia(in []model.Media) []model.Media {
	out := make([]model.Media, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Tags = append([]string(nil), in[i].Tags...)
		out[i].DownloadIDs = append([]string(nil), in[i].DownloadIDs...)
		out[i].Torrents = append([]model.Torrent(nil), in[i].Torrents...)
		out[i].Reasons = append([]model.Reason(nil), in[i].Reasons...)
	}
	return out
}
func (s *Service) fail(e error) error { s.mu.Lock(); s.lastErr = e; s.mu.Unlock(); return e }
func (s *Service) Snapshot() ([]model.Media, time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]model.Media(nil), s.items...), s.updated, s.lastErr
}
func (s *Service) TorrentSnapshot() []model.Torrent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Torrent, len(s.torrents))
	copy(out, s.torrents)
	return out
}

// TorrentDetail enriches the locally indexed relationship/storage facts with
// live client-owned details. Nothing fetched here is persisted.
func (s *Service) TorrentDetail(hash string) (model.Torrent, error) {
	var indexed model.Torrent
	found := false
	for _, t := range s.TorrentSnapshot() {
		if strings.EqualFold(t.Hash, hash) {
			indexed = t
			found = true
			break
		}
	}
	if !found {
		return model.Torrent{}, fmt.Errorf("torrent not found")
	}
	live, err := s.qb.Detail(hash)
	if err != nil {
		return indexed, err
	}
	// Preserve Togetharr-owned interpretations and expensive reconciliation facts.
	live.Value = indexed.Value
	live.ValueReasons = indexed.ValueReasons
	live.AssociationStatus = indexed.AssociationStatus
	live.AssociationReason = indexed.AssociationReason
	live.MediaItems = indexed.MediaItems
	live.FormerMediaItems = indexed.FormerMediaItems
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

func (s *Service) IsRefreshing() bool    { s.mu.RLock(); defer s.mu.RUnlock(); return s.refreshing }
func (s *Service) Config() config.Config { return s.cfg }

func (s *Service) UnclaimedSnapshot() ([]model.UnclaimedFile, time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.UnclaimedFile, len(s.unclaimed))
	copy(out, s.unclaimed)
	return out, s.unclaimedUpdated, s.unclaimedErr
}

// RemoveManagedFile delegates removal of one managed file to the application
// that owns that file. The Media object itself remains in the owner application.
func (s *Service) RemoveManagedFile(ref model.MediaFileRef) error {
	switch strings.ToLower(ref.Source) {
	case "radarr":
		if ref.SourceFileID <= 0 {
			return fmt.Errorf("radarr managed file id is missing")
		}
		return s.rad.DeleteFile(ref.SourceFileID)
	case "sonarr":
		if ref.SourceFileID <= 0 {
			return fmt.Errorf("sonarr managed file id is missing")
		}
		return s.son.DeleteFile(ref.SourceFileID)
	default:
		return fmt.Errorf("unsupported managed-file owner %q", ref.Source)
	}
}

// RemoveTorrent delegates torrent and torrent-owned data removal to qBittorrent.
func (s *Service) RemoveTorrent(hash string) error { return s.qb.Delete(hash) }

func (s *Service) SetMovieMonitored(id int, monitored bool) error {
	return s.rad.SetMonitored(id, monitored)
}

func (s *Service) SetEpisodesMonitored(ids []int, monitored bool) error {
	return s.son.SetEpisodesMonitored(ids, monitored)
}

// VerifyUnclaimed fails closed before direct filesystem removal. It refreshes
// qBittorrent's authoritative file ownership and also rejects paths currently
// indexed as managed Media files. This is intentionally heavier than routine
// browsing because direct OS removal must never rely on stale absence alone.
func (s *Service) VerifyUnclaimed(paths []string) error {
	torrents := s.TorrentSnapshot()
	tm := make(map[string]model.Torrent, len(torrents))
	for _, t := range torrents {
		tm[strings.ToLower(t.Hash)] = t
	}
	claimed, _, err := s.qb.ClaimedFiles(tm)
	if err != nil {
		return fmt.Errorf("cannot verify torrent ownership: %w", err)
	}
	wanted := map[string]bool{}
	for _, p := range paths {
		wanted[filepath.Clean(p)] = true
	}
	for p := range wanted {
		if claimed[p] {
			return fmt.Errorf("file is now claimed by qBittorrent: %s", p)
		}
	}
	_, mr, _, _, _ := s.FileSnapshot()
	for _, r := range mr {
		p := filepath.Clean(r.Path)
		if wanted[p] {
			return fmt.Errorf("file is claimed by managed media: %s", p)
		}
	}
	return nil
}
