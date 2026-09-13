package inventory

import (
	"fmt"
	"stewarr/internal/model"
	"strings"
)

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

// externalIDsChanged reports whether a's external ids differ from b's —
// e.g. Radarr/Sonarr re-matching an item to a different TMDB/TVDB/IMDB
// entry. When true, any cached enrichment keyed to the old identity
// belongs to a different title now and must not be carried forward.
func externalIDsChanged(a, b model.Media) bool {
	return a.TMDBID != b.TMDBID || a.TVDBID != b.TVDBID || a.IMDBID != b.IMDBID
}
