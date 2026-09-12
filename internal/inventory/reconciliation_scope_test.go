package inventory

import (
	"path/filepath"
	"testing"

	"stewarr/internal/config"
	"stewarr/internal/model"
	"stewarr/internal/store"
)

func TestQueueReconciliationDurablyMergesScopes(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := New(config.Config{}, database)
	if err := service.QueueReconciliation(ReconciliationScope{Paths: []string{"/data/a", "/data/a"}, Owners: []ReconciliationOwner{{Type: model.Movie, ID: 7}}, Torrents: []string{"ABC"}}); err != nil {
		t.Fatal(err)
	}
	if err := service.QueueReconciliation(ReconciliationScope{Full: true, Reasons: []string{"uncertain"}, Paths: []string{"/data/b"}, Owners: []ReconciliationOwner{{Type: model.Series, ID: 8}}}); err != nil {
		t.Fatal(err)
	}
	restarted := New(config.Config{}, database)
	scope, err := restarted.reconciliationScope()
	if err != nil {
		t.Fatal(err)
	}
	if !scope.Full || len(scope.Paths) != 2 || len(scope.Owners) != 2 || len(scope.Reasons) != 1 || len(scope.Torrents) != 1 || scope.Torrents[0] != "abc" {
		t.Fatalf("scope=%#v", scope)
	}
}
