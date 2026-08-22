package qbittorrent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"spartarr/internal/model"
)

type Client struct {
	name, base, username, password, apiKey string
	hc                                     *http.Client
}

func New(name, base, username, password, apiKey string) *Client {
	jar, _ := cookiejar.New(nil)
	if name == "" {
		name = "qBittorrent"
	}
	return &Client{name: name, base: strings.TrimRight(base, "/"), username: username, password: password, apiKey: apiKey, hc: &http.Client{Timeout: 30 * time.Second, Jar: jar}}
}

func (c *Client) enabled() bool { return c.base != "" }

func (c *Client) login() error {
	if !c.enabled() || c.apiKey != "" {
		return nil
	}
	form := url.Values{"username": {c.username}, "password": {c.password}}
	req, _ := http.NewRequest(http.MethodPost, c.base+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", c.base)
	r, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(r.Body, 1024))
	if r.StatusCode/100 != 2 || strings.TrimSpace(string(b)) != "Ok." {
		return fmt.Errorf("qbittorrent login: %s: %s", r.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

func (c *Client) get(path string, out any) error {
	req, _ := http.NewRequest(http.MethodGet, c.base+path, nil)
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	req.Header.Set("Referer", c.base)
	r, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 2048))
		return fmt.Errorf("qbittorrent %s: %s: %s", path, r.Status, bytes.TrimSpace(b))
	}
	return json.NewDecoder(r.Body).Decode(out)
}

type torrentInfo struct {
	AddedOn           int64   `json:"added_on"`
	AmountLeft        int64   `json:"amount_left"`
	AutoTMM           bool    `json:"auto_tmm"`
	Availability      float64 `json:"availability"`
	Category          string  `json:"category"`
	Completed         int64   `json:"completed"`
	CompletionOn      int64   `json:"completion_on"`
	ContentPath       string  `json:"content_path"`
	DLLimit           int64   `json:"dl_limit"`
	DLSpeed           int64   `json:"dlspeed"`
	Downloaded        int64   `json:"downloaded"`
	DownloadedSession int64   `json:"downloaded_session"`
	ETA               int64   `json:"eta"`
	ForceStart        bool    `json:"force_start"`
	Hash              string  `json:"hash"`
	Private           bool    `json:"isPrivate"`
	LastActivity      int64   `json:"last_activity"`
	MaxRatio          float64 `json:"max_ratio"`
	Name              string  `json:"name"`
	NumComplete       int     `json:"num_complete"`
	NumIncomplete     int     `json:"num_incomplete"`
	NumLeechs         int     `json:"num_leechs"`
	NumSeeds          int     `json:"num_seeds"`
	Progress          float64 `json:"progress"`
	Ratio             float64 `json:"ratio"`
	Reannounce        int64   `json:"reannounce"`
	SavePath          string  `json:"save_path"`
	SeedingTime       int64   `json:"seeding_time"`
	SeenComplete      int64   `json:"seen_complete"`
	Sequential        bool    `json:"seq_dl"`
	Size              int64   `json:"size"`
	State             string  `json:"state"`
	SuperSeeding      bool    `json:"super_seeding"`
	Tags              string  `json:"tags"`
	TimeActive        int64   `json:"time_active"`
	TotalSize         int64   `json:"total_size"`
	Tracker           string  `json:"tracker"`
	UPLimit           int64   `json:"up_limit"`
	Uploaded          int64   `json:"uploaded"`
	UploadedSession   int64   `json:"uploaded_session"`
	UPSpeed           int64   `json:"upspeed"`
}

type syncResponse struct {
	RID             int64                  `json:"rid"`
	FullUpdate      bool                   `json:"full_update"`
	Torrents        map[string]torrentSync `json:"torrents"`
	TorrentsRemoved []string               `json:"torrents_removed"`
}

type torrentSync struct {
	AddedOn           *int64   `json:"added_on"`
	AmountLeft        *int64   `json:"amount_left"`
	AutoTMM           *bool    `json:"auto_tmm"`
	Availability      *float64 `json:"availability"`
	Category          *string  `json:"category"`
	Completed         *int64   `json:"completed"`
	CompletionOn      *int64   `json:"completion_on"`
	ContentPath       *string  `json:"content_path"`
	DLLimit           *int64   `json:"dl_limit"`
	DLSpeed           *int64   `json:"dlspeed"`
	Downloaded        *int64   `json:"downloaded"`
	DownloadedSession *int64   `json:"downloaded_session"`
	ETA               *int64   `json:"eta"`
	ForceStart        *bool    `json:"force_start"`
	LastActivity      *int64   `json:"last_activity"`
	MaxRatio          *float64 `json:"max_ratio"`
	Name              *string  `json:"name"`
	NumComplete       *int     `json:"num_complete"`
	NumIncomplete     *int     `json:"num_incomplete"`
	NumLeechs         *int     `json:"num_leechs"`
	NumSeeds          *int     `json:"num_seeds"`
	Progress          *float64 `json:"progress"`
	Ratio             *float64 `json:"ratio"`
	Reannounce        *int64   `json:"reannounce"`
	SavePath          *string  `json:"save_path"`
	SeedingTime       *int64   `json:"seeding_time"`
	SeenComplete      *int64   `json:"seen_complete"`
	Sequential        *bool    `json:"seq_dl"`
	Size              *int64   `json:"size"`
	State             *string  `json:"state"`
	SuperSeeding      *bool    `json:"super_seeding"`
	Tags              *string  `json:"tags"`
	TimeActive        *int64   `json:"time_active"`
	TotalSize         *int64   `json:"total_size"`
	Tracker           *string  `json:"tracker"`
	UPLimit           *int64   `json:"up_limit"`
	Uploaded          *int64   `json:"uploaded"`
	UploadedSession   *int64   `json:"uploaded_session"`
	UPSpeed           *int64   `json:"upspeed"`
}

func applySync(t *model.Torrent, x torrentSync) {
	if x.AddedOn != nil {
		t.AddedOn = *x.AddedOn
	}
	if x.AmountLeft != nil {
		t.AmountLeftBytes = *x.AmountLeft
	}
	if x.AutoTMM != nil {
		t.AutoTMM = *x.AutoTMM
	}
	if x.Availability != nil {
		t.Availability = *x.Availability
	}
	if x.Category != nil {
		t.Category = *x.Category
	}
	if x.Completed != nil {
		t.CompletedBytes = *x.Completed
	}
	if x.CompletionOn != nil {
		t.CompletionOn = *x.CompletionOn
	}
	if x.ContentPath != nil {
		t.ContentPath = *x.ContentPath
	}
	if x.DLLimit != nil {
		t.DownloadLimit = *x.DLLimit
	}
	if x.DLSpeed != nil {
		t.DownloadSpeed = *x.DLSpeed
	}
	if x.Downloaded != nil {
		t.DownloadedBytes = *x.Downloaded
	}
	if x.DownloadedSession != nil {
		t.DownloadedSession = *x.DownloadedSession
	}
	if x.ETA != nil {
		t.ETA = *x.ETA
	}
	if x.ForceStart != nil {
		t.ForceStart = *x.ForceStart
	}
	if x.LastActivity != nil {
		t.LastActivity = *x.LastActivity
	}
	if x.MaxRatio != nil {
		t.MaxRatio = *x.MaxRatio
	}
	if x.Name != nil {
		t.Name = *x.Name
	}
	if x.NumComplete != nil {
		t.SeedsSwarm = *x.NumComplete
	}
	if x.NumIncomplete != nil {
		t.LeechersSwarm = *x.NumIncomplete
	}
	if x.NumLeechs != nil {
		t.LeechersConnected = *x.NumLeechs
	}
	if x.NumSeeds != nil {
		t.SeedsConnected = *x.NumSeeds
	}
	if x.Progress != nil {
		t.Progress = *x.Progress
	}
	if x.Ratio != nil {
		t.Ratio = *x.Ratio
	}
	if x.Reannounce != nil {
		t.Reannounce = *x.Reannounce
	}
	if x.SavePath != nil {
		t.SavePath = *x.SavePath
	}
	if x.SeedingTime != nil {
		t.SeedingTime = *x.SeedingTime
	}
	if x.SeenComplete != nil {
		t.SeenComplete = *x.SeenComplete
	}
	if x.Sequential != nil {
		t.Sequential = *x.Sequential
	}
	if x.Size != nil {
		t.SizeBytes = *x.Size
	}
	if x.State != nil {
		t.State = *x.State
	}
	if x.SuperSeeding != nil {
		t.SuperSeeding = *x.SuperSeeding
	}
	if x.Tags != nil {
		t.Tags = *x.Tags
	}
	if x.TimeActive != nil {
		t.TimeActive = *x.TimeActive
	}
	if x.TotalSize != nil {
		t.TotalSizeBytes = *x.TotalSize
	}
	if x.Tracker != nil {
		t.Tracker = *x.Tracker
	}
	if x.UPLimit != nil {
		t.UploadLimit = *x.UPLimit
	}
	if x.Uploaded != nil {
		t.UploadedBytes = *x.Uploaded
	}
	if x.UploadedSession != nil {
		t.UploadedSession = *x.UploadedSession
	}
	if x.UPSpeed != nil {
		t.UploadSpeed = *x.UPSpeed
	}
}

// Sync uses qBittorrent's incremental main-data API. rid=0 requests a full
// snapshot; subsequent calls return only changed/removed torrents.
func (c *Client) Sync(previous map[string]model.Torrent, rid int64) (map[string]model.Torrent, int64, error) {
	out := make(map[string]model.Torrent, len(previous))
	for h, t := range previous {
		out[strings.ToLower(h)] = t
	}
	if !c.enabled() {
		return out, rid, nil
	}
	if err := c.login(); err != nil {
		return nil, rid, err
	}
	var sr syncResponse
	if err := c.get("/api/v2/sync/maindata?rid="+fmt.Sprint(rid), &sr); err != nil {
		return nil, rid, err
	}
	if sr.FullUpdate {
		out = map[string]model.Torrent{}
	}
	for h, x := range sr.Torrents {
		key := strings.ToLower(strings.TrimSpace(h))
		t := out[key]
		t.Client = c.name
		t.Hash = key
		applySync(&t, x)
		out[key] = t
	}
	for _, h := range sr.TorrentsRemoved {
		delete(out, strings.ToLower(strings.TrimSpace(h)))
	}
	return out, sr.RID, nil
}

func (c *Client) Inventory() (map[string]model.Torrent, error) {
	out := map[string]model.Torrent{}
	if !c.enabled() {
		return out, nil
	}
	if err := c.login(); err != nil {
		return nil, err
	}
	var xs []torrentInfo
	if err := c.get("/api/v2/torrents/info", &xs); err != nil {
		return nil, err
	}
	for _, x := range xs {
		h := strings.ToLower(strings.TrimSpace(x.Hash))
		out[h] = model.Torrent{Client: c.name, Hash: x.Hash, Name: x.Name, State: x.State, Category: x.Category, Tags: x.Tags, Tracker: x.Tracker, SavePath: x.SavePath, ContentPath: x.ContentPath,
			SizeBytes: x.Size, TotalSizeBytes: x.TotalSize, CompletedBytes: x.Completed, AmountLeftBytes: x.AmountLeft, DownloadedBytes: x.Downloaded, UploadedBytes: x.Uploaded,
			DownloadedSession: x.DownloadedSession, UploadedSession: x.UploadedSession, DownloadSpeed: x.DLSpeed, UploadSpeed: x.UPSpeed, DownloadLimit: x.DLLimit, UploadLimit: x.UPLimit,
			Ratio: x.Ratio, MaxRatio: x.MaxRatio, Progress: x.Progress, Availability: x.Availability, SeedsConnected: x.NumSeeds, LeechersConnected: x.NumLeechs, SeedsSwarm: x.NumComplete, LeechersSwarm: x.NumIncomplete,
			AddedOn: x.AddedOn, CompletionOn: x.CompletionOn, LastActivity: x.LastActivity, SeenComplete: x.SeenComplete, TimeActive: x.TimeActive, SeedingTime: x.SeedingTime, ETA: x.ETA, Reannounce: x.Reannounce,
			ForceStart: x.ForceStart, AutoTMM: x.AutoTMM, Sequential: x.Sequential, SuperSeeding: x.SuperSeeding, Private: x.Private}
	}
	return out, nil
}
