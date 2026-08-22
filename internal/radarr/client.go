package radarr

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"spartarr/internal/model"
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
		// Spartarr manages storage, not missing-library metadata. Items that
		// occupy no disk space are intentionally excluded from inventory.
		if m.SizeOnDisk <= 0 {
			continue
		}
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
func (c *Client) Delete(id int) error {
	req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v3/movie/%d?deleteFiles=true&addImportExclusion=false", c.base, id), nil)
	req.Header.Set("X-Api-Key", c.key)
	r, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		return fmt.Errorf("radarr delete: %s", r.Status)
	}
	return nil
}
