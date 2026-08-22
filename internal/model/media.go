package model

import "time"

type MediaType string

const (
	Movie  MediaType = "movie"
	Series MediaType = "series"
)

type Reason struct {
	Label  string  `json:"label"`
	Value  string  `json:"value"`
	Points float64 `json:"points"`
}

type MediaRef struct {
	Type     MediaType `json:"type"`
	SourceID int       `json:"sourceId"`
	Title    string    `json:"title"`
	Year     int       `json:"year"`
}

type Torrent struct {
	AssociationStatus string     `json:"torrentStatus,omitempty"`
	AssociationReason string     `json:"associationReason,omitempty"`
	MediaItems        []MediaRef `json:"media,omitempty"`
	FormerMediaItems  []MediaRef `json:"formerMedia,omitempty"`
	SupersededByHash  string     `json:"supersededByHash,omitempty"`
	ReclaimableKnown  bool       `json:"reclaimableKnown,omitempty"`
	ReclaimableBytes  int64      `json:"reclaimableBytes,omitempty"`
	SharedBytes       int64      `json:"sharedBytes,omitempty"`
	InspectedBytes    int64      `json:"inspectedBytes,omitempty"`
	InspectedFiles    int        `json:"inspectedFiles,omitempty"`
	SharedFiles       int        `json:"sharedFiles,omitempty"`
	StorageError      string     `json:"storageError,omitempty"`
	Client            string     `json:"client"`
	Hash              string     `json:"hash"`
	Name              string     `json:"name"`
	State             string     `json:"state"`
	Category          string     `json:"category"`
	Tags              string     `json:"tags"`
	Tracker           string     `json:"tracker"`
	SavePath          string     `json:"savePath"`
	ContentPath       string     `json:"contentPath"`
	SizeBytes         int64      `json:"sizeBytes"`
	TotalSizeBytes    int64      `json:"totalSizeBytes"`
	CompletedBytes    int64      `json:"completedBytes"`
	AmountLeftBytes   int64      `json:"amountLeftBytes"`
	DownloadedBytes   int64      `json:"downloadedBytes"`
	UploadedBytes     int64      `json:"uploadedBytes"`
	DownloadedSession int64      `json:"downloadedSessionBytes"`
	UploadedSession   int64      `json:"uploadedSessionBytes"`
	DownloadSpeed     int64      `json:"downloadSpeed"`
	UploadSpeed       int64      `json:"uploadSpeed"`
	DownloadLimit     int64      `json:"downloadLimit"`
	UploadLimit       int64      `json:"uploadLimit"`
	Ratio             float64    `json:"ratio"`
	MaxRatio          float64    `json:"maxRatio"`
	Progress          float64    `json:"progress"`
	Availability      float64    `json:"availability"`
	SeedsConnected    int        `json:"seedsConnected"`
	LeechersConnected int        `json:"leechersConnected"`
	SeedsSwarm        int        `json:"seedsSwarm"`
	LeechersSwarm     int        `json:"leechersSwarm"`
	AddedOn           int64      `json:"addedOn"`
	CompletionOn      int64      `json:"completionOn"`
	LastActivity      int64      `json:"lastActivity"`
	SeenComplete      int64      `json:"seenComplete"`
	TimeActive        int64      `json:"timeActive"`
	SeedingTime       int64      `json:"seedingTime"`
	ETA               int64      `json:"eta"`
	Reannounce        int64      `json:"reannounce"`
	ForceStart        bool       `json:"forceStart"`
	AutoTMM           bool       `json:"autoTmm"`
	Sequential        bool       `json:"sequential"`
	SuperSeeding      bool       `json:"superSeeding"`
	Private           bool       `json:"private"`
}

type Media struct {
	Type      MediaType `json:"type"`
	SourceID  int       `json:"sourceId"`
	Title     string    `json:"title"`
	Year      int       `json:"year"`
	Path      string    `json:"path"`
	SizeBytes int64     `json:"sizeBytes"`
	Rating    float64   `json:"rating"`
	VoteCount int       `json:"voteCount"`
	AddedAt   time.Time `json:"addedAt"`
	Tags      []string  `json:"tags"`
	TMDBID    int       `json:"tmdbId"`
	TVDBID    int       `json:"tvdbId"`
	IMDBID    string    `json:"imdbId"`

	Views         int        `json:"views"`
	UniqueViewers int        `json:"uniqueViewers"`
	LastWatched   *time.Time `json:"lastWatched,omitempty"`
	Favorite      bool       `json:"favorite"`

	Requested   bool       `json:"requested"`
	RequestedAt *time.Time `json:"requestedAt,omitempty"`

	DownloadIDs []string  `json:"downloadIds,omitempty"`
	Torrents    []Torrent `json:"torrents,omitempty"`

	Protected        bool     `json:"protected"`
	ProtectionReason string   `json:"protectionReason,omitempty"`
	Strength         float64  `json:"strength"`
	Reasons          []Reason `json:"reasons"`
}
