package httpui

import (
	"log"
	"net/http"
	"net/url"
	"strings"

	"stewarr/internal/tasks"
)

type taskGroup struct {
	Name  string
	Tasks []tasks.Status
}

type tasksData struct {
	Groups    []taskGroup
	Workflows []tasks.WorkflowStatus
}

// taskGroups separates the flat task list into the "Library & Storage" core
// tasks (always shown) and one group per enrichment service, shown only
// when that service is actually configured.
func (server *Server) taskGroups(all []tasks.Status) []taskGroup {
	cfg := server.inv.Config()
	byID := make(map[string]tasks.Status, len(all))
	for _, status := range all {
		byID[status.ID] = status
	}
	var groups []taskGroup
	var core []tasks.Status
	for _, id := range []string{"inventory", "files", removalTaskID, autoRemovalTaskID} {
		if status, ok := byID[id]; ok {
			core = append(core, status)
		}
	}
	if len(core) > 0 {
		groups = append(groups, taskGroup{Name: "Library & Storage", Tasks: core})
	}
	if cfg.Jellyfin.URL != "" {
		if status, ok := byID["jellyfin"]; ok {
			groups = append(groups, taskGroup{Name: "Jellyfin", Tasks: []tasks.Status{status}})
		}
	}
	if cfg.Seerr.URL != "" {
		if status, ok := byID["seerr"]; ok {
			groups = append(groups, taskGroup{Name: "Seerr", Tasks: []tasks.Status{status}})
		}
	}
	if cfg.TMDB.APIKey != "" {
		if status, ok := byID["tmdb"]; ok {
			groups = append(groups, taskGroup{Name: "TMDB", Tasks: []tasks.Status{status}})
		}
	}
	return groups
}

type operationPageData struct {
	Active     string
	FragmentID string
	Label      string
	Notice     operationNotice
	BackURL    string
	BackLabel  string
}

func (server *Server) renderOperation(response http.ResponseWriter, data operationPageData) {
	if strings.TrimSpace(data.Label) == "" {
		data.Label = "Removal operation"
	}
	if err := renderTemplate(response, server.operationTpl, data); err != nil {
		log.Printf("[http] render operation state: %v", err)
	}
}

func (server *Server) tasksPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/tasks" {
		http.NotFound(w, r)
		return
	}
	if server.tasks == nil {
		log.Printf("[removal] [operation=0] rejected reason=%q", "durable scheduler unavailable")
		http.Error(w, "task manager unavailable", http.StatusServiceUnavailable)
		return
	}
	all := server.tasks.Snapshot()
	if err := renderTemplate(w, server.tasksTpl, tasksData{Groups: server.taskGroups(all), Workflows: server.tasks.WorkflowStatuses()}); err != nil {
		log.Printf("[http] render tasks: %v", err)
	}
}

func (server *Server) runTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if server.tasks == nil {
		http.Error(w, "task manager unavailable", http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		http.Error(w, "missing task id", http.StatusBadRequest)
		return
	}
	receipt, err := server.tasks.RunAsyncApp(id)
	if err != nil {
		http.Error(w, "task could not be scheduled: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("X-Stewarr-Trigger-ID", receipt.TriggerID)
	http.Redirect(w, r, "/tasks?trigger="+url.QueryEscape(receipt.TriggerID), http.StatusSeeOther)
}
