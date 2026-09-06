package httpui

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/http/pprof"
	"strconv"
	"strings"
	"sync"
	"time"

	"connarr/internal/cleanup"
	"connarr/internal/inventory"
	"connarr/internal/model"
	"connarr/internal/product"
	"connarr/internal/tasks"
)

type Server struct {
	inv                *inventory.Service
	tasks              *tasks.Manager
	homeTpl            *template.Template
	storageTpl         *template.Template
	servicesTpl        *template.Template
	addIntegrationTpl  *template.Template
	editIntegrationTpl *template.Template
	libraryTpl         *template.Template
	historyTpl         *template.Template
	profileTpl         *template.Template
	torrentTpl         *template.Template
	torrentDetailTpl   *template.Template
	unmanagedTpl       *template.Template
	tasksTpl           *template.Template
	removalTpl         *template.Template
	operationTpl       *template.Template
	staticHandler      http.Handler
	revisions          *revisionHub
	startOnce          sync.Once
	admissionMu        sync.Mutex
	homeMu             sync.Mutex
	homeRevision       uint64
	homeCache          homeData
	// sseMaxLifetime bounds how long any single /ui/events connection is
	// kept open before the server ends it and lets the client's built-in
	// EventSource auto-reconnect start a fresh one — see uiEvents. Zero
	// value in New() is replaced with the real default; tests override it
	// directly to verify the rotation fires without waiting minutes.
	sseMaxLifetime time.Duration
}

func New(inventoryService *inventory.Service, taskManager *tasks.Manager) (*Server, error) {
	templateFunctions := template.FuncMap{"appName": func() string { return product.Name }, "appVersion": func() string { return product.Version }, "head": appHead, "chrome": func(active string) template.HTML { return template.HTML(appChrome(active)) }, "human": func(bytes int64) string {
		if bytes < 0 {
			bytes = 0
		}
		return cleanup.Human(uint64(bytes))
	}, "humanU": func(bytes uint64) string { return cleanup.Human(bytes) }, "rate": func(bytesPerSecond int64) string { return cleanup.Human(uint64(max64(bytesPerSecond, 0))) + "/s" }, "duration": humanDuration, "durationGo": func(duration time.Duration) string { return humanDuration(int64(duration / time.Second)) }, "unixTime": unixTime, "pct": func(ratio float64) string { return fmt.Sprintf("%.1f%%", ratio*100) }, "fmtTime": func(timestamp *time.Time) string {
		if timestamp == nil {
			return "Never"
		}
		return timestamp.Local().Format("2006-01-02")
	}, "join": strings.Join, "add": func(first, second int) int { return first + second }, "managedKey": managedFileKey, "shortPath": shortPath, "widthPct": func(part, total uint64) string {
		if total == 0 {
			return "0"
		}
		return fmt.Sprintf("%.3f", float64(part)/float64(total)*100)
	}, "fmtUpdated": func(timestamp time.Time) string {
		if timestamp.IsZero() {
			return "Never"
		}
		return timestamp.Local().Format("2006-01-02 15:04:05")
	}}
	homeTemplate, err := parseUITemplate("home.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	storageTemplate, err := parseUITemplate("storage.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	servicesTemplate, err := parseUITemplate("services.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	addIntegrationTemplate, err := parseUITemplate("add_integration.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	editIntegrationTemplate, err := parseUITemplate("edit_integration.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	libraryTemplate, err := parseUITemplate("library.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	historyTemplate, err := parseUITemplate("history.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	profileTemplate, err := parseUITemplate("media.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	torrentTemplate, err := parseUITemplate("torrents.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	torrentDetailTemplate, err := parseUITemplate("torrent.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	unmanagedTemplate, err := parseUITemplate("unmanaged.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	tasksTemplate, err := parseUITemplate("tasks.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	removalTemplate, err := parseUITemplate("removal.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	operationTemplate, err := parseUITemplate("operation.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	staticHandler, err := uiStaticHandler()
	if err != nil {
		return nil, err
	}
	server := &Server{inv: inventoryService, tasks: taskManager, homeTpl: homeTemplate, storageTpl: storageTemplate, servicesTpl: servicesTemplate, addIntegrationTpl: addIntegrationTemplate, editIntegrationTpl: editIntegrationTemplate, libraryTpl: libraryTemplate, historyTpl: historyTemplate, profileTpl: profileTemplate, torrentTpl: torrentTemplate, torrentDetailTpl: torrentDetailTemplate, unmanagedTpl: unmanagedTemplate, tasksTpl: tasksTemplate, removalTpl: removalTemplate, operationTpl: operationTemplate, staticHandler: staticHandler, revisions: newRevisionHub(), sseMaxLifetime: 3 * time.Minute}
	if taskManager != nil {
		if err := taskManager.Register(tasks.Definition{ID: removalTaskID, Name: "Removal operations", Description: "Execute durable owner and filesystem mutations.", PayloadRunner: server.runScheduledRemoval, Resources: []tasks.ResourceClaim{{Resource: "owner-filesystem-mutation", Mode: tasks.ClaimExclusive}}, Priority: tasks.PriorityMutation, Recovery: tasks.RecoveryAttention}); err != nil {
			return nil, err
		}
		if err := taskManager.Register(tasks.Definition{ID: autoRemovalTaskID, Name: "Automatic removal", Description: "Evaluate cross-domain cleanup plans and submit removals for opted-in integrations.", Interval: autoRemovalEvalInterval, Runner: server.runAutoRemovalEvaluation}); err != nil {
			return nil, err
		}
		if inventoryService != nil {
			if database := inventoryService.Store(); database != nil {
				if err := server.recoverScheduledRemovals(database); err != nil {
					return nil, err
				}
			}
		}
	}
	return server, nil
}
func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// Temporary diagnostic endpoints for tracking down a real, still-open
	// production hang (app-wide slow pages with no CPU/disk/memory signal,
	// surviving an earlier lock-contention fix that didn't resolve it).
	// /debug/pprof/goroutine?debug=2 dumps every goroutine's stack — captured
	// during an actual hang, it shows exactly what's blocked and on what,
	// instead of guessing from reading code. Remove once this is resolved.
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.Handle("/assets/", server.staticHandler)
	mux.HandleFunc("/ui/events", server.uiEvents)
	mux.HandleFunc("/ui/status", server.uiStatus)
	mux.HandleFunc("/", server.home)
	mux.HandleFunc("/storage", server.storagePage)
	mux.HandleFunc("/services", server.servicesPage)
	mux.HandleFunc("/services/add", server.addIntegrationForm)
	mux.HandleFunc("/services/integrations", server.addIntegration)
	mux.HandleFunc("/services/edit", server.editIntegrationForm)
	mux.HandleFunc("/services/remove", server.removeIntegration)
	mux.HandleFunc("/library", server.library)
	mux.HandleFunc("/library/", server.media)
	mux.HandleFunc("/media/", server.media)
	mux.HandleFunc("/torrents", server.torrents)
	mux.HandleFunc("/torrents/", server.torrentDetail)
	mux.HandleFunc("/downloads/unmanaged", server.unmanagedDownloads)
	mux.HandleFunc("/downloads/unmanaged/scan", server.scanUnmanagedNow)
	mux.HandleFunc("/history", server.history)
	mux.HandleFunc("/tasks", server.tasksPage)
	mux.HandleFunc("/tasks/run", server.runTask)
	mux.HandleFunc("/removal/media", server.removalMedia)
	mux.HandleFunc("/removal/torrent", server.removalTorrent)
	mux.HandleFunc("/removal/unmanaged", server.removalUnmanaged)
	mux.HandleFunc("/removal/execute", server.executeRemoval)
	mux.HandleFunc("/api/media", server.apiMedia)
	mux.HandleFunc("/api/dashboard", server.apiDashboard)
	mux.HandleFunc("/api/torrents", server.apiTorrents)
	mux.HandleFunc("/api/unmanaged", server.apiUnmanaged)
	mux.HandleFunc("/api/files", server.apiFiles)
	mux.HandleFunc("/api/refresh", server.refresh)
	mux.HandleFunc("/api/plan", server.plan)
	mux.HandleFunc("/healthz", func(response http.ResponseWriter, _ *http.Request) { _, _ = response.Write([]byte("ok")) })
	return sameOriginWrites(mux)
}

func (server *Server) planningReliable(r inventory.Reliability) bool {
	return r.Inventory && r.Valuation && r.FileModel == "reliable" && (server.tasks == nil || !server.tasks.ConsistencyPending())
}

// deviceViews builds one cleanup plan per known storage device, since there
// is no longer a single global storage path to build one plan against.
func (server *Server) deviceViews(items []model.Media, torrents []model.Torrent, planningReliable bool) []deviceView {
	devices := server.inv.StorageDevices()
	if len(devices) == 0 {
		return nil
	}
	mediaByDevice := server.inv.MediaByDevice(items)
	torrentsByDevice := server.inv.TorrentsByDevice(torrents)
	cfg := server.inv.Config()
	views := make([]deviceView, 0, len(devices))
	for _, device := range devices {
		p, planErr := cleanup.Build(device.RepresentativePath, cfg.Storage.TargetUsagePercent, cfg.Storage.CriticalUsagePercent, mediaByDevice[device.RepresentativePath], torrentsByDevice[device.RepresentativePath], planningReliable)
		views = append(views, deviceView{Storage: device, Plan: p, PlanErr: planErr})
	}
	return views
}

// deviceView pairs one physical storage device with its own cleanup plan:
// removing the single global storage path means there is no longer one
// answer to "is cleanup needed," only a per-device one.
type deviceView struct {
	Storage inventory.StorageDevice
	Plan    cleanup.Plan
	PlanErr error
}

func renderTemplate(w http.ResponseWriter, tpl *template.Template, data any) error {
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		http.Error(w, "template rendering failed", http.StatusInternalServerError)
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := w.Write(buf.Bytes())
	return err
}

func queryInt(r *http.Request, key string, fallback int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return fallback
	}
	return v
}
func allowedPageSize(v int) int {
	for _, n := range []int{25, 50, 100, 250} {
		if v == n {
			return v
		}
	}
	return 50
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func normalizeTorrentStatus(v string) string {
	return model.NormalizeTorrentStatus(v)
}

func detailErrString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
func errString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
func Run(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	log.Printf("[http] %s listening on %s", product.Name, addr)
	e := srv.ListenAndServe()
	if e == http.ErrServerClosed {
		return nil
	}
	return fmt.Errorf("http: %w", e)
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
func humanDuration(sec int64) string {
	if sec < 0 || sec >= 8640000 {
		return "—"
	}
	d := time.Duration(sec) * time.Second
	if d >= 24*time.Hour {
		return fmt.Sprintf("%dd %dh", int(d/(24*time.Hour)), int((d%(24*time.Hour))/time.Hour))
	}
	if d >= time.Hour {
		return fmt.Sprintf("%dh %dm", int(d/time.Hour), int((d%time.Hour)/time.Minute))
	}
	if d >= time.Minute {
		return fmt.Sprintf("%dm %ds", int(d/time.Minute), int((d%time.Minute)/time.Second))
	}
	return fmt.Sprintf("%ds", sec)
}
func shortPath(v string) string {
	const max = 88
	if len(v) <= max {
		return v
	}
	const left, right = 34, 51
	if len(v) <= left+right+3 {
		return v
	}
	return v[:left] + "..." + v[len(v)-right:]
}

func unixTime(sec int64) string {
	if sec <= 0 {
		return "—"
	}
	return time.Unix(sec, 0).Local().Format("2006-01-02 15:04:05")
}
