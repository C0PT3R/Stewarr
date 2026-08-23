package radarr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"togetharr/internal/model"
)

type Client struct {
	base, key string
	hc        *http.Client
}

func New(base, key string) *Client {
	return &Client{strings.TrimRight(base, "/"), key, &http.Client{Timeout: 20 * time.Second}}
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

func (c *Client) get(path string, out any) error {
	req, _ := http.NewRequest(http.MethodGet, c.base+path, nil)
	req.Header.Set("X-Api-Key", c.key)
	r, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		return fmt.Errorf("radarr %s: %s", path, r.Status)
	}
	return json.NewDecoder(r.Body).Decode(out)
}
func (c *Client) Inventory() ([]model.Media, error) {
	var ms []movie
	if err := c.get("/api/v3/movie", &ms); err != nil {
		return nil, err
	}
	var ts []tag
	_ = c.get("/api/v3/tag", &ts)
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
func (c *Client) Files(movieIDs []int) ([]FileRecord, error) {
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
		if err := c.get("/api/v3/moviefile?"+q.Encode(), &xs); err != nil {
			return nil, err
		}
		out = append(out, xs...)
	}
	return out, nil
}

func (c *Client) DeleteFile(id int) error {
	req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v3/moviefile/%d", c.base, id), nil)
	req.Header.Set("X-Api-Key", c.key)
	r, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		return fmt.Errorf("radarr movie file delete: %s", r.Status)
	}
	return nil
}

func (c *Client) SetMonitored(id int, monitored bool) error {
	var movie map[string]any
	if err := c.get(fmt.Sprintf("/api/v3/movie/%d", id), &movie); err != nil {
		return err
	}
	movie["monitored"] = monitored
	b, err := json.Marshal(movie)
	if err != nil {
		return err
	}
	req, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/v3/movie/%d", c.base, id), bytes.NewReader(b))
	req.Header.Set("X-Api-Key", c.key)
	req.Header.Set("Content-Type", "application/json")
	r, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		return fmt.Errorf("radarr movie update: %s", r.Status)
	}
	return nil
}

type RootFolder struct {
	Path string `json:"path"`
}

// StorageRoots returns Radarr's authoritative configured root folders.
func (c *Client) StorageRoots() ([]string, error) {
	if c.base == "" {
		return nil, nil
	}
	var xs []RootFolder
	if err := c.get("/api/v3/rootfolder", &xs); err != nil {
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
func (c *Client) Validate() error {
	if c.base == "" {
		return nil
	}
	var x map[string]any
	return c.get("/api/v3/system/status", &x)
}
