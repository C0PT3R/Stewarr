package inventory

import (
	"connarr/internal/config"
	"connarr/internal/integrations/jellyfin"
	"connarr/internal/integrations/qbittorrent"
	"connarr/internal/integrations/radarr"
	"connarr/internal/integrations/seerr"
	"connarr/internal/integrations/sonarr"
	"connarr/internal/model"
	"connarr/internal/tasks"
	"connarr/internal/valuation"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ValidateIntegrations is a hard preflight: no task may start from unchecked
// integration state. Optional/unconfigured integrations are skipped.
func (service *Service) ValidateIntegrations(ctx context.Context) error {
	return service.validateIntegrationTypes(ctx, nil)
}

func (service *Service) ValidateBaseIntegrations(ctx context.Context) error {
	return service.validateIntegrationTypes(ctx, map[string]bool{"radarr": true, "sonarr": true, "qbittorrent": true})
}

func (service *Service) validateIntegrationTypes(ctx context.Context, allowed map[string]bool) error {
	service.mu.RLock()
	validatedAt := service.validatedAt
	if allowed != nil {
		validatedAt = service.validatedBaseAt
	}
	service.mu.RUnlock()
	if !validatedAt.IsZero() && time.Since(validatedAt) < 30*time.Second {
		return nil
	}
	type check struct {
		name string
		fn   func() error
	}
	checks := []check{}
	for _, i := range service.cfg.Integrations {
		i := i
		if allowed != nil && !allowed[i.Type] {
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
			return fmt.Errorf("integration %q has no registered adapter for type %q", i.Name, i.Type)
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
		return fmt.Errorf("integration validation failed: %s", strings.Join(errs, "; "))
	}
	service.mu.Lock()
	if allowed == nil {
		service.validatedAt = time.Now()
	} else {
		service.validatedBaseAt = time.Now()
	}
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
	Path        string
	Integration config.Integration
	Label       string
}

func configuredOrDiscoveredRoots(i config.Integration, discovered []string) []storageRoot {
	out := make([]storageRoot, 0, len(discovered)+1)
	for _, p := range discovered {
		out = append(out, storageRoot{Path: p, Integration: i})
	}
	if len(discovered) == 0 && strings.TrimSpace(i.RootPath) != "" {
		out = append(out, storageRoot{Path: i.RootPath, Integration: i})
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
	// Collapse nested roots only within the same integration. Cross-integration
	// overlap is meaningful because each integration contributes its own storage
	// context/claims.
	byIntegration := map[string][]storageRoot{}
	for _, r := range in {
		r.Path = filepath.Clean(strings.TrimSpace(r.Path))
		if r.Path == "" || r.Path == "." {
			continue
		}
		if r.Label == "" {
			r.Label = rootLabel(r.Path)
		}
		key := r.Integration.ID
		if key == "" {
			key = strings.ToLower(r.Integration.Name) + "\x00" + r.Integration.Type
		}
		byIntegration[key] = append(byIntegration[key], r)
	}
	out := []storageRoot{}
	for _, xs := range byIntegration {
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
		return out[i].Integration.Name < out[j].Integration.Name
	})
	return out
}

func walkStorageRoots(roots []storageRoot) ([]model.File, error) {
	contextRoots := collapseStorageRoots(roots)
	physicalCandidates := make([]string, 0, len(contextRoots))
	for _, contextRoot := range contextRoots {
		info, err := os.Stat(contextRoot.Path)
		if err != nil {
			return nil, fmt.Errorf("storage root %s (%s): %w", contextRoot.Path, contextRoot.Integration.Name, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("storage root %s (%s) is not a directory", contextRoot.Path, contextRoot.Integration.Name)
		}
		physicalCandidates = append(physicalCandidates, contextRoot.Path)
	}
	// Overlapping integrations describe extra claims, not extra I/O. Walk each
	// physical subtree once and attach every applicable integration context.
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
					contexts = append(contexts, model.StorageContext{IntegrationID: contextRoot.Integration.ID, IntegrationName: contextRoot.Integration.Name, IntegrationType: contextRoot.Integration.Type, Root: contextRoot.Path, RootLabel: contextRoot.Label})
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

func integrationName(c config.Config, typ, fallback string) string {
	if i, ok := c.FirstIntegration(typ); ok && i.Name != "" {
		return i.Name
	}
	return fallback
}
func integrationID(c config.Config, typ string) string {
	if i, ok := c.FirstIntegration(typ); ok {
		return i.ID
	}
	return typ
}

func walkRoots(paths []string) ([]model.File, error) {
	roots := make([]storageRoot, 0, len(paths))
	for _, p := range paths {
		roots = append(roots, storageRoot{Path: p, Integration: config.Integration{ID: "test", Name: "Storage"}})
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
// inventory -> integration claims -> Claimed/Unclaimed projection. Nothing is
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
	if err := service.ValidateBaseIntegrations(ctx); err != nil {
		return service.setFilesError(err)
	}
	metrics["validation"] = time.Since(stageStarted).Round(time.Millisecond)
	service.mu.RLock()
	media := cloneMedia(service.items)
	torrents := append([]model.Torrent(nil), service.torrents...)
	generation := service.generation
	service.mu.RUnlock()
	torrentsByHash := map[string]model.Torrent{}
	for _, t := range torrents {
		torrentsByHash[strings.ToLower(t.Hash)] = t
	}
	rad := service.rad.WithContext(ctx)
	son := service.son.WithContext(ctx)
	qb := service.qb.WithContext(ctx)
	var radarrRoots, sonarrRoots, qbittorrentRoots []string
	var radarrErr, sonarrErr, qbittorrentErr error
	var wg sync.WaitGroup
	stageStarted = time.Now()
	wg.Add(3)
	go func() { defer wg.Done(); radarrRoots, radarrErr = rad.StorageRoots() }()
	go func() { defer wg.Done(); sonarrRoots, sonarrErr = son.StorageRoots() }()
	go func() { defer wg.Done(); qbittorrentRoots, qbittorrentErr = qb.StorageRoots(torrentsByHash) }()
	wg.Wait()
	if radarrErr != nil {
		return service.setFilesError(fmt.Errorf("radarr storage roots: %w", radarrErr))
	}
	if sonarrErr != nil {
		return service.setFilesError(fmt.Errorf("sonarr storage roots: %w", sonarrErr))
	}
	if qbittorrentErr != nil {
		return service.setFilesError(fmt.Errorf("qbittorrent storage roots: %w", qbittorrentErr))
	}
	metrics["roots"] = time.Since(stageStarted).Round(time.Millisecond)
	var roots []storageRoot
	if i, ok := service.cfg.FirstIntegration("radarr"); ok {
		roots = append(roots, configuredOrDiscoveredRoots(i, radarrRoots)...)
	}
	if i, ok := service.cfg.FirstIntegration("sonarr"); ok {
		roots = append(roots, configuredOrDiscoveredRoots(i, sonarrRoots)...)
	}
	if i, ok := service.cfg.FirstIntegration("qbittorrent"); ok {
		roots = append(roots, configuredOrDiscoveredRoots(i, qbittorrentRoots)...)
	}
	if len(roots) == 0 {
		return service.setFilesError(fmt.Errorf("no integration storage roots are available"))
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
	movieIDs, seriesIDs := []int{}, []int{}
	mediaRootByKey := map[string]string{}
	for _, m := range media {
		mediaRootByKey[fmt.Sprintf("%s:%d", m.Type, m.SourceID)] = m.Path
		if m.Type == model.Movie {
			movieIDs = append(movieIDs, m.SourceID)
		} else if m.Type == model.Series {
			seriesIDs = append(seriesIDs, m.SourceID)
		}
	}
	var radarrFiles []radarr.FileRecord
	var sonarrFiles []sonarr.FileRecord
	var qbittorrentFiles map[string][]qbittorrent.File
	torrentsToFetch := torrentsByHash
	metrics["torrents_cached"] = 0
	metrics["torrents_fetched"] = len(torrentsToFetch)
	stageStarted = time.Now()
	wg = sync.WaitGroup{}
	wg.Add(3)
	go func() { defer wg.Done(); radarrFiles, radarrErr = rad.Files(movieIDs) }()
	go func() { defer wg.Done(); sonarrFiles, sonarrErr = son.Files(seriesIDs) }()
	go func() { defer wg.Done(); qbittorrentFiles, qbittorrentErr = qb.AllFiles(torrentsToFetch) }()
	wg.Wait()
	if radarrErr != nil {
		return service.setFilesError(fmt.Errorf("radarr files: %w", radarrErr))
	}
	if sonarrErr != nil {
		return service.setFilesError(fmt.Errorf("sonarr files: %w", sonarrErr))
	}
	if qbittorrentErr != nil {
		return service.setFilesError(fmt.Errorf("qbittorrent files: %w", qbittorrentErr))
	}
	metrics["claims"] = time.Since(stageStarted).Round(time.Millisecond)
	mediaRefs := []model.MediaFileRef{}
	torrentRefs := []model.TorrentFileRef{}
	claimed := map[string]bool{}
	for _, f := range radarrFiles {
		root := mediaRootByKey[fmt.Sprintf("%s:%d", model.Movie, f.MovieID)]
		if root == "" {
			continue
		}
		p := filepath.Clean(filepath.Join(root, f.Relative))
		mediaRefs = append(mediaRefs, model.MediaFileRef{IntegrationID: integrationID(service.cfg, "radarr"), IntegrationName: integrationName(service.cfg, "radarr", "Movies"), MediaType: model.Movie, MediaID: f.MovieID, Source: "radarr", SourceFileID: f.ID, Path: p})
		if byPath[p] {
			claimed[p] = true
		}
	}
	for _, f := range sonarrFiles {
		root := mediaRootByKey[fmt.Sprintf("%s:%d", model.Series, f.SeriesID)]
		if root == "" {
			continue
		}
		p := filepath.Clean(filepath.Join(root, f.Relative))
		mediaRefs = append(mediaRefs, model.MediaFileRef{IntegrationID: integrationID(service.cfg, "sonarr"), IntegrationName: integrationName(service.cfg, "sonarr", "Series"), MediaType: model.Series, MediaID: f.SeriesID, Source: "sonarr", SourceFileID: f.ID, Path: p, Parts: append([]model.MediaFilePart(nil), f.Parts...)})
		if byPath[p] {
			claimed[p] = true
		}
	}
	for h, xs := range qbittorrentFiles {
		t, ok := torrentsByHash[h]
		if !ok {
			continue
		}
		for _, x := range xs {
			p := filepath.Clean(filepath.Join(t.SavePath, filepath.FromSlash(x.Name)))
			torrentRefs = append(torrentRefs, model.TorrentFileRef{IntegrationID: integrationID(service.cfg, "qbittorrent"), IntegrationName: integrationName(service.cfg, "qbittorrent", t.Client), Client: t.Client, Hash: h, FileIndex: x.Index, Path: p})
			if byPath[p] {
				claimed[p] = true
			}
		}
	}
	unclaimedFiles := []model.UnclaimedFile{}
	for _, f := range files {
		if claimed[f.Path] {
			continue
		}
		u := model.UnclaimedFile{Path: f.Path, SizeBytes: f.SizeBytes, ModifiedAt: f.ModifiedAt, Device: f.Device, Inode: f.Inode, Links: f.Links, ReclaimableKnown: f.IdentityKnown}
		if f.IdentityKnown && f.Links <= 1 {
			u.ReclaimableBytes = f.SizeBytes
		} else if f.IdentityKnown {
			u.SharedBytes = f.SizeBytes
		}
		unclaimedFiles = append(unclaimedFiles, u)
	}
	service.mu.RLock()
	generationCurrent := service.generation == generation
	service.mu.RUnlock()
	if !generationCurrent {
		return service.setFilesError(fmt.Errorf("inventory changed during file reconciliation; result discarded"))
	}
	service.mu.Lock()
	if service.generation != generation {
		service.mu.Unlock()
		return service.setFilesError(fmt.Errorf("inventory changed while file reconciliation was being committed; result discarded"))
	}
	tc := append([]model.Torrent(nil), service.torrents...)
	mc := cloneMedia(service.items)
	applyTorrentFileEstimates(tc, files, torrentRefs)
	applyTorrentMediaHardlinks(tc, mc, files, mediaRefs, torrentRefs)
	applyMediaFileEstimates(mc, files, mediaRefs)
	projectTorrentRelations(mc, tc)
	valuation.ApplyTorrents(tc, service.cfg)
	valuation.ApplyMedia(mc, service.cfg)
	stageStarted = time.Now()
	if service.db != nil {
		// Keep the generation check and its complete durable publication under the
		// Service lock. Inventory cannot advance generation between validation and
		// commit, and SQLite receives either every projection or none of them.
		if e := service.db.PublishReconciliation(generation, files, mediaRefs, torrentRefs, unclaimedFiles, tc, mc); e != nil {
			err := fmt.Errorf("persist reconciled generation: %w", e)
			service.filesErr = err
			service.reliability.FileModel = "stale"
			service.mu.Unlock()
			return err
		}
	}
	now := time.Now()
	service.files = files
	service.mediaFileRefs = mediaRefs
	service.torrentFileRefs = torrentRefs
	service.filesUpdated = now
	service.filesErr = nil
	service.unclaimed = unclaimedFiles
	service.unclaimedUpdated = now
	service.unclaimedErr = nil
	service.reliability.FileModel = "reliable"
	service.fileGeneration = generation
	service.torrents = tc
	service.items = mc
	service.mu.Unlock()
	metrics["publish"] = time.Since(stageStarted).Round(time.Millisecond)
	service.setStageTiming("file reconciliation", started)
	return nil
}
