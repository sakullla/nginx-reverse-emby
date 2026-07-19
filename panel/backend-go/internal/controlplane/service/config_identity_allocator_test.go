package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestConfigIdentityAllocatorIDNamespaces(t *testing.T) {
	t.Parallel()

	rules := newConfigIdentityAllocator(configIdentityAllocatorState{
		HTTPRules: []storage.HTTPRuleRow{{ID: 7}},
		L4Rules:   []storage.L4RuleRow{{ID: 8}},
	})
	if got := rules.AllocateRuleID(0); got != 9 {
		t.Fatalf("shared rule namespace ID = %d, want 9", got)
	}

	resources := newConfigIdentityAllocator(configIdentityAllocatorState{
		RelayListeners: []storage.RelayListenerRow{{ID: 5}},
		EgressProfiles: []storage.EgressProfileRow{{ID: 5}},
		Certificates:   []storage.ManagedCertificateRow{{ID: 5}},
	})
	if listener, egress, cert := resources.AllocateListenerID(0), resources.AllocateEgressProfileID(0), resources.AllocateCertificateID(0); listener != 6 || egress != 6 || cert != 6 {
		t.Fatalf("independent namespace IDs = listener:%d egress:%d cert:%d", listener, egress, cert)
	}

	preferred := newConfigIdentityAllocator(configIdentityAllocatorState{
		HTTPRules: []storage.HTTPRuleRow{{ID: 42}},
	})
	if got := preferred.AllocateRuleID(42); got != 43 {
		t.Fatalf("occupied preferred rule ID = %d, want 43", got)
	}
	if got := preferred.AllocateEgressProfileID(42); got != 42 {
		t.Fatalf("free preferred egress ID = %d, want 42", got)
	}
}

func TestConfigIdentityAllocatorRevisionFloors(t *testing.T) {
	t.Parallel()

	agents := newConfigIdentityAllocator(configIdentityAllocatorState{
		Agents: []storage.AgentRow{
			{ID: "edge-a", DesiredRevision: 4, CurrentRevision: 8},
			{ID: "edge-b", DesiredRevision: 11, CurrentRevision: 10},
		},
	})
	if got := agents.AllocateRevisionForTargets([]string{"edge-a", "edge-b"}, 6); got != 12 {
		t.Fatalf("target revision = %d, want 12", got)
	}
	if got := agents.AllocateRevisionForAgent("edge-a", 0); got != 13 {
		t.Fatalf("follow-up agent revision = %d, want 13", got)
	}

	local := newConfigIdentityAllocator(configIdentityAllocatorState{
		LocalAgentID: "local",
		LocalState: storage.LocalAgentStateRow{
			DesiredRevision: 5,
			CurrentRevision: 8,
		},
	})
	if got := local.AllocateRevisionForAgent("local", 3); got != 9 {
		t.Fatalf("local revision = %d, want 9", got)
	}
}

func TestConfigIdentityAllocatorFromStoreUsesWireGuardRevisionFloor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "data"), "local")
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
		t.Fatalf("rule ID = %d, want 10", got)
	}
	for index, want := range []int{21, 22, 23} {
		if got := allocator.AllocateRevisionForAgent("edge-a", index+3); got != want {
			t.Fatalf("agent revision %d = %d, want %d", index, got, want)
		}
	}
	if got := allocator.AllocateRevisionForTargets([]string{"edge-a"}, 6); got != 24 {
		t.Fatalf("target revision = %d, want 24", got)
	}
}
