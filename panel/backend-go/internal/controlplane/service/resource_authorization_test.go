//go:build !integration

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type recordingResourceQuotaStore struct {
	called bool
	actor  storage.QuotaActor
}

func (s *recordingResourceQuotaStore) ConsumeQuotaForResource(ctx context.Context, _, _, _, _, metric string, _ int64) (storage.QuotaDecision, error) {
	s.called = true
	s.actor, _ = storage.QuotaActorFromContext(ctx)
	return storage.QuotaDecision{Metric: metric, Allowed: true}, nil
}

func TestGovernedMutationWithoutPrincipalFailsClosed(t *testing.T) {
	store := &recordingResourceQuotaStore{}
	ctx := context.Background()

	if err := authorizeReferencedResource(ctx, store, "agent", "edge-a"); !errors.Is(err, ErrMutationPrincipalRequired) {
		t.Fatalf("authorizeReferencedResource() error = %v, want %v", err, ErrMutationPrincipalRequired)
	}
	if err := consumeResourceQuota(ctx, store, "http_rule", "edge-a:1", "agent", "edge-a", "rule_count", 1); !errors.Is(err, ErrMutationPrincipalRequired) {
		t.Fatalf("consumeResourceQuota() error = %v, want %v", err, ErrMutationPrincipalRequired)
	}
	if store.called {
		t.Fatal("quota store was called without an authenticated or system principal")
	}
}

func TestExplicitSystemMutationPrincipalReachesGovernedQuotaStore(t *testing.T) {
	store := &recordingResourceQuotaStore{}
	ctx := WithSystemMutationPrincipal(context.Background(), "system:reconciler")

	if err := authorizeReferencedResource(ctx, store, "agent", "edge-a"); err != nil {
		t.Fatalf("authorizeReferencedResource() error = %v", err)
	}
	if err := consumeResourceQuota(ctx, store, "http_rule", "edge-a:1", "agent", "edge-a", "rule_count", 1); err != nil {
		t.Fatalf("consumeResourceQuota() error = %v", err)
	}
	if !store.called {
		t.Fatal("quota store was not called for an explicit system principal")
	}
	if store.actor.UserID != "system:reconciler" || !store.actor.Bootstrap {
		t.Fatalf("quota actor = %+v, want explicit bootstrap system principal", store.actor)
	}
}
