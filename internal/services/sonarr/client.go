package sonarr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"stewarr/internal/model"
)

type Client struct {
	base, key string
	hc        *http.Client
	ctx       context.Context
}

func New(base, key string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), key: key, hc: &http.Client{Timeout: 20 * time.Second}}
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

type series struct {
	ID         int    `json:"id"`
	Title      string `json:"title"`
	Year       int    `json:"year"`
	Path       string `json:"path"`
	Statistics struct {
		SizeOnDisk int64 `json:"sizeOnDisk"`
	} `json:"statistics"`
	Added   time.Time `json:"added"`
	TVDBID  int       `json:"tvdbId"`
	IMDBID  string    `json:"imdbId"`
	Tags    []int     `json:"tags"`
	Ratings struct {
		Value float64 `json:"value"`
		Votes int     `json:"votes"`
	} `json:"ratings"`
}
type tag struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

func (client *Client) get(path string, out any) error {
	req, _ := http.NewRequestWithContext(client.requestContext(), http.MethodGet, client.base+path, nil)
	req.Header.Set("X-Api-Key", client.key)
	r, e := client.hc.Do(req)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		return fmt.Errorf("sonarr %s: %s", path, r.Status)
	}
	return json.NewDecoder(r.Body).Decode(out)
}

func (client *Client) postJSON(path string, body any) error {
	encodedBody, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(client.requestContext(), http.MethodPost, client.base+path, bytes.NewReader(encodedBody))
	if err != nil {
		return err
	}
	request.Header.Set("X-Api-Key", client.key)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.hc.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("sonarr %s: %s", path, response.Status)
	}
	return nil
}
func (client *Client) Inventory() ([]model.Media, error) {
	var ss []series
	var ts []tag
	var seriesErr, tagErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); seriesErr = client.get("/api/v3/series", &ss) }()
	go func() { defer wg.Done(); tagErr = client.get("/api/v3/tag", &ts) }()
	wg.Wait()
	if seriesErr != nil {
		return nil, seriesErr
	}
	if tagErr != nil {
		return nil, fmt.Errorf("sonarr tags: %w", tagErr)
	}
	tm := map[int]string{}
	for _, t := range ts {
		tm[t.ID] = t.Label
	}
	out := make([]model.Media, 0, len(ss))
	for _, s := range ss {
		labels := []string{}
		for _, id := range s.Tags {
			if x := tm[id]; x != "" {
				labels = append(labels, x)
			}
		}
		out = append(out, model.Media{Type: model.Series, SourceID: s.ID, Title: s.Title, Year: s.Year, Path: s.Path, SizeBytes: s.Statistics.SizeOnDisk, Rating: s.Ratings.Value, VoteCount: s.Ratings.Votes, AddedAt: s.Added, Tags: labels, TVDBID: s.TVDBID, IMDBID: s.IMDBID})
	}
	return out, nil
}

type FileRecord struct {
	ID           int                   `json:"id"`
	SeriesID     int                   `json:"seriesId"`
	SeasonNumber int                   `json:"-"`
	Relative     string                `json:"relativePath"`
	Size         int64                 `json:"size"`
	DateAdded    time.Time             `json:"dateAdded"`
	Parts        []model.MediaFilePart `json:"-"`
}

type episodeRecord struct {
	ID            int       `json:"id"`
	EpisodeFileID int       `json:"episodeFileId"`
	SeasonNumber  int       `json:"seasonNumber"`
	EpisodeNumber int       `json:"episodeNumber"`
	Title         string    `json:"title"`
	AirDateUtc    time.Time `json:"airDateUtc"`
}

// Files returns authoritative current episode files. Sonarr's v3 endpoint is
// scoped to one series, so reconciliation uses a small worker pool and is kept
// out of the normal inventory refresh path.
func (client *Client) Files(seriesIDs []int) ([]FileRecord, error) {
	if len(seriesIDs) == 0 {
		return nil, nil
	}
	type result struct {
		files []FileRecord
		err   error
	}
	jobs := make(chan int)
	results := make(chan result, len(seriesIDs))
	workers := 8
	if len(seriesIDs) < workers {
		workers = len(seriesIDs)
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				var xs []FileRecord
				var episodes []episodeRecord
				var filesErr, episodesErr error
				var calls sync.WaitGroup
				calls.Add(2)
				go func() {
					defer calls.Done()
					filesErr = client.get(fmt.Sprintf("/api/v3/episodefile?seriesId=%d", id), &xs)
				}()
				go func() {
					defer calls.Done()
					episodesErr = client.get(fmt.Sprintf("/api/v3/episode?seriesId=%d", id), &episodes)
				}()
				calls.Wait()
				if filesErr != nil {
					results <- result{nil, filesErr}
					continue
				}
				if episodesErr != nil {
					results <- result{nil, episodesErr}
					continue
				}
				parts := map[int][]model.MediaFilePart{}
				seasonByFileID := map[int]int{}
				for _, e := range episodes {
					if e.EpisodeFileID <= 0 {
						continue
					}
					label := fmt.Sprintf("S%02dE%02d", e.SeasonNumber, e.EpisodeNumber)
					if strings.TrimSpace(e.Title) != "" {
						label += " · " + e.Title
					}
					parts[e.EpisodeFileID] = append(parts[e.EpisodeFileID], model.MediaFilePart{
						Group: fmt.Sprintf("Season %d", e.SeasonNumber), Label: label,
						Order: e.SeasonNumber*100000 + e.EpisodeNumber, SourcePartID: e.ID,
						AiredAt: e.AirDateUtc,
					})
					seasonByFileID[e.EpisodeFileID] = e.SeasonNumber
				}
				for i := range xs {
					xs[i].SeriesID = id
					xs[i].SeasonNumber = seasonByFileID[xs[i].ID]
					xs[i].Parts = append([]model.MediaFilePart(nil), parts[xs[i].ID]...)
				}
				results <- result{xs, nil}
			}
		}()
	}
	go func() {
		for _, id := range seriesIDs {
			jobs <- id
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	out := make([]FileRecord, 0)
	for r := range results {
		if r.err != nil {
			return nil, r.err
		}
		out = append(out, r.files...)
	}
	return out, nil
}

func (client *Client) DeleteFile(id int) error {
	req, _ := http.NewRequestWithContext(client.requestContext(), http.MethodDelete, fmt.Sprintf("%s/api/v3/episodefile/%d", client.base, id), nil)
	req.Header.Set("X-Api-Key", client.key)
	r, e := client.hc.Do(req)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		return fmt.Errorf("sonarr episode file delete: %s", r.Status)
	}
	return nil
}

type queueRecord struct {
	ID         int    `json:"id"`
	DownloadID string `json:"downloadId"`
}
type queueResponse struct {
	PageSize     int           `json:"pageSize"`
	TotalRecords int           `json:"totalRecords"`
	Records      []queueRecord `json:"records"`
}

// FindQueueItem returns the queue entry id for the download with the given
// info hash, if Sonarr currently has one — e.g. an incomplete torrent it's
// still waiting to import. Comparison is case-insensitive since Sonarr's
// downloadId casing isn't guaranteed to match the torrent client's.
func (client *Client) FindQueueItem(hash string) (int, bool, error) {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return 0, false, fmt.Errorf("hash is required")
	}
	for page := 1; ; page++ {
		var resp queueResponse
		if err := client.get(fmt.Sprintf("/api/v3/queue?page=%d&pageSize=250", page), &resp); err != nil {
			return 0, false, err
		}
		for _, rec := range resp.Records {
			if strings.EqualFold(rec.DownloadID, hash) {
				return rec.ID, true, nil
			}
		}
		if len(resp.Records) == 0 || page*resp.PageSize >= resp.TotalRecords {
			return 0, false, nil
		}
	}
}

// RemoveQueueItem removes a queue entry and asks Sonarr to instruct the
// download client to remove the underlying download too, rather than
// leaving an orphaned client-side download behind once the queue entry
// Sonarr was tracking it under is gone.
func (client *Client) RemoveQueueItem(id int) error {
	req, _ := http.NewRequestWithContext(client.requestContext(), http.MethodDelete, fmt.Sprintf("%s/api/v3/queue/%d?removeFromClient=true&blocklist=false", client.base, id), nil)
	req.Header.Set("X-Api-Key", client.key)
	r, e := client.hc.Do(req)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		return fmt.Errorf("sonarr queue delete: %s", r.Status)
	}
	return nil
}

func (client *Client) SetEpisodesMonitored(ids []int, monitored bool) error {
	if len(ids) == 0 {
		return nil
	}
	body := struct {
		EpisodeIDs []int `json:"episodeIds"`
		Monitored  bool  `json:"monitored"`
	}{EpisodeIDs: ids, Monitored: monitored}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(client.requestContext(), http.MethodPut, client.base+"/api/v3/episode/monitor", bytes.NewReader(b))
	req.Header.Set("X-Api-Key", client.key)
	req.Header.Set("Content-Type", "application/json")
	r, err := client.hc.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		return fmt.Errorf("sonarr episode monitoring update: %s", r.Status)
	}
	return nil
}

// resolveOrCreateTagID returns the id of the tag labeled label, creating
// it in Sonarr first if no such tag exists yet. Sonarr's own tags are
// referenced by numeric id everywhere else in its API, never by label
// directly.
func (client *Client) resolveOrCreateTagID(label string) (int, error) {
	var tags []tag
	if err := client.get("/api/v3/tag", &tags); err != nil {
		return 0, err
	}
	for _, t := range tags {
		if strings.EqualFold(t.Label, label) {
			return t.ID, nil
		}
	}
	encodedBody, err := json.Marshal(map[string]string{"label": label})
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(client.requestContext(), http.MethodPost, client.base+"/api/v3/tag", bytes.NewReader(encodedBody))
	if err != nil {
		return 0, err
	}
	request.Header.Set("X-Api-Key", client.key)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.hc.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return 0, fmt.Errorf("sonarr create tag: %s", response.Status)
	}
	var created tag
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		return 0, err
	}
	return created.ID, nil
}

// AddKeepTag tags seriesID with label (creating the tag in Sonarr if it
// doesn't already exist there) — used to permanently protect a series
// from cleanup directly from Stewarr's own UI, the same mechanism a user
// manually tagging it in Sonarr itself would produce. A no-op, not an
// error, if the series already carries the tag.
func (client *Client) AddKeepTag(seriesID int, label string) error {
	tagID, err := client.resolveOrCreateTagID(label)
	if err != nil {
		return err
	}
	var s map[string]any
	if err := client.get(fmt.Sprintf("/api/v3/series/%d", seriesID), &s); err != nil {
		return err
	}
	rawTags, _ := s["tags"].([]any)
	for _, t := range rawTags {
		if id, ok := t.(float64); ok && int(id) == tagID {
			return nil
		}
	}
	s["tags"] = append(rawTags, tagID)
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(client.requestContext(), http.MethodPut, fmt.Sprintf("%s/api/v3/series/%d", client.base, seriesID), bytes.NewReader(b))
	req.Header.Set("X-Api-Key", client.key)
	req.Header.Set("Content-Type", "application/json")
	r, err := client.hc.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		return fmt.Errorf("sonarr series update: %s", r.Status)
	}
	return nil
}

// AddImportListExclusion prevents a configured Sonarr import list from
// re-adding a series whose managed files were intentionally removed.
func (client *Client) AddImportListExclusion(title string, tvdbID int) error {
	if tvdbID <= 0 {
		return fmt.Errorf("sonarr import-list exclusion requires a TVDB id")
	}
	body := struct {
		TVDBID int    `json:"tvdbId"`
		Title  string `json:"title"`
	}{TVDBID: tvdbID, Title: title}
	return client.postJSON("/api/v3/importlistexclusion", body)
}

type RootFolder struct {
	Path string `json:"path"`
}

// StorageRoots returns Sonarr's authoritative configured root folders.
func (client *Client) StorageRoots() ([]string, error) {
	if client.base == "" {
		return nil, nil
	}
	var xs []RootFolder
	if err := client.get("/api/v3/rootfolder", &xs); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if strings.TrimSpace(x.Path) != "" {
			out = append(out, x.Path)
		}
	}
	return out, nil
}

// Validate verifies that the configured Sonarr endpoint and credentials work.
func (client *Client) Validate() error {
	if client.base == "" {
		return nil
	}
	var x map[string]any
	return client.get("/api/v3/system/status", &x)
}
