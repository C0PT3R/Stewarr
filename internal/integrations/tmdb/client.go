// Package tmdb fetches rating/vote/popularity facts directly from The Movie
// Database. Unlike Radarr/Sonarr/Jellyfin/Seerr, TMDB is not self-hosted:
// there is one fixed public API and one API key, never a URL to configure
// or more than one instance.
package tmdb

import (
	"connarr/internal/model"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const defaultBase = "https://api.themoviedb.org/3"

type Client struct {
	apiKey string
	base   string
	hc     *http.Client
	ctx    context.Context
}

func New(apiKey string) *Client {
	return &Client{apiKey: apiKey, base: defaultBase, hc: &http.Client{Timeout: 20 * time.Second}}
}

// NewWithBaseURL is New with the API base overridden — for tests outside
// this package that need a real *Client pointed at a local httptest
// server instead of the real TMDB API.
func NewWithBaseURL(apiKey, baseURL string) *Client {
	client := New(apiKey)
	client.base = baseURL
	return client
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

func (client *Client) get(path string, extra url.Values, out any) error {
	req, err := http.NewRequestWithContext(client.requestContext(), http.MethodGet, client.base+path, nil)
	if err != nil {
		return err
	}
	query := extra
	if query == nil {
		query = url.Values{}
	}
	query.Set("api_key", client.apiKey)
	req.URL.RawQuery = query.Encode()
	response, err := client.hc.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("tmdb %s: %s", path, response.Status)
	}
	return json.NewDecoder(response.Body).Decode(out)
}

func (client *Client) Validate() error {
	if client.apiKey == "" {
		return nil
	}
	var configuration map[string]any
	return client.get("/configuration", nil, &configuration)
}

type detailsResponse struct {
	Popularity  float64 `json:"popularity"`
	VoteAverage float64 `json:"vote_average"`
	VoteCount   int     `json:"vote_count"`
}

type findResponse struct {
	TVResults []struct {
		ID int `json:"id"`
	} `json:"tv_results"`
}

func (client *Client) details(kind model.MediaType, tmdbID int) (detailsResponse, error) {
	segment := "movie"
	if kind == model.Series {
		segment = "tv"
	}
	var details detailsResponse
	err := client.get(fmt.Sprintf("/%s/%d", segment, tmdbID), nil, &details)
	return details, err
}

// resolveSeriesTMDBID maps a Sonarr series' TVDB id to a TMDB TV id. The
// mapping never changes once found, so a caller only needs to do this once
// per series — see Media.TMDBID, which the merge step backfills so this
// isn't repeated on every enrichment pass.
func (client *Client) resolveSeriesTMDBID(tvdbID int) (int, error) {
	var found findResponse
	err := client.get(fmt.Sprintf("/find/%d", tvdbID), url.Values{"external_source": {"tvdb_id"}}, &found)
	if err != nil {
		return 0, err
	}
	if len(found.TVResults) == 0 {
		return 0, nil
	}
	return found.TVResults[0].ID, nil
}

// Apply enriches items in place with TMDB's own rating, vote count, and
// popularity. A movie is looked up directly by its TMDBID (already known
// from Radarr); a series is looked up by resolving its TVDBID to a TMDB TV
// id first. One item's lookup failing (no match, transient error) never
// aborts the rest of the pass — it's simply left unenriched, and valuation
// falls back to Radarr/Sonarr's own Rating/VoteCount for it.
//
// The one error this does return is the context's own — e.g. context.
// Canceled when the caller's task was interrupted mid-pass (the TMDB task
// is registered Interruptible, so higher-priority work like a removal can
// cancel it). That distinction matters to the caller: the task scheduler
// only recognizes an interruption (and fast-retries via InterruptionDelay,
// instead of waiting for the next scheduled run) by checking
// errors.Is(err, context.Canceled) on what the runner returns. Swallowing
// this the same way per-item failures are swallowed would misreport a
// cut-short pass as a plain success.
func (client *Client) Apply(items []model.Media) error {
	if client.apiKey == "" {
		return nil
	}
	const workers = 8
	workerCount := workers
	if len(items) < workerCount {
		workerCount = len(items)
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workerCount; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				client.applyOne(&items[i])
			}
		}()
	}
	for i := range items {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return client.requestContext().Err()
}

func (client *Client) applyOne(item *model.Media) {
	tmdbID := item.TMDBID
	if item.Type == model.Series && tmdbID == 0 && item.TVDBID > 0 {
		resolved, err := client.resolveSeriesTMDBID(item.TVDBID)
		if err != nil || resolved == 0 {
			return
		}
		item.TMDBID = resolved
		tmdbID = resolved
	}
	if tmdbID == 0 {
		return
	}
	details, err := client.details(item.Type, tmdbID)
	if err != nil {
		return
	}
	item.TMDBRating = details.VoteAverage
	item.TMDBVoteCount = details.VoteCount
	item.Popularity = details.Popularity
	item.TMDBEnrichedAt = time.Now()
}
