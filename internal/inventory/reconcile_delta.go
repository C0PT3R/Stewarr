package inventory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"connarr/internal/integrations/qbittorrent"
	"connarr/internal/integrations/radarr"
	"connarr/internal/integrations/sonarr"
	"connarr/internal/model"
	"connarr/internal/store"
)

// inventoryDelta is the set of media/torrent identities whose base-catalog
// facts changed between two consecutive Refresh() cycles, split by what kind
// of file-topology work each requires.
type inventoryDelta struct {
	newOwners     []ReconciliationOwner
	changedOwners []ReconciliationOwner
	removedOwners []ReconciliationOwner

	newTorrentHashes     []string
	changedTorrentHashes []string
	removedTorrentHashes []string
}

func (d inventoryDelta) empty() bool {
	return len(d.newOwners) == 0 && len(d.changedOwners) == 0 && len(d.removedOwners) == 0 &&
		len(d.newTorrentHashes) == 0 && len(d.changedTorrentHashes) == 0 && len(d.removedTorrentHashes) == 0
}

func mediaKey(m model.Media) string { return fmt.Sprintf("%s:%d", m.Type, m.SourceID) }

// computeInventoryDelta diffs the previous and freshly fetched base catalogs
// by identity (Type/SourceID for media, Hash for torrents) so a periodic
// Refresh() can reconcile file topology for exactly what changed, instead of
// re-verifying or waiting on the whole library.
func computeInventoryDelta(oldMedia, newMedia []model.Media, oldTorrents, newTorrents []model.Torrent) inventoryDelta {
	oldByKey := make(map[string]model.Media, len(oldMedia))
	for _, m := range oldMedia {
		oldByKey[mediaKey(m)] = m
	}
	newByKey := make(map[string]model.Media, len(newMedia))
	for _, m := range newMedia {
		newByKey[mediaKey(m)] = m
	}
	var delta inventoryDelta
	for key, m := range newByKey {
		old, existed := oldByKey[key]
		if !existed {
			delta.newOwners = append(delta.newOwners, ReconciliationOwner{Type: m.Type, ID: m.SourceID})
			continue
		}
		if filepath.Clean(old.Path) != filepath.Clean(m.Path) || old.SizeBytes != m.SizeBytes {
			delta.changedOwners = append(delta.changedOwners, ReconciliationOwner{Type: m.Type, ID: m.SourceID})
		}
	}
	for key, m := range oldByKey {
		if _, exists := newByKey[key]; !exists {
			delta.removedOwners = append(delta.removedOwners, ReconciliationOwner{Type: m.Type, ID: m.SourceID})
		}
	}

	oldTorrentByHash := make(map[string]model.Torrent, len(oldTorrents))
	for _, t := range oldTorrents {
		oldTorrentByHash[strings.ToLower(t.Hash)] = t
	}
	newTorrentByHash := make(map[string]model.Torrent, len(newTorrents))
	for _, t := range newTorrents {
		newTorrentByHash[strings.ToLower(t.Hash)] = t
	}
	for hash, t := range newTorrentByHash {
		old, existed := oldTorrentByHash[hash]
		if !existed {
			delta.newTorrentHashes = append(delta.newTorrentHashes, hash)
			continue
		}
		if filepath.Clean(old.SavePath) != filepath.Clean(t.SavePath) {
			delta.changedTorrentHashes = append(delta.changedTorrentHashes, hash)
		}
	}
	for hash := range oldTorrentByHash {
		if _, exists := newTorrentByHash[hash]; !exists {
			delta.removedTorrentHashes = append(delta.removedTorrentHashes, hash)
		}
	}

	sort.Slice(delta.newOwners, func(i, j int) bool { return ownerLess(delta.newOwners[i], delta.newOwners[j]) })
	sort.Slice(delta.changedOwners, func(i, j int) bool { return ownerLess(delta.changedOwners[i], delta.changedOwners[j]) })
	sort.Slice(delta.removedOwners, func(i, j int) bool { return ownerLess(delta.removedOwners[i], delta.removedOwners[j]) })
	sort.Strings(delta.newTorrentHashes)
	sort.Strings(delta.changedTorrentHashes)
	sort.Strings(delta.removedTorrentHashes)
	return delta
}

func ownerLess(a, b ReconciliationOwner) bool {
	if a.Type != b.Type {
		return a.Type < b.Type
	}
	return a.ID < b.ID
}

// deltaFileTopology is the file-topology publication a successful
// reconcileInventoryDelta produces: the full replacement files/refs/unmanaged
// slices ready to publish in memory, plus the narrower set of rows that
// actually need touching in the database.
type deltaFileTopology struct {
	files       []model.File
	mediaRefs   []model.MediaFileRef
	torrentRefs []model.TorrentFileRef
	unmanaged   []model.UnmanagedFile

	// The DB delta API deletes rows for exactly the keys named below, then
	// inserts exactly these rows — never the full in-memory slices above.
	// Passing the full slices would try to re-insert every untouched row
	// still present in the database and violate its path/owner uniqueness.
	affectedPaths        []string
	filesToInsert        []model.File
	mediaOwners          []store.MediaIdentity
	mediaRefsToInsert    []model.MediaFileRef
	unmanagedToInsert    []model.UnmanagedFile
	torrentRefsToInsert  []model.TorrentFileRef
	removedTorrentHashes []string
}

// reconcileInventoryDelta re-verifies file topology for exactly the media and
// torrents an inventoryDelta identifies as new, changed, or removed. It walks
// only those items' own directories and queries only their owning
// integration for their file list, so its cost scales with the size of the
// delta rather than the whole library — unlike a full files reconciliation,
// which re-walks every configured root. It does no relationship/valuation
// computation and does not publish anything; the caller (Refresh) merges the
// result into its own atomic publish alongside the base catalog it belongs to.
func (service *Service) reconcileInventoryDelta(ctx context.Context, delta inventoryDelta, oldMedia, newMedia []model.Media, oldTorrents, newTorrents []model.Torrent) (deltaFileTopology, error) {
	var result deltaFileTopology

	service.mu.RLock()
	oldFiles := append([]model.File(nil), service.files...)
	oldMediaRefs := append([]model.MediaFileRef(nil), service.mediaFileRefs...)
	oldTorrentRefs := append([]model.TorrentFileRef(nil), service.torrentFileRefs...)
	service.mu.RUnlock()

	newMediaByKey := make(map[string]model.Media, len(newMedia))
	for _, m := range newMedia {
		newMediaByKey[mediaKey(m)] = m
	}
	oldMediaByKey := make(map[string]model.Media, len(oldMedia))
	for _, m := range oldMedia {
		oldMediaByKey[mediaKey(m)] = m
	}
	newTorrentByHash := make(map[string]model.Torrent, len(newTorrents))
	for _, t := range newTorrents {
		newTorrentByHash[strings.ToLower(t.Hash)] = t
	}
	oldTorrentByHash := make(map[string]model.Torrent, len(oldTorrents))
	for _, t := range oldTorrents {
		oldTorrentByHash[strings.ToLower(t.Hash)] = t
	}

	replacedOwners := append(append([]ReconciliationOwner{}, delta.newOwners...), delta.changedOwners...)
	ownerSet := map[string]bool{}
	movieIDs, seriesIDs := []int{}, []int{}
	for _, owner := range replacedOwners {
		ownerSet[fmt.Sprintf("%s:%d", owner.Type, owner.ID)] = true
		if owner.Type == model.Movie {
			movieIDs = append(movieIDs, owner.ID)
		} else {
			seriesIDs = append(seriesIDs, owner.ID)
		}
	}
	removedOwnerSet := map[string]bool{}
	for _, owner := range delta.removedOwners {
		removedOwnerSet[fmt.Sprintf("%s:%d", owner.Type, owner.ID)] = true
	}

	replacedTorrentHashes := append(append([]string{}, delta.newTorrentHashes...), delta.changedTorrentHashes...)
	torrentHashSet := map[string]bool{}
	for _, hash := range replacedTorrentHashes {
		torrentHashSet[hash] = true
	}
	removedTorrentSet := map[string]bool{}
	for _, hash := range delta.removedTorrentHashes {
		removedTorrentSet[hash] = true
	}

	rad := service.rad.WithContext(ctx)
	son := service.son.WithContext(ctx)
	qb := service.qb.WithContext(ctx)

	var radarrFiles []radarr.FileRecord
	var sonarrFiles []sonarr.FileRecord
	var radarrErr, sonarrErr, qbErr error
	var wait sync.WaitGroup
	wait.Add(3)
	go func() {
		defer wait.Done()
		if len(movieIDs) > 0 {
			radarrFiles, radarrErr = rad.Files(movieIDs)
		}
	}()
	go func() {
		defer wait.Done()
		if len(seriesIDs) > 0 {
			sonarrFiles, sonarrErr = son.Files(seriesIDs)
		}
	}()
	scopedTorrents := map[string]model.Torrent{}
	for hash := range torrentHashSet {
		if t, ok := newTorrentByHash[hash]; ok {
			scopedTorrents[hash] = t
		}
	}
	var qbittorrentFiles map[string][]qbittorrent.File
	go func() {
		defer wait.Done()
		if len(scopedTorrents) > 0 {
			qbittorrentFiles, qbErr = qb.AllFiles(scopedTorrents)
		}
	}()
	wait.Wait()
	if radarrErr != nil {
		return result, fmt.Errorf("radarr files: %w", radarrErr)
	}
	if sonarrErr != nil {
		return result, fmt.Errorf("sonarr files: %w", sonarrErr)
	}
	if qbErr != nil {
		return result, fmt.Errorf("qbittorrent files: %w", qbErr)
	}

	mediaRootByKey := make(map[string]string, len(newMedia))
	for _, m := range newMedia {
		mediaRootByKey[mediaKey(m)] = m.Path
	}
	replacementMediaRefs := targetedMediaRefs(service, radarrFiles, sonarrFiles, mediaRootByKey)

	replacementTorrentRefs := make([]model.TorrentFileRef, 0, len(qbittorrentFiles))
	for hash, xs := range qbittorrentFiles {
		t, ok := newTorrentByHash[hash]
		if !ok {
			continue
		}
		for _, x := range xs {
			p := filepath.Clean(filepath.Join(t.SavePath, filepath.FromSlash(x.Name)))
			replacementTorrentRefs = append(replacementTorrentRefs, model.TorrentFileRef{
				IntegrationID: integrationID(service.cfg, "qbittorrent"), IntegrationName: integrationName(service.cfg, "qbittorrent", t.Client),
				Client: t.Client, Hash: hash, FileIndex: x.Index, Path: p,
			})
		}
	}

	// Walk only the affected roots/save-paths, so unmanaged siblings inside
	// them are still discovered — bounded to just these items' own
	// directories, not the whole library.
	var scopedRoots []storageRoot
	for key := range ownerSet {
		if root := mediaRootByKey[key]; strings.TrimSpace(root) != "" {
			scopedRoots = append(scopedRoots, storageRoot{Path: root})
		}
	}
	for key := range removedOwnerSet {
		if m, ok := oldMediaByKey[key]; ok && strings.TrimSpace(m.Path) != "" {
			scopedRoots = append(scopedRoots, storageRoot{Path: m.Path})
		}
	}
	for hash := range torrentHashSet {
		if t, ok := newTorrentByHash[hash]; ok && strings.TrimSpace(t.SavePath) != "" {
			scopedRoots = append(scopedRoots, storageRoot{Path: t.SavePath})
		}
	}
	for hash := range removedTorrentSet {
		if t, ok := oldTorrentByHash[hash]; ok && strings.TrimSpace(t.SavePath) != "" {
			scopedRoots = append(scopedRoots, storageRoot{Path: t.SavePath})
		}
	}

	collapsedRoots := collapseStorageRoots(scopedRoots)
	scopedFiles := []model.File{}
	for _, root := range collapsedRoots {
		info, err := os.Stat(root.Path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return result, fmt.Errorf("stat scoped root %s: %w", root.Path, err)
		}
		if !info.IsDir() {
			continue
		}
		walked, err := walkStorageRoots([]storageRoot{root})
		if err != nil {
			return result, fmt.Errorf("scan scoped root %s: %w", root.Path, err)
		}
		scopedFiles = append(scopedFiles, walked...)
	}

	affectedPaths := map[string]bool{}
	for _, f := range scopedFiles {
		affectedPaths[filepath.Clean(f.Path)] = true
	}
	// A path that used to live under a scoped root but was not rediscovered by
	// the walk above (the file was deleted) is still affected: it must be
	// dropped, not left lingering with stale facts.
	for _, f := range oldFiles {
		p := filepath.Clean(f.Path)
		for _, root := range collapsedRoots {
			if under(root.Path, p) {
				affectedPaths[p] = true
				break
			}
		}
	}

	files := make([]model.File, 0, len(oldFiles))
	for _, f := range oldFiles {
		if !affectedPaths[filepath.Clean(f.Path)] {
			files = append(files, f)
		}
	}
	files = append(files, scopedFiles...)
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Path) < strings.ToLower(files[j].Path) })

	mediaRefs := make([]model.MediaFileRef, 0, len(oldMediaRefs)+len(replacementMediaRefs))
	for _, ref := range oldMediaRefs {
		key := fmt.Sprintf("%s:%d", ref.MediaType, ref.MediaID)
		if ownerSet[key] || removedOwnerSet[key] {
			continue
		}
		mediaRefs = append(mediaRefs, ref)
	}
	mediaRefs = append(mediaRefs, replacementMediaRefs...)

	torrentRefs := make([]model.TorrentFileRef, 0, len(oldTorrentRefs)+len(replacementTorrentRefs))
	for _, ref := range oldTorrentRefs {
		hash := strings.ToLower(ref.Hash)
		if torrentHashSet[hash] || removedTorrentSet[hash] {
			continue
		}
		torrentRefs = append(torrentRefs, ref)
	}
	torrentRefs = append(torrentRefs, replacementTorrentRefs...)

	unmanaged := projectUnmanaged(files, mediaRefs, torrentRefs)
	unmanagedToInsert := make([]model.UnmanagedFile, 0, len(scopedFiles))
	for _, file := range unmanaged {
		if affectedPaths[filepath.Clean(file.Path)] {
			unmanagedToInsert = append(unmanagedToInsert, file)
		}
	}

	mediaOwners := make([]store.MediaIdentity, 0, len(replacedOwners)+len(delta.removedOwners))
	for _, owner := range replacedOwners {
		mediaOwners = append(mediaOwners, store.MediaIdentity{Kind: owner.Type, SourceID: owner.ID})
	}
	for _, owner := range delta.removedOwners {
		mediaOwners = append(mediaOwners, store.MediaIdentity{Kind: owner.Type, SourceID: owner.ID})
	}
	removedTorrentHashesForDB := append(append([]string{}, replacedTorrentHashes...), delta.removedTorrentHashes...)

	result = deltaFileTopology{
		files: files, mediaRefs: mediaRefs, torrentRefs: torrentRefs, unmanaged: unmanaged,
		affectedPaths: mapKeys(affectedPaths), filesToInsert: scopedFiles,
		mediaOwners: mediaOwners, mediaRefsToInsert: replacementMediaRefs, unmanagedToInsert: unmanagedToInsert,
		torrentRefsToInsert: replacementTorrentRefs, removedTorrentHashes: removedTorrentHashesForDB,
	}
	return result, nil
}
