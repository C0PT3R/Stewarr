package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"togetharr/internal/config"
	"togetharr/internal/httpui"
	"togetharr/internal/inventory"
	"togetharr/internal/store"
	"togetharr/internal/tasks"
)

const (
	databasePath          = "/config/state/togetharr.db"
	legacyDatabasePath    = "/config/state/spartarr.db"
	unclaimedInterval     = 12 * time.Hour
	fileReconcileInterval = 12 * time.Hour
)

func migrateLegacyDatabase() error {
	if _, err := os.Stat(databasePath); err == nil {
		return nil
	}
	if _, err := os.Stat(legacyDatabasePath); os.IsNotExist(err) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o775); err != nil {
		return err
	}
	if err := os.Rename(legacyDatabasePath, databasePath); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		oldp := legacyDatabasePath + suffix
		newp := databasePath + suffix
		if _, err := os.Stat(oldp); err == nil {
			_ = os.Rename(oldp, newp)
		}
	}
	log.Printf("migrated legacy database %s to %s", legacyDatabasePath, databasePath)
	return nil
}

func main() {
	configPath := flag.String("config", "/config/config.json", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if err := migrateLegacyDatabase(); err != nil {
		log.Fatalf("database migration: %v", err)
	}
	db, err := store.Open(databasePath)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer db.Close()

	inv := inventory.New(cfg, db)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	taskManager := tasks.New(
		tasks.Definition{ID: "inventory", Name: "Inventory refresh", Description: "Refresh media and torrents.", Interval: cfg.RefreshInterval, Preflight: inv.ValidateIntegrations, Runner: inv.Refresh},
		tasks.Definition{ID: "files", Name: "File reconciliation", Description: "Scan integration storage and reconcile file ownership.", Interval: fileReconcileInterval, Preflight: inv.ValidateIntegrations, Runner: inv.ReconcileFiles},
	)
	go taskManager.Start(ctx)
	go func() {
		if err := taskManager.Run(ctx, "inventory"); err != nil {
			log.Printf("initial inventory refresh failed: %v", err)
		}
		if ctx.Err() == nil {
			if err := taskManager.Run(ctx, "files"); err != nil {
				log.Printf("initial file reconciliation failed: %v", err)
			}
		}
	}()

	ui, err := httpui.New(inv, taskManager)
	if err != nil {
		log.Fatal(err)
	}
	if err := httpui.Run(ctx, cfg.Server.Listen, ui.Handler()); err != nil {
		log.Fatal(err)
	}
}
