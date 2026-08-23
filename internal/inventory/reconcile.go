package inventory

import (
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
	"togetharr/internal/config"
	"togetharr/internal/jellyfin"
	"togetharr/internal/model"
	"togetharr/internal/qbittorrent"
	"togetharr/internal/radarr"
	"togetharr/internal/seerr"
	"togetharr/internal/sonarr"
	"togetharr/internal/valuation"
)

// ValidateIntegrations is a hard preflight: no task may start from unchecked
// integration state. Optional/unconfigured integrations are skipped.
func (s *Service) ValidateIntegrations(ctx context.Context) error {
	_ = ctx
	type check struct {
		name string
		fn   func() error
	}
	checks := []check{}
	for _, i := range s.cfg.Integrations {
		i := i
		if !i.Enabled() {
			continue
		}
		var fn func() error
		switch i.Type {
		case "radarr":
			fn = radarr.New(i.URL, i.APIKey).Validate
		case "sonarr":
			fn = sonarr.New(i.URL, i.APIKey).Validate
		case "jellyfin":
			fn = jellyfin.New(i.URL, i.APIKey).Validate
		case "seerr":
			fn = seerr.New(i.URL, i.APIKey).Validate
		case "qbittorrent":
			fn = qbittorrent.New(i.Name, i.URL, i.Username, i.Password, i.APIKey).Validate
		default:
			fn = func() error {
				st, err := os.Stat(i.RootPath)
				if err != nil {
					return fmt.Errorf("root_path %s: %w", i.RootPath, err)
				}
				if !st.IsDir() {
					return fmt.Errorf("root_path %s is not a directory", i.RootPath)
				}
				return nil
			}
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
		s.setStatus(r.c.name, true, r.err == nil, r.err)
		if r.err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", r.c.name, r.err))
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("integration validation failed: %s", strings.Join(errs, "; "))
	}
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
	byPath := map[string]*model.File{}
	for _, sr := range collapseStorageRoots(roots) {
		root := sr.Path
		i, e := os.Stat(root)
		if e != nil {
			return nil, fmt.Errorf("storage root %s (%s): %w", root, sr.Integration.Name, e)
		}
		if !i.IsDir() {
			return nil, fmt.Errorf("storage root %s (%s) is not a directory", root, sr.Integration.Name)
		}
		e = filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
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
			ctx := model.StorageContext{IntegrationID: sr.Integration.ID, IntegrationName: sr.Integration.Name, Root: root, RootLabel: sr.Label}
			if existing := byPath[p]; existing != nil {
				dup := false
				for _, c := range existing.StorageContexts {
					if c.IntegrationID == ctx.IntegrationID && c.Root == ctx.Root {
						dup = true
						break
					}
				}
				if !dup {
					existing.StorageContexts = append(existing.StorageContexts, ctx)
				}
				return nil
			}
			f := model.File{Path: p, SizeBytes: i.Size(), Exists: true, ModifiedAt: i.ModTime(), StorageContexts: []model.StorageContext{ctx}}
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
			return nil, fmt.Errorf("scan storage root %s (%s): %w", root, sr.Integration.Name, e)
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

// ReconcileFiles performs one authoritative generation: roots -> physical
// filesystem inventory -> integration claims -> Claimed/Unclaimed projection.
// Nothing is published unless every phase succeeds.
func (s *Service) ReconcileFiles(ctx context.Context) error {
	if err := s.ValidateIntegrations(ctx); err != nil {
		return s.setFilesError(err)
	}
	media, _, _ := s.Snapshot()
	torrents := s.TorrentSnapshot()
	tm := map[string]model.Torrent{}
	for _, t := range torrents {
		tm[strings.ToLower(t.Hash)] = t
	}
	var rr, sr, qr []string
	var re, se, qe error
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); rr, re = s.rad.StorageRoots() }()
	go func() { defer wg.Done(); sr, se = s.son.StorageRoots() }()
	go func() { defer wg.Done(); qr, qe = s.qb.StorageRoots(tm) }()
	wg.Wait()
	if re != nil {
		return s.setFilesError(fmt.Errorf("radarr storage roots: %w", re))
	}
	if se != nil {
		return s.setFilesError(fmt.Errorf("sonarr storage roots: %w", se))
	}
	if qe != nil {
		return s.setFilesError(fmt.Errorf("qbittorrent storage roots: %w", qe))
	}
	var roots []storageRoot
	if i, ok := s.cfg.FirstIntegration("radarr"); ok {
		for _, p := range rr {
			roots = append(roots, storageRoot{Path: p, Integration: i})
		}
	}
	if i, ok := s.cfg.FirstIntegration("sonarr"); ok {
		for _, p := range sr {
			roots = append(roots, storageRoot{Path: p, Integration: i})
		}
	}
	if i, ok := s.cfg.FirstIntegration("qbittorrent"); ok {
		for _, p := range qr {
			roots = append(roots, storageRoot{Path: p, Integration: i})
		}
	}
	// Configured fallback roots are used only by integration types that do not
	// expose authoritative roots through their API. API-owned roots are facts and
	// are never silently overridden by configuration.
	for _, i := range s.cfg.Integrations {
		if i.RootPath == "" {
			continue
		}
		switch i.Type {
		case "radarr", "sonarr", "qbittorrent":
			continue
		}
		roots = append(roots, storageRoot{Path: i.RootPath, Integration: i})
	}
	if len(roots) == 0 {
		return s.setFilesError(fmt.Errorf("no integration storage roots are available"))
	}
	files, err := walkStorageRoots(roots)
	if err != nil {
		return s.setFilesError(err)
	}
	byPath := map[string]bool{}
	for _, f := range files {
		byPath[f.Path] = true
	}
	mids, sids := []int{}, []int{}
	mroot := map[string]string{}
	for _, m := range media {
		mroot[fmt.Sprintf("%s:%d", m.Type, m.SourceID)] = m.Path
		if m.Type == model.Movie {
			mids = append(mids, m.SourceID)
		} else if m.Type == model.Series {
			sids = append(sids, m.SourceID)
		}
	}
	var rf []radarr.FileRecord
	var sf []sonarr.FileRecord
	var qf map[string][]qbittorrent.File
	wg = sync.WaitGroup{}
	wg.Add(3)
	go func() { defer wg.Done(); rf, re = s.rad.Files(mids) }()
	go func() { defer wg.Done(); sf, se = s.son.Files(sids) }()
	go func() { defer wg.Done(); qf, qe = s.qb.AllFiles(tm) }()
	wg.Wait()
	if re != nil {
		return s.setFilesError(fmt.Errorf("radarr files: %w", re))
	}
	if se != nil {
		return s.setFilesError(fmt.Errorf("sonarr files: %w", se))
	}
	if qe != nil {
		return s.setFilesError(fmt.Errorf("qbittorrent files: %w", qe))
	}
	mr := []model.MediaFileRef{}
	tr := []model.TorrentFileRef{}
	claimed := map[string]bool{}
	for _, f := range rf {
		root := mroot[fmt.Sprintf("%s:%d", model.Movie, f.MovieID)]
		if root == "" {
			continue
		}
		p := filepath.Clean(filepath.Join(root, f.Relative))
		mr = append(mr, model.MediaFileRef{IntegrationID: integrationID(s.cfg, "radarr"), IntegrationName: integrationName(s.cfg, "radarr", "Movies"), MediaType: model.Movie, MediaID: f.MovieID, Source: "radarr", SourceFileID: f.ID, Path: p})
		if byPath[p] {
			claimed[p] = true
		}
	}
	for _, f := range sf {
		root := mroot[fmt.Sprintf("%s:%d", model.Series, f.SeriesID)]
		if root == "" {
			continue
		}
		p := filepath.Clean(filepath.Join(root, f.Relative))
		mr = append(mr, model.MediaFileRef{IntegrationID: integrationID(s.cfg, "sonarr"), IntegrationName: integrationName(s.cfg, "sonarr", "Series"), MediaType: model.Series, MediaID: f.SeriesID, Source: "sonarr", SourceFileID: f.ID, Path: p, Parts: append([]model.MediaFilePart(nil), f.Parts...)})
		if byPath[p] {
			claimed[p] = true
		}
	}
	for h, xs := range qf {
		t, ok := tm[h]
		if !ok {
			continue
		}
		for _, x := range xs {
			p := filepath.Clean(filepath.Join(t.SavePath, filepath.FromSlash(x.Name)))
			tr = append(tr, model.TorrentFileRef{IntegrationID: integrationID(s.cfg, "qbittorrent"), IntegrationName: integrationName(s.cfg, "qbittorrent", t.Client), Client: t.Client, Hash: h, FileIndex: x.Index, Path: p})
			if byPath[p] {
				claimed[p] = true
			}
		}
	}
	uf := []model.UnclaimedFile{}
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
		uf = append(uf, u)
	}
	if s.db != nil {
		if e := s.db.ReplaceFiles(files, mr, tr); e != nil {
			return s.setFilesError(e)
		}
		if e := s.db.SaveUnclaimedFiles(uf); e != nil {
			return s.setFilesError(e)
		}
	}
	now := time.Now()
	s.mu.Lock()
	s.files = files
	s.mediaFileRefs = mr
	s.torrentFileRefs = tr
	s.filesUpdated = now
	s.filesErr = nil
	s.unclaimed = uf
	s.unclaimedUpdated = now
	s.unclaimedErr = nil
	applyTorrentFileEstimates(s.torrents, files, tr)
	projectTorrentRelations(s.items, s.torrents)
	valuation.ApplyTorrents(s.torrents, s.cfg)
	valuation.ApplyMedia(s.items, s.cfg)
	tc := append([]model.Torrent(nil), s.torrents...)
	mc := cloneMedia(s.items)
	s.mu.Unlock()
	if s.db != nil {
		_ = s.db.SaveTorrents(tc)
		_ = s.db.SaveMedia(mc)
	}
	return nil
}
