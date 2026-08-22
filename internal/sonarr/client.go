package sonarr

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

func (c *Client) get(path string, out any) error {
	req, _ := http.NewRequest(http.MethodGet, c.base+path, nil)
	req.Header.Set("X-Api-Key", c.key)
	r, e := c.hc.Do(req)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		return fmt.Errorf("sonarr %s: %s", path, r.Status)
	}
	return json.NewDecoder(r.Body).Decode(out)
}
func (c *Client) Inventory() ([]model.Media, error) {
	var ss []series
	if e := c.get("/api/v3/series", &ss); e != nil {
		return nil, e
	}
	var ts []tag
	_ = c.get("/api/v3/tag", &ts)
	tm := map[int]string{}
	for _, t := range ts {
		tm[t.ID] = t.Label
	}
	out := make([]model.Media, 0, len(ss))
	for _, s := range ss {
		// A series with no episode files cannot reclaim storage, so it does
		// not belong in Spartarr's inventory. Partially available series remain.
		if s.Statistics.SizeOnDisk <= 0 {
			continue
		}
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
func (c *Client) Delete(id int) error {
	req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v3/series/%d?deleteFiles=true&addImportListExclusion=false", c.base, id), nil)
	req.Header.Set("X-Api-Key", c.key)
	r, e := c.hc.Do(req)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		return fmt.Errorf("sonarr delete: %s", r.Status)
	}
	return nil
}
