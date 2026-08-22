package inventory

import (
	"context"
	"fmt"
	"sort"
	"spartarr/internal/config"
	"spartarr/internal/jellyfin"
	"spartarr/internal/model"
	"spartarr/internal/qbittorrent"
	"spartarr/internal/radarr"
	"spartarr/internal/scoring"
	"spartarr/internal/seerr"
	"spartarr/internal/sonarr"
	"spartarr/internal/store"
	"spartarr/internal/torrentstorage"
	"strings"
	"sync"
	"time"
)

type ServiceStatus struct {
	Name       string
	Configured bool
	OK         bool
	Message    string
	CheckedAt  time.Time
}

type Service struct {
	cfg        config.Config
	mu         sync.RWMutex
	items      []model.Media
	torrents   []model.Torrent
	updated    time.Time
	lastErr    error
	refreshing bool
	statuses   map[string]ServiceStatus
	rad        *radarr.Client
	son        *sonarr.Client
	jf         *jellyfin.Client
	seerr      *seerr.Client
	qb         *qbittorrent.Client
	db         *store.Store
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
	names := []string{"Radarr", "Sonarr", "Jellyfin", "Seerr", "qBittorrent"}
	out := make([]ServiceStatus, 0, len(names))
	for _, name := range names {
		if st, ok := s.statuses[name]; ok {
			out = append(out, st)
			continue
		}
		configured := true
		switch name {
		case "Radarr":
			configured = s.cfg.Radarr.URL != ""
		case "Sonarr":
			configured = s.cfg.Sonarr.URL != ""
		case "Jellyfin":
			configured = s.cfg.Jellyfin.URL != ""
		case "Seerr":
			configured = s.cfg.Seerr.URL != ""
		case "qBittorrent":
			configured = s.cfg.QBittorrent.URL != ""
		}
		out = append(out, ServiceStatus{Name: name, Configured: configured, OK: false, Message: "Not checked yet"})
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
	_ = ctx
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
		s.setStatus("Radarr", s.cfg.Radarr.URL != "", re == nil, re)
	}()
	go func() {
		defer wg.Done()
		sm, se = s.son.Inventory()
		s.setStatus("Sonarr", s.cfg.Sonarr.URL != "", se == nil, se)
	}()
	wg.Wait()
	if re != nil {
		return s.fail(re)
	}
	if se != nil {
		return s.fail(se)
	}
	all := append(rm, sm...)
	if e := s.jf.Apply(all); e != nil {
		s.setStatus("Jellyfin", s.cfg.Jellyfin.URL != "", false, e)
		return s.fail(fmt.Errorf("jellyfin: %w", e))
	} else {
		s.setStatus("Jellyfin", s.cfg.Jellyfin.URL != "", true, nil)
	}
	if e := s.seerr.Apply(all); e != nil {
		s.setStatus("Seerr", s.cfg.Seerr.URL != "", false, e)
		return s.fail(fmt.Errorf("seerr: %w", e))
	} else {
		s.setStatus("Seerr", s.cfg.Seerr.URL != "", true, nil)
	}
	scoring.Apply(all, s.cfg)
	s.mu.Lock()
	s.items = cloneMedia(all)
	s.updated = time.Now()
	s.lastErr = nil
	s.mu.Unlock()
	if s.db != nil {
		_ = s.db.SaveMedia(all)
	}

	if s.cfg.QBittorrent.URL == "" {
		s.setStatus("qBittorrent", false, false, nil)
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
		s.setStatus("qBittorrent", true, false, qe)
		return s.fail(fmt.Errorf("qbittorrent: %w", qe))
	}
	s.setStatus("qBittorrent", true, true, nil)

	torrentMediaItems := map[string][]model.MediaRef{}
	for i := range all {
		m := &all[i]
		if m.Type == model.Movie {
			m.DownloadIDs = rids[m.SourceID]
		} else {
			m.DownloadIDs = sids[m.SourceID]
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

		if t.AssociationStatus == "ORPHANED" || t.AssociationStatus == "SUPERSEDED" {
			r := torrentstorage.Inspect(s.cfg.Storage.Path, t.ContentPath)
			t.ReclaimableKnown = r.Known
			t.ReclaimableBytes = r.ReclaimableBytes
			t.SharedBytes = r.SharedBytes
			t.InspectedBytes = r.TotalBytes
			t.InspectedFiles = r.Files
			t.SharedFiles = r.SharedFiles
			t.StorageError = r.Error
		}

		torrentList = append(torrentList, t)
		torrentMap[h] = t
	}
	sort.SliceStable(torrentList, func(i, j int) bool {
		if torrentList[i].AssociationStatus != torrentList[j].AssociationStatus {
			return torrentList[i].AssociationStatus < torrentList[j].AssociationStatus
		}
		return strings.ToLower(torrentList[i].Name) < strings.ToLower(torrentList[j].Name)
	})
	for i := range all {
		m := &all[i]
		seen := map[string]bool{}
		for _, id := range m.DownloadIDs {
			h := strings.ToLower(id)
			if t, ok := torrentMap[h]; ok && !seen[h] {
				m.Torrents = append(m.Torrents, t)
				seen[h] = true
			}
		}
	}
	scoring.Apply(all, s.cfg)
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
func (s *Service) IsRefreshing() bool    { s.mu.RLock(); defer s.mu.RUnlock(); return s.refreshing }
func (s *Service) Config() config.Config { return s.cfg }
