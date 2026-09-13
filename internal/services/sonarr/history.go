package sonarr

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

type historyPage struct {
	Page         int             `json:"page"`
	PageSize     int             `json:"pageSize"`
	TotalRecords int             `json:"totalRecords"`
	Records      []historyRecord `json:"records"`
}
type historyRecord struct {
	SeriesID   int    `json:"seriesId"`
	EpisodeID  int    `json:"episodeId"`
	DownloadID string `json:"downloadId"`
	EventType  string `json:"eventType"`
	Date       string `json:"date"`
}

type ImportEvent struct {
	SeriesID   int
	EpisodeID  int
	DownloadID string
	Date       time.Time
}

// ImportEventsSince walks history newest-first and stops at records older than
// since. A zero since performs the one-time full bootstrap.
func (client *Client) ImportEventsSince(since time.Time) ([]ImportEvent, time.Time, error) {
	var out []ImportEvent
	var newest time.Time
	page := 1
	const pageSize = 1000
	for {
		var hp historyPage
		q := url.Values{}
		q.Set("page", fmt.Sprint(page))
		q.Set("pageSize", fmt.Sprint(pageSize))
		q.Set("sortKey", "date")
		q.Set("sortDirection", "descending")
		if err := client.get("/api/v3/history?"+q.Encode(), &hp); err != nil {
			return nil, time.Time{}, err
		}
		stop := false
		for _, r := range hp.Records {
			d, err := time.Parse(time.RFC3339Nano, r.Date)
			if err != nil {
				d, _ = time.Parse(time.RFC3339, r.Date)
			}
			if !d.IsZero() && newest.IsZero() {
				newest = d
			}
			if !since.IsZero() && !d.IsZero() && d.Before(since) {
				stop = true
				break
			}
			if !strings.EqualFold(r.EventType, "downloadFolderImported") || r.SeriesID <= 0 || r.EpisodeID <= 0 || strings.TrimSpace(r.DownloadID) == "" || d.IsZero() {
				continue
			}
			out = append(out, ImportEvent{SeriesID: r.SeriesID, EpisodeID: r.EpisodeID, DownloadID: strings.ToLower(strings.TrimSpace(r.DownloadID)), Date: d})
		}
		if stop || len(hp.Records) == 0 || page*pageSize >= hp.TotalRecords {
			break
		}
		page++
	}
	return out, newest, nil
}
