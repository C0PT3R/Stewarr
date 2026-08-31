package inventory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"connarr/internal/integrations/radarr"
	"connarr/internal/integrations/sonarr"
	"connarr/internal/model"
	"connarr/internal/store"
	"connarr/internal/tasks"
	"connarr/internal/valuation"
)

func (service *Service) promoteTargeted(ctx context.Context, reason string) error {
	tasks.AddMetric(ctx, "promotion", reason)
	return service.ReconcileFiles(ctx)
}

func (service *Service) reconcileTargeted(ctx context.Context) error {
	started := time.Now()
	scope, err := service.reconciliationScope()
	if err != nil {
		return service.promoteTargeted(ctx, "scope_unavailable")
	}
	if scope.Full {
		return service.promoteTargeted(ctx, "scope_requires_full")
	}
	if len(scope.Paths) == 0 {
		return service.promoteTargeted(ctx, "scope_missing")
	}
	tasks.AddMetric(ctx, "mode", "targeted")
	tasks.AddMetric(ctx, "scope_paths", len(scope.Paths))
	tasks.AddMetric(ctx, "scope_owners", len(scope.Owners))
	tasks.AddMetric(ctx, "scope_torrents", len(scope.Torrents))

	service.mu.RLock()
	media := cloneMedia(service.items)
	torrents := append([]model.Torrent(nil), service.torrents...)
	oldFiles := append([]model.File(nil), service.files...)
	oldMediaRefs := append([]model.MediaFileRef(nil), service.mediaFileRefs...)
	oldTorrentRefs := append([]model.TorrentFileRef(nil), service.torrentFileRefs...)
	generation := service.generation
	service.mu.RUnlock()

	stageStarted := time.Now()
	affectedPaths, refreshedFiles, err := refreshTargetedPaths(oldFiles, scope.Paths)
	if err != nil {
		return service.promoteTargeted(ctx, "filesystem_invariant")
	}
	tasks.AddMetric(ctx, "filesystem", time.Since(stageStarted).Round(time.Millisecond))
	tasks.AddMetric(ctx, "paths", len(affectedPaths))
	removedHashes := map[string]bool{}
	for _, hash := range scope.Torrents {
		removedHashes[strings.ToLower(hash)] = true
	}
	filteredTorrents := torrents[:0]
	for _, torrent := range torrents {
		if !removedHashes[strings.ToLower(torrent.Hash)] {
			filteredTorrents = append(filteredTorrents, torrent)
		}
	}
	torrents = filteredTorrents

	ownerSet := map[string]bool{}
	movieIDs, seriesIDs := []int{}, []int{}
	for _, owner := range scope.Owners {
		ownerSet[fmt.Sprintf("%s:%d", owner.Type, owner.ID)] = true
		if owner.Type == model.Movie {
			movieIDs = append(movieIDs, owner.ID)
		} else {
			seriesIDs = append(seriesIDs, owner.ID)
		}
	}
	mediaRootByKey := map[string]string{}
	for _, item := range media {
		mediaRootByKey[fmt.Sprintf("%s:%d", item.Type, item.SourceID)] = item.Path
	}
	stageStarted = time.Now()
	var radarrFiles []radarr.FileRecord
	var sonarrFiles []sonarr.FileRecord
	var radarrErr, sonarrErr error
	var wait sync.WaitGroup
	wait.Add(2)
	go func() { defer wait.Done(); radarrFiles, radarrErr = service.rad.WithContext(ctx).Files(movieIDs) }()
	go func() { defer wait.Done(); sonarrFiles, sonarrErr = service.son.WithContext(ctx).Files(seriesIDs) }()
	wait.Wait()
	if radarrErr != nil || sonarrErr != nil {
		return service.promoteTargeted(ctx, "owner_query_failed")
	}
	replacementMediaRefs := targetedMediaRefs(service, radarrFiles, sonarrFiles, mediaRootByKey)
	if !mediaRefChangesWithinScope(oldMediaRefs, replacementMediaRefs, ownerSet, affectedPaths) {
		return service.promoteTargeted(ctx, "owner_scope_mismatch")
	}
	tasks.AddMetric(ctx, "claims", time.Since(stageStarted).Round(time.Millisecond))

	mediaRefs := make([]model.MediaFileRef, 0, len(oldMediaRefs)+len(replacementMediaRefs))
	for _, ref := range oldMediaRefs {
		if !ownerSet[fmt.Sprintf("%s:%d", ref.MediaType, ref.MediaID)] {
			mediaRefs = append(mediaRefs, ref)
		}
	}
	mediaRefs = append(mediaRefs, replacementMediaRefs...)
	currentTorrents := map[string]bool{}
	currentTorrentByHash := map[string]model.Torrent{}
	for _, torrent := range torrents {
		hash := strings.ToLower(torrent.Hash)
		currentTorrents[hash] = true
		currentTorrentByHash[hash] = torrent
	}
	oldTorrentRefsByHash := map[string][]model.TorrentFileRef{}
	for _, ref := range oldTorrentRefs {
		hash := strings.ToLower(ref.Hash)
		oldTorrentRefsByHash[hash] = append(oldTorrentRefsByHash[hash], ref)
	}
	for hash := range removedHashes {
		refs := oldTorrentRefsByHash[hash]
		if len(refs) == 0 {
			return service.promoteTargeted(ctx, "torrent_scope_mismatch")
		}
		for _, ref := range refs {
			if !affectedPaths[filepath.Clean(ref.Path)] {
				return service.promoteTargeted(ctx, "torrent_scope_mismatch")
			}
		}
	}
	for hash, torrent := range currentTorrentByHash {
		refs := oldTorrentRefsByHash[hash]
		if len(refs) == 0 || strings.TrimSpace(torrent.SavePath) == "" {
			return service.promoteTargeted(ctx, "torrent_inventory_changed")
		}
		for _, ref := range refs {
			if !under(torrent.SavePath, ref.Path) {
				return service.promoteTargeted(ctx, "torrent_inventory_changed")
			}
		}
	}
	torrentRefs := make([]model.TorrentFileRef, 0, len(oldTorrentRefs))
	for _, ref := range oldTorrentRefs {
		hash := strings.ToLower(ref.Hash)
		if currentTorrents[hash] && !removedHashes[hash] {
			torrentRefs = append(torrentRefs, ref)
			continue
		}
		if !affectedPaths[filepath.Clean(ref.Path)] {
			return service.promoteTargeted(ctx, "torrent_scope_mismatch")
		}
		removedHashes[hash] = true
	}

	files := make([]model.File, 0, len(oldFiles)-len(affectedPaths)+len(refreshedFiles))
	for _, file := range oldFiles {
		if !affectedPaths[filepath.Clean(file.Path)] {
			files = append(files, file)
		}
	}
	files = append(files, refreshedFiles...)
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Path) < strings.ToLower(files[j].Path) })
	unmanaged := projectUnmanaged(files, mediaRefs, torrentRefs)

	service.mu.RLock()
	generationCurrent := service.generation == generation
	service.mu.RUnlock()
	if !generationCurrent {
		return service.promoteTargeted(ctx, "generation_changed")
	}
	service.mu.Lock()
	if service.generation != generation {
		service.mu.Unlock()
		return service.promoteTargeted(ctx, "generation_changed")
	}
	tc := make([]model.Torrent, 0, len(service.torrents))
	for _, torrent := range service.torrents {
		if !removedHashes[strings.ToLower(torrent.Hash)] {
			tc = append(tc, torrent)
		}
	}
	mc := cloneMedia(service.items)
	ownerSizes := targetedOwnerSizes(radarrFiles, sonarrFiles)
	for mediaIndex := range mc {
		key := fmt.Sprintf("%s:%d", mc[mediaIndex].Type, mc[mediaIndex].SourceID)
		if ownerSet[key] {
			mc[mediaIndex].SizeBytes = ownerSizes[key]
		}
	}
	applyTorrentFileEstimates(tc, files, torrentRefs)
	applyTorrentMediaHardlinks(tc, mc, files, mediaRefs, torrentRefs)
	applyMediaFileEstimates(mc, files, mediaRefs)
	attachSeasons(mc, mediaRefs, files)
	applySeasonFileEstimates(mc, files, mediaRefs)
	projectTorrentRelations(mc, tc)
	valuation.ApplyTorrents(tc, service.cfg)
	valuation.ApplyMedia(mc, service.cfg)

	stageStarted = time.Now()
	if service.db != nil {
		owners := make([]store.MediaIdentity, 0, len(scope.Owners))
		for _, owner := range scope.Owners {
			owners = append(owners, store.MediaIdentity{Kind: owner.Type, SourceID: owner.ID})
		}
		paths := mapKeys(affectedPaths)
		updatedUnmanaged := make([]model.UnmanagedFile, 0)
		for _, file := range unmanaged {
			if affectedPaths[filepath.Clean(file.Path)] {
				updatedUnmanaged = append(updatedUnmanaged, file)
			}
		}
		if err := service.db.PublishReconciliationDelta(store.ReconciliationDelta{
			Paths: paths, Files: refreshedFiles, MediaOwners: owners, MediaRefs: replacementMediaRefs,
			RemovedTorrentHashes: mapKeys(removedHashes), Unmanaged: updatedUnmanaged,
			Torrents: tc, Media: mc, Generation: generation, ScopeMetadataKey: reconciliationScopeKey,
		}); err != nil {
			service.filesErr = fmt.Errorf("persist targeted reconciliation: %w", err)
			service.reliability.FileModel = "stale"
			service.mu.Unlock()
			return service.filesErr
		}
	}
	now := time.Now()
	service.files, service.mediaFileRefs, service.torrentFileRefs = files, mediaRefs, torrentRefs
	service.filesUpdated, service.filesErr = now, nil
	service.unmanaged, service.unmanagedUpdated, service.unmanagedErr = unmanaged, now, nil
	service.reliability.FileModel = "reliable"
	service.fileGeneration = generation
	service.fileTopologyVersion++
	service.torrents, service.items = tc, mc
	// Keep the base-inventory fingerprint in sync with the state this targeted
	// reconciliation just published. RefreshAfterMutation deliberately skips a
	// full base refresh for a targeted scope, so without this the next
	// periodic Refresh() would see this mutation as a surprise change against
	// a stale fingerprint baseline and flip FileModel back to stale for a
	// change that was already correctly reconciled here.
	service.baseFingerprint = inventoryFingerprint(mc, tc)
	service.hasBaseFingerprint = true
	service.mu.Unlock()
	tasks.AddMetric(ctx, "publish", time.Since(stageStarted).Round(time.Millisecond))
	tasks.AddMetric(ctx, "total", time.Since(started).Round(time.Millisecond))
	service.setStageTiming("file reconciliation", started)
	service.publishChange()
	return nil
}

func targetedOwnerSizes(radarrFiles []radarr.FileRecord, sonarrFiles []sonarr.FileRecord) map[string]int64 {
	sizes := map[string]int64{}
	for _, file := range radarrFiles {
		sizes[fmt.Sprintf("%s:%d", model.Movie, file.MovieID)] += file.Size
	}
	for _, file := range sonarrFiles {
		sizes[fmt.Sprintf("%s:%d", model.Series, file.SeriesID)] += file.Size
	}
	return sizes
}

func refreshTargetedPaths(files []model.File, requested []string) (map[string]bool, []model.File, error) {
	byPath := map[string]model.File{}
	byIdentity := map[[2]uint64][]string{}
	for _, file := range files {
		path := filepath.Clean(file.Path)
		byPath[path] = file
		if file.IdentityKnown {
			byIdentity[[2]uint64{file.Device, file.Inode}] = append(byIdentity[[2]uint64{file.Device, file.Inode}], path)
		}
	}
	affected := map[string]bool{}
	for _, requestedPath := range requested {
		path := filepath.Clean(requestedPath)
		old, exists := byPath[path]
		if !exists {
			return nil, nil, fmt.Errorf("unknown scoped path %s", path)
		}
		affected[path] = true
		if old.IdentityKnown {
			for _, peer := range byIdentity[[2]uint64{old.Device, old.Inode}] {
				affected[peer] = true
			}
		}
	}
	refreshed := []model.File{}
	for path := range affected {
		old := byPath[path]
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("stat scoped path %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, nil, fmt.Errorf("scoped path is no longer a regular file: %s", path)
		}
		stat, known := info.Sys().(*syscall.Stat_t)
		if old.IdentityKnown && (!known || old.Device != uint64(stat.Dev) || old.Inode != uint64(stat.Ino)) {
			return nil, nil, fmt.Errorf("identity changed for scoped path %s", path)
		}
		file := model.File{Path: path, SizeBytes: info.Size(), Exists: true, ModifiedAt: info.ModTime(), StorageContexts: append([]model.StorageContext(nil), old.StorageContexts...)}
		if known {
			file.IdentityKnown, file.Device, file.Inode, file.Links = true, uint64(stat.Dev), uint64(stat.Ino), uint64(stat.Nlink)
		}
		refreshed = append(refreshed, file)
	}
	return affected, refreshed, nil
}

func targetedMediaRefs(service *Service, radarrFiles []radarr.FileRecord, sonarrFiles []sonarr.FileRecord, roots map[string]string) []model.MediaFileRef {
	refs := []model.MediaFileRef{}
	for _, file := range radarrFiles {
		root := roots[fmt.Sprintf("%s:%d", model.Movie, file.MovieID)]
		refs = append(refs, model.MediaFileRef{IntegrationID: integrationID(service.cfg, "radarr"), IntegrationName: integrationName(service.cfg, "radarr", "Movies"), MediaType: model.Movie, MediaID: file.MovieID, Source: "radarr", SourceFileID: file.ID, Path: filepath.Clean(filepath.Join(root, file.Relative))})
	}
	for _, file := range sonarrFiles {
		root := roots[fmt.Sprintf("%s:%d", model.Series, file.SeriesID)]
		refs = append(refs, model.MediaFileRef{IntegrationID: integrationID(service.cfg, "sonarr"), IntegrationName: integrationName(service.cfg, "sonarr", "Series"), MediaType: model.Series, MediaID: file.SeriesID, Source: "sonarr", SourceFileID: file.ID, Path: filepath.Clean(filepath.Join(root, file.Relative)), Parts: append([]model.MediaFilePart(nil), file.Parts...), AddedAt: file.DateAdded})
	}
	return refs
}

func mediaRefChangesWithinScope(oldRefs, newRefs []model.MediaFileRef, owners map[string]bool, paths map[string]bool) bool {
	oldPaths, newPaths := map[string]bool{}, map[string]bool{}
	for _, ref := range oldRefs {
		if owners[fmt.Sprintf("%s:%d", ref.MediaType, ref.MediaID)] {
			oldPaths[filepath.Clean(ref.Path)] = true
		}
	}
	for _, ref := range newRefs {
		newPaths[filepath.Clean(ref.Path)] = true
	}
	for path := range oldPaths {
		if !newPaths[path] && !paths[path] {
			return false
		}
	}
	for path := range newPaths {
		if !oldPaths[path] && !paths[path] {
			return false
		}
	}
	return true
}

func projectUnmanaged(files []model.File, mediaRefs []model.MediaFileRef, torrentRefs []model.TorrentFileRef) []model.UnmanagedFile {
	claimed := map[string]bool{}
	for _, ref := range mediaRefs {
		claimed[filepath.Clean(ref.Path)] = true
	}
	for _, ref := range torrentRefs {
		claimed[filepath.Clean(ref.Path)] = true
	}
	unmanaged := []model.UnmanagedFile{}
	for _, file := range files {
		if claimed[filepath.Clean(file.Path)] {
			continue
		}
		item := model.UnmanagedFile{Path: file.Path, SizeBytes: file.SizeBytes, ModifiedAt: file.ModifiedAt, Device: file.Device, Inode: file.Inode, Links: file.Links, ReclaimableKnown: file.IdentityKnown, StorageContexts: append([]model.StorageContext(nil), file.StorageContexts...)}
		if file.IdentityKnown && file.Links <= 1 {
			item.ReclaimableBytes = file.SizeBytes
		} else if file.IdentityKnown {
			item.SharedBytes = file.SizeBytes
		}
		unmanaged = append(unmanaged, item)
	}
	return unmanaged
}

func mapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
