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

	"stewarr/internal/config"
	"stewarr/internal/integrations/radarr"
	"stewarr/internal/integrations/sonarr"
	"stewarr/internal/model"
	"stewarr/internal/store"
	"stewarr/internal/tasks"
	"stewarr/internal/valuation"
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

	service.mu.RLock()
	radInstances := service.cfg.ServicesOfType("radarr")
	sonInstances := service.cfg.ServicesOfType("sonarr")
	radClients := service.rad
	sonClients := service.son
	service.mu.RUnlock()
	// A scope's owners predate multi-instance support and may carry no
	// ServiceID; that's only safe to infer when exactly one instance of
	// the relevant type is configured. Any owner this can't resolve makes the
	// whole scope untrustworthy — fall back to a full reconciliation rather
	// than risk querying the wrong instance's client.
	resolveInstance := func(owner ReconciliationOwner, instances []config.Service) (string, bool) {
		if owner.ServiceID != "" {
			return owner.ServiceID, true
		}
		if len(instances) == 1 {
			return instances[0].ID, true
		}
		return "", false
	}
	ownerSet := map[string]bool{}
	movieIDsByInstance := map[string][]int{}
	seriesIDsByInstance := map[string][]int{}
	for _, owner := range scope.Owners {
		if owner.Type == model.Movie {
			id, ok := resolveInstance(owner, radInstances)
			if !ok {
				return service.promoteTargeted(ctx, "owner_instance_ambiguous")
			}
			ownerSet[fmt.Sprintf("%s:%s:%d", owner.Type, id, owner.ID)] = true
			movieIDsByInstance[id] = append(movieIDsByInstance[id], owner.ID)
		} else {
			id, ok := resolveInstance(owner, sonInstances)
			if !ok {
				return service.promoteTargeted(ctx, "owner_instance_ambiguous")
			}
			ownerSet[fmt.Sprintf("%s:%s:%d", owner.Type, id, owner.ID)] = true
			seriesIDsByInstance[id] = append(seriesIDsByInstance[id], owner.ID)
		}
	}
	mediaRootByKey := map[ownerKey]string{}
	for _, item := range media {
		mediaRootByKey[ownerKey{ServiceID: item.ServiceID, OwnerID: item.SourceID}] = item.Path
	}
	stageStarted = time.Now()
	radInstanceByID := map[string]config.Service{}
	for _, svc := range radInstances {
		radInstanceByID[svc.ID] = svc
	}
	sonInstanceByID := map[string]config.Service{}
	for _, svc := range sonInstances {
		sonInstanceByID[svc.ID] = svc
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
	radFetches := make([]radarrFilesFetch, 0, len(movieIDsByInstance))
	for id := range movieIDsByInstance {
		radFetches = append(radFetches, radarrFilesFetch{svcID: id})
	}
	sonFetches := make([]sonarrFilesFetch, 0, len(seriesIDsByInstance))
	for id := range seriesIDsByInstance {
		sonFetches = append(sonFetches, sonarrFilesFetch{svcID: id})
	}
	var wait sync.WaitGroup
	wait.Add(len(radFetches) + len(sonFetches))
	for i := range radFetches {
		go func(i int) {
			defer wait.Done()
			id := radFetches[i].svcID
			radFetches[i].files, radFetches[i].err = radClients[id].WithContext(ctx).Files(movieIDsByInstance[id])
		}(i)
	}
	for i := range sonFetches {
		go func(i int) {
			defer wait.Done()
			id := sonFetches[i].svcID
			sonFetches[i].files, sonFetches[i].err = sonClients[id].WithContext(ctx).Files(seriesIDsByInstance[id])
		}(i)
	}
	wait.Wait()
	for _, f := range radFetches {
		if f.err != nil {
			return service.promoteTargeted(ctx, "owner_query_failed")
		}
	}
	for _, f := range sonFetches {
		if f.err != nil {
			return service.promoteTargeted(ctx, "owner_query_failed")
		}
	}
	var replacementMediaRefs []model.MediaFileRef
	for _, f := range radFetches {
		replacementMediaRefs = append(replacementMediaRefs, radarrMediaRefs(radInstanceByID[f.svcID], f.files, mediaRootByKey)...)
	}
	for _, f := range sonFetches {
		replacementMediaRefs = append(replacementMediaRefs, sonarrMediaRefs(sonInstanceByID[f.svcID], f.files, mediaRootByKey)...)
	}
	if !mediaRefChangesWithinScope(oldMediaRefs, replacementMediaRefs, ownerSet, affectedPaths) {
		return service.promoteTargeted(ctx, "owner_scope_mismatch")
	}
	tasks.AddMetric(ctx, "claims", time.Since(stageStarted).Round(time.Millisecond))

	mediaRefs := make([]model.MediaFileRef, 0, len(oldMediaRefs)+len(replacementMediaRefs))
	for _, ref := range oldMediaRefs {
		if !ownerSet[fmt.Sprintf("%s:%s:%d", ref.MediaType, ref.ServiceID, ref.MediaID)] {
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
	tc := make([]model.Torrent, 0, len(service.torrents))
	for _, torrent := range service.torrents {
		if !removedHashes[strings.ToLower(torrent.Hash)] {
			tc = append(tc, torrent)
		}
	}
	mc := cloneMedia(service.items)
	cfg := service.cfg
	service.mu.RUnlock()
	if !generationCurrent {
		return service.promoteTargeted(ctx, "generation_changed")
	}
	ownerSizes := map[string]int64{}
	for _, f := range radFetches {
		for _, file := range f.files {
			ownerSizes[fmt.Sprintf("%s:%s:%d", model.Movie, f.svcID, file.MovieID)] += file.Size
		}
	}
	for _, f := range sonFetches {
		for _, file := range f.files {
			ownerSizes[fmt.Sprintf("%s:%s:%d", model.Series, f.svcID, file.SeriesID)] += file.Size
		}
	}
	for mediaIndex := range mc {
		key := fmt.Sprintf("%s:%s:%d", mc[mediaIndex].Type, mc[mediaIndex].ServiceID, mc[mediaIndex].SourceID)
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
	valuation.ApplyTorrents(tc, cfg)
	valuation.ApplyMedia(mc, cfg)

	stageStarted = time.Now()
	// publishMu serializes this generation-check-then-write sequence against
	// every other reconciliation publisher, without holding mu (which every
	// page load needs just to read cached state) across the database write.
	service.publishMu.Lock()
	service.mu.RLock()
	generationCurrent = service.generation == generation
	service.mu.RUnlock()
	if !generationCurrent {
		// promoteTargeted recurses into ReconcileFiles, which itself acquires
		// publishMu — release it first, or this self-deadlocks (and, since
		// publishMu would then never unlock, every other reconciliation
		// publisher would hang behind it forever too).
		service.publishMu.Unlock()
		return service.promoteTargeted(ctx, "generation_changed")
	}
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
			service.mu.Lock()
			service.filesErr = fmt.Errorf("persist targeted reconciliation: %w", err)
			service.reliability.FileModel = "stale"
			service.mu.Unlock()
			service.publishMu.Unlock()
			return service.filesErr
		}
	}
	service.mu.Lock()
	if service.generation != generation {
		service.mu.Unlock()
		service.publishMu.Unlock()
		return service.promoteTargeted(ctx, "generation_changed")
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
	service.publishMu.Unlock()
	tasks.AddMetric(ctx, "publish", time.Since(stageStarted).Round(time.Millisecond))
	tasks.AddMetric(ctx, "total", time.Since(started).Round(time.Millisecond))
	service.setStageTiming("file reconciliation", started)
	service.publishChange()
	return nil
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

// radarrMediaRefs/sonarrMediaRefs build MediaFileRefs for one specific
// configured instance's fetched files, tagged with that instance's own ID —
// callers with more than one configured instance of a type must call these
// once per instance and merge the results, never merge raw file lists first
// (movie/series IDs are only unique within one instance).
func radarrMediaRefs(svc config.Service, files []radarr.FileRecord, roots map[ownerKey]string) []model.MediaFileRef {
	refs := []model.MediaFileRef{}
	for _, file := range files {
		root := roots[ownerKey{ServiceID: svc.ID, OwnerID: file.MovieID}]
		refs = append(refs, model.MediaFileRef{ServiceID: svc.ID, ServiceName: svc.Name, MediaType: model.Movie, MediaID: file.MovieID, Source: "radarr", SourceFileID: file.ID, Path: filepath.Clean(filepath.Join(root, file.Relative))})
	}
	return refs
}

func sonarrMediaRefs(svc config.Service, files []sonarr.FileRecord, roots map[ownerKey]string) []model.MediaFileRef {
	refs := []model.MediaFileRef{}
	for _, file := range files {
		root := roots[ownerKey{ServiceID: svc.ID, OwnerID: file.SeriesID}]
		refs = append(refs, model.MediaFileRef{ServiceID: svc.ID, ServiceName: svc.Name, MediaType: model.Series, MediaID: file.SeriesID, Source: "sonarr", SourceFileID: file.ID, Path: filepath.Clean(filepath.Join(root, file.Relative)), Parts: append([]model.MediaFilePart(nil), file.Parts...), AddedAt: file.DateAdded})
	}
	return refs
}

func mediaRefChangesWithinScope(oldRefs, newRefs []model.MediaFileRef, owners map[string]bool, paths map[string]bool) bool {
	oldPaths, newPaths := map[string]bool{}, map[string]bool{}
	for _, ref := range oldRefs {
		if owners[fmt.Sprintf("%s:%s:%d", ref.MediaType, ref.ServiceID, ref.MediaID)] {
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
