package radarr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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

type movie struct {
	ID         int       `json:"id"`
	Title      string    `json:"title"`
	Year       int       `json:"year"`
	Path       string    `json:"path"`
	SizeOnDisk int64     `json:"sizeOnDisk"`
	Added      time.Time `json:"added"`
	TMDBID     int       `json:"tmdbId"`
	IMDBID     string    `json:"imdbId"`
	Tags       []int     `json:"tags"`
	Ratings    struct {
		TMDB struct {
			Value float64 `json:"value"`
			Votes int     `json:"votes"`
		} `json:"tmdb"`
		IMDB struct {
			Value float64 `json:"value"`
			Votes int     `json:"votes"`
		} `json:"imdb"`
	} `json:"ratings"`
}
type tag struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

func (client *Client) get(path string, out any) error {
	req, _ := http.NewRequestWithContext(client.requestContext(), http.MethodGet, client.base+path, nil)
	req.Header.Set("X-Api-Key", client.key)
	r, err := client.hc.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		return fmt.Errorf("radarr %s: %s", path, r.Status)
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
		return fmt.Errorf("radarr %s: %s", path, response.Status)
	}
	return nil
}
func (client *Client) Inventory() ([]model.Media, error) {
	var ms []movie
	var ts []tag
	var mediaErr, tagErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); mediaErr = client.get("/api/v3/movie", &ms) }()
	go func() { defer wg.Done(); tagErr = client.get("/api/v3/tag", &ts) }()
	wg.Wait()
	if mediaErr != nil {
		return nil, mediaErr
	}
	if tagErr != nil {
		return nil, fmt.Errorf("radarr tags: %w", tagErr)
	}
	tm := map[int]string{}
	for _, t := range ts {
		tm[t.ID] = t.Label
	}
	out := make([]model.Media, 0, len(ms))
	for _, m := range ms {
		rating, votes := m.Ratings.TMDB.Value, m.Ratings.TMDB.Votes
		if rating == 0 {
			rating, votes = m.Ratings.IMDB.Value, m.Ratings.IMDB.Votes
		}
		labels := []string{}
		for _, id := range m.Tags {
			if s := tm[id]; s != "" {
				labels = append(labels, s)
			}
		}
		out = append(out, model.Media{Type: model.Movie, SourceID: m.ID, Title: m.Title, Year: m.Year, Path: m.Path, SizeBytes: m.SizeOnDisk, Rating: rating, VoteCount: votes, AddedAt: m.Added, Tags: labels, TMDBID: m.TMDBID, IMDBID: m.IMDBID})
	}
	return out, nil
}

type FileRecord struct {
	ID        int       `json:"id"`
	MovieID   int       `json:"movieId"`
	Relative  string    `json:"relativePath"`
	Size      int64     `json:"size"`
	DateAdded time.Time `json:"dateAdded"`
}

// Files returns authoritative current movie files in bounded bulk requests.
// Radarr accepts repeated movieId query parameters, so this avoids one request
// per movie while keeping URLs to a reasonable size.
func (client *Client) Files(movieIDs []int) ([]FileRecord, error) {
	if len(movieIDs) == 0 {
		return nil, nil
	}
	const chunk = 100
	out := make([]FileRecord, 0)
	for start := 0; start < len(movieIDs); start += chunk {
		end := start + chunk
		if end > len(movieIDs) {
			end = len(movieIDs)
		}
		q := url.Values{}
		for _, id := range movieIDs[start:end] {
			q.Add("movieId", fmt.Sprint(id))
		}
		var xs []FileRecord
		if err := client.get("/api/v3/moviefile?"+q.Encode(), &xs); err != nil {
			return nil, err
		}
		out = append(out, xs...)
	}
	return out, nil
}

func (client *Client) DeleteFile(id int) error {
	req, _ := http.NewRequestWithContext(client.requestContext(), http.MethodDelete, fmt.Sprintf("%s/api/v3/moviefile/%d", client.base, id), nil)
	req.Header.Set("X-Api-Key", client.key)
	r, err := client.hc.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		return fmt.Errorf("radarr movie file delete: %s", r.Status)
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
// info hash, if Radarr currently has one — e.g. an incomplete torrent it's
// still waiting to import. Comparison is case-insensitive since Radarr's
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

// RemoveQueueItem removes a queue entry and asks Radarr to instruct the
// download client to remove the underlying download too, rather than
// leaving an orphaned client-side download behind once the queue entry
// Radarr was tracking it under is gone.
func (client *Client) RemoveQueueItem(id int) error {
	req, _ := http.NewRequestWithContext(client.requestContext(), http.MethodDelete, fmt.Sprintf("%s/api/v3/queue/%d?removeFromClient=true&blocklist=false", client.base, id), nil)
	req.Header.Set("X-Api-Key", client.key)
	r, err := client.hc.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		return fmt.Errorf("radarr queue delete: %s", r.Status)
	}
	return nil
}

func (client *Client) SetMonitored(id int, monitored bool) error {
	var movie map[string]any
	if err := client.get(fmt.Sprintf("/api/v3/movie/%d", id), &movie); err != nil {
		return err
	}
	movie["monitored"] = monitored
	b, err := json.Marshal(movie)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(client.requestContext(), http.MethodPut, fmt.Sprintf("%s/api/v3/movie/%d", client.base, id), bytes.NewReader(b))
	req.Header.Set("X-Api-Key", client.key)
	req.Header.Set("Content-Type", "application/json")
	r, err := client.hc.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		return fmt.Errorf("radarr movie update: %s", r.Status)
	}
	return nil
}

// AddImportListExclusion prevents a configured Radarr import list from
// re-adding a movie whose managed files were intentionally removed.
func (client *Client) AddImportListExclusion(title string, year, tmdbID int) error {
	if tmdbID <= 0 {
		return fmt.Errorf("radarr import-list exclusion requires a TMDB id")
	}
	body := struct {
		MovieTitle string `json:"movieTitle"`
		TMDBID     int    `json:"tmdbId"`
		MovieYear  int    `json:"movieYear"`
	}{MovieTitle: title, TMDBID: tmdbID, MovieYear: year}
	return client.postJSON("/api/v3/exclusions", body)
}

type RootFolder struct {
	Path string `json:"path"`
}

// StorageRoots returns Radarr's authoritative configured root folders.
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

// Validate verifies that the configured Radarr endpoint and credentials work.
func (client *Client) Validate() error {
	if client.base == "" {
		return nil
	}
	var x map[string]any
	return client.get("/api/v3/system/status", &x)
}
