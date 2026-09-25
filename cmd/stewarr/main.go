package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"stewarr/internal/applog"
	"stewarr/internal/config"
	"stewarr/internal/httpui"
	"stewarr/internal/inventory"
	"stewarr/internal/product"
	"stewarr/internal/store"
	"stewarr/internal/tasks"
)

const (
	databasePath               = "/config/state/inventory.db"
	logDirectory               = "/config/log"
	logRetentionDays           = 10
	fileReconcileInterval      = 12 * time.Hour
	jellyfinEnrichmentInterval = time.Hour
	seerrEnrichmentInterval    = time.Hour
	// tmdbEnrichmentInterval is much longer than Jellyfin/Seerr's: rating,
	// vote, and popularity data don't need to track library changes in
	// real time the way playback/request facts do, and refreshing less
	// often keeps steady-state API usage low regardless of library size.
	tmdbEnrichmentInterval    = 24 * time.Hour
	enrichmentRemovalCooldown = 30 * time.Minute
	torrentHistoryInterval    = 30 * time.Minute
	// imdbRatingsCheckInterval is short and cheap on purpose: the task
	// itself self-gates on the configured wall-clock hour (see
	// imdbRatingsDue in internal/inventory/imdb_ratings.go) rather than
	// this interval actually pacing the real, once-a-day download.
	imdbRatingsCheckInterval = 30 * time.Minute
)

func main() {
	configPath := flag.String("config", "/config/config.json", "path to config file")
	flag.Parse()
	applicationLog, err := applog.OpenDaily(logDirectory, "stewarr", logRetentionDays, time.Now)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Stewarr cannot start without its persistent application log: %v\n", err)
		os.Exit(1)
	}
	defer applicationLog.Close()
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.SetOutput(io.MultiWriter(os.Stdout, applicationLog))
	log.Printf("[app] starting %s %s", product.Name, product.Version)
	// Temporary, for tracking down a real production hang: makes
	// /debug/pprof/mutex and /debug/pprof/block meaningful instead of empty.
	// Remove alongside the /debug/pprof/ routes once this is resolved.
	runtime.SetMutexProfileFraction(5)
	runtime.SetBlockProfileRate(1_000_000)

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("[app] config: %v", err)
	}

	db, err := store.Open(databasePath)
	if err != nil {
		log.Fatalf("[app] database: %v", err)
	}
	defer db.Close()
	inv := inventory.New(cfg, db)
	inv.SetConfigPath(*configPath)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		select {
		case loggingError := <-applicationLog.Errors():
			fmt.Fprintf(os.Stderr, "Stewarr is stopping because its mandatory application log failed: %v\n", loggingError)
			cancel()
		case <-ctx.Done():
		}
	}()

	maintenanceClaim := tasks.ResourceClaim{Resource: "owner-filesystem-mutation", Mode: tasks.ClaimShared}
	publicationClaim := tasks.ResourceClaim{Resource: "inventory-publication", Mode: tasks.ClaimExclusive}
	retryPolicy := tasks.RetryPolicy{MaxAttempts: 3, InitialDelay: time.Minute, MaxDelay: 15 * time.Minute}
	retryable := func(runner tasks.Runner) tasks.Runner {
		return func(ctx context.Context) error { return tasks.Retryable(runner(ctx)) }
	}
	fullScanAttachCompatible := func(active []tasks.TriggerKind, requested tasks.TriggerKind) bool {
		if requested == tasks.TriggerWorkflow {
			return true
		}
		for _, kind := range active {
			if kind != tasks.TriggerWorkflow {
				return true
			}
		}
		return false
	}
	taskManager, err := tasks.NewPersistent(db,
		tasks.Definition{ID: "inventory", Name: "Base inventory", Description: "Refresh Radarr, Sonarr, qBittorrent and import provenance.", Interval: cfg.RefreshInterval, Preflight: retryable(inv.ValidateReconciliation), AttachCompatible: fullScanAttachCompatible, Runner: retryable(func(ctx context.Context) error {
			var err error
			if tasks.TriggeredOnlyBy(ctx, tasks.TriggerWorkflow) {
				err = inv.RefreshAfterMutation(ctx)
			} else {
				err = inv.Refresh(ctx)
			}
			if err != nil {
				return err
			}
			// Fire-and-forget: a newly imported (or re-identified) movie or
			// show gets every applicable source's enrichment right away
			// instead of waiting for that source's next scheduled pass —
			// see EnrichNewMedia's doc comment for why this is the only
			// place a new item's enrichment is ever fetched. Failure here
			// isn't fatal to the base refresh itself — the next base
			// refresh's own trigger, or that source's next scheduled run,
			// will retry.
			go func() {
				if enrichErr := inv.EnrichNewMedia(context.Background()); enrichErr != nil {
					log.Printf("[inventory] immediate enrichment of newly discovered media failed: %v", enrichErr)
				}
			}()
			return nil
		}), Advisory: inv.RefreshAdvisory, Resources: []tasks.ResourceClaim{maintenanceClaim, publicationClaim}, Priority: tasks.PriorityPeriodic, Retry: retryPolicy, Recovery: tasks.RecoveryRetry},
		tasks.Definition{ID: "jellyfin", Name: "Jellyfin enrichment", Description: "Refresh playback and favorite facts used by automatic planning.", Interval: jellyfinEnrichmentInterval, Preflight: retryable(inv.ValidateJellyfin), Runner: retryable(inv.RefreshJellyfin), Resources: []tasks.ResourceClaim{maintenanceClaim, publicationClaim}, Interruptible: true, InterruptionDelay: enrichmentRemovalCooldown, Priority: tasks.PriorityPeriodic, Retry: retryPolicy, Recovery: tasks.RecoveryRetry},
		tasks.Definition{ID: "seerr", Name: "Seerr enrichment", Description: "Refresh request facts used by automatic planning.", Interval: seerrEnrichmentInterval, Preflight: retryable(inv.ValidateSeerr), Runner: retryable(inv.RefreshSeerr), Resources: []tasks.ResourceClaim{maintenanceClaim, publicationClaim}, Priority: tasks.PriorityPeriodic, Retry: retryPolicy, Recovery: tasks.RecoveryRetry},
		tasks.Definition{ID: "tmdb", Name: "TMDB enrichment", Description: "Refresh rating, vote, and popularity facts used by automatic planning.", Interval: tmdbEnrichmentInterval, Preflight: retryable(inv.ValidateTMDB), Runner: retryable(inv.RefreshTMDB), Resources: []tasks.ResourceClaim{maintenanceClaim, publicationClaim}, Interruptible: true, InterruptionDelay: enrichmentRemovalCooldown, Priority: tasks.PriorityPeriodic, Retry: retryPolicy, Recovery: tasks.RecoveryRetry},
		tasks.Definition{ID: "imdb", Name: "IMDb ratings refresh", Description: "Once daily, replace IMDb's own rating/vote-count dataset used to prefer its numbers over TMDB's.", Interval: imdbRatingsCheckInterval, Runner: retryable(inv.RefreshIMDbRatings), Resources: []tasks.ResourceClaim{maintenanceClaim, publicationClaim}, Interruptible: true, InterruptionDelay: enrichmentRemovalCooldown, Priority: tasks.PriorityPeriodic, Retry: retryPolicy, Recovery: tasks.RecoveryRetry},
		tasks.Definition{ID: "files", Name: "File reconciliation", Description: "Scan service storage and reconcile file ownership.", Interval: fileReconcileInterval, Preflight: retryable(inv.ValidateReconciliation), AttachCompatible: fullScanAttachCompatible, Runner: retryable(func(ctx context.Context) error {
			if tasks.TriggeredOnlyBy(ctx, tasks.TriggerWorkflow) {
				return inv.ReconcileFilesAfterMutation(ctx)
			}
			return inv.ReconcileFiles(ctx)
		}), Resources: []tasks.ResourceClaim{maintenanceClaim}, Priority: tasks.PriorityPeriodic, Retry: retryPolicy, Recovery: tasks.RecoveryRetry},
		tasks.Definition{ID: "torrent-history", Name: "Torrent history sampling", Description: "Record torrent health signals over time for sustained-history-based torrent valuation.", Interval: torrentHistoryInterval, Runner: retryable(inv.TorrentHistorySampling), Priority: tasks.PriorityPeriodic, Retry: retryPolicy, Recovery: tasks.RecoveryRetry},
	)
	if err != nil {
		log.Fatalf("[scheduler] initialization: %v", err)
	}
	if err := taskManager.RegisterWorkflow(tasks.WorkflowDefinition{ID: "inventory-and-files-consistency", Name: "Inventory and files consistency", Description: "Restore authoritative inventory and file topology after a mutation (a removal, or a service being added/edited/removed).", Steps: []string{"inventory", "files"}}); err != nil {
		log.Fatalf("[scheduler] workflow registration: %v", err)
	}

	ui, err := httpui.New(inv, taskManager)
	if err != nil {
		log.Fatalf("[http] initialize UI: %v", err)
	}
	ui.Start(ctx)
	go taskManager.Start(ctx)
	go func() {
		inventoryTrigger, triggerErr := taskManager.Submit(tasks.Request{TaskID: "inventory", Kind: tasks.TriggerStartup, Priority: tasks.PriorityManual, Durable: false, Cause: "Application startup"})
		if triggerErr != nil {
			log.Printf("[scheduler] initial base inventory could not be submitted: %v", triggerErr)
			return
		}
		if _, err := taskManager.Await(ctx, inventoryTrigger.TriggerID); err != nil {
			log.Printf("[inventory] initial base inventory failed: %v", err)
		}
		if !inv.WaitForBase(ctx) {
			return
		}
		// File topology is independent of value enrichment and runs alongside it.
		_, _ = taskManager.Submit(tasks.Request{TaskID: "files", Kind: tasks.TriggerStartup, Priority: tasks.PriorityManual, Durable: false, Cause: "Application startup"})
		// IMDb's own due-check inside RefreshIMDbRatings decides whether a
		// fetch is actually needed (past the fetch hour, not already done
		// today) — submitting it unconditionally at every startup, rather
		// than waiting for its next periodic tick, is what catches the
		// very first run (no data yet) and a restart after a day or more
		// of downtime (stale data) immediately instead of up to
		// imdbRatingsCheckInterval later.
		_, _ = taskManager.Submit(tasks.Request{TaskID: "imdb", Kind: tasks.TriggerStartup, Priority: tasks.PriorityManual, Durable: false, Cause: "Application startup"})
		// Enrichments share a scheduler group, so they publish one at a time.
		seerrTrigger, err := taskManager.Submit(tasks.Request{TaskID: "seerr", Kind: tasks.TriggerStartup, Priority: tasks.PriorityManual, Durable: false, Cause: "Application startup"})
		if err == nil {
			_, err = taskManager.Await(ctx, seerrTrigger.TriggerID)
		}
		if err != nil {
			log.Printf("[inventory] initial Seerr enrichment failed: %v", err)
		}
		jellyfinTrigger, err := taskManager.Submit(tasks.Request{TaskID: "jellyfin", Kind: tasks.TriggerStartup, Priority: tasks.PriorityManual, Durable: false, Cause: "Application startup"})
		if err == nil {
			_, err = taskManager.Await(ctx, jellyfinTrigger.TriggerID)
		}
		if err != nil {
			log.Printf("[inventory] initial Jellyfin enrichment failed: %v", err)
		}
	}()

	runError := httpui.Run(ctx, cfg.Server.Listen, ui.Handler())
	cancel()
	<-taskManager.Done()
	if loggingError := applicationLog.Err(); loggingError != nil {
		fmt.Fprintf(os.Stderr, "Stewarr terminated after mandatory application log failure: %v\n", loggingError)
		os.Exit(1)
	}
	if runError != nil {
		log.Fatalf("[http] server: %v", runError)
	}
}
