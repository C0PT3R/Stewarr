package jellyfin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"spartarr/internal/model"
	"strings"
	"time"
)

type Client struct {
	base, key string
	hc        *http.Client
}

func New(base, key string) *Client {
	return &Client{strings.TrimRight(base, "/"), key, &http.Client{Timeout: 30 * time.Second}}
}

type user struct {
	ID string `json:"Id"`
}
type page struct {
	Items []item `json:"Items"`
}
type item struct {
	Type        string            `json:"Type"`
	ProviderIDs map[string]string `json:"ProviderIds"`
	UserData    struct {
		PlayCount      int        `json:"PlayCount"`
		LastPlayedDate *time.Time `json:"LastPlayedDate"`
		IsFavorite     bool       `json:"IsFavorite"`
	} `json:"UserData"`
}

func (c *Client) get(path string, out any) error {
	req, _ := http.NewRequest(http.MethodGet, c.base+path, nil)
	req.Header.Set("X-Emby-Token", c.key)
	r, e := c.hc.Do(req)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		return fmt.Errorf("jellyfin %s: %s", path, r.Status)
	}
	return json.NewDecoder(r.Body).Decode(out)
}
func (c *Client) Apply(ms []model.Media) error {
	if c.base == "" || c.key == "" {
		return nil
	}
	var us []user
	if e := c.get("/Users", &us); e != nil {
		return e
	}
	byTM := map[string][]int{}
	byTV := map[string][]int{}
	byIM := map[string][]int{}
	for i, m := range ms {
		if m.TMDBID > 0 {
			byTM[fmt.Sprint(m.TMDBID)] = append(byTM[fmt.Sprint(m.TMDBID)], i)
		}
		if m.TVDBID > 0 {
			byTV[fmt.Sprint(m.TVDBID)] = append(byTV[fmt.Sprint(m.TVDBID)], i)
		}
		if m.IMDBID != "" {
			byIM[m.IMDBID] = append(byIM[m.IMDBID], i)
		}
	}
	for _, u := range us {
		q := url.Values{}
		q.Set("Recursive", "true")
		q.Set("IncludeItemTypes", "Movie,Series")
		q.Set("Fields", "ProviderIds")
		q.Set("EnableUserData", "true")
		var p page
		if e := c.get("/Users/"+u.ID+"/Items?"+q.Encode(), &p); e != nil {
			return e
		}
		seen := map[int]bool{}
		for _, it := range p.Items {
			var ids []int
			if x := it.ProviderIDs["Tmdb"]; x != "" {
				ids = byTM[x]
			}
			if len(ids) == 0 {
				if x := it.ProviderIDs["Tvdb"]; x != "" {
					ids = byTV[x]
				}
			}
			if len(ids) == 0 {
				if x := it.ProviderIDs["Imdb"]; x != "" {
					ids = byIM[x]
				}
			}
			for _, i := range ids {
				ms[i].Views += it.UserData.PlayCount
				if it.UserData.PlayCount > 0 && !seen[i] {
					ms[i].UniqueViewers++
					seen[i] = true
				}
				if it.UserData.IsFavorite {
					ms[i].Favorite = true
				}
				if t := it.UserData.LastPlayedDate; t != nil && (ms[i].LastWatched == nil || t.After(*ms[i].LastWatched)) {
					tt := *t
					ms[i].LastWatched = &tt
				}
			}
		}
	}
	return nil
}
