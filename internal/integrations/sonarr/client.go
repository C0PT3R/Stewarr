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
