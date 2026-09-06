package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"connarr/internal/model"
	"connarr/internal/tasks"
)

const reconciliationScopeKey = "inventory.reconciliation.scope.v1"

type ReconciliationOwner struct {
	Type model.MediaType `json:"type"`
	ID   int             `json:"id"`
	// ServiceID disambiguates ID across multiple configured instances of
	// the same service type. Empty on scopes persisted before
	// multi-instance support existed.
	ServiceID string `json:"serviceId,omitempty"`
}

type ReconciliationScope struct {
	Full     bool                  `json:"full,omitempty"`
	Reasons  []string              `json:"reasons,omitempty"`
	Paths    []string              `json:"paths,omitempty"`
	Owners   []ReconciliationOwner `json:"owners,omitempty"`
	Torrents []string              `json:"torrents,omitempty"`
}

func (scope ReconciliationScope) normalized() ReconciliationScope {
	paths := map[string]bool{}
	for _, path := range scope.Paths {
		path = filepath.Clean(strings.TrimSpace(path))
		if path != "" && path != "." {
			paths[path] = true
		}
	}
	scope.Paths = scope.Paths[:0]
	for path := range paths {
		scope.Paths = append(scope.Paths, path)
	}
	sort.Strings(scope.Paths)
	torrents := map[string]bool{}
	for _, hash := range scope.Torrents {
		if hash = strings.ToLower(strings.TrimSpace(hash)); hash != "" {
			torrents[hash] = true
		}
	}
	scope.Torrents = scope.Torrents[:0]
	for hash := range torrents {
		scope.Torrents = append(scope.Torrents, hash)
	}
	sort.Strings(scope.Torrents)
	owners := map[string]ReconciliationOwner{}
	for _, owner := range scope.Owners {
		if owner.ID > 0 && (owner.Type == model.Movie || owner.Type == model.Series) {
			owners[fmt.Sprintf("%s:%s:%d", owner.Type, owner.ServiceID, owner.ID)] = owner
		}
	}
	scope.Owners = scope.Owners[:0]
	for _, owner := range owners {
		scope.Owners = append(scope.Owners, owner)
	}
	sort.Slice(scope.Owners, func(i, j int) bool {
		if scope.Owners[i].Type != scope.Owners[j].Type {
			return scope.Owners[i].Type < scope.Owners[j].Type
		}
		return scope.Owners[i].ID < scope.Owners[j].ID
	})
	reasons := map[string]bool{}
	for _, reason := range scope.Reasons {
		if reason = strings.TrimSpace(reason); reason != "" {
			reasons[reason] = true
		}
	}
	scope.Reasons = scope.Reasons[:0]
	for reason := range reasons {
		scope.Reasons = append(scope.Reasons, reason)
	}
	sort.Strings(scope.Reasons)
	return scope
}

func mergeReconciliationScopes(first, second ReconciliationScope) ReconciliationScope {
	return (ReconciliationScope{
		Full: first.Full || second.Full, Reasons: append(first.Reasons, second.Reasons...),
		Paths: append(first.Paths, second.Paths...), Owners: append(first.Owners, second.Owners...),
		Torrents: append(first.Torrents, second.Torrents...),
	}).normalized()
}

// RefreshAfterMutation avoids a service-wide refresh only when the
// durable mutation scope is exact. Confirmed owner results are consumed by the
// following targeted File reconciliation. Uncertain work retains a full run.
func (service *Service) RefreshAfterMutation(ctx context.Context) error {
	scope, err := service.reconciliationScope()
	if err != nil || scope.Full || len(scope.Paths) == 0 {
		tasks.AddMetric(ctx, "mode", "full")
		return service.Refresh(ctx)
	}
	tasks.AddMetric(ctx, "mode", "targeted")
	tasks.AddMetric(ctx, "scope_paths", len(scope.Paths))
	tasks.AddMetric(ctx, "scope_owners", len(scope.Owners))
	tasks.AddMetric(ctx, "scope_torrents", len(scope.Torrents))
	return nil
}

func (service *Service) ValidateReconciliation(ctx context.Context) error {
	if tasks.TriggeredOnlyBy(ctx, tasks.TriggerWorkflow) {
		scope, err := service.reconciliationScope()
		if err == nil && !scope.Full && len(scope.Paths) > 0 {
			return nil
		}
	}
	return service.ValidateBaseServices(ctx)
}

func (service *Service) QueueReconciliation(scope ReconciliationScope) error {
	if service.db == nil {
		return fmt.Errorf("reconciliation scope requires durable state")
	}
	service.reconciliationMu.Lock()
	defer service.reconciliationMu.Unlock()
	current, err := service.reconciliationScope()
	if err != nil {
		return err
	}
	merged := mergeReconciliationScopes(current, scope)
	encoded, err := json.Marshal(merged)
	if err != nil {
		return fmt.Errorf("encode reconciliation scope: %w", err)
	}
	if err := service.db.SetMeta(reconciliationScopeKey, string(encoded)); err != nil {
		return fmt.Errorf("persist reconciliation scope: %w", err)
	}
	return nil
}

func (service *Service) reconciliationScope() (ReconciliationScope, error) {
	if service.db == nil {
		return ReconciliationScope{}, nil
	}
	encoded, err := service.db.Meta(reconciliationScopeKey)
	if err != nil {
		return ReconciliationScope{}, fmt.Errorf("load reconciliation scope: %w", err)
	}
	if strings.TrimSpace(encoded) == "" {
		return ReconciliationScope{}, nil
	}
	var scope ReconciliationScope
	if err := json.Unmarshal([]byte(encoded), &scope); err != nil {
		return ReconciliationScope{}, fmt.Errorf("decode reconciliation scope: %w", err)
	}
	return scope.normalized(), nil
}
