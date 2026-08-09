package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
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
	store := &policyCatalogStub{items: []storage.PluginPolicy{testCatalogPolicy(t, "waf-main", policyExtensionHTTP, nil)}}
	if err := validateRulePolicyReference(t.Context(), store, "edge-a", &storage.PolicyRef{ID: "waf-main"}, policyExtensionHTTP); err != nil {
		t.Fatalf("active policy reference rejected: %v", err)
	}
	if store.agentID != "edge-a" {
		t.Fatalf("catalog resolved for agent %q", store.agentID)
	}
	if err := validateRulePolicyReference(t.Context(), store, "edge-a", &storage.PolicyRef{ID: "missing"}, policyExtensionHTTP); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("missing policy reference error = %v", err)
	}
	if err := validateRulePolicyReference(t.Context(), struct{}{}, "edge-a", &storage.PolicyRef{ID: "waf-main"}, policyExtensionHTTP); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("unavailable policy catalog error = %v", err)
	}
	if err := validateRulePolicyReference(t.Context(), struct{}{}, "edge-a", nil, policyExtensionHTTP); err != nil {
		t.Fatalf("empty policy reference required a catalog: %v", err)
	}
}

func TestRulePolicyReferenceValidatesExtensionAndCompleteFrameBudget(t *testing.T) {
	overlay := []byte(`{"site":"media"}`)
	policy := testCatalogPolicy(t, "shared", policyExtensionHTTP, overlay)
	store := &policyCatalogStub{items: []storage.PluginPolicy{policy}}
	ref := &storage.PolicyRef{ID: "shared", Overlay: overlay}
	if err := validateRulePolicyReference(t.Context(), store, "edge-a", ref, policyExtensionHTTP); err != nil {
		t.Fatalf("exact complete frame budget rejected: %v", err)
	}
	store.items[0].Stages[0].ResourceBudget.InputBytes--
	if err := validateRulePolicyReference(t.Context(), store, "edge-a", ref, policyExtensionHTTP); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("undersized complete frame budget error = %v", err)
	}
	store.items[0] = testCatalogPolicy(t, "shared", policyExtensionHTTP, overlay)
	if err := validateRulePolicyReference(t.Context(), store, "edge-a", ref, policyExtensionL4); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("extension mismatch error = %v", err)
	}
}

func testCatalogPolicy(t *testing.T, id, extension string, overlay []byte) storage.PluginPolicy {
	t.Helper()
	frameBytes, err := pluginsdk.PolicyV1EvaluateRequestFrameBytes(extension, strings.Repeat("r", pluginsdk.PolicyRequestIDMaxBytes), overlay)
	if err != nil {
		t.Fatal(err)
	}
	return storage.PluginPolicy{ID: id, Stages: []storage.PolicyStage{{
		InstanceID: "instance-1", ExtensionPoints: []string{extension},
		ResourceBudget: storage.PolicyResourceBudget{InputBytes: int64(frameBytes)},
	}}}
}

func TestRulePolicyReferenceFailsClosedOnCatalogIntegrityError(t *testing.T) {
	want := errors.New("active package signer mismatch")
	store := &policyCatalogStub{err: want}
	err := validateRulePolicyReference(t.Context(), store, "edge-a", &storage.PolicyRef{ID: "waf-main"}, policyExtensionHTTP)
	if !errors.Is(err, want) {
		t.Fatalf("catalog integrity error = %v", err)
	}
}
