package service

import (
	"testing"

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

func TestConfigIdentityAllocatorPreservesHiddenIDsWithoutUsingHiddenRevisionFloors(t *testing.T) {
	t.Parallel()

	hiddenEgressID := 77
	allocator := newConfigIdentityAllocator(configIdentityAllocatorState{
		LocalAgentID: "local",
		HTTPRules: []storage.HTTPRuleRow{{
			ID: 80, AgentID: "local", EgressProfileID: &hiddenEgressID, Revision: 96,
		}},
		L4Rules: []storage.L4RuleRow{{
			ID: 79, AgentID: "local", ListenMode: "unsupported", Revision: 97,
		}},
		RelayListeners: []storage.RelayListenerRow{{
			ID: 78, AgentID: "local", TransportMode: "unsupported", Revision: 98,
		}},
		EgressProfiles: []storage.EgressProfileRow{{
			ID: hiddenEgressID, Type: "unsupported", Revision: 99,
		}},
	})

	if got := allocator.AllocateRevisionGlobal(0); got != 1 {
		t.Fatalf("ordinary revision = %d, want 1", got)
	}
	if got := allocator.AllocateRuleID(80); got == 80 {
		t.Fatalf("rule ID = %d, want hidden physical ID reserved", got)
	}
	if got := allocator.AllocateListenerID(78); got == 78 {
		t.Fatalf("listener ID = %d, want hidden physical ID reserved", got)
	}
	if got := allocator.AllocateEgressProfileID(hiddenEgressID); got == hiddenEgressID {
		t.Fatalf("egress ID = %d, want hidden physical ID reserved", got)
	}
}
