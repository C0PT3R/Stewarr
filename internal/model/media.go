package model

import (
	"strings"
	"time"
)

type MediaType string

const (
	Movie  MediaType = "movie"
	Series MediaType = "series"
)

type Reason struct {
	Label  string  `json:"label"`
	Value  string  `json:"value"`
	Points float64 `json:"points"`
	Note   string  `json:"note,omitempty"`
}

const (
	TorrentCurrent = "CURRENT"
	// TorrentSuperseded means provenance proves a specific newer import
	// replaced this exact torrent for the same media (SupersededByHash
	// records which one).
	TorrentSuperseded = "SUPERSEDED"
	// TorrentOrphaned means import provenance exists for this torrent (it
	// was managed once — FormerMediaItems records what), but nothing
	// proves it was replaced by a specific newer import; e.g. the media it
	// belonged to was removed from Radarr/Sonarr entirely.
	TorrentOrphaned = "ORPHANED"
	// TorrentUnassociated means Stewarr has no import provenance for this
	// torrent at all — unlike Superseded or Orphaned, there is no known
	// relationship to lean on, historical or otherwise. It may simply be
	// something downloaded through that client for personal use, or from
	// a service Stewarr doesn't track.
	TorrentUnassociated = "UNASSOCIATED"
)

// NormalizeTorrentStatus migrates legacy cached provenance labels into the
// current four-state relationship model.
func NormalizeTorrentStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "OPEN", "ASSOCIATED", TorrentCurrent:
		return TorrentCurrent
	case TorrentSuperseded:
		return TorrentSuperseded
	case TorrentOrphaned:
		return TorrentOrphaned
	case "UNMATCHED", TorrentUnassociated:
		return TorrentUnassociated
	default:
		return TorrentUnassociated
	}
}

type MediaRef struct {
	// ServiceID disambiguates SourceID across multiple configured
	// instances of the same service type — Radarr's own movie IDs (like
	// Sonarr's series IDs) are unique only within one instance, never
	// globally, so identifying "this media item" always requires both.
	ServiceID string    `json:"serviceId,omitempty"`
	Type      MediaType `json:"type"`
	SourceID  int       `json:"sourceId"`
	Title     string    `json:"title"`
	Year      int       `json:"year"`
}

type Torrent struct {
	ServiceID           string   `json:"serviceId,omitempty"`
	TorrentValue        float64  `json:"torrentValue"`
	TorrentValueReasons []Reason `json:"torrentValueReasons,omitempty"`
	Protected           bool     `json:"protected,omitempty"`
	ProtectionReason    string   `json:"protectionReason,omitempty"`
	// RemovalRestricted is true when this torrent's own service has not
	// checked "Allow automatic removal" — distinct from Protected (a
	// KeepTag/ratio/etc. judgment the item earns on its own merits, shown
	// as such throughout the UI). This is a plain administrative
	// permission: the owning service simply hasn't been opted in, so the
	// planner (internal/cleanup) must never offer it, or any bundle it's
	// part of, as a removal candidate at all — not even for manual review.
	// It does not gate a human directly removing this one torrent by hand
	// from its own detail page.
	RemovalRestricted bool       `json:"-"`
	AssociationStatus string     `json:"torrentStatus,omitempty"`
	AssociationReason string     `json:"associationReason,omitempty"`
	MediaItems        []MediaRef `json:"media,omitempty"`
	FormerMediaItems  []MediaRef `json:"formerMedia,omitempty"`
	// Hardlink facts are computed from the last published file topology. The
	// slices describe global current relationships; the scalar fields describe
	// the relationship of a torrent copy projected into one media item.
	HardlinkKnownMediaItems []MediaRef `json:"hardlinkKnownMedia,omitempty"`
	HardlinkedMediaItems    []MediaRef `json:"hardlinkedMedia,omitempty"`
	MediaHardlinkKnown      bool       `json:"mediaHardlinkKnown,omitempty"`
	MediaHardlinked         bool       `json:"mediaHardlinked,omitempty"`
	// HardlinkedSeasons is the season number(s) of the media in
	// HardlinkedMediaItems that this torrent's files are hardlinked to. Only
	// meaningful when MediaHardlinked is true and the media is a Series.
	HardlinkedSeasons []int   `json:"hardlinkedSeasons,omitempty"`
	SupersededByHash  string  `json:"supersededByHash,omitempty"`
	ReclaimableKnown  bool    `json:"reclaimableKnown,omitempty"`
	ReclaimableBytes  int64   `json:"reclaimableBytes,omitempty"`
	SharedBytes       int64   `json:"sharedBytes,omitempty"`
	InspectedBytes    int64   `json:"inspectedBytes,omitempty"`
	InspectedFiles    int     `json:"inspectedFiles,omitempty"`
	SharedFiles       int     `json:"sharedFiles,omitempty"`
	StorageError      string  `json:"storageError,omitempty"`
	Client            string  `json:"client"`
	Hash              string  `json:"hash"`
	Name              string  `json:"name"`
	State             string  `json:"state"`
	Category          string  `json:"category"`
	Tags              string  `json:"tags"`
	Tracker           string  `json:"tracker"`
	SavePath          string  `json:"savePath"`
	ContentPath       string  `json:"contentPath"`
	SizeBytes         int64   `json:"sizeBytes"`
	TotalSizeBytes    int64   `json:"totalSizeBytes"`
	CompletedBytes    int64   `json:"completedBytes"`
	AmountLeftBytes   int64   `json:"amountLeftBytes"`
	DownloadedBytes   int64   `json:"downloadedBytes"`
	UploadedBytes     int64   `json:"uploadedBytes"`
	DownloadedSession int64   `json:"downloadedSessionBytes"`
	UploadedSession   int64   `json:"uploadedSessionBytes"`
	DownloadLimit     int64   `json:"downloadLimit"`
	UploadLimit       int64   `json:"uploadLimit"`
	Ratio             float64 `json:"ratio"`
	MaxRatio          float64 `json:"maxRatio"`
	Progress          float64 `json:"progress"`
	Availability      float64 `json:"availability"`
	SeedsConnected    int     `json:"seedsConnected"`
	LeechersConnected int     `json:"leechersConnected"`
	SeedsSwarm        int     `json:"seedsSwarm"`
	LeechersSwarm     int     `json:"leechersSwarm"`
	AddedOn           int64   `json:"addedOn"`
	CompletionOn      int64   `json:"completionOn"`
	LastActivity      int64   `json:"lastActivity"`
	SeenComplete      int64   `json:"seenComplete"`
	TimeActive        int64   `json:"timeActive"`
	SeedingTime       int64   `json:"seedingTime"`
	ETA               int64   `json:"eta"`
	Reannounce        int64   `json:"reannounce"`
	ForceStart        bool    `json:"forceStart"`
	AutoTMM           bool    `json:"autoTmm"`
	Sequential        bool    `json:"sequential"`
	SuperSeeding      bool    `json:"superSeeding"`
	Private           bool    `json:"private"`
}

// File is an existing filesystem path with physical identity facts. Ownership is
// declared separately by service references; paths sharing device/inode are
// the same physical file and are grouped as such by topology/removal logic.
type StorageContext struct {
	ServiceID   string `json:"serviceId,omitempty"`
	ServiceName string `json:"serviceName,omitempty"`
	ServiceType string `json:"serviceType,omitempty"`
	Root        string `json:"root,omitempty"`
	RootLabel   string `json:"rootLabel,omitempty"`
}

type File struct {
	Path            string           `json:"path"`
	SizeBytes       int64            `json:"sizeBytes"`
	Exists          bool             `json:"exists"`
	IdentityKnown   bool             `json:"identityKnown"`
	Device          uint64           `json:"device,omitempty"`
	Inode           uint64           `json:"inode,omitempty"`
	Links           uint64           `json:"links,omitempty"`
	ModifiedAt      time.Time        `json:"modifiedAt,omitempty"`
	StorageContexts []StorageContext `json:"storageContexts,omitempty"`
}

type MediaFilePart struct {
	Group        string `json:"group,omitempty"`
	Label        string `json:"label,omitempty"`
	Order        int    `json:"order,omitempty"`
	SourcePartID int    `json:"sourcePartId,omitempty"`
	// AiredAt is this part's (episode's) original broadcast date, when
	// known — distinct from when the file was added to the library. Zero
	// for an unaired episode or a source that doesn't report air dates.
	AiredAt time.Time `json:"airedAt,omitempty"`
}

type MediaFileRef struct {
	ServiceID    string          `json:"serviceId,omitempty"`
	ServiceName  string          `json:"serviceName,omitempty"`
	MediaType    MediaType       `json:"mediaType"`
	MediaID      int             `json:"mediaId"`
	Source       string          `json:"source"`
	SourceFileID int             `json:"sourceFileId"`
	Path         string          `json:"path"`
	Parts        []MediaFilePart `json:"parts,omitempty"`
	// AddedAt is the owning service's own "date added" fact for this
	// specific file, when it exposes one (Sonarr episode files do). It
	// persists across refresh cycles so season-level recency scoring does
	// not depend on a fresh service fetch being in flight.
	AddedAt time.Time `json:"addedAt,omitempty"`
}

type TorrentFileRef struct {
	ServiceID   string `json:"serviceId,omitempty"`
	ServiceName string `json:"serviceName,omitempty"`
	Client      string `json:"client"`
	Hash        string `json:"hash"`
	FileIndex   int    `json:"fileIndex"`
	Path        string `json:"path"`
}

type UnmanagedFile struct {
	Path             string           `json:"path"`
	SizeBytes        int64            `json:"sizeBytes"`
	ModifiedAt       time.Time        `json:"modifiedAt"`
	Device           uint64           `json:"device"`
	Inode            uint64           `json:"inode"`
	Links            uint64           `json:"links"`
	ReclaimableKnown bool             `json:"reclaimableKnown"`
	ReclaimableBytes int64            `json:"reclaimableBytes"`
	SharedBytes      int64            `json:"sharedBytes"`
	StorageContexts  []StorageContext `json:"storageContexts,omitempty"`
}

type Media struct {
	ServiceID   string    `json:"serviceId,omitempty"`
	ServiceName string    `json:"serviceName,omitempty"`
	Type        MediaType `json:"type"`
	SourceID    int       `json:"sourceId"`
	Title       string    `json:"title"`
	Year        int       `json:"year"`
	Path        string    `json:"path"`
	SizeBytes   int64     `json:"sizeBytes"`
	Rating      float64   `json:"rating"`
	VoteCount   int       `json:"voteCount"`
	AddedAt     time.Time `json:"addedAt"`
	Tags        []string  `json:"tags"`
	TMDBID      int       `json:"tmdbId"`
	TVDBID      int       `json:"tvdbId"`
	IMDBID      string    `json:"imdbId"`

	Views         int        `json:"views"`
	UniqueViewers int        `json:"uniqueViewers"`
	LastWatched   *time.Time `json:"lastWatched,omitempty"`
	Favorite      bool       `json:"favorite"`
	// JellyfinEnrichedAt is when Views/UniqueViewers/LastWatched/Favorite
	// were last confirmed against Jellyfin for this specific item — set
	// after every successful Apply() call this item was included in
	// (Jellyfin's own pass is all-or-nothing, unlike TMDB's per-item
	// fetches, so a successful call means every item it covered was
	// checked, whether or not Jellyfin actually had a match for it). Zero
	// means never yet checked — see EnrichNewMedia in
	// internal/inventory/service.go, which fetches a newly discovered (or
	// re-identified) item's enrichment immediately rather than waiting for
	// RefreshJellyfin's next scheduled pass.
	JellyfinEnrichedAt time.Time `json:"jellyfinEnrichedAt,omitempty"`

	Requested   bool       `json:"requested"`
	RequestedAt *time.Time `json:"requestedAt,omitempty"`
	// SeerrEnrichedAt mirrors JellyfinEnrichedAt for Requested/RequestedAt.
	SeerrEnrichedAt time.Time `json:"seerrEnrichedAt,omitempty"`

	// TMDBRating/TMDBVoteCount are TMDB's own rating data, populated by TMDB
	// enrichment when configured. They are deliberately separate from
	// Rating/VoteCount (Radarr/Sonarr's own numbers) rather than overwriting
	// them in place: valuation prefers these when present (TMDBVoteCount>0)
	// and falls back to Rating/VoteCount otherwise, so an unenriched item —
	// TMDB unconfigured, unreachable, or no match for this title — never
	// loses its existing rating signal.
	TMDBRating    float64 `json:"tmdbRating,omitempty"`
	TMDBVoteCount int     `json:"tmdbVoteCount,omitempty"`
	// Popularity is TMDB's own popularity score. It has no fallback source
	// anywhere — zero means "not yet enriched," not "unpopular," the same
	// treatment VoteCount==0 already gets.
	Popularity float64 `json:"popularity,omitempty"`
	// TMDBEnrichedAt is when TMDBRating/TMDBVoteCount/Popularity were last
	// successfully fetched for this specific item — TMDB enrichment fetches
	// one item at a time, so unlike Jellyfin/Seerr's all-or-nothing passes,
	// one item's fetch can fail while its neighbors succeed. Zero means
	// never successfully enriched. This does not gate whether valuation
	// uses the data (a transient failure preserves the last known values
	// rather than blanking them); it only lets auto-removal specifically
	// exclude an item whose data has gone stale — see cmd/stewarr/main.go
	// and internal/httpui/auto_removal.go.
	TMDBEnrichedAt time.Time `json:"tmdbEnrichedAt,omitempty"`

	DownloadIDs []string  `json:"downloadIds,omitempty"`
	Torrents    []Torrent `json:"torrents,omitempty"`

	Protected        bool   `json:"protected"`
	ProtectionReason string `json:"protectionReason,omitempty"`
	// RemovalRestricted mirrors Torrent.RemovalRestricted for Media: true
	// when this item's own service has not checked "Allow automatic
	// removal", so internal/cleanup must never offer it (or a bundle it's
	// part of) as a removal candidate — see the doc comment there for why
	// this is deliberately separate from Protected.
	RemovalRestricted      bool     `json:"-"`
	ReclaimableKnown       bool     `json:"reclaimableKnown,omitempty"`
	ReclaimableBytes       int64    `json:"reclaimableBytes,omitempty"`
	BundleReclaimableKnown bool     `json:"bundleReclaimableKnown,omitempty"`
	BundleReclaimableBytes int64    `json:"bundleReclaimableBytes,omitempty"`
	RetentionValue         float64  `json:"retentionValue"`
	RetentionValueReasons  []Reason `json:"retentionValueReasons"`

	// Seasons is populated only for Type == Series, one entry per season with
	// at least one known file. A Season carries its own Retention Value so
	// the planner can propose removing old seasons of an otherwise-kept show
	// instead of only ever reasoning about a whole series at once.
	Seasons []Season `json:"seasons,omitempty"`
}

// Season is a per-season Retention Value slice of a Series Media item.
type Season struct {
	Number    int   `json:"number"`
	SizeBytes int64 `json:"sizeBytes"`
	// LastAiredAt is the most recent original broadcast date among this
	// season's episodes — this drives season-recency scoring so it tracks
	// how recently the content itself is, not when Stewarr's library
	// happened to import it (a show backfilled all at once would otherwise
	// give every season nearly the same import date).
	LastAiredAt      time.Time `json:"lastAiredAt,omitempty"`
	EpisodeFileCount int       `json:"episodeFileCount"`
	Protected        bool      `json:"protected,omitempty"`
	ProtectionReason string    `json:"protectionReason,omitempty"`
	// RemovalRestricted inherits from the parent Media's field of the same
	// name — a season of a series owned by a non-opted-in service.
	RemovalRestricted      bool     `json:"-"`
	ReclaimableKnown       bool     `json:"reclaimableKnown,omitempty"`
	ReclaimableBytes       int64    `json:"reclaimableBytes,omitempty"`
	BundleReclaimableKnown bool     `json:"bundleReclaimableKnown,omitempty"`
	BundleReclaimableBytes int64    `json:"bundleReclaimableBytes,omitempty"`
	RetentionValue         float64  `json:"retentionValue"`
	RetentionValueReasons  []Reason `json:"retentionValueReasons,omitempty"`
	// FileGroup is the exact "Season %d" label MediaFileRef.Parts already
	// uses, so removal code can select this season's files without
	// re-deriving the format.
	FileGroup string `json:"fileGroup"`
}

// CurrentHardlinkedTorrentsForSeason returns the subset of m.Torrents proven
// both Current and physically hardlinked to files belonging to the given
// season number. These must always be removed together with that season,
// never separately, for the same reason as CurrentHardlinkedTorrents.
func (m Media) CurrentHardlinkedTorrentsForSeason(season int) []Torrent {
	var out []Torrent
	for _, t := range m.Torrents {
		if NormalizeTorrentStatus(t.AssociationStatus) != TorrentCurrent || !t.MediaHardlinkKnown || !t.MediaHardlinked {
			continue
		}
		for _, s := range t.HardlinkedSeasons {
			if s == season {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

// CurrentHardlinkedTorrents returns the subset of m.Torrents proven both
// Current and physically hardlinked to this media's files. These must always
// be removed together with the media, never separately: unlinking only one
// side frees none of the shared bytes while still destroying real value.
func (m Media) CurrentHardlinkedTorrents() []Torrent {
	var out []Torrent
	for _, t := range m.Torrents {
		if NormalizeTorrentStatus(t.AssociationStatus) == TorrentCurrent && t.MediaHardlinkKnown && t.MediaHardlinked {
			out = append(out, t)
		}
	}
	return out
}
