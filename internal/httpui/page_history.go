package httpui

import (
	"encoding/json"
	"log"
	"net/http"

	"stewarr/internal/removal"
	"stewarr/internal/store"
)

type historyEventView struct {
	store.HistoryEvent
	Files   []removal.FileState
	Results []string
	Errors  []string
}

func historyEventViews(events []store.HistoryEvent) []historyEventView {
	views := make([]historyEventView, 0, len(events))
	for _, event := range events {
		view := historyEventView{HistoryEvent: event}
		var payload struct {
			Plan struct {
				Files []removal.FileState `json:"files"`
			} `json:"plan"`
			Results []string `json:"results"`
			Errors  []string `json:"errors"`
		}
		if json.Unmarshal(event.Payload, &payload) == nil {
			view.Files = payload.Plan.Files
			view.Results = payload.Results
			view.Errors = payload.Errors
		}
		views = append(views, view)
	}
	return views
}

func (server *Server) history(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/history" {
		http.NotFound(w, r)
		return
	}
	var stats store.CleanupStats
	var runs []store.CleanupRun
	var events []store.HistoryEvent
	if db := server.inv.Store(); db != nil {
		stats, _ = db.CleanupStatistics()
		runs, _ = db.CleanupRuns(100)
		events, _ = db.HistoryEvents(200)
	}
	d := struct {
		Stats  store.CleanupStats
		Runs   []store.CleanupRun
		Events []historyEventView
	}{stats, runs, historyEventViews(events)}
	if e := renderTemplate(w, server.historyTpl, d); e != nil {
		log.Printf("[http] render history: %v", e)
	}
}
