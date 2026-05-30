package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestConfigIdentityAllocatorRuleNamespaceSharedAcrossHTTPAndL4(t *testing.T) {
	allocator := newConfigIdentityAllocator(configIdentityAllocatorState{
		HTTPRules: []storage.HTTPRuleRow{{ID: 7, AgentID: "edge-a", Revision: 3}},
		L4Rules:   []storage.L4RuleRow{{ID: 8, AgentID: "edge-a", Revision: 4}},
	})

	if got := allocator.AllocateRuleID(0); got != 9 {
		t.Fatalf("AllocateRuleID() = %d, want 9", got)
	}
}

func TestConfigIdentityAllocatorNamespacesStayIndependent(t *testing.T) {
	allocator := newConfigIdentityAllocator(configIdentityAllocatorState{
		RelayListeners: []storage.RelayListenerRow{{ID: 5, AgentID: "edge-a", Revision: 2}},
		EgressProfiles: []storage.EgressProfileRow{{ID: 5, Name: "office socks", Type: "socks", Revision: 2}},
		Certificates:   []storage.ManagedCertificateRow{{ID: 5, Domain: "media.example.com", Revision: 2}},
	})

	if got := allocator.AllocateListenerID(0); got != 6 {
		t.Fatalf("AllocateListenerID() = %d, want 6", got)
	}
	if got := allocator.AllocateEgressProfileID(0); got != 6 {
		t.Fatalf("AllocateEgressProfileID() = %d, want 6", got)
	}
	if got := allocator.AllocateCertificateID(0); got != 6 {
		t.Fatalf("AllocateCertificateID() = %d, want 6", got)
	}
}

func TestConfigIdentityAllocatorPreservesPreferredIDWhenUnused(t *testing.T) {
	allocator := newConfigIdentityAllocator(configIdentityAllocatorState{})

	if got := allocator.AllocateRuleID(42); got != 42 {
		t.Fatalf("AllocateRuleID(42) = %d, want 42", got)
	}
	if got := allocator.AllocateEgressProfileID(42); got != 42 {
		t.Fatalf("AllocateEgressProfileID(42) = %d, want 42", got)
	}
}

func TestConfigIdentityAllocatorReassignsPreferredIDWhenUsed(t *testing.T) {
	allocator := newConfigIdentityAllocator(configIdentityAllocatorState{
		HTTPRules: []storage.HTTPRuleRow{{ID: 42, AgentID: "edge-a", Revision: 1}},
	})

	if got := allocator.AllocateRuleID(42); got != 43 {
		t.Fatalf("AllocateRuleID(42) = %d, want 43", got)
	}
}

func TestConfigIdentityAllocatorAllocatesRevisionAboveAgentFloor(t *testing.T) {
	allocator := newConfigIdentityAllocator(configIdentityAllocatorState{
		Agents: []storage.AgentRow{{
			ID:              "edge-a",
			DesiredRevision: 9,
			CurrentRevision: 7,
		}},
	})

	if got := allocator.AllocateRevisionForAgent("edge-a", 4); got != 10 {
		t.Fatalf("AllocateRevisionForAgent() = %d, want 10", got)
	}
}

func TestConfigIdentityAllocatorAllocatesRevisionAcrossTargets(t *testing.T) {
	allocator := newConfigIdentityAllocator(configIdentityAllocatorState{
		Agents: []storage.AgentRow{
			{ID: "edge-a", DesiredRevision: 4, CurrentRevision: 8},
			{ID: "edge-b", DesiredRevision: 11, CurrentRevision: 10},
		},
	})

	if got := allocator.AllocateRevisionForTargets([]string{"edge-a", "edge-b"}, 6); got != 12 {
		t.Fatalf("AllocateRevisionForTargets() = %d, want 12", got)
	}
	if next := allocator.AllocateRevisionForAgent("edge-a", 0); next != 13 {
		t.Fatalf("follow-up AllocateRevisionForAgent() = %d, want 13", next)
	}
}

func TestConfigIdentityAllocatorUsesLocalAgentStateFloor(t *testing.T) {
	allocator := newConfigIdentityAllocator(configIdentityAllocatorState{
		LocalAgentID: "local",
		LocalState: storage.LocalAgentStateRow{
			DesiredRevision: 5,
			CurrentRevision: 8,
		},
	})

	if got := allocator.AllocateRevisionForAgent("local", 3); got != 9 {
		t.Fatalf("AllocateRevisionForAgent(local) = %d, want 9", got)
	}
}

func TestConfigIdentityAllocatorFromStoreUsesWireGuardRevisionFloor(t *testing.T) {
	ctx := context.Background()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "data"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "edge-a", Name: "edge-a"}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := store.SaveWireGuardProfiles(ctx, "edge-a", []storage.WireGuardProfileRow{{
		ID:            9,
		Name:          "relay tunnel",
		PrivateKey:    testWireGuardPrivateKey,
		AddressesJSON: `["10.0.0.1/24"]`,
		PeersJSON:     `[]`,
		Revision:      20,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}

	allocator, err := newConfigIdentityAllocatorFromStore(ctx, config.Config{LocalAgentID: "local"}, store)
	if err != nil {
		t.Fatalf("newConfigIdentityAllocatorFromStore() error = %v", err)
	}

	if got := allocator.AllocateRuleID(9); got != 10 {
		t.Fatalf("AllocateRuleID(9) = %d, want 10", got)
	}
	if got := allocator.AllocateRevisionForAgent("edge-a", 3); got != 21 {
		t.Fatalf("L4 revision = %d, want 21", got)
	}
	if got := allocator.AllocateRevisionForAgent("edge-a", 4); got != 22 {
		t.Fatalf("HTTP revision = %d, want 22", got)
	}
	if got := allocator.AllocateRevisionForAgent("edge-a", 5); got != 23 {
		t.Fatalf("relay revision = %d, want 23", got)
	}
	if got := allocator.AllocateRevisionForTargets([]string{"edge-a"}, 6); got != 24 {
		t.Fatalf("cert revision = %d, want 24", got)
	}
}
