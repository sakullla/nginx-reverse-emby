package core

import (
	"context"
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestSnapshotActivatorActivatesInOrderAndPassesPayloads(t *testing.T) {
	ctx := context.Background()
	var calls []string
	var gotHTTP SnapshotHTTPInput
	var gotL4 SnapshotL4Input
	var gotRelay SnapshotRelayInput

	activator := NewSnapshotActivator("agent-a", "alpha", SnapshotActivationHandlers{
		ActivateManagedCertificates: func(_ context.Context, bundles []model.ManagedCertificateBundle, policies []model.ManagedCertificatePolicy) error {
			calls = append(calls, "certs")
			if len(bundles) != 1 || bundles[0].ID != 10 {
				t.Fatalf("cert bundles = %+v", bundles)
			}
			if len(policies) != 1 || policies[0].ID != 20 {
				t.Fatalf("cert policies = %+v", policies)
			}
			return nil
		},
		ActivateAgentConfig: func(_ context.Context, cfg model.AgentConfig) error {
			calls = append(calls, "agent_config")
			if cfg.OutboundProxyURL != "http://127.0.0.1:8080" {
				t.Fatalf("OutboundProxyURL = %q", cfg.OutboundProxyURL)
			}
			return nil
		},
		ActivateHTTPRules: func(_ context.Context, input SnapshotHTTPInput) error {
			calls = append(calls, "http")
			gotHTTP = input
			return nil
		},
		ActivateL4Rules: func(_ context.Context, input SnapshotL4Input) error {
			calls = append(calls, "l4")
			gotL4 = input
			return nil
		},
		ActivateRelayListeners: func(_ context.Context, input SnapshotRelayInput) error {
			calls = append(calls, "relay")
			gotRelay = input
			return nil
		},
	})

	previous := model.Snapshot{
		Rules: []model.HTTPRule{{
			ID:          1,
			FrontendURL: "https://old.example.com",
			RelayLayers: [][]int{{1}},
		}},
		L4Rules: []model.L4Rule{{
			ID:          2,
			ListenPort:  19000,
			RelayLayers: [][]int{{1}},
		}},
		RelayListeners: []model.RelayListener{
			{ID: 1, AgentID: "agent-a", Name: "local", PublicPort: 1001},
			{ID: 2, AgentID: "agent-b", Name: "remote", PublicPort: 1002},
		},
		WireGuardProfiles: []model.WireGuardProfile{{ID: 1, Name: "wg-old"}},
		EgressProfiles:    []model.EgressProfile{{ID: 1, Name: "egress-old"}},
	}
	next := model.Snapshot{
		AgentConfig: model.AgentConfig{OutboundProxyURL: "http://127.0.0.1:8080"},
		Rules: []model.HTTPRule{{
			ID:          1,
			FrontendURL: "https://new.example.com",
			RelayLayers: [][]int{{1}},
		}},
		L4Rules: []model.L4Rule{{
			ID:          2,
			ListenPort:  19001,
			RelayLayers: [][]int{{1}},
		}},
		RelayListeners: []model.RelayListener{
			{ID: 1, AgentID: "agent-a", Name: "local", PublicPort: 2001},
			{ID: 2, AgentID: "agent-b", Name: "remote", PublicPort: 2002},
		},
		WireGuardProfiles: []model.WireGuardProfile{{ID: 1, Name: "wg-new"}},
		EgressProfiles:    []model.EgressProfile{{ID: 1, Name: "egress-new"}},
		Certificates:      []model.ManagedCertificateBundle{{ID: 10}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{
			ID: 20,
		}},
	}

	if err := activator(ctx, previous, next); err != nil {
		t.Fatalf("activator returned error: %v", err)
	}

	if want := []string{"certs", "agent_config", "http", "l4", "relay"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %+v, want %+v", calls, want)
	}
	if !reflect.DeepEqual(gotHTTP.Rules, next.Rules) ||
		!reflect.DeepEqual(gotHTTP.RelayListeners, next.RelayListeners) ||
		!reflect.DeepEqual(gotHTTP.WireGuardProfiles, next.WireGuardProfiles) ||
		!reflect.DeepEqual(gotHTTP.EgressProfiles, next.EgressProfiles) {
		t.Fatalf("http input = %+v", gotHTTP)
	}
	if !reflect.DeepEqual(gotL4.Rules, next.L4Rules) ||
		!reflect.DeepEqual(gotL4.RelayListeners, next.RelayListeners) ||
		!reflect.DeepEqual(gotL4.WireGuardProfiles, next.WireGuardProfiles) ||
		!reflect.DeepEqual(gotL4.EgressProfiles, next.EgressProfiles) {
		t.Fatalf("l4 input = %+v", gotL4)
	}
	if want := []model.RelayListener{next.RelayListeners[0]}; !reflect.DeepEqual(gotRelay.RelayListeners, want) {
		t.Fatalf("relay listeners = %+v, want %+v", gotRelay.RelayListeners, want)
	}
	if !reflect.DeepEqual(gotRelay.WireGuardProfiles, next.WireGuardProfiles) ||
		!reflect.DeepEqual(gotRelay.EgressProfiles, next.EgressProfiles) {
		t.Fatalf("relay input = %+v", gotRelay)
	}
}

func TestSnapshotActivatorSkipsHTTPAndL4ForUnrelatedRelayListenerChanges(t *testing.T) {
	var httpCalls int
	var l4Calls int
	var relayCalls int

	activator := NewSnapshotActivator("agent-a", "alpha", SnapshotActivationHandlers{
		ActivateHTTPRules: func(context.Context, SnapshotHTTPInput) error {
			httpCalls++
			return nil
		},
		ActivateL4Rules: func(context.Context, SnapshotL4Input) error {
			l4Calls++
			return nil
		},
		ActivateRelayListeners: func(context.Context, SnapshotRelayInput) error {
			relayCalls++
			return nil
		},
	})

	previous := model.Snapshot{
		Rules:          []model.HTTPRule{{ID: 1, RelayLayers: [][]int{{1}}}},
		L4Rules:        []model.L4Rule{{ID: 1, RelayLayers: [][]int{{1}}}},
		RelayListeners: []model.RelayListener{{ID: 1, AgentID: "agent-a", PublicPort: 1001}, {ID: 2, AgentID: "agent-a", PublicPort: 1002}},
	}
	next := previous
	next.RelayListeners = append([]model.RelayListener(nil), previous.RelayListeners...)
	next.RelayListeners[1].PublicPort = 2002

	if err := activator(context.Background(), previous, next); err != nil {
		t.Fatalf("activator returned error: %v", err)
	}

	if httpCalls != 0 {
		t.Fatalf("http calls = %d, want 0", httpCalls)
	}
	if l4Calls != 0 {
		t.Fatalf("l4 calls = %d, want 0", l4Calls)
	}
	if relayCalls != 1 {
		t.Fatalf("relay calls = %d, want 1", relayCalls)
	}
}

func TestSnapshotActivatorRefreshesHTTPAndL4ForWireGuardAndEgressChanges(t *testing.T) {
	egressID := 7
	var httpCalls int
	var l4Calls int

	activator := NewSnapshotActivator("agent-a", "alpha", SnapshotActivationHandlers{
		ActivateHTTPRules: func(_ context.Context, input SnapshotHTTPInput) error {
			httpCalls++
			if len(input.WireGuardProfiles) != 1 || input.WireGuardProfiles[0].Revision != 2 {
				t.Fatalf("http wireguard profiles = %+v", input.WireGuardProfiles)
			}
			if len(input.EgressProfiles) != 1 || input.EgressProfiles[0].Revision != 2 {
				t.Fatalf("http egress profiles = %+v", input.EgressProfiles)
			}
			return nil
		},
		ActivateL4Rules: func(_ context.Context, input SnapshotL4Input) error {
			l4Calls++
			if len(input.WireGuardProfiles) != 1 || input.WireGuardProfiles[0].Revision != 2 {
				t.Fatalf("l4 wireguard profiles = %+v", input.WireGuardProfiles)
			}
			if len(input.EgressProfiles) != 1 || input.EgressProfiles[0].Revision != 2 {
				t.Fatalf("l4 egress profiles = %+v", input.EgressProfiles)
			}
			return nil
		},
	})

	previous := model.Snapshot{
		Rules: []model.HTTPRule{{
			ID:                    1,
			WireGuardEntryEnabled: true,
			EgressProfileID:       &egressID,
		}},
		L4Rules: []model.L4Rule{{
			ID:              1,
			ListenMode:      "wireguard",
			EgressProfileID: &egressID,
		}},
		WireGuardProfiles: []model.WireGuardProfile{{ID: 1, Revision: 1}},
		EgressProfiles:    []model.EgressProfile{{ID: egressID, Revision: 1}},
	}
	next := previous
	next.WireGuardProfiles = []model.WireGuardProfile{{ID: 1, Revision: 2}}
	next.EgressProfiles = []model.EgressProfile{{ID: egressID, Revision: 2}}

	if err := activator(context.Background(), previous, next); err != nil {
		t.Fatalf("activator returned error: %v", err)
	}

	if httpCalls != 1 {
		t.Fatalf("http calls = %d, want 1", httpCalls)
	}
	if l4Calls != 1 {
		t.Fatalf("l4 calls = %d, want 1", l4Calls)
	}
}

func TestSnapshotActivatorRefreshesRelayForWireGuardChangesAndFiltersLocalListeners(t *testing.T) {
	for _, tt := range []struct {
		name      string
		listener  model.RelayListener
		wantCalls int
		wantLocal []model.RelayListener
	}{
		{
			name:      "local by id",
			listener:  model.RelayListener{ID: 1, AgentID: "agent-a"},
			wantCalls: 1,
			wantLocal: []model.RelayListener{{ID: 1, AgentID: "agent-a"}},
		},
		{
			name:      "local by name",
			listener:  model.RelayListener{ID: 1, AgentID: "alpha"},
			wantCalls: 1,
			wantLocal: []model.RelayListener{{ID: 1, AgentID: "alpha"}},
		},
		{name: "remote", listener: model.RelayListener{ID: 1, AgentID: "agent-b"}, wantCalls: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var relayCalls int
			var got SnapshotRelayInput
			activator := NewSnapshotActivator("agent-a", "alpha", SnapshotActivationHandlers{
				ActivateRelayListeners: func(_ context.Context, input SnapshotRelayInput) error {
					relayCalls++
					got = input
					return nil
				},
			})

			previous := model.Snapshot{
				RelayListeners:    []model.RelayListener{tt.listener},
				WireGuardProfiles: []model.WireGuardProfile{{ID: 1, Revision: 1}},
				EgressProfiles:    []model.EgressProfile{{ID: 1, Revision: 1}},
			}
			next := previous
			next.WireGuardProfiles = []model.WireGuardProfile{{ID: 1, Revision: 2}}
			next.EgressProfiles = []model.EgressProfile{{ID: 1, Revision: 2}}

			if err := activator(context.Background(), previous, next); err != nil {
				t.Fatalf("activator returned error: %v", err)
			}

			if relayCalls != tt.wantCalls {
				t.Fatalf("relay calls = %d, want %d", relayCalls, tt.wantCalls)
			}
			if !(len(tt.wantLocal) == 0 && len(got.RelayListeners) == 0) && !reflect.DeepEqual(got.RelayListeners, tt.wantLocal) {
				t.Fatalf("relay listeners = %+v, want %+v", got.RelayListeners, tt.wantLocal)
			}
			if len(got.WireGuardProfiles) != 1 || got.WireGuardProfiles[0].Revision != 2 {
				t.Fatalf("wireguard profiles = %+v", got.WireGuardProfiles)
			}
			if len(got.EgressProfiles) != 1 || got.EgressProfiles[0].Revision != 2 {
				t.Fatalf("egress profiles = %+v", got.EgressProfiles)
			}
		})
	}
}

func TestSnapshotActivatorSkipsRelayForEgressOnlyChangeWhenNoLocalListenersExist(t *testing.T) {
	var relayCalls int
	activator := NewSnapshotActivator("agent-a", "alpha", SnapshotActivationHandlers{
		ActivateRelayListeners: func(context.Context, SnapshotRelayInput) error {
			relayCalls++
			return nil
		},
	})

	previous := model.Snapshot{
		RelayListeners: []model.RelayListener{{ID: 1, AgentID: "agent-b"}},
		EgressProfiles: []model.EgressProfile{{ID: 1, Revision: 1}},
	}
	next := previous
	next.EgressProfiles = []model.EgressProfile{{ID: 1, Revision: 2}}

	if err := activator(context.Background(), previous, next); err != nil {
		t.Fatalf("activator returned error: %v", err)
	}
	if relayCalls != 0 {
		t.Fatalf("relay calls = %d, want 0", relayCalls)
	}
}
