package runtime

import (
	"context"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestRuntimeActivatesHTTPRelayAndL4FromOneSnapshot(t *testing.T) {
	ctx := context.Background()

	calls := make(map[string]int)
	var gotHTTPRelayPort int
	var gotL4RelayPort int
	var gotRelayPort int

	r := NewWithActivator(NewSnapshotActivator(SnapshotActivationHandlers{
		ActivateHTTPRules: func(_ context.Context, rules []model.HTTPRule, relayListeners []model.RelayListener) error {
			calls["http"]++
			if len(rules) != 1 {
				t.Fatalf("expected one http rule, got %d", len(rules))
			}
			if len(relayListeners) != 1 {
				t.Fatalf("expected one relay listener for http activation, got %d", len(relayListeners))
			}
			gotHTTPRelayPort = relayListeners[0].PublicPort
			return nil
		},
		ActivateRelayListeners: func(_ context.Context, relayListeners []model.RelayListener) error {
			calls["relay"]++
			if len(relayListeners) != 1 {
				t.Fatalf("expected one relay listener for relay activation, got %d", len(relayListeners))
			}
			gotRelayPort = relayListeners[0].PublicPort
			return nil
		},
		ActivateL4Rules: func(_ context.Context, rules []model.L4Rule, relayListeners []model.RelayListener) error {
			calls["l4"]++
			if len(rules) != 1 {
				t.Fatalf("expected one l4 rule, got %d", len(rules))
			}
			if len(relayListeners) != 1 {
				t.Fatalf("expected one relay listener for l4 activation, got %d", len(relayListeners))
			}
			gotL4RelayPort = relayListeners[0].PublicPort
			return nil
		},
	}))

	previous := model.Snapshot{
		DesiredVersion: "v1",
		Revision:       1,
		Rules: []model.HTTPRule{{
			FrontendURL: "https://relay-http.example.com",
			Backends: []model.HTTPBackend{
				{URL: "http://10.0.0.10:8096"},
			},
			RelayChain: []int{1},
		}},
		L4Rules: []model.L4Rule{{
			Protocol:   "tcp",
			ListenHost: "127.0.0.1",
			ListenPort: 19000,
			Backends: []model.L4Backend{
				{Host: "10.0.0.20", Port: 9000},
			},
			RelayChain: []int{1},
		}},
		RelayListeners: []model.RelayListener{{
			ID:         1,
			AgentID:    "agent-a",
			Name:       "relay-a",
			ListenHost: "127.0.0.1",
			BindHosts:  []string{"127.0.0.1"},
			ListenPort: 9443,
			PublicHost: "relay-a.example.com",
			PublicPort: 29443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "spki_sha256",
				Value: "cGlubmVk",
			}},
		}},
	}

	if err := r.Apply(ctx, model.Snapshot{}, previous); err != nil {
		t.Fatalf("priming apply failed: %v", err)
	}

	calls = make(map[string]int)
	next := previous
	next.Revision = 2
	next.RelayListeners = append([]model.RelayListener(nil), previous.RelayListeners...)
	next.RelayListeners[0].PublicPort = 39443

	if err := r.Apply(ctx, previous, next); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if calls["http"] != 1 || calls["relay"] != 1 || calls["l4"] != 1 {
		t.Fatalf("expected one activation each for http/relay/l4, got %+v", calls)
	}
	if gotHTTPRelayPort != 39443 {
		t.Fatalf("http activation did not receive updated relay listener: got port %d", gotHTTPRelayPort)
	}
	if gotRelayPort != 39443 {
		t.Fatalf("relay activation did not receive updated relay listener: got port %d", gotRelayPort)
	}
	if gotL4RelayPort != 39443 {
		t.Fatalf("l4 activation did not receive updated relay listener: got port %d", gotL4RelayPort)
	}
}

func TestRuntimeSkipsHTTPAndL4ForUnrelatedRelayListenerChanges(t *testing.T) {
	ctx := context.Background()

	var httpCalls int
	var relayCalls int
	var l4Calls int

	r := NewWithActivator(NewSnapshotActivator(SnapshotActivationHandlers{
		ActivateHTTPRules: func(_ context.Context, _ []model.HTTPRule, _ []model.RelayListener) error {
			httpCalls++
			return nil
		},
		ActivateRelayListeners: func(_ context.Context, _ []model.RelayListener) error {
			relayCalls++
			return nil
		},
		ActivateL4Rules: func(_ context.Context, _ []model.L4Rule, _ []model.RelayListener) error {
			l4Calls++
			return nil
		},
	}))

	previous := model.Snapshot{
		DesiredVersion: "v1",
		Revision:       1,
		Rules: []model.HTTPRule{{
			RelayChain: []int{1},
		}},
		L4Rules: []model.L4Rule{{
			Protocol:   "tcp",
			ListenHost: "127.0.0.1",
			ListenPort: 19000,
			Backends: []model.L4Backend{
				{Host: "10.0.0.20", Port: 9000},
			},
			RelayChain: []int{1},
		}},
		RelayListeners: []model.RelayListener{
			{
				ID:         1,
				ListenHost: "127.0.0.1",
				BindHosts:  []string{"127.0.0.1"},
				ListenPort: 9443,
				PublicPort: 29443,
				Enabled:    true,
				TLSMode:    "pin_only",
				PinSet: []model.RelayPin{{
					Type:  "spki_sha256",
					Value: "cGlubmVk",
				}},
			},
			{
				ID:         2,
				ListenHost: "127.0.0.1",
				BindHosts:  []string{"127.0.0.1"},
				ListenPort: 9543,
				PublicPort: 29543,
				Enabled:    true,
				TLSMode:    "pin_only",
				PinSet: []model.RelayPin{{
					Type:  "spki_sha256",
					Value: "cGlubmVk",
				}},
			},
		},
	}

	if err := r.Apply(ctx, model.Snapshot{}, previous); err != nil {
		t.Fatalf("priming apply failed: %v", err)
	}

	httpCalls = 0
	relayCalls = 0
	l4Calls = 0

	next := previous
	next.Revision = 2
	next.RelayListeners = append([]model.RelayListener(nil), previous.RelayListeners...)
	next.RelayListeners[1].PublicPort = 39543

	if err := r.Apply(ctx, previous, next); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if httpCalls != 0 {
		t.Fatalf("expected no http activation for unrelated relay listener change, got %d", httpCalls)
	}
	if l4Calls != 0 {
		t.Fatalf("expected no l4 activation for unrelated relay listener change, got %d", l4Calls)
	}
	if relayCalls != 1 {
		t.Fatalf("expected relay activation on relay listener change, got %d", relayCalls)
	}
}
