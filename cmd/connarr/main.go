package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"connarr/internal/applog"
	"connarr/internal/config"
	"connarr/internal/httpui"
	"connarr/internal/inventory"
	"connarr/internal/product"
	"connarr/internal/store"
	"connarr/internal/tasks"
)

const (
	databasePath               = "/config/state/connarr.db"
	logDirectory               = "/config/log"
	logRetentionDays           = 10
	fileReconcileInterval      = 12 * time.Hour
	jellyfinEnrichmentInterval = time.Hour
	seerrEnrichmentInterval    = time.Hour
	enrichmentRemovalCooldown  = 30 * time.Minute
)

var legacyDatabasePaths = []string{
	"/config/state/togetharr.db",
	"/config/state/spartarr.db",
}

func migrateLegacyDatabase() error {
	return migrateLegacyDatabaseAt(databasePath, legacyDatabasePaths...)
}

func migrateLegacyDatabaseAt(destination string, candidates ...string) error {
	if _, err := os.Stat(destination); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	var source string
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			source = candidate
			break
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if source == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o775); err != nil {
		return err
	}
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		oldp := source + suffix
		newp := destination + suffix
		if _, err := os.Stat(oldp); err == nil {
			_ = os.Rename(oldp, newp)
		}
	}
	log.Printf("[app] migrated legacy database %s to %s", source, destination)
	return nil
}

func main() {
	configPath := flag.String("config", "/config/config.json", "path to config file")
	flag.Parse()
	applicationLog, err := applog.OpenDaily(logDirectory, "connarr", logRetentionDays, time.Now)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Connarr cannot start without its persistent application log: %v\n", err)
		os.Exit(1)
	}
	defer applicationLog.Close()
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.SetOutput(io.MultiWriter(os.Stdout, applicationLog))
	log.Printf("[app] starting %s %s", product.Name, product.Version)

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("[app] config: %v", err)
	}

	if err := migrateLegacyDatabase(); err != nil {
		log.Fatalf("[app] database migration: %v", err)
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
			fmt.Fprintf(os.Stderr, "Connarr is stopping because its mandatory application log failed: %v\n", loggingError)
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
			if tasks.TriggeredOnlyBy(ctx, tasks.TriggerWorkflow) {
				return inv.RefreshAfterMutation(ctx)
			}
			return inv.Refresh(ctx)
		}), Advisory: inv.RefreshAdvisory, Resources: []tasks.ResourceClaim{maintenanceClaim, publicationClaim}, Priority: tasks.PriorityPeriodic, Retry: retryPolicy, Recovery: tasks.RecoveryRetry},
		tasks.Definition{ID: "jellyfin", Name: "Jellyfin enrichment", Description: "Refresh playback and favorite facts used by automatic planning.", Interval: jellyfinEnrichmentInterval, Preflight: retryable(inv.ValidateJellyfin), Runner: retryable(inv.RefreshJellyfin), Resources: []tasks.ResourceClaim{maintenanceClaim, publicationClaim}, Interruptible: true, InterruptionDelay: enrichmentRemovalCooldown, Priority: tasks.PriorityPeriodic, Retry: retryPolicy, Recovery: tasks.RecoveryRetry},
		tasks.Definition{ID: "seerr", Name: "Seerr enrichment", Description: "Refresh request facts used by automatic planning.", Interval: seerrEnrichmentInterval, Preflight: retryable(inv.ValidateSeerr), Runner: retryable(inv.RefreshSeerr), Resources: []tasks.ResourceClaim{maintenanceClaim, publicationClaim}, Priority: tasks.PriorityPeriodic, Retry: retryPolicy, Recovery: tasks.RecoveryRetry},
		tasks.Definition{ID: "files", Name: "File reconciliation", Description: "Scan integration storage and reconcile file ownership.", Interval: fileReconcileInterval, Preflight: retryable(inv.ValidateReconciliation), AttachCompatible: fullScanAttachCompatible, Runner: retryable(func(ctx context.Context) error {
			if tasks.TriggeredOnlyBy(ctx, tasks.TriggerWorkflow) {
				return inv.ReconcileFilesAfterMutation(ctx)
			}
			return inv.ReconcileFiles(ctx)
		}), Resources: []tasks.ResourceClaim{maintenanceClaim}, Priority: tasks.PriorityPeriodic, Retry: retryPolicy, Recovery: tasks.RecoveryRetry},
	)
	if err != nil {
		log.Fatalf("[scheduler] initialization: %v", err)
	}
	if err := taskManager.RegisterWorkflow(tasks.WorkflowDefinition{ID: "inventory-and-files-consistency", Name: "Inventory and files consistency", Description: "Restore authoritative inventory and file topology after a mutation (a removal, or an integration being added/edited/removed).", Steps: []string{"inventory", "files"}}); err != nil {
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
		fmt.Fprintf(os.Stderr, "Connarr terminated after mandatory application log failure: %v\n", loggingError)
		os.Exit(1)
	}
	if runError != nil {
		log.Fatalf("[http] server: %v", runError)
	}
}
