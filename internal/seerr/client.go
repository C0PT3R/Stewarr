package seerr

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"spartarr/internal/model"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	base, key string
	hc        *http.Client
}

func New(base, key string) *Client {
	return &Client{strings.TrimRight(base, "/"), key, &http.Client{Timeout: 20 * time.Second}}
}

type response struct {
	PageInfo struct {
		Pages int `json:"pages"`
	} `json:"pageInfo"`
	Results []struct {
		CreatedAt time.Time `json:"createdAt"`
		Media     struct {
			MediaType string `json:"mediaType"`
			TMDBID    int    `json:"tmdbId"`
			TVDBID    int    `json:"tvdbId"`
		} `json:"media"`
	} `json:"results"`
}

func (c *Client) Apply(items []model.Media) error {
	if c.base == "" || c.key == "" {
		return nil
	}
	byTM := map[int][]int{}
	byTV := map[int][]int{}
	for i, m := range items {
		if m.TMDBID > 0 {
			byTM[m.TMDBID] = append(byTM[m.TMDBID], i)
		}
		if m.TVDBID > 0 {
			byTV[m.TVDBID] = append(byTV[m.TVDBID], i)
		}
	}
	skip := 0
	for {
		u := c.base + "/api/v1/request?take=100&skip=" + strconv.Itoa(skip) + "&sort=added"
		req, _ := http.NewRequest(http.MethodGet, u, nil)
		req.Header.Set("X-Api-Key", c.key)
		r, e := c.hc.Do(req)
		if e != nil {
			return e
		}
		if r.StatusCode/100 != 2 {
			r.Body.Close()
			return fmt.Errorf("seerr requests: %s", r.Status)
		}
		var x response
		e = json.NewDecoder(r.Body).Decode(&x)
		r.Body.Close()
		if e != nil {
			return e
		}
		if len(x.Results) == 0 {
			return nil
		}
		for _, q := range x.Results {
			var ids []int
			if q.Media.TMDBID > 0 {
				ids = byTM[q.Media.TMDBID]
			}
			if len(ids) == 0 && q.Media.TVDBID > 0 {
				ids = byTV[q.Media.TVDBID]
			}
			for _, i := range ids {
				items[i].Requested = true
				t := q.CreatedAt
				if items[i].RequestedAt == nil || t.After(*items[i].RequestedAt) {
					items[i].RequestedAt = &t
				}
			}
		}
		skip += len(x.Results)
		if len(x.Results) < 100 {
			return nil
		}
		_ = url.Values{}
	}
}
