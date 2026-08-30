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
	TorrentCurrent      = "CURRENT"
	TorrentSuperseded   = "SUPERSEDED"
	TorrentUnassociated = "UNASSOCIATED"
)

// NormalizeTorrentStatus migrates legacy cached provenance labels into the
// current three-state relationship model. "Orphaned" is historical context,
// not a present torrent state.
func NormalizeTorrentStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "OPEN", "ASSOCIATED", TorrentCurrent:
		return TorrentCurrent
	case TorrentSuperseded:
		return TorrentSuperseded
	case "ORPHANED", "UNMATCHED", TorrentUnassociated:
		return TorrentUnassociated
	default:
		return TorrentUnassociated
	}
}

type MediaRef struct {
	Type     MediaType `json:"type"`
	SourceID int       `json:"sourceId"`
	Title    string    `json:"title"`
	Year     int       `json:"year"`
}

type Torrent struct {
	SwarmValue        float64    `json:"swarmValue"`
	SwarmValueReasons []Reason   `json:"swarmValueReasons,omitempty"`
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
	SupersededByHash        string     `json:"supersededByHash,omitempty"`
	ReclaimableKnown        bool       `json:"reclaimableKnown,omitempty"`
	ReclaimableBytes        int64      `json:"reclaimableBytes,omitempty"`
	SharedBytes             int64      `json:"sharedBytes,omitempty"`
	InspectedBytes          int64      `json:"inspectedBytes,omitempty"`
	InspectedFiles          int        `json:"inspectedFiles,omitempty"`
	SharedFiles             int        `json:"sharedFiles,omitempty"`
	StorageError            string     `json:"storageError,omitempty"`
	Client                  string     `json:"client"`
	Hash                    string     `json:"hash"`
	Name                    string     `json:"name"`
	State                   string     `json:"state"`
	Category                string     `json:"category"`
	Tags                    string     `json:"tags"`
	Tracker                 string     `json:"tracker"`
	SavePath                string     `json:"savePath"`
	ContentPath             string     `json:"contentPath"`
	SizeBytes               int64      `json:"sizeBytes"`
	TotalSizeBytes          int64      `json:"totalSizeBytes"`
	CompletedBytes          int64      `json:"completedBytes"`
	AmountLeftBytes         int64      `json:"amountLeftBytes"`
	DownloadedBytes         int64      `json:"downloadedBytes"`
	UploadedBytes           int64      `json:"uploadedBytes"`
	DownloadedSession       int64      `json:"downloadedSessionBytes"`
	UploadedSession         int64      `json:"uploadedSessionBytes"`
	DownloadSpeed           int64      `json:"downloadSpeed"`
	UploadSpeed             int64      `json:"uploadSpeed"`
	DownloadLimit           int64      `json:"downloadLimit"`
	UploadLimit             int64      `json:"uploadLimit"`
	Ratio                   float64    `json:"ratio"`
	MaxRatio                float64    `json:"maxRatio"`
	Progress                float64    `json:"progress"`
	Availability            float64    `json:"availability"`
	SeedsConnected          int        `json:"seedsConnected"`
	LeechersConnected       int        `json:"leechersConnected"`
	SeedsSwarm              int        `json:"seedsSwarm"`
	LeechersSwarm           int        `json:"leechersSwarm"`
	AddedOn                 int64      `json:"addedOn"`
	CompletionOn            int64      `json:"completionOn"`
	LastActivity            int64      `json:"lastActivity"`
	SeenComplete            int64      `json:"seenComplete"`
	TimeActive              int64      `json:"timeActive"`
	SeedingTime             int64      `json:"seedingTime"`
	ETA                     int64      `json:"eta"`
	Reannounce              int64      `json:"reannounce"`
	ForceStart              bool       `json:"forceStart"`
	AutoTMM                 bool       `json:"autoTmm"`
	Sequential              bool       `json:"sequential"`
	SuperSeeding            bool       `json:"superSeeding"`
	Private                 bool       `json:"private"`
}

// File is an existing filesystem path with physical identity facts. Ownership is
// declared separately by integration references; paths sharing device/inode are
// the same physical file and are grouped as such by topology/removal logic.
type StorageContext struct {
	IntegrationID   string `json:"integrationId,omitempty"`
	IntegrationName string `json:"integrationName,omitempty"`
	IntegrationType string `json:"integrationType,omitempty"`
	Root            string `json:"root,omitempty"`
	RootLabel       string `json:"rootLabel,omitempty"`
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
}

type MediaFileRef struct {
	IntegrationID   string          `json:"integrationId,omitempty"`
	IntegrationName string          `json:"integrationName,omitempty"`
	MediaType       MediaType       `json:"mediaType"`
	MediaID         int             `json:"mediaId"`
	Source          string          `json:"source"`
	SourceFileID    int             `json:"sourceFileId"`
	Path            string          `json:"path"`
	Parts           []MediaFilePart `json:"parts,omitempty"`
}

type TorrentFileRef struct {
	IntegrationID   string `json:"integrationId,omitempty"`
	IntegrationName string `json:"integrationName,omitempty"`
	Client          string `json:"client"`
	Hash            string `json:"hash"`
	FileIndex       int    `json:"fileIndex"`
	Path            string `json:"path"`
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
	IntegrationID   string    `json:"integrationId,omitempty"`
	IntegrationName string    `json:"integrationName,omitempty"`
	Type            MediaType `json:"type"`
	SourceID        int       `json:"sourceId"`
	Title           string    `json:"title"`
	Year            int       `json:"year"`
	Path            string    `json:"path"`
	SizeBytes       int64     `json:"sizeBytes"`
	Rating          float64   `json:"rating"`
	VoteCount       int       `json:"voteCount"`
	AddedAt         time.Time `json:"addedAt"`
	Tags            []string  `json:"tags"`
	TMDBID          int       `json:"tmdbId"`
	TVDBID          int       `json:"tvdbId"`
	IMDBID          string    `json:"imdbId"`

	Views         int        `json:"views"`
	UniqueViewers int        `json:"uniqueViewers"`
	LastWatched   *time.Time `json:"lastWatched,omitempty"`
	Favorite      bool       `json:"favorite"`

	Requested   bool       `json:"requested"`
	RequestedAt *time.Time `json:"requestedAt,omitempty"`

	DownloadIDs []string  `json:"downloadIds,omitempty"`
	Torrents    []Torrent `json:"torrents,omitempty"`

	Protected             bool     `json:"protected"`
	ProtectionReason      string   `json:"protectionReason,omitempty"`
	ReclaimableKnown      bool     `json:"reclaimableKnown,omitempty"`
	ReclaimableBytes      int64    `json:"reclaimableBytes,omitempty"`
	RetentionValue        float64  `json:"retentionValue"`
	RetentionValueReasons []Reason `json:"retentionValueReasons"`
}
