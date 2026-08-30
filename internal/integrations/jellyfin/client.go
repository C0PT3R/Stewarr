package jellyfin

import (
	"connarr/internal/model"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	base, key string
	hc        *http.Client
	ctx       context.Context
}

func New(base, key string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), key: key, hc: &http.Client{Timeout: 30 * time.Second}}
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

type user struct {
	ID     string `json:"Id"`
	Policy struct {
		IsDisabled bool `json:"IsDisabled"`
	} `json:"Policy"`
}
type page struct {
	Items            []item `json:"Items"`
	TotalRecordCount int    `json:"TotalRecordCount"`
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

func (client *Client) get(path string, out any) error {
	req, _ := http.NewRequestWithContext(client.requestContext(), http.MethodGet, client.base+path, nil)
	req.Header.Set("X-Emby-Token", client.key)
	r, e := client.hc.Do(req)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		return fmt.Errorf("jellyfin %s: %s", path, r.Status)
	}
	return json.NewDecoder(r.Body).Decode(out)
}
func (client *Client) Apply(ms []model.Media) error {
	if client.base == "" || client.key == "" {
		return nil
	}
	var us []user
	if e := client.get("/Users", &us); e != nil {
		return e
	}
	byTM := map[string][]int{}
	byTV := map[string][]int{}
	byIM := map[string][]int{}
	for i, m := range ms {
		kind := string(m.Type)
		if m.TMDBID > 0 {
			key := kind + "\x00" + fmt.Sprint(m.TMDBID)
			byTM[key] = append(byTM[key], i)
		}
		if m.TVDBID > 0 {
			key := kind + "\x00" + fmt.Sprint(m.TVDBID)
			byTV[key] = append(byTV[key], i)
		}
		if m.IMDBID != "" {
			key := kind + "\x00" + strings.ToLower(m.IMDBID)
			byIM[key] = append(byIM[key], i)
		}
	}
	const pageSize = 500
	for _, u := range us {
		if u.Policy.IsDisabled {
			continue
		}
		seen := map[int]bool{}
		// Jellyfin user data is sparse. Fetch only played and favorite items
		// instead of walking the complete catalogue once for every user.
		for _, filter := range []struct {
			key, value string
			played     bool
		}{
			{key: "IsPlayed", value: "true", played: true},
			{key: "IsFavorite", value: "true"},
		} {
			for start := 0; ; start += pageSize {
				q := url.Values{}
				q.Set("Recursive", "true")
				q.Set("IncludeItemTypes", "Movie,Series")
				q.Set("Fields", "ProviderIds")
				q.Set("EnableUserData", "true")
				q.Set(filter.key, filter.value)
				q.Set("StartIndex", fmt.Sprint(start))
				q.Set("Limit", fmt.Sprint(pageSize))
				var p page
				if e := client.get("/Users/"+u.ID+"/Items?"+q.Encode(), &p); e != nil {
					return e
				}
				for _, it := range p.Items {
					kind := strings.ToLower(it.Type)
					if kind != string(model.Movie) && kind != string(model.Series) {
						continue
					}
					var ids []int
					if x := it.ProviderIDs["Tmdb"]; x != "" {
						ids = byTM[kind+"\x00"+x]
					}
					if len(ids) == 0 {
						if x := it.ProviderIDs["Tvdb"]; x != "" {
							ids = byTV[kind+"\x00"+x]
						}
					}
					if len(ids) == 0 {
						if x := it.ProviderIDs["Imdb"]; x != "" {
							ids = byIM[kind+"\x00"+strings.ToLower(x)]
						}
					}
					for _, i := range ids {
						if filter.played {
							ms[i].Views += it.UserData.PlayCount
							if it.UserData.PlayCount > 0 && !seen[i] {
								ms[i].UniqueViewers++
								seen[i] = true
							}
							if t := it.UserData.LastPlayedDate; t != nil && (ms[i].LastWatched == nil || t.After(*ms[i].LastWatched)) {
								tt := *t
								ms[i].LastWatched = &tt
							}
						}
						if !filter.played && it.UserData.IsFavorite {
							ms[i].Favorite = true
						}
					}
				}
				if len(p.Items) < pageSize || (p.TotalRecordCount > 0 && start+len(p.Items) >= p.TotalRecordCount) {
					break
				}
			}
		}
	}
	return nil
}

func (client *Client) Validate() error {
	if client.base == "" || client.key == "" {
		return nil
	}
	var x map[string]any
	return client.get("/System/Info", &x)
}
