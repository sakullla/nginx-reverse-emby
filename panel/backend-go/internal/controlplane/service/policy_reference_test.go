package service

import (
	"context"
	"errors"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type policyCatalogStub struct {
	agentID string
	items   []storage.PluginPolicy
	err     error
}

func (s *policyCatalogStub) LoadAgentPluginPolicies(_ context.Context, agentID string) ([]storage.PluginPolicy, error) {
	s.agentID = agentID
	return append([]storage.PluginPolicy(nil), s.items...), s.err
}

func TestRulePolicyReferenceResolvesAgainstAgentActiveCatalog(t *testing.T) {
	store := &policyCatalogStub{items: []storage.PluginPolicy{{ID: "waf-main"}}}
	if err := validateRulePolicyReference(t.Context(), store, "edge-a", &storage.PolicyRef{ID: "waf-main"}); err != nil {
		t.Fatalf("active policy reference rejected: %v", err)
	}
	if store.agentID != "edge-a" {
		t.Fatalf("catalog resolved for agent %q", store.agentID)
	}
	if err := validateRulePolicyReference(t.Context(), store, "edge-a", &storage.PolicyRef{ID: "missing"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("missing policy reference error = %v", err)
	}
	if err := validateRulePolicyReference(t.Context(), struct{}{}, "edge-a", &storage.PolicyRef{ID: "waf-main"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("unavailable policy catalog error = %v", err)
	}
	if err := validateRulePolicyReference(t.Context(), struct{}{}, "edge-a", nil); err != nil {
		t.Fatalf("empty policy reference required a catalog: %v", err)
	}
}

func TestRulePolicyReferenceFailsClosedOnCatalogIntegrityError(t *testing.T) {
	want := errors.New("active package signer mismatch")
	store := &policyCatalogStub{err: want}
	err := validateRulePolicyReference(t.Context(), store, "edge-a", &storage.PolicyRef{ID: "waf-main"})
	if !errors.Is(err, want) {
		t.Fatalf("catalog integrity error = %v", err)
	}
}
