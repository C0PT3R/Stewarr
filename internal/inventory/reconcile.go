package inventory

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"stewarr/internal/config"
	"stewarr/internal/model"
	"stewarr/internal/services/jellyfin"
	"stewarr/internal/services/qbittorrent"
	"stewarr/internal/services/radarr"
	"stewarr/internal/services/seerr"
	"stewarr/internal/services/sonarr"
	"stewarr/internal/tasks"
	"stewarr/internal/valuation"
	"strings"
	"sync"
	"syscall"
	"time"
)

func (service *Service) ValidateBaseServices(ctx context.Context) error {
	return service.validateServiceTypes(ctx, map[string]bool{"radarr": true, "sonarr": true, "qbittorrent": true})
}

func (service *Service) validateServiceTypes(ctx context.Context, allowed map[string]bool) error {
	service.mu.RLock()
	validatedAt := service.validatedBaseAt
	service.mu.RUnlock()
	if !validatedAt.IsZero() && time.Since(validatedAt) < 30*time.Second {
		return nil
	}
	type check struct {
		name string
		fn   func() error
	}
	checks := []check{}
	for _, i := range service.cfg.Services {
		i := i
		if !allowed[i.Type] {
			continue
		}
		if !i.Enabled() {
			continue
		}
		var fn func() error
		switch i.Type {
		case "radarr":
			fn = radarr.New(i.URL, i.APIKey).WithContext(ctx).Validate
		case "sonarr":
			fn = sonarr.New(i.URL, i.APIKey).WithContext(ctx).Validate
		case "jellyfin":
			fn = jellyfin.New(i.URL, i.APIKey).WithContext(ctx).Validate
		case "seerr":
			fn = seerr.New(i.URL, i.APIKey).WithContext(ctx).Validate
		case "qbittorrent":
			fn = qbittorrent.New(i.Name, i.URL, i.Username, i.Password, i.APIKey).WithContext(ctx).Validate
		default:
			return fmt.Errorf("service %q has no registered adapter for type %q", i.Name, i.Type)
		}
		checks = append(checks, check{name: i.Name, fn: fn})
	}
	type result struct {
		c   check
		err error
	}
	ch := make(chan result, len(checks))
	var wg sync.WaitGroup
	for _, c := range checks {
		wg.Add(1)
		go func(c check) { defer wg.Done(); ch <- result{c, c.fn()} }(c)
	}
	wg.Wait()
	close(ch)
	errs := []string{}
	for r := range ch {
		service.setStatus(r.c.name, true, r.err == nil, r.err)
		if r.err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", r.c.name, r.err))
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("service validation failed: %s", strings.Join(errs, "; "))
	}
	service.mu.Lock()
	service.validatedBaseAt = time.Now()
	service.mu.Unlock()
	return nil
}

func under(root, p string) bool {
	rel, e := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func collapse(paths []string) []string {
	set := map[string]bool{}
	for _, p := range paths {
		p = filepath.Clean(strings.TrimSpace(p))
		if p != "" && p != "." {
			set[p] = true
		}
	}
	xs := []string{}
	for p := range set {
		xs = append(xs, p)
	}
	sort.Slice(xs, func(i, j int) bool { return len(xs[i]) < len(xs[j]) })
	out := []string{}
	for _, p := range xs {
		nested := false
		for _, r := range out {
			if under(r, p) {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, p)
		}
	}
	return out
}

type storageRoot struct {
	Path    string
	Service config.Service
	Label   string
	// Purpose distinguishes why a root exists at all, e.g.
	// qbittorrent.RootPurposeIncompleteDownloads — empty for an ordinary
	// root. Surfaced on UnreachableServiceRoots so the reason a path needs
	// mounting is visible, not just the bare path.
	Purpose string
}

func configuredOrDiscoveredRoots(i config.Service, discovered []string) []storageRoot {
	out := make([]storageRoot, 0, len(discovered)+1)
	for _, p := range discovered {
		out = append(out, storageRoot{Path: p, Service: i})
	}
	if len(discovered) == 0 && strings.TrimSpace(i.RootPath) != "" {
		out = append(out, storageRoot{Path: i.RootPath, Service: i})
	}
	return out
}

// qbittorrentDiscoveredRoots mirrors configuredOrDiscoveredRoots for
// qBittorrent's richer qbittorrent.RootPath (path + purpose) instead of a
// bare path string, so an incomplete-downloads root carries that label all
// the way through to UnreachableServiceRoots.
func qbittorrentDiscoveredRoots(i config.Service, discovered []qbittorrent.RootPath) []storageRoot {
	out := make([]storageRoot, 0, len(discovered)+1)
	for _, r := range discovered {
		out = append(out, storageRoot{Path: r.Path, Service: i, Purpose: r.Purpose})
	}
	if len(discovered) == 0 && strings.TrimSpace(i.RootPath) != "" {
		out = append(out, storageRoot{Path: i.RootPath, Service: i})
	}
	return out
}

func rootLabel(path string) string {
	p := filepath.Clean(path)
	base := filepath.Base(p)
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

func collapseStorageRoots(in []storageRoot) []storageRoot {
	// Collapse nested roots only within the same service. Cross-service
	// overlap is meaningful because each service contributes its own storage
	// context/claims.
	byService := map[string][]storageRoot{}
	for _, r := range in {
		r.Path = filepath.Clean(strings.TrimSpace(r.Path))
		if r.Path == "" || r.Path == "." {
			continue
		}
		if r.Label == "" {
			r.Label = rootLabel(r.Path)
		}
		key := r.Service.ID
		if key == "" {
			key = strings.ToLower(r.Service.Name) + "\x00" + r.Service.Type
		}
		byService[key] = append(byService[key], r)
	}
	out := []storageRoot{}
	for _, xs := range byService {
		sort.Slice(xs, func(i, j int) bool { return len(xs[i].Path) < len(xs[j].Path) })
		kept := []storageRoot{}
		seen := map[string]bool{}
		for _, r := range xs {
			if seen[r.Path] {
				continue
			}
			seen[r.Path] = true
			nested := false
			for _, k := range kept {
				if under(k.Path, r.Path) {
					nested = true
					break
				}
			}
			if !nested {
				kept = append(kept, r)
			}
		}
		out = append(out, kept...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Service.Name < out[j].Service.Name
	})
	return out
}

func walkStorageRoots(roots []storageRoot) ([]model.File, error) {
	contextRoots := collapseStorageRoots(roots)
	physicalCandidates := make([]string, 0, len(contextRoots))
	for _, contextRoot := range contextRoots {
		info, err := os.Stat(contextRoot.Path)
		if err != nil {
			return nil, fmt.Errorf("storage root %s (%s): %w", contextRoot.Path, contextRoot.Service.Name, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("storage root %s (%s) is not a directory", contextRoot.Path, contextRoot.Service.Name)
		}
		physicalCandidates = append(physicalCandidates, contextRoot.Path)
	}
	// Overlapping services describe extra claims, not extra I/O. Walk each
	// physical subtree once and attach every applicable service context.
	physicalRoots := collapse(physicalCandidates)
	byPath := map[string]*model.File{}
	for _, root := range physicalRoots {
		e := filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			i, e := d.Info()
			if e != nil {
				return e
			}
			if !i.Mode().IsRegular() {
				return nil
			}
			p = filepath.Clean(p)
			contexts := []model.StorageContext{}
			for _, contextRoot := range contextRoots {
				if under(contextRoot.Path, p) {
					contexts = append(contexts, model.StorageContext{ServiceID: contextRoot.Service.ID, ServiceName: contextRoot.Service.Name, ServiceType: contextRoot.Service.Type, Root: contextRoot.Path, RootLabel: contextRoot.Label})
				}
			}
			f := model.File{Path: p, SizeBytes: i.Size(), Exists: true, ModifiedAt: i.ModTime(), StorageContexts: contexts}
			if st, ok := i.Sys().(*syscall.Stat_t); ok {
				f.IdentityKnown = true
				f.Device = uint64(st.Dev)
				f.Inode = uint64(st.Ino)
				f.Links = uint64(st.Nlink)
			}
			byPath[p] = &f
			return nil
		})
		if e != nil {
			return nil, fmt.Errorf("scan storage root %s: %w", root, e)
		}
	}
	out := make([]model.File, 0, len(byPath))
	for _, f := range byPath {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	return out, nil
}

func serviceName(c config.Config, typ, fallback string) string {
	if i, ok := c.FirstService(typ); ok && i.Name != "" {
		return i.Name
	}
	return fallback
}
func svcID(c config.Config, typ string) string {
	if i, ok := c.FirstService(typ); ok {
		return i.ID
	}
	return typ
}

func walkRoots(paths []string) ([]model.File, error) {
	roots := make([]storageRoot, 0, len(paths))
	for _, p := range paths {
		roots = append(roots, storageRoot{Path: p, Service: config.Service{ID: "test", Name: "Storage"}})
	}
	return walkStorageRoots(roots)
}

// ReconcileFiles performs a full authoritative reconciliation. Startup,
// periodic, and manual executions always use this path.
func (service *Service) ReconcileFiles(ctx context.Context) error {
	if err := service.reconcileFiles(ctx); err != nil {
		return err
	}
	if service.db != nil {
		if err := service.db.SetMeta(reconciliationScopeKey, ""); err != nil {
			return service.setFilesError(fmt.Errorf("clear reconciliation scope after full publication: %w", err))
		}
	}
	return nil
}

// ReconcileFilesAfterMutation applies one durable mutation scope to affected
// paths and owners, promoting to the full path when any invariant is uncertain.
func (service *Service) ReconcileFilesAfterMutation(ctx context.Context) error {
	return service.reconcileTargeted(ctx)
}

// reconcileFiles performs one atomic generation: roots -> physical filesystem
// inventory -> service claims -> Claimed/Unmanaged projection. Nothing is
// published unless every phase succeeds.
func (service *Service) reconcileFiles(ctx context.Context) error {
	defer service.publishChange()
	started := time.Now()
	metrics := map[string]any{"mode": "full"}
	defer func() {
		metrics["total"] = time.Since(started).Round(time.Millisecond)
		for name, value := range metrics {
			tasks.AddMetric(ctx, name, value)
		}
	}()
	service.mu.Lock()
	service.reliability.FileModel = "pending"
	service.mu.Unlock()
	stageStarted := time.Now()
	if err := service.ValidateBaseServices(ctx); err != nil {
		return service.setFilesError(err)
	}
	metrics["validation"] = time.Since(stageStarted).Round(time.Millisecond)
	service.mu.RLock()
	media := cloneMedia(service.items)
	torrents := append([]model.Torrent(nil), service.torrents...)
	generation := service.generation
	service.mu.RUnlock()
	torrentsByInstance := map[string]map[string]model.Torrent{}
	for _, t := range torrents {
		if torrentsByInstance[t.ServiceID] == nil {
			torrentsByInstance[t.ServiceID] = map[string]model.Torrent{}
		}
		torrentsByInstance[t.ServiceID][strings.ToLower(t.Hash)] = t
	}
	service.mu.RLock()
	radInstances := service.cfg.ServicesOfType("radarr")
	sonInstances := service.cfg.ServicesOfType("sonarr")
	qbInstances := service.cfg.ServicesOfType("qbittorrent")
	radClients := service.rad
	sonClients := service.son
	qbClients := service.qb
	service.mu.RUnlock()

	type rootsFetch struct {
		svc   config.Service
		roots []string
		err   error
	}
	type qbRootsFetch struct {
		svc   config.Service
		roots []qbittorrent.RootPath
		err   error
	}
	radRootFetches := make([]rootsFetch, len(radInstances))
	sonRootFetches := make([]rootsFetch, len(sonInstances))
	qbRootFetches := make([]qbRootsFetch, len(qbInstances))
	var wg sync.WaitGroup
	stageStarted = time.Now()
	wg.Add(len(radInstances) + len(sonInstances) + len(qbInstances))
	for i, svc := range radInstances {
		go func(i int, svc config.Service) {
			defer wg.Done()
			roots, err := radClients[svc.ID].WithContext(ctx).StorageRoots()
			radRootFetches[i] = rootsFetch{svc: svc, roots: roots, err: err}
		}(i, svc)
	}
	for i, svc := range sonInstances {
		go func(i int, svc config.Service) {
			defer wg.Done()
			roots, err := sonClients[svc.ID].WithContext(ctx).StorageRoots()
			sonRootFetches[i] = rootsFetch{svc: svc, roots: roots, err: err}
		}(i, svc)
	}
	for i, svc := range qbInstances {
		go func(i int, svc config.Service) {
			defer wg.Done()
			roots, err := qbClients[svc.ID].WithContext(ctx).StorageRoots(torrentsByInstance[svc.ID])
			qbRootFetches[i] = qbRootsFetch{svc: svc, roots: roots, err: err}
		}(i, svc)
	}
	wg.Wait()
	for _, f := range radRootFetches {
		if f.err != nil {
			return service.setFilesError(fmt.Errorf("radarr (%s) storage roots: %w", f.svc.Name, f.err))
		}
	}
	for _, f := range sonRootFetches {
		if f.err != nil {
			return service.setFilesError(fmt.Errorf("sonarr (%s) storage roots: %w", f.svc.Name, f.err))
		}
	}
	for _, f := range qbRootFetches {
		if f.err != nil {
			return service.setFilesError(fmt.Errorf("qbittorrent (%s) storage roots: %w", f.svc.Name, f.err))
		}
	}
	metrics["roots"] = time.Since(stageStarted).Round(time.Millisecond)
	var roots []storageRoot
	for _, f := range radRootFetches {
		roots = append(roots, configuredOrDiscoveredRoots(f.svc, f.roots)...)
	}
	for _, f := range sonRootFetches {
		roots = append(roots, configuredOrDiscoveredRoots(f.svc, f.roots)...)
	}
	for _, f := range qbRootFetches {
		roots = append(roots, qbittorrentDiscoveredRoots(f.svc, f.roots)...)
	}
	// Zero storage-owning services configured at all is a legitimate,
	// expected state on a fresh install — not a failure. Only treat an empty
	// root set as an error when at least one service is configured but
	// none of them reported a usable root, which is a real misconfiguration
	// worth surfacing.
	if len(roots) == 0 && len(radInstances)+len(sonInstances)+len(qbInstances) > 0 {
		return service.setFilesError(fmt.Errorf("no service storage roots are available"))
	}
	stageStarted = time.Now()
	files, err := walkStorageRoots(roots)
	if err != nil {
		return service.setFilesError(err)
	}
	metrics["filesystem"] = time.Since(stageStarted).Round(time.Millisecond)
	metrics["paths"] = len(files)
	byPath := map[string]bool{}
	for _, f := range files {
		byPath[f.Path] = true
	}
	movieIDsByInstance := map[string][]int{}
	seriesIDsByInstance := map[string][]int{}
	mediaRootByKey := map[ownerKey]string{}
	for _, m := range media {
		mediaRootByKey[ownerKey{ServiceID: m.ServiceID, OwnerID: m.SourceID}] = m.Path
		if m.Type == model.Movie {
			movieIDsByInstance[m.ServiceID] = append(movieIDsByInstance[m.ServiceID], m.SourceID)
		} else if m.Type == model.Series {
			seriesIDsByInstance[m.ServiceID] = append(seriesIDsByInstance[m.ServiceID], m.SourceID)
		}
	}
	type radarrFilesFetch struct {
		svc   config.Service
		files []radarr.FileRecord
		err   error
	}
	type sonarrFilesFetch struct {
		svc   config.Service
		files []sonarr.FileRecord
		err   error
	}
	type qbittorrentFilesFetch struct {
		svc   config.Service
		files map[string][]qbittorrent.File
		err   error
	}
	radFilesFetches := make([]radarrFilesFetch, len(radInstances))
	sonFilesFetches := make([]sonarrFilesFetch, len(sonInstances))
	qbFilesFetches := make([]qbittorrentFilesFetch, len(qbInstances))
	metrics["torrents_fetched"] = len(torrents)
	stageStarted = time.Now()
	wg = sync.WaitGroup{}
	wg.Add(len(radInstances) + len(sonInstances) + len(qbInstances))
	for i, svc := range radInstances {
		go func(i int, svc config.Service) {
			defer wg.Done()
			files, err := radClients[svc.ID].WithContext(ctx).Files(movieIDsByInstance[svc.ID])
			radFilesFetches[i] = radarrFilesFetch{svc: svc, files: files, err: err}
		}(i, svc)
	}
	for i, svc := range sonInstances {
		go func(i int, svc config.Service) {
			defer wg.Done()
			files, err := sonClients[svc.ID].WithContext(ctx).Files(seriesIDsByInstance[svc.ID])
			sonFilesFetches[i] = sonarrFilesFetch{svc: svc, files: files, err: err}
		}(i, svc)
	}
	for i, svc := range qbInstances {
		go func(i int, svc config.Service) {
			defer wg.Done()
			files, err := qbClients[svc.ID].WithContext(ctx).AllFiles(torrentsByInstance[svc.ID])
			qbFilesFetches[i] = qbittorrentFilesFetch{svc: svc, files: files, err: err}
		}(i, svc)
	}
	wg.Wait()
	for _, f := range radFilesFetches {
		if f.err != nil {
			return service.setFilesError(fmt.Errorf("radarr (%s) files: %w", f.svc.Name, f.err))
		}
	}
	for _, f := range sonFilesFetches {
		if f.err != nil {
			return service.setFilesError(fmt.Errorf("sonarr (%s) files: %w", f.svc.Name, f.err))
		}
	}
	for _, f := range qbFilesFetches {
		if f.err != nil {
			return service.setFilesError(fmt.Errorf("qbittorrent (%s) files: %w", f.svc.Name, f.err))
		}
	}
	metrics["claims"] = time.Since(stageStarted).Round(time.Millisecond)
	mediaRefs := []model.MediaFileRef{}
	torrentRefs := []model.TorrentFileRef{}
	claimed := map[string]bool{}
	for _, rf := range radFilesFetches {
		for _, f := range rf.files {
			root := mediaRootByKey[ownerKey{ServiceID: rf.svc.ID, OwnerID: f.MovieID}]
			if root == "" {
				continue
			}
			p := filepath.Clean(filepath.Join(root, f.Relative))
			mediaRefs = append(mediaRefs, model.MediaFileRef{ServiceID: rf.svc.ID, ServiceName: rf.svc.Name, MediaType: model.Movie, MediaID: f.MovieID, Source: "radarr", SourceFileID: f.ID, Path: p})
			if byPath[p] {
				claimed[p] = true
			}
		}
	}
	for _, sf := range sonFilesFetches {
		for _, f := range sf.files {
			root := mediaRootByKey[ownerKey{ServiceID: sf.svc.ID, OwnerID: f.SeriesID}]
			if root == "" {
				continue
			}
			p := filepath.Clean(filepath.Join(root, f.Relative))
			mediaRefs = append(mediaRefs, model.MediaFileRef{ServiceID: sf.svc.ID, ServiceName: sf.svc.Name, MediaType: model.Series, MediaID: f.SeriesID, Source: "sonarr", SourceFileID: f.ID, Path: p, Parts: append([]model.MediaFilePart(nil), f.Parts...), AddedAt: f.DateAdded})
			if byPath[p] {
				claimed[p] = true
			}
		}
	}
	for _, qf := range qbFilesFetches {
		for h, xs := range qf.files {
			t, ok := torrentsByInstance[qf.svc.ID][h]
			if !ok {
				continue
			}
			for _, x := range xs {
				p := filepath.Clean(filepath.Join(t.SavePath, filepath.FromSlash(x.Name)))
				torrentRefs = append(torrentRefs, model.TorrentFileRef{ServiceID: qf.svc.ID, ServiceName: qf.svc.Name, Client: t.Client, Hash: h, FileIndex: x.Index, Path: p})
				if byPath[p] {
					claimed[p] = true
				}
			}
		}
	}
	unmanagedFiles := []model.UnmanagedFile{}
	for _, f := range files {
		if claimed[f.Path] {
			continue
		}
		u := model.UnmanagedFile{Path: f.Path, SizeBytes: f.SizeBytes, ModifiedAt: f.ModifiedAt, Device: f.Device, Inode: f.Inode, Links: f.Links, ReclaimableKnown: f.IdentityKnown, StorageContexts: append([]model.StorageContext(nil), f.StorageContexts...)}
		if f.IdentityKnown && f.Links <= 1 {
			u.ReclaimableBytes = f.SizeBytes
		} else if f.IdentityKnown {
			u.SharedBytes = f.SizeBytes
		}
		unmanagedFiles = append(unmanagedFiles, u)
	}
	service.mu.RLock()
	generationCurrent := service.generation == generation
	tc := append([]model.Torrent(nil), service.torrents...)
	mc := cloneMedia(service.items)
	cfg := service.cfg
	service.mu.RUnlock()
	if !generationCurrent {
		return service.setFilesError(fmt.Errorf("inventory changed during file reconciliation; result discarded"))
	}
	relationshipsStarted := time.Now()
	applyTorrentFileEstimates(tc, files, torrentRefs)
	applyTorrentMediaHardlinks(tc, mc, files, mediaRefs, torrentRefs)
	applyMediaFileEstimates(mc, files, mediaRefs)
	attachSeasons(mc, mediaRefs, files)
	applySeasonFileEstimates(mc, files, mediaRefs)
	projectTorrentRelations(mc, tc)
	valuation.ApplyTorrentValue(tc, cfg, service.recentTorrentHistory())
	valuation.ApplyMedia(mc, cfg)
	metrics["relationships"] = time.Since(relationshipsStarted).Round(time.Millisecond)
	stageStarted = time.Now()
	// publishMu serializes this generation-check-then-write sequence against
	// every other reconciliation publisher (full/delta/targeted), so two
	// publishers can never interleave — without holding mu (which every page
	// load needs just to read cached state) across the database write
	// itself. A single slow write must never stall the whole app.
	service.publishMu.Lock()
	defer service.publishMu.Unlock()
	service.mu.RLock()
	generationCurrent = service.generation == generation
	service.mu.RUnlock()
	if !generationCurrent {
		return service.setFilesError(fmt.Errorf("inventory changed while file reconciliation was being committed; result discarded"))
	}
	if service.db != nil {
		if e := service.db.PublishReconciliation(generation, files, mediaRefs, torrentRefs, unmanagedFiles, tc, mc); e != nil {
			err := fmt.Errorf("persist reconciled generation: %w", e)
			service.mu.Lock()
			service.filesErr = err
			service.reliability.FileModel = "stale"
			service.mu.Unlock()
			return err
		}
	}
	now := time.Now()
	service.mu.Lock()
	if service.generation != generation {
		service.mu.Unlock()
		return service.setFilesError(fmt.Errorf("inventory changed while file reconciliation was being committed; result discarded"))
	}
	service.files = files
	service.mediaFileRefs = mediaRefs
	service.torrentFileRefs = torrentRefs
	service.storageRoots = collapseStorageRoots(roots)
	service.filesUpdated = now
	service.filesErr = nil
	service.unmanaged = unmanagedFiles
	service.unmanagedUpdated = now
	service.unmanagedErr = nil
	service.reliability.FileModel = "reliable"
	service.fileGeneration = generation
	service.fileTopologyVersion++
	service.torrents = tc
	service.items = mc
	service.mu.Unlock()
	metrics["publish"] = time.Since(stageStarted).Round(time.Millisecond)
	service.setStageTiming("file reconciliation", started)
	// This is the one point with the complete current device set (a
	// targeted/delta reconciliation only ever sees a partial scope, so
	// syncDeviceRegistry must never run from there — it would wrongly
	// prune a device merely absent from that scope).
	service.syncDeviceRegistry()
	return nil
}
