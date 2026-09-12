package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLegacyDatabaseUsesNewestKnownName(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "connarr.db")
	togetharr := filepath.Join(dir, "togetharr.db")
	spartarr := filepath.Join(dir, "spartarr.db")
	if err := os.WriteFile(togetharr, []byte("newer"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spartarr, []byte("older"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacyDatabaseAt(destination, togetharr, spartarr); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "newer" {
		t.Fatalf("destination = %q, want newer legacy database", got)
	}
	if _, err := os.Stat(spartarr); err != nil {
		t.Fatalf("older legacy database should remain untouched: %v", err)
	}
}

func TestMigrateLegacyDatabaseDoesNotReplaceDestination(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "connarr.db")
	legacy := filepath.Join(dir, "togetharr.db")
	if err := os.WriteFile(destination, []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacyDatabaseAt(destination, legacy); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "current" {
		t.Fatalf("destination = %q, want current database preserved", got)
	}
}
