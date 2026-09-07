package httpui

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
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

// elementID turns an arbitrary stable key (a device's representative path)
// into a short, HTML-id-safe token — templates ranging over storage devices
// need one DOM id per device, and a raw filesystem path isn't safe to embed
// directly into an id/selector.
func elementID(key string) string {
	digest := fnv.New64a()
	_, _ = digest.Write([]byte(key))
	return fmt.Sprintf("%x", digest.Sum64())
}

type Server struct {
	inv                *inventory.Service
	tasks              *tasks.Manager
	homeTpl            *template.Template
	storageTpl         *template.Template
	servicesTpl        *template.Template
	addServiceTpl      *template.Template
	editServiceTpl     *template.Template
	serviceProgressTpl *template.Template
	libraryTpl         *template.Template
	historyTpl         *template.Template
	profileTpl         *template.Template
	torrentTpl         *template.Template
	torrentDetailTpl   *template.Template
	unmanagedTpl       *template.Template
	tasksTpl           *template.Template
	removalTpl         *template.Template
	operationTpl       *template.Template
	setupTpl           *template.Template
	loginTpl           *template.Template
	settingsTpl        *template.Template
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
	loginLimiter   *loginLimiter
	// authBypassWarned ensures the "no durable store, authentication
	// disabled" warning logs once per server instance rather than once per
	// request.
	authBypassWarned sync.Once
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
	}, "join": strings.Join, "add": func(first, second int) int { return first + second }, "managedKey": managedFileKey, "elementID": elementID, "shortPath": shortPath, "fileOwner": fileOwnerLabel, "peerOwner": filePeerOwnerLabel, "unmanagedRemovalURL": unmanagedRemovalURL, "torrentCleanupActions": torrentCleanupActions, "mediaCleanupActions": mediaCleanupActions, "torrentActionContext": torrentActionContext, "relatedTorrentMeta": relatedTorrentMeta, "widthPct": func(part, total uint64) string {
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
	addServiceTemplate, err := parseUITemplate("add_service.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	editServiceTemplate, err := parseUITemplate("edit_service.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	serviceProgressTemplate, err := parseUITemplate("service_progress.html", templateFunctions)
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
	setupTemplate, err := parseUITemplate("setup.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	loginTemplate, err := parseUITemplate("login.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	settingsTemplate, err := parseUITemplate("settings.html", templateFunctions)
	if err != nil {
		return nil, err
	}
	staticHandler, err := uiStaticHandler()
	if err != nil {
		return nil, err
	}
	server := &Server{inv: inventoryService, tasks: taskManager, homeTpl: homeTemplate, storageTpl: storageTemplate, servicesTpl: servicesTemplate, addServiceTpl: addServiceTemplate, editServiceTpl: editServiceTemplate, serviceProgressTpl: serviceProgressTemplate, libraryTpl: libraryTemplate, historyTpl: historyTemplate, profileTpl: profileTemplate, torrentTpl: torrentTemplate, torrentDetailTpl: torrentDetailTemplate, unmanagedTpl: unmanagedTemplate, tasksTpl: tasksTemplate, removalTpl: removalTemplate, operationTpl: operationTemplate, setupTpl: setupTemplate, loginTpl: loginTemplate, settingsTpl: settingsTemplate, staticHandler: staticHandler, revisions: newRevisionHub(), sseMaxLifetime: 3 * time.Minute, loginLimiter: newLoginLimiter()}
	if taskManager != nil {
		if err := taskManager.Register(tasks.Definition{ID: removalTaskID, Name: "Removal operations", Description: "Execute durable owner and filesystem mutations.", PayloadRunner: server.runScheduledRemoval, Resources: []tasks.ResourceClaim{{Resource: "owner-filesystem-mutation", Mode: tasks.ClaimExclusive}}, Priority: tasks.PriorityMutation, Recovery: tasks.RecoveryAttention}); err != nil {
			return nil, err
		}
		if err := taskManager.Register(tasks.Definition{ID: autoRemovalTaskID, Name: "Automatic removal", Description: "Evaluate cross-domain cleanup plans and submit removals for opted-in services.", Interval: autoRemovalEvalInterval, Runner: server.runAutoRemovalEvaluation}); err != nil {
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
	mux.HandleFunc("/storage/stats", server.storageStats)
	mux.HandleFunc("/storage/device-threshold", server.setDeviceThreshold)
	mux.HandleFunc("/services", server.servicesPage)
	mux.HandleFunc("/services/add", server.addServiceForm)
	mux.HandleFunc("/services/test", server.testServiceConnection)
	mux.HandleFunc("/services/create", server.addService)
	mux.HandleFunc("/services/edit", server.editServiceForm)
	mux.HandleFunc("/services/remove", server.removeService)
	mux.HandleFunc("/services/consistency-progress", server.serviceConsistencyProgressForm)
	mux.HandleFunc("/services/consistency-status", server.serviceConsistencyStatus)
	mux.HandleFunc("/library", server.library)
	mux.HandleFunc("/library/", server.media)
	mux.HandleFunc("/media/", server.media)
	mux.HandleFunc("/torrents", server.torrents)
	mux.HandleFunc("/torrents/", server.torrentDetail)
	mux.HandleFunc("/unmanaged", server.unmanagedDownloads)
	mux.HandleFunc("/unmanaged/scan", server.scanUnmanagedNow)
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
	mux.HandleFunc("/setup", server.setupPage)
	mux.HandleFunc("/login", server.loginPage)
	mux.HandleFunc("/logout", server.logout)
	mux.HandleFunc("/settings", server.settingsPage)
	mux.HandleFunc("/settings/password", server.changePassword)
	mux.HandleFunc("/settings/tmdb", server.setTMDBAPIKey)
	mux.HandleFunc("/settings/tmdb/test", server.testTMDBAPIKey)
	return server.authGate(sameOriginWrites(mux))
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
		target, critical := cfg.ThresholdsFor(device.RepresentativePath)
		p, planErr := cleanup.Build(device.RepresentativePath, target, critical, mediaByDevice[device.RepresentativePath], torrentsByDevice[device.RepresentativePath], planningReliable)
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

// fileOwnerLabel describes which service's root a file path was discovered
// under (its StorageContexts), for display next to a file row so "who owns
// this path" doesn't require cross-referencing the Services page.
func fileOwnerLabel(contexts []model.StorageContext) string {
	names := make([]string, 0, len(contexts))
	seen := map[string]bool{}
	for _, context := range contexts {
		name := context.ServiceName
		if name == "" {
			name = context.RootLabel
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// filePeerOwnerLabel describes which media item or torrent a hardlinked
// sibling path belongs to, so a "Files" section's other known paths read as
// more than an unexplained list of strings.
func filePeerOwnerLabel(peer inventory.FilePeer) string {
	owners := make([]string, 0, len(peer.Media)+len(peer.Torrents))
	for _, ref := range peer.Media {
		if ref.Title == "" {
			continue
		}
		if ref.Year > 0 {
			owners = append(owners, fmt.Sprintf("%s (%d)", ref.Title, ref.Year))
		} else {
			owners = append(owners, ref.Title)
		}
	}
	for _, torrent := range peer.Torrents {
		if torrent.Name == "" {
			continue
		}
		owners = append(owners, "torrent: "+torrent.Name)
	}
	return strings.Join(owners, ", ")
}

// torrentCleanupActions and mediaCleanupActions split a device's cleanup
// candidates into the same two tiers cleanup.rank() already computes
// internally (every torrent-only candidate before any media/season one),
// so the Storage page can present them as two grouped, labeled lists
// instead of one flat one where the grouping is invisible.
func torrentCleanupActions(actions []cleanup.Action) []cleanup.Action {
	filtered := make([]cleanup.Action, 0, len(actions))
	for _, action := range actions {
		if action.Kind == cleanup.StandaloneTorrent {
			filtered = append(filtered, action)
		}
	}
	return filtered
}

func mediaCleanupActions(actions []cleanup.Action) []cleanup.Action {
	filtered := make([]cleanup.Action, 0, len(actions))
	for _, action := range actions {
		if action.Kind != cleanup.StandaloneTorrent {
			filtered = append(filtered, action)
		}
	}
	return filtered
}

// torrentActionContext describes why a standalone-torrent cleanup candidate
// is only a torrent action rather than part of a media/season action: its
// association state, and — the fact that most directly answers "would this
// touch my library copy" — whether it's a Current torrent proven to be an
// independent, non-hardlinked copy rather than the same physical file as
// its media. Includes the related media title when one is known, so a
// bare release name isn't the only clue to what this actually is.
func torrentActionContext(t model.Torrent) string {
	var parts []string
	switch model.NormalizeTorrentStatus(t.AssociationStatus) {
	case model.TorrentCurrent:
		if t.MediaHardlinkKnown && !t.MediaHardlinked {
			parts = append(parts, "independent copy, not hardlinked to its media")
		} else {
			parts = append(parts, "current")
		}
	case model.TorrentSuperseded:
		parts = append(parts, "superseded")
	case model.TorrentOrphaned:
		parts = append(parts, "orphaned")
	default:
		parts = append(parts, "unassociated")
	}
	mediaRefs := t.MediaItems
	if len(mediaRefs) == 0 {
		mediaRefs = t.FormerMediaItems
	}
	for _, ref := range mediaRefs {
		if ref.Title == "" {
			continue
		}
		if ref.Year > 0 {
			parts = append(parts, fmt.Sprintf("%s (%d)", ref.Title, ref.Year))
		} else {
			parts = append(parts, ref.Title)
		}
	}
	return strings.Join(parts, " · ")
}

func unixTime(sec int64) string {
	if sec <= 0 {
		return "—"
	}
	return time.Unix(sec, 0).Local().Format("2006-01-02 15:04:05")
}
