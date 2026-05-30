package runtime

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestApplyFailureKeepsPreviousSnapshot(t *testing.T) {
	ctx := context.Background()

	failingActivator := func(_ context.Context, previous, next model.Snapshot) error {
		if next.Revision == 2 {
			return errors.New("activation failed")
		}
		return nil
	}

	r := newRuntimeWithActivator(failingActivator)
	initial := model.Snapshot{
		DesiredVersion: "v1",
		Revision:       1,
		Certificates: []model.ManagedCertificateBundle{{
			ID:       1,
			Domain:   "sync.example.com",
			Revision: 1,
			CertPEM:  "CERT",
			KeyPEM:   "KEY",
		}},
	}
	if err := r.Apply(ctx, model.Snapshot{}, initial); err != nil {
		t.Fatalf("priming apply failed: %v", err)
	}

	next := model.Snapshot{DesiredVersion: "v2", Revision: 2}
	if err := r.Apply(ctx, initial, next); err == nil {
		t.Fatalf("expected apply to fail")
	}

	got := r.ActiveSnapshot()
	if !snapshotEqual(got, initial) {
		t.Fatalf("active snapshot mutated on failure: got %+v want %+v", got, initial)
	}

	state := r.State()
	if state.CurrentRevision != initial.Revision {
		t.Fatalf("current revision advanced on failure: got %d want %d", state.CurrentRevision, initial.Revision)
	}

	if state.Status != "error" {
		t.Fatalf("runtime state not error after failure: got %q", state.Status)
	}
}

func TestApplySuccessSwapsSnapshot(t *testing.T) {
	r := New()
	ctx := context.Background()
	first := model.Snapshot{
		DesiredVersion: "stable",
		Revision:       1,
		CertificatePolicies: []model.ManagedCertificatePolicy{{
			ID:              7,
			Domain:          "stable.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			Revision:        1,
			Usage:           "https",
			CertificateType: "uploaded",
		}},
	}
	if err := r.Apply(ctx, model.Snapshot{}, first); err != nil {
		t.Fatalf("priming apply failed: %v", err)
	}

	next := model.Snapshot{
		DesiredVersion: "stable-next",
		Revision:       2,
		CertificatePolicies: []model.ManagedCertificatePolicy{{
			ID:              8,
			Domain:          "next.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			Revision:        2,
			Usage:           "relay_ca",
			CertificateType: "internal_ca",
			SelfSigned:      true,
		}},
	}
	if err := r.Apply(ctx, first, next); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if got := r.ActiveSnapshot(); !snapshotEqual(got, next) {
		t.Fatalf("active snapshot not updated on success: got %+v want %+v", got, next)
	}

	state := r.State()
	if state.CurrentRevision != next.Revision {
		t.Fatalf("current revision not advanced: got %d want %d", state.CurrentRevision, next.Revision)
	}

	if state.Status != "active" {
		t.Fatalf("runtime state not active after success: got %q", state.Status)
	}

	if value, ok := state.Metadata["current_revision"]; !ok || value != strconv.FormatInt(next.Revision, 10) {
		t.Fatalf("expected metadata current_revision to match revision, got %q", value)
	}
}

func TestApplyPreviousMismatchReportsError(t *testing.T) {
	r := New()
	ctx := context.Background()
	base := model.Snapshot{
		DesiredVersion: "base",
		Revision:       1,
		Certificates: []model.ManagedCertificateBundle{{
			ID:       9,
			Domain:   "base.example.com",
			Revision: 1,
			CertPEM:  "CERT",
			KeyPEM:   "KEY",
		}},
	}
	if err := r.Apply(ctx, model.Snapshot{}, base); err != nil {
		t.Fatalf("priming apply failed: %v", err)
	}

	prev := model.Snapshot{DesiredVersion: "mismatch", Revision: 999}
	if err := r.Apply(ctx, prev, model.Snapshot{DesiredVersion: "next", Revision: 2}); err == nil {
		t.Fatalf("expected previous mismatch to return error")
	}

	state := r.State()
	if value, ok := state.Metadata["current_revision"]; !ok || value != strconv.FormatInt(base.Revision, 10) {
		t.Fatalf("current revision metadata changed after mismatch: %v", state.Metadata["current_revision"])
	}

	if state.Status != "error" {
		t.Fatalf("runtime state not error after mismatch: got %q", state.Status)
	}
}

func TestApplyPreviousMismatchTreatsAgentConfigAndEgressAsSnapshotPayload(t *testing.T) {
	tests := []struct {
		name     string
		previous model.Snapshot
	}{
		{
			name: "agent config only",
			previous: model.Snapshot{
				AgentConfig: model.AgentConfig{
					OutboundProxyURL: "http://127.0.0.1:8080",
				},
			},
		},
		{
			name: "egress profiles only",
			previous: model.Snapshot{
				EgressProfiles: []model.EgressProfile{{
					ID:       7,
					Name:     "exit",
					Type:     "socks",
					ProxyURL: "socks5://127.0.0.1:1080",
					Enabled:  true,
				}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			active := model.Snapshot{DesiredVersion: "active", Revision: 1}
			if err := r.Apply(context.Background(), model.Snapshot{}, active); err != nil {
				t.Fatalf("priming apply failed: %v", err)
			}

			err := r.Apply(context.Background(), tc.previous, model.Snapshot{DesiredVersion: "next", Revision: 2})
			if err == nil {
				t.Fatal("expected previous mismatch to return error")
			}
			if got := r.ActiveSnapshot(); !snapshotEqual(got, active) {
				t.Fatalf("active snapshot changed after mismatch: got %+v want %+v", got, active)
			}
		})
	}
}

func TestStateReturnsMetadataCopy(t *testing.T) {
	r := New()
	ctx := context.Background()
	initial := model.Snapshot{
		DesiredVersion: "copy",
		Revision:       1,
		AgentConfig: model.AgentConfig{
			OutboundProxyURL:     "http://127.0.0.1:8080",
			TrafficStatsInterval: "30s",
			TrafficStatsEnabled:  ptrBool(true),
		},
		Rules: []model.HTTPRule{{
			FrontendURL: "https://frontend.example.com",
			Backends: []model.HTTPBackend{
				{URL: "http://10.0.0.11:8096"},
				{URL: "http://10.0.0.12:8096"},
			},
			LoadBalancing: model.LoadBalancing{
				Strategy: "random",
			},
			CustomHeaders: []model.HTTPHeader{{
				Name:  "X-Test",
				Value: "one",
			}},
			Revision: 1,
		}},
		L4Rules: []model.L4Rule{{
			Protocol:   "tcp",
			ListenHost: "127.0.0.1",
			ListenPort: 9000,
			Backends: []model.L4Backend{
				{Host: "10.0.0.21", Port: 9001},
				{Host: "10.0.0.22", Port: 9002},
			},
			LoadBalancing: model.LoadBalancing{
				Strategy: "round_robin",
			},
			Tuning: model.L4Tuning{
				ProxyProtocol: model.L4ProxyProtocolTuning{
					Decode: true,
					Send:   true,
				},
			},
			RelayLayers: [][]int{{1}, {2}},
			Revision:    1,
		}},
		RelayListeners: []model.RelayListener{{
			ID:         10,
			AgentID:    "agent-a",
			Name:       "relay-a",
			ListenHost: "127.0.0.1",
			BindHosts:  []string{"127.0.0.1", "127.0.0.2"},
			ListenPort: 9443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "pin-one",
			}},
			TrustedCACertificateIDs: []int{7},
			Tags:                    []string{"tag-one"},
			Revision:                1,
		}},
		Certificates: []model.ManagedCertificateBundle{{
			ID:       3,
			Domain:   "copy.example.com",
			Revision: 1,
			CertPEM:  "CERT",
			KeyPEM:   "KEY",
		}},
	}
	if err := r.Apply(ctx, model.Snapshot{}, initial); err != nil {
		t.Fatalf("priming apply failed: %v", err)
	}

	state := r.State()
	state.Metadata["leak"] = "mutated"

	second := r.State()
	if _, ok := second.Metadata["leak"]; ok {
		t.Fatalf("metadata copy leaked: %v", second.Metadata)
	}
}

func TestActiveSnapshotReturnsSliceIsolation(t *testing.T) {
	r := New()
	ctx := context.Background()
	initial := model.Snapshot{
		DesiredVersion: "copy",
		Revision:       1,
		AgentConfig: model.AgentConfig{
			OutboundProxyURL:     "http://127.0.0.1:8080",
			TrafficStatsInterval: "30s",
			TrafficStatsEnabled:  ptrBool(true),
		},
		Rules: []model.HTTPRule{{
			FrontendURL: "https://frontend.example.com",
			Backends: []model.HTTPBackend{
				{URL: "http://10.0.0.11:8096"},
				{URL: "http://10.0.0.12:8096"},
			},
			LoadBalancing: model.LoadBalancing{
				Strategy: "random",
			},
			CustomHeaders: []model.HTTPHeader{{
				Name:  "X-Test",
				Value: "one",
			}},
			WireGuardProfileID: ptrInt(51),
			EgressProfileID:    ptrInt(52),
			RelayChain:         []int{31, 32},
			RelayLayers:        [][]int{{1, 2}, {3}},
			Tags:               []string{"http-tag"},
			Revision:           1,
		}},
		L4Rules: []model.L4Rule{{
			Protocol:   "tcp",
			ListenHost: "127.0.0.1",
			ListenPort: 9000,
			Backends: []model.L4Backend{
				{Host: "10.0.0.21", Port: 9001},
				{Host: "10.0.0.22", Port: 9002},
			},
			LoadBalancing: model.LoadBalancing{
				Strategy: "round_robin",
			},
			Tuning: model.L4Tuning{
				ProxyProtocol: model.L4ProxyProtocolTuning{
					Decode: true,
					Send:   true,
				},
			},
			WireGuardProfileID: ptrInt(61),
			EgressProfileID:    ptrInt(62),
			RelayChain:         []int{41, 42},
			RelayLayers:        [][]int{{4}, {5, 6}},
			Tags:               []string{"l4-tag"},
			Revision:           1,
		}},
		RelayListeners: []model.RelayListener{{
			ID:                 10,
			AgentID:            "agent-a",
			Name:               "relay-a",
			ListenHost:         "127.0.0.1",
			BindHosts:          []string{"127.0.0.1", "127.0.0.2"},
			ListenPort:         9443,
			Enabled:            true,
			CertificateID:      ptrInt(71),
			WireGuardProfileID: ptrInt(72),
			TLSMode:            "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "pin-one",
			}},
			TrustedCACertificateIDs: []int{7},
			Tags:                    []string{"tag-one"},
			Revision:                1,
		}},
		Certificates: []model.ManagedCertificateBundle{{
			ID:       3,
			Domain:   "copy.example.com",
			Revision: 1,
			CertPEM:  "CERT",
			KeyPEM:   "KEY",
		}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{
			ID:              4,
			Domain:          "policy.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			Revision:        1,
			Usage:           "https",
			CertificateType: "uploaded",
			Tags:            []string{"one"},
		}},
		WireGuardProfiles: []model.WireGuardProfile{{
			ID:         11,
			AgentID:    "agent-a",
			Name:       "wg-a",
			Mode:       "generic_wireguard",
			PrivateKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			ListenPort: 51820,
			BindAddresses: []string{
				"192.0.2.10",
				"2001:db8::10",
			},
			Addresses: []string{"10.20.0.1/24"},
			Peers: []model.WireGuardPeer{{
				Name:         "peer-a",
				PublicKey:    "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=",
				PresharedKey: "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC=",
				Endpoint:     "peer.example.com:51820",
				AllowedIPs:   []string{"10.20.0.2/32"},
				Reserved:     []byte{1, 2, 3},
			}},
			DNS:      []string{"1.1.1.1"},
			MTU:      1420,
			Enabled:  true,
			Tags:     []string{"edge"},
			Revision: 1,
		}},
		EgressProfiles: []model.EgressProfile{{
			ID:      12,
			Name:    "egress-wg",
			Type:    "wireguard",
			Enabled: true,
			WireGuardConfig: &model.EgressWireGuardConfig{
				PrivateKey: "DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD=",
				Addresses:  []string{"10.30.0.1/24"},
				Peers: []model.WireGuardPeer{{
					Name:         "egress-peer",
					PublicKey:    "EEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEM=",
					PresharedKey: "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF=",
					Endpoint:     "egress.example.com:51820",
					AllowedIPs:   []string{"10.30.0.2/32"},
					Reserved:     []byte{4, 5, 6},
				}},
				DNS: []string{"9.9.9.9"},
				MTU: 1280,
			},
			Revision: 1,
		}},
	}
	if err := r.Apply(ctx, model.Snapshot{}, initial); err != nil {
		t.Fatalf("priming apply failed: %v", err)
	}

	snap := r.ActiveSnapshot()
	*snap.AgentConfig.TrafficStatsEnabled = false
	snap.Rules[0].CustomHeaders[0].Value = "mutated"
	snap.Rules[0].Backends[0].URL = "http://mutated.example.internal:8096"
	*snap.Rules[0].WireGuardProfileID = 99
	*snap.Rules[0].EgressProfileID = 98
	snap.Rules[0].RelayChain[0] = 99
	snap.Rules[0].RelayLayers[0][0] = 99
	snap.Rules[0].Tags[0] = "mutated"
	snap.L4Rules[0].WireGuardProfileID = ptrInt(97)
	snap.L4Rules[0].EgressProfileID = ptrInt(96)
	snap.L4Rules[0].RelayChain[0] = 99
	snap.L4Rules[0].RelayLayers[0][0] = 99
	snap.L4Rules[0].RelayLayers[1][0] = 88
	snap.L4Rules[0].Backends[0].Host = "mutated-host"
	snap.L4Rules[0].Tags[0] = "mutated"
	*snap.RelayListeners[0].CertificateID = 95
	*snap.RelayListeners[0].WireGuardProfileID = 94
	snap.RelayListeners[0].BindHosts[0] = "127.0.0.99"
	snap.RelayListeners[0].PinSet[0].Value = "mutated"
	snap.RelayListeners[0].TrustedCACertificateIDs[0] = 99
	snap.RelayListeners[0].Tags[0] = "mutated"
	snap.Certificates[0].Domain = "mutated.example.com"
	snap.CertificatePolicies[0].Tags[0] = "mutated"
	snap.WireGuardProfiles[0].BindAddresses[0] = "192.0.2.99"
	snap.WireGuardProfiles[0].Peers[0].AllowedIPs[0] = "10.20.0.99/32"
	snap.WireGuardProfiles[0].Peers[0].Reserved[0] = 9
	snap.EgressProfiles[0].Name = "mutated-egress"
	snap.EgressProfiles[0].WireGuardConfig.Addresses[0] = "10.30.0.99/32"
	snap.EgressProfiles[0].WireGuardConfig.Peers[0].AllowedIPs[0] = "10.30.0.99/32"
	snap.EgressProfiles[0].WireGuardConfig.Peers[0].Reserved[0] = 9
	snap.EgressProfiles[0].WireGuardConfig.DNS[0] = "8.8.8.8"

	current := r.ActiveSnapshot()
	if current.AgentConfig.TrafficStatsEnabled == nil || !*current.AgentConfig.TrafficStatsEnabled {
		t.Fatalf("agent config traffic stats enabled leaked mutation: %+v", current.AgentConfig.TrafficStatsEnabled)
	}
	if current.Rules[0].CustomHeaders[0].Value != "one" {
		t.Fatalf("http rule slice leaked mutation: %+v", current.Rules)
	}
	if current.Rules[0].Backends[0].URL != "http://10.0.0.11:8096" {
		t.Fatalf("http backends leaked mutation: %+v", current.Rules[0].Backends)
	}
	if current.Rules[0].WireGuardProfileID == nil || *current.Rules[0].WireGuardProfileID != 51 {
		t.Fatalf("http wireguard profile id leaked mutation: %+v", current.Rules[0].WireGuardProfileID)
	}
	if current.Rules[0].EgressProfileID == nil || *current.Rules[0].EgressProfileID != 52 {
		t.Fatalf("http egress profile id leaked mutation: %+v", current.Rules[0].EgressProfileID)
	}
	if current.Rules[0].RelayChain[0] != 31 {
		t.Fatalf("http relay_chain leaked mutation: %+v", current.Rules)
	}
	if current.Rules[0].RelayLayers[0][0] != 1 {
		t.Fatalf("http relay_layers leaked mutation: %+v", current.Rules)
	}
	if current.Rules[0].Tags[0] != "http-tag" {
		t.Fatalf("http tags leaked mutation: %+v", current.Rules)
	}
	if current.L4Rules[0].RelayChain[0] != 41 {
		t.Fatalf("l4 relay_chain leaked mutation: %+v", current.L4Rules)
	}
	if current.L4Rules[0].RelayLayers[0][0] != 4 {
		t.Fatalf("l4 relay_layers leaked mutation: %+v", current.L4Rules)
	}
	if current.L4Rules[0].RelayLayers[1][0] != 5 {
		t.Fatalf("l4 relay_layers leaked mutation: %+v", current.L4Rules)
	}
	if current.L4Rules[0].Backends[0].Host != "10.0.0.21" {
		t.Fatalf("l4 backends leaked mutation: %+v", current.L4Rules[0].Backends)
	}
	if current.L4Rules[0].WireGuardProfileID == nil || *current.L4Rules[0].WireGuardProfileID != 61 {
		t.Fatalf("l4 wireguard profile id leaked mutation: %+v", current.L4Rules[0].WireGuardProfileID)
	}
	if current.L4Rules[0].EgressProfileID == nil || *current.L4Rules[0].EgressProfileID != 62 {
		t.Fatalf("l4 egress profile id leaked mutation: %+v", current.L4Rules[0].EgressProfileID)
	}
	if current.L4Rules[0].Tags[0] != "l4-tag" {
		t.Fatalf("l4 tags leaked mutation: %+v", current.L4Rules)
	}
	if current.RelayListeners[0].BindHosts[0] != "127.0.0.1" {
		t.Fatalf("relay bind_hosts leaked mutation: %+v", current.RelayListeners)
	}
	if current.RelayListeners[0].CertificateID == nil || *current.RelayListeners[0].CertificateID != 71 {
		t.Fatalf("relay certificate id leaked mutation: %+v", current.RelayListeners[0].CertificateID)
	}
	if current.RelayListeners[0].WireGuardProfileID == nil || *current.RelayListeners[0].WireGuardProfileID != 72 {
		t.Fatalf("relay wireguard profile id leaked mutation: %+v", current.RelayListeners[0].WireGuardProfileID)
	}
	if current.RelayListeners[0].PinSet[0].Value != "pin-one" {
		t.Fatalf("relay pin_set leaked mutation: %+v", current.RelayListeners)
	}
	if current.RelayListeners[0].TrustedCACertificateIDs[0] != 7 {
		t.Fatalf("relay trusted ca leaked mutation: %+v", current.RelayListeners)
	}
	if current.RelayListeners[0].Tags[0] != "tag-one" {
		t.Fatalf("relay tags leaked mutation: %+v", current.RelayListeners)
	}
	if current.Certificates[0].Domain != "copy.example.com" {
		t.Fatalf("certificate slice leaked mutation: %+v", current.Certificates)
	}
	if current.CertificatePolicies[0].Tags[0] != "one" {
		t.Fatalf("policy tags leaked mutation: %+v", current.CertificatePolicies)
	}
	if current.WireGuardProfiles[0].BindAddresses[0] != "192.0.2.10" {
		t.Fatalf("wireguard bind_addresses leaked mutation: %+v", current.WireGuardProfiles)
	}
	if current.WireGuardProfiles[0].Peers[0].AllowedIPs[0] != "10.20.0.2/32" {
		t.Fatalf("wireguard allowed_ips leaked mutation: %+v", current.WireGuardProfiles[0].Peers)
	}
	if current.WireGuardProfiles[0].Peers[0].Reserved[0] != 1 {
		t.Fatalf("wireguard reserved leaked mutation: %+v", current.WireGuardProfiles[0].Peers)
	}
	if current.EgressProfiles[0].Name != "egress-wg" {
		t.Fatalf("egress profile slice leaked mutation: %+v", current.EgressProfiles)
	}
	if current.EgressProfiles[0].WireGuardConfig.Addresses[0] != "10.30.0.1/24" {
		t.Fatalf("egress wireguard addresses leaked mutation: %+v", current.EgressProfiles[0].WireGuardConfig)
	}
	if current.EgressProfiles[0].WireGuardConfig.Peers[0].AllowedIPs[0] != "10.30.0.2/32" {
		t.Fatalf("egress wireguard peer allowed_ips leaked mutation: %+v", current.EgressProfiles[0].WireGuardConfig.Peers)
	}
	if current.EgressProfiles[0].WireGuardConfig.Peers[0].Reserved[0] != 4 {
		t.Fatalf("egress wireguard peer reserved leaked mutation: %+v", current.EgressProfiles[0].WireGuardConfig.Peers)
	}
	if current.EgressProfiles[0].WireGuardConfig.DNS[0] != "9.9.9.9" {
		t.Fatalf("egress wireguard dns leaked mutation: %+v", current.EgressProfiles[0].WireGuardConfig)
	}
}

func TestApplyMismatchErrorRedactsCertificateMaterial(t *testing.T) {
	r := New()
	ctx := context.Background()
	base := model.Snapshot{
		DesiredVersion: "base",
		Revision:       1,
		Certificates: []model.ManagedCertificateBundle{{
			ID:       9,
			Domain:   "base.example.com",
			Revision: 1,
			CertPEM:  "SECRET_CERT",
			KeyPEM:   "SECRET_KEY",
		}},
	}
	if err := r.Apply(ctx, model.Snapshot{}, base); err != nil {
		t.Fatalf("priming apply failed: %v", err)
	}

	err := r.Apply(ctx, model.Snapshot{DesiredVersion: "mismatch", Revision: 999}, model.Snapshot{})
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	if strings.Contains(err.Error(), "SECRET_CERT") || strings.Contains(err.Error(), "SECRET_KEY") {
		t.Fatalf("mismatch error leaked certificate material: %v", err)
	}
}

func TestActiveSnapshotPreservesExplicitEmptySlices(t *testing.T) {
	r := New()
	ctx := context.Background()
	initial := model.Snapshot{
		DesiredVersion:      "empty",
		Revision:            1,
		Certificates:        []model.ManagedCertificateBundle{},
		CertificatePolicies: []model.ManagedCertificatePolicy{},
	}
	if err := r.Apply(ctx, model.Snapshot{}, initial); err != nil {
		t.Fatalf("priming apply failed: %v", err)
	}

	snap := r.ActiveSnapshot()
	if snap.Certificates == nil || len(snap.Certificates) != 0 {
		t.Fatalf("expected explicit empty certificates slice, got %+v", snap.Certificates)
	}
	if snap.CertificatePolicies == nil || len(snap.CertificatePolicies) != 0 {
		t.Fatalf("expected explicit empty certificate policies slice, got %+v", snap.CertificatePolicies)
	}
}

func ptrInt(v int) *int {
	return &v
}

func ptrBool(v bool) *bool {
	return &v
}
