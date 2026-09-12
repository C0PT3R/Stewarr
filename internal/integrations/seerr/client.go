package seerr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"stewarr/internal/model"
	"strconv"
	"strings"
	"time"
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

func typedID(kind string, id int) string { return strings.ToLower(kind) + "\x00" + strconv.Itoa(id) }

func (client *Client) Apply(items []model.Media) error {
	if client.base == "" || client.key == "" {
		return nil
	}
	byTM := map[string][]int{}
	byTV := map[string][]int{}
	for i, m := range items {
		kind := string(m.Type)
		if m.TMDBID > 0 {
			byTM[typedID(kind, m.TMDBID)] = append(byTM[typedID(kind, m.TMDBID)], i)
		}
		if m.TVDBID > 0 {
			byTV[typedID(kind, m.TVDBID)] = append(byTV[typedID(kind, m.TVDBID)], i)
		}
	}
	skip := 0
	for {
		u := client.base + "/api/v1/request?take=100&skip=" + strconv.Itoa(skip) + "&sort=added"
		req, _ := http.NewRequestWithContext(client.requestContext(), http.MethodGet, u, nil)
		req.Header.Set("X-Api-Key", client.key)
		r, e := client.hc.Do(req)
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
			kind := strings.ToLower(q.Media.MediaType)
			if kind == "tv" {
				kind = string(model.Series)
			}
			var ids []int
			if q.Media.TMDBID > 0 {
				ids = byTM[typedID(kind, q.Media.TMDBID)]
			}
			if len(ids) == 0 && q.Media.TVDBID > 0 {
				ids = byTV[typedID(kind, q.Media.TVDBID)]
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
	}
}

func (client *Client) Validate() error {
	if client.base == "" || client.key == "" {
		return nil
	}
	req, _ := http.NewRequestWithContext(client.requestContext(), http.MethodGet, client.base+"/api/v1/status", nil)
	req.Header.Set("X-Api-Key", client.key)
	r, err := client.hc.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		return fmt.Errorf("seerr status: %s", r.Status)
	}
	return nil
}
