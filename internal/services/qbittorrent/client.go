package qbittorrent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"stewarr/internal/model"
)

// NotFoundError reports that qBittorrent authoritatively has no such object
// (HTTP 404) — a definitive fact, not an ambiguous or transient failure. A
// torrent removed directly in qBittorrent between Stewarr's own sync and a
// subsequent per-hash lookup is the common case: callers should treat that
// object as having nothing to report rather than failing an entire batch.
type NotFoundError struct {
	Path string
	Body string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("qbittorrent %s: not found: %s", e.Path, e.Body)
}

type Client struct {
	name, base, username, password, apiKey string
	hc                                     *http.Client
	ctx                                    context.Context
}

func New(name, base, username, password, apiKey string) *Client {
	jar, _ := cookiejar.New(nil)
	if name == "" {
		name = "qBittorrent"
	}
	return &Client{name: name, base: strings.TrimRight(base, "/"), username: username, password: password, apiKey: apiKey, hc: &http.Client{Timeout: 30 * time.Second, Jar: jar}}
}

func (client *Client) WithContext(ctx context.Context) *Client {
	clientCopy := *client
	clientCopy.ctx = ctx
	return &clientCopy
}
func (client *Client) requestContext() context.Context {
	if client.ctx != nil {
		return client.ctx
	}
	return context.Background()
}

func (client *Client) enabled() bool { return client.base != "" }

func (client *Client) login() error {
	if !client.enabled() || client.apiKey != "" {
		return nil
	}
	form := url.Values{"username": {client.username}, "password": {client.password}}
	req, _ := http.NewRequestWithContext(client.requestContext(), http.MethodPost, client.base+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", client.base)
	r, err := client.hc.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(r.Body, 1024))
	// The documented success body is "Ok." with a 200, but real deployments
	// diverge from that: some respond 204 with an empty body, and a
	// qBittorrent instance with "bypass authentication for whitelisted
	// IPs" enabled (a very common home-lab setup when Stewarr and
	// qBittorrent share a network) never needs to hand back a SID session
	// cookie either, since every request from that IP is auto-authorized
	// regardless. The one thing qBittorrent's API actually documents as a
	// failure signal is the literal body "Fails." on wrong credentials —
	// that's the only case to reject; treat every other 2xx response as
	// success rather than matching one specific body/cookie shape.
	if r.StatusCode/100 != 2 || strings.TrimSpace(string(b)) == "Fails." {
		return fmt.Errorf("qbittorrent login: %s: %s", r.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

func (client *Client) postForm(path string, form url.Values) error {
	if err := client.login(); err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(client.requestContext(), http.MethodPost, client.base+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", client.base)
	if client.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+client.apiKey)
	}
	r, err := client.hc.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 2048))
		return fmt.Errorf("qbittorrent %s: %s: %s", path, r.Status, bytes.TrimSpace(b))
	}
	return nil
}

func (client *Client) get(path string, out any) error {
	req, _ := http.NewRequestWithContext(client.requestContext(), http.MethodGet, client.base+path, nil)
	if client.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+client.apiKey)
	}
	req.Header.Set("Referer", client.base)
	r, err := client.hc.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 2048))
		if r.StatusCode == http.StatusNotFound {
			return &NotFoundError{Path: path, Body: string(bytes.TrimSpace(b))}
		}
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
}

// Sync uses qBittorrent's incremental main-data API. rid=0 requests a full
// snapshot; subsequent calls return only changed/removed torrents.
func (client *Client) Sync(previous map[string]model.Torrent, rid int64) (map[string]model.Torrent, int64, error) {
	out := make(map[string]model.Torrent, len(previous))
	for h, t := range previous {
		out[strings.ToLower(h)] = t
	}
	if !client.enabled() {
		return out, rid, nil
	}
	if err := client.login(); err != nil {
		return nil, rid, err
	}
	var sr syncResponse
	if err := client.get("/api/v2/sync/maindata?rid="+fmt.Sprint(rid), &sr); err != nil {
		return nil, rid, err
	}
	if sr.FullUpdate {
		out = map[string]model.Torrent{}
	}
	for h, x := range sr.Torrents {
		key := strings.ToLower(strings.TrimSpace(h))
		t := out[key]
		t.Client = client.name
		t.Hash = key
		applySync(&t, x)
		out[key] = t
	}
	for _, h := range sr.TorrentsRemoved {
		delete(out, strings.ToLower(strings.TrimSpace(h)))
	}
	return out, sr.RID, nil
}

func (client *Client) Inventory() (map[string]model.Torrent, error) {
	out := map[string]model.Torrent{}
	if !client.enabled() {
		return out, nil
	}
	if err := client.login(); err != nil {
		return nil, err
	}
	var xs []torrentInfo
	if err := client.get("/api/v2/torrents/info", &xs); err != nil {
		return nil, err
	}
	for _, x := range xs {
		h := strings.ToLower(strings.TrimSpace(x.Hash))
		out[h] = model.Torrent{Client: client.name, Hash: x.Hash, Name: x.Name, State: x.State, Category: x.Category, Tags: x.Tags, Tracker: x.Tracker, SavePath: x.SavePath, ContentPath: x.ContentPath,
			SizeBytes: x.Size, TotalSizeBytes: x.TotalSize, CompletedBytes: x.Completed, AmountLeftBytes: x.AmountLeft, DownloadedBytes: x.Downloaded, UploadedBytes: x.Uploaded,
			DownloadedSession: x.DownloadedSession, UploadedSession: x.UploadedSession, DownloadLimit: x.DLLimit, UploadLimit: x.UPLimit,
			Ratio: x.Ratio, MaxRatio: x.MaxRatio, Progress: x.Progress, Availability: x.Availability, SeedsConnected: x.NumSeeds, LeechersConnected: x.NumLeechs, SeedsSwarm: x.NumComplete, LeechersSwarm: x.NumIncomplete,
			AddedOn: x.AddedOn, CompletionOn: x.CompletionOn, LastActivity: x.LastActivity, SeenComplete: x.SeenComplete, TimeActive: x.TimeActive, SeedingTime: x.SeedingTime, ETA: x.ETA, Reannounce: x.Reannounce,
			ForceStart: x.ForceStart, AutoTMM: x.AutoTMM, Sequential: x.Sequential, SuperSeeding: x.SuperSeeding, Private: x.Private}
	}
	return out, nil
}

// Detail fetches the current full qBittorrent record for one torrent. It is
// intentionally lazy: list/refresh paths keep only the indexed fields needed
// by Stewarr, while the detail page asks the owning application for details.
func (client *Client) Detail(hash string) (model.Torrent, error) {
	if !client.enabled() {
		return model.Torrent{}, fmt.Errorf("qbittorrent is not configured")
	}
	if err := client.login(); err != nil {
		return model.Torrent{}, err
	}
	var xs []torrentInfo
	path := "/api/v2/torrents/info?hashes=" + url.QueryEscape(strings.TrimSpace(hash))
	if err := client.get(path, &xs); err != nil {
		return model.Torrent{}, err
	}
	if len(xs) == 0 {
		return model.Torrent{}, fmt.Errorf("torrent not found")
	}
	x := xs[0]
	return model.Torrent{Client: client.name, Hash: strings.ToLower(x.Hash), Name: x.Name, State: x.State, Category: x.Category, Tags: x.Tags, Tracker: x.Tracker, SavePath: x.SavePath, ContentPath: x.ContentPath, SizeBytes: x.Size, TotalSizeBytes: x.TotalSize, CompletedBytes: x.Completed, AmountLeftBytes: x.AmountLeft, DownloadedBytes: x.Downloaded, UploadedBytes: x.Uploaded, DownloadedSession: x.DownloadedSession, UploadedSession: x.UploadedSession, DownloadLimit: x.DLLimit, UploadLimit: x.UPLimit, Ratio: x.Ratio, MaxRatio: x.MaxRatio, Progress: x.Progress, Availability: x.Availability, SeedsConnected: x.NumSeeds, LeechersConnected: x.NumLeechs, SeedsSwarm: x.NumComplete, LeechersSwarm: x.NumIncomplete, AddedOn: x.AddedOn, CompletionOn: x.CompletionOn, LastActivity: x.LastActivity, SeenComplete: x.SeenComplete, TimeActive: x.TimeActive, SeedingTime: x.SeedingTime, ETA: x.ETA, Reannounce: x.Reannounce, ForceStart: x.ForceStart, AutoTMM: x.AutoTMM, Sequential: x.Sequential, SuperSeeding: x.SuperSeeding, Private: x.Private}, nil
}

type File struct {
	Index    int     `json:"index"`
	Name     string  `json:"name"`
	Size     int64   `json:"size"`
	Progress float64 `json:"progress"`
}

// torrentFileRoot returns the base directory a torrent's file names (as
// reported by /torrents/files) should be joined against. ContentPath's
// parent, not SavePath, since ContentPath reflects where the content
// actually currently lives — the incomplete-downloads temp path while a
// torrent is still downloading (when that's configured separately from
// SavePath), SavePath itself once complete — while SavePath alone is
// always the torrent's final destination regardless of its current state.
// Using SavePath unconditionally meant a still-downloading torrent's real
// files, sitting under the temp path, never matched the reconstructed
// path — silently misclassifying live, in-progress download data as
// Unmanaged (and, worse, letting VerifyUnmanaged's ownership check miss
// it too). Falls back to SavePath when ContentPath hasn't been populated
// yet (e.g. stale/partial torrent metadata).
func TorrentFileRoot(t model.Torrent) string {
	if cp := strings.TrimSpace(t.ContentPath); cp != "" {
		return filepath.Dir(cp)
	}
	return strings.TrimSpace(t.SavePath)
}

// ClaimedFiles returns the exact filesystem paths claimed by all current
// torrents plus the distinct save roots that should be checked for leftovers.
// It fails closed: if any torrent file list cannot be retrieved, no result is
// returned so callers cannot misclassify owned data as unmanaged.
func (client *Client) ClaimedFiles(torrents map[string]model.Torrent) (map[string]bool, []string, error) {
	claimed := map[string]bool{}
	rootsSet := map[string]bool{}
	if !client.enabled() {
		return claimed, nil, nil
	}
	if err := client.login(); err != nil {
		return nil, nil, err
	}
	type job struct {
		hash string
		t    model.Torrent
	}
	jobs := make(chan job)
	type result struct {
		paths []string
		root  string
		err   error
	}
	results := make(chan result, len(torrents))
	workers := 12
	if len(torrents) < workers {
		workers = len(torrents)
	}
	if workers < 1 {
		workers = 1
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				var fs []File
				path := "/api/v2/torrents/files?hash=" + url.QueryEscape(strings.TrimSpace(j.hash))
				if err := client.get(path, &fs); err != nil {
					results <- result{err: fmt.Errorf("%s: %w", j.hash, err)}
					continue
				}
				root := TorrentFileRoot(j.t)
				ps := make([]string, 0, len(fs))
				for _, f := range fs {
					if strings.TrimSpace(f.Name) != "" {
						ps = append(ps, filepath.Clean(filepath.Join(root, filepath.FromSlash(f.Name))))
					}
				}
				results <- result{paths: ps, root: filepath.Clean(root)}
			}
		}()
	}
	go func() {
		for h, t := range torrents {
			jobs <- job{h, t}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	for r := range results {
		if r.err != nil {
			return nil, nil, r.err
		}
		if r.root != "" && r.root != "." {
			rootsSet[r.root] = true
		}
		for _, p := range r.paths {
			claimed[p] = true
		}
	}
	roots := make([]string, 0, len(rootsSet))
	for r := range rootsSet {
		roots = append(roots, r)
	}
	sort.Strings(roots)
	return claimed, roots, nil
}

func pathInside(root, path string) bool {
	root, path = filepath.Clean(root), filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// VerifyPathsUnmanaged checks the live torrent index rather than a cached
// Stewarr snapshot. content_path cheaply narrows exact file-membership calls
// to torrents that could own a requested path. Rare records without a usable
// content_path are conservatively included in the exact check.
func (client *Client) VerifyPathsUnmanaged(paths []string) error {
	torrents, err := client.Inventory()
	if err != nil {
		return err
	}
	candidates := map[string]model.Torrent{}
	for h, t := range torrents {
		root := strings.TrimSpace(t.ContentPath)
		if root == "" {
			candidates[h] = t
			continue
		}
		for _, p := range paths {
			if pathInside(root, p) {
				candidates[h] = t
				break
			}
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	claimed, _, err := client.ClaimedFiles(candidates)
	if err != nil {
		return err
	}
	for _, p := range paths {
		if claimed[filepath.Clean(p)] {
			return fmt.Errorf("file is now claimed by qBittorrent: %s", filepath.Clean(p))
		}
	}
	return nil
}

// AllFiles returns authoritative file lists for all supplied torrents. It is a
// reconciliation operation, not a normal refresh primitive: qBittorrent does
// not include file membership in its incremental main-data feed.
func (client *Client) AllFiles(torrents map[string]model.Torrent) (map[string][]File, error) {
	out := make(map[string][]File, len(torrents))
	if !client.enabled() || len(torrents) == 0 {
		return out, nil
	}
	if err := client.login(); err != nil {
		return nil, err
	}
	type result struct {
		hash  string
		files []File
		err   error
	}
	jobs := make(chan string)
	results := make(chan result, len(torrents))
	workers := 12
	if len(torrents) < workers {
		workers = len(torrents)
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for hash := range jobs {
				var fs []File
				path := "/api/v2/torrents/files?hash=" + url.QueryEscape(strings.TrimSpace(hash))
				err := client.get(path, &fs)
				results <- result{hash: hash, files: fs, err: err}
			}
		}()
	}
	go func() {
		for hash := range torrents {
			jobs <- hash
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	for r := range results {
		if r.err != nil {
			var notFound *NotFoundError
			if errors.As(r.err, &notFound) {
				// qBittorrent authoritatively has no such torrent anymore
				// (e.g. removed directly, outside Stewarr, between the last
				// sync and this fetch). That torrent has zero known files;
				// it must not fail every other torrent's file fetch too.
				continue
			}
			return nil, fmt.Errorf("%s: %w", r.hash, r.err)
		}
		out[strings.ToLower(r.hash)] = r.files
	}
	return out, nil
}

// Delete removes a torrent and asks qBittorrent, the owning application, to
// remove its torrent-owned data. Hardlinked library paths remain intact.
func (client *Client) Delete(hash string) error {
	if !client.enabled() {
		return fmt.Errorf("qbittorrent is not configured")
	}
	return client.postForm("/api/v2/torrents/delete", url.Values{"hashes": {strings.TrimSpace(hash)}, "deleteFiles": {"true"}})
}

// Validate verifies that the configured qBittorrent endpoint and credentials work.
func (client *Client) Validate() error {
	if !client.enabled() {
		return nil
	}
	return client.login()
}

// RootPurposeIncompleteDownloads labels a root discovered from qBittorrent's
// own "keep incomplete torrents in" setting (temp_path), as distinct from an
// ordinary save-path root — a directory that only ever holds in-progress
// downloads is easy to leave out of a Docker mount by accident, since it's
// not the directory most setups think of as "the media/downloads path."
const RootPurposeIncompleteDownloads = "incomplete downloads"

// RootPath is one storage root discovered from qBittorrent, together with
// why it's a root at all — see RootPurposeIncompleteDownloads. Purpose is
// empty for an ordinary save-path root.
type RootPath struct {
	Path    string
	Purpose string
}

// StorageRoots returns save roots qBittorrent exposes. Current torrent save
// paths are authoritative; the default save_path is included for empty/new
// clients. It also discovers the separate incomplete-downloads directory
// two ways: proactively from qBittorrent's own temp_path/temp_path_enabled
// preference (so it's known even with nothing currently downloading into
// it), and reactively from each torrent's live ContentPath (which qBittorrent
// reports as wherever the content actually currently lives — the temp
// directory while still downloading, the final one after completion) —
// catching it even if temp_path_enabled were ever misreported.
func (client *Client) StorageRoots(torrents map[string]model.Torrent) ([]RootPath, error) {
	if !client.enabled() {
		return nil, nil
	}
	if err := client.login(); err != nil {
		return nil, err
	}
	set := map[string]bool{}
	incomplete := map[string]bool{}
	var prefs struct {
		SavePath        string `json:"save_path"`
		TempPath        string `json:"temp_path"`
		TempPathEnabled bool   `json:"temp_path_enabled"`
	}
	if err := client.get("/api/v2/app/preferences", &prefs); err != nil {
		return nil, err
	}
	if strings.TrimSpace(prefs.SavePath) != "" {
		set[filepath.Clean(prefs.SavePath)] = true
	}
	if prefs.TempPathEnabled && strings.TrimSpace(prefs.TempPath) != "" {
		p := filepath.Clean(prefs.TempPath)
		set[p] = true
		incomplete[p] = true
	}
	for _, t := range torrents {
		if strings.TrimSpace(t.SavePath) != "" {
			set[filepath.Clean(t.SavePath)] = true
		}
		if strings.TrimSpace(t.ContentPath) != "" {
			set[filepath.Clean(t.ContentPath)] = true
		}
	}
	paths := make([]string, 0, len(set))
	for p := range set {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]RootPath, 0, len(paths))
	for _, p := range paths {
		root := RootPath{Path: p}
		if incomplete[p] {
			root.Purpose = RootPurposeIncompleteDownloads
		}
		out = append(out, root)
	}
	return out, nil
}
