package service

import (
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestFullSnapshotValidatorAcceptsCompleteResourceGraph(t *testing.T) {
	t.Parallel()
	validator := FullSnapshotValidator{}
	snapshot := validSnapshotForValidation()
	if err := validator.Validate(t.Context(), revision.SnapshotValidation{
		Target:   revision.Target{AgentID: "edge-1", Capabilities: []string{"wireguard", "egress_profiles"}},
		Snapshot: snapshot,
	}); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestFullSnapshotValidatorAllowsRemoteWireGuardProviderWithoutConsumerCapability(t *testing.T) {
	t.Parallel()
	profileID := 7
	snapshot := storage.Snapshot{
		Rules: []storage.HTTPRule{{
			ID: 1, AgentID: "consumer", FrontendURL: "https://consumer.example.com",
			Backends:    []storage.HTTPBackend{{URL: "http://127.0.0.1:8096"}},
			RelayLayers: [][]int{{5}},
		}},
		RelayListeners: []storage.RelayListener{{
			ID: 5, AgentID: "provider", Name: "wireguard relay", Enabled: true,
			TransportMode: "wireguard", WireGuardProfileID: &profileID,
		}},
		WireGuardProfiles: []storage.WireGuardProfile{{
			ID: profileID, AgentID: "provider", Name: "provider profile", Enabled: true,
		}},
	}

	err := (FullSnapshotValidator{}).Validate(t.Context(), revision.SnapshotValidation{
		Target:   revision.Target{AgentID: "consumer"},
		Snapshot: snapshot,
	})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestFullSnapshotValidatorPreservesWireGuardProviderDependencyValidation(t *testing.T) {
	t.Parallel()
	profileID := 7
	wireGuardRelay := func(agentID string) storage.RelayListener {
		return storage.RelayListener{
			ID: 5, AgentID: agentID, Name: "wireguard relay", Enabled: true,
			TransportMode: "wireguard", WireGuardProfileID: &profileID,
		}
	}
	wireGuardProfile := func(agentID string, enabled bool) storage.WireGuardProfile {
		return storage.WireGuardProfile{
			ID: profileID, AgentID: agentID, Name: "wireguard profile", Enabled: enabled,
		}
	}

	tests := []struct {
		name         string
		target       revision.Target
		snapshot     storage.Snapshot
		wantCode     revision.ErrorCode
		wantContains string
	}{
		{
			name:   "target profile still requires wireguard capability",
			target: revision.Target{AgentID: "consumer"},
			snapshot: storage.Snapshot{
				WireGuardProfiles: []storage.WireGuardProfile{wireGuardProfile("consumer", true)},
			},
			wantCode:     revision.ErrorCodeUnprocessable,
			wantContains: "requires the wireguard capability",
		},
		{
			name:   "remote relay copy may omit provider profile",
			target: revision.Target{AgentID: "consumer"},
			snapshot: storage.Snapshot{
				RelayListeners: []storage.RelayListener{wireGuardRelay("provider")},
			},
		},
		{
			name:   "target relay rejects missing profile",
			target: revision.Target{AgentID: "consumer", Capabilities: []string{"wireguard"}},
			snapshot: storage.Snapshot{
				RelayListeners: []storage.RelayListener{wireGuardRelay("consumer")},
			},
			wantCode:     revision.ErrorCodeNotFound,
			wantContains: "missing wireguard profile",
		},
		{
			name:   "target relay rejects disabled profile",
			target: revision.Target{AgentID: "consumer", Capabilities: []string{"wireguard"}},
			snapshot: storage.Snapshot{
				RelayListeners:    []storage.RelayListener{wireGuardRelay("consumer")},
				WireGuardProfiles: []storage.WireGuardProfile{wireGuardProfile("consumer", false)},
			},
			wantCode:     revision.ErrorCodeUnprocessable,
			wantContains: "disabled wireguard profile",
		},
		{
			name:   "target relay rejects profile owned by another agent",
			target: revision.Target{AgentID: "consumer", Capabilities: []string{"wireguard"}},
			snapshot: storage.Snapshot{
				RelayListeners:    []storage.RelayListener{wireGuardRelay("consumer")},
				WireGuardProfiles: []storage.WireGuardProfile{wireGuardProfile("provider", true)},
			},
			wantCode:     revision.ErrorCodeUnprocessable,
			wantContains: "belongs to agent",
		},
		{
			name:   "remote relay rejects disabled provider profile copy",
			target: revision.Target{AgentID: "consumer"},
			snapshot: storage.Snapshot{
				RelayListeners:    []storage.RelayListener{wireGuardRelay("provider")},
				WireGuardProfiles: []storage.WireGuardProfile{wireGuardProfile("provider", false)},
			},
			wantCode:     revision.ErrorCodeUnprocessable,
			wantContains: "disabled wireguard profile",
		},
		{
			name:   "remote relay rejects profile copy owned by another provider",
			target: revision.Target{AgentID: "consumer"},
			snapshot: storage.Snapshot{
				RelayListeners:    []storage.RelayListener{wireGuardRelay("provider")},
				WireGuardProfiles: []storage.WireGuardProfile{wireGuardProfile("other-provider", true)},
			},
			wantCode:     revision.ErrorCodeUnprocessable,
			wantContains: "belongs to agent",
		},
		{
			name:   "target HTTP rule rejects remote profile",
			target: revision.Target{AgentID: "consumer", Capabilities: []string{"wireguard"}},
			snapshot: storage.Snapshot{
				Rules: []storage.HTTPRule{{
					ID: 1, AgentID: "consumer", FrontendURL: "https://consumer.example.com",
					Backends:           []storage.HTTPBackend{{URL: "http://127.0.0.1:8096"}},
					WireGuardProfileID: &profileID,
				}},
				WireGuardProfiles: []storage.WireGuardProfile{wireGuardProfile("provider", true)},
			},
			wantCode:     revision.ErrorCodeUnprocessable,
			wantContains: "belongs to agent",
		},
		{
			name:   "target snapshot rejects HTTP rule owned by another agent",
			target: revision.Target{AgentID: "consumer"},
			snapshot: storage.Snapshot{
				Rules: []storage.HTTPRule{{
					ID: 1, AgentID: "provider", FrontendURL: "https://provider.example.com",
					Backends: []storage.HTTPBackend{{URL: "http://127.0.0.1:8096"}},
				}},
			},
			wantCode:     revision.ErrorCodeUnprocessable,
			wantContains: "belongs to agent",
		},
	}

	validator := FullSnapshotValidator{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.Validate(t.Context(), revision.SnapshotValidation{
				Target:   tt.target,
				Snapshot: tt.snapshot,
			})
			if revision.ErrorCodeOf(err) != tt.wantCode {
				t.Fatalf("Validate() error = %v, code = %q, want %q", err, revision.ErrorCodeOf(err), tt.wantCode)
			}
			if tt.wantContains != "" && !strings.Contains(err.Error(), tt.wantContains) {
				t.Fatalf("Validate() error = %v, want message containing %q", err, tt.wantContains)
			}
		})
	}
}

func TestFullSnapshotValidatorClassifiesReferenceCapabilityAndConflictErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		mutate       func(*storage.Snapshot)
		capabilities []string
		wantCode     revision.ErrorCode
	}{
		{
			name: "missing relay reference",
			mutate: func(snapshot *storage.Snapshot) {
				snapshot.Rules[0].RelayLayers = [][]int{{999}}
			},
			capabilities: []string{"wireguard", "egress_profiles"},
			wantCode:     revision.ErrorCodeNotFound,
		},
		{
			name: "missing wireguard capability",
			mutate: func(_ *storage.Snapshot) {
			},
			capabilities: []string{"egress_profiles"},
			wantCode:     revision.ErrorCodeUnprocessable,
		},
		{
			name: "duplicate frontend",
			mutate: func(snapshot *storage.Snapshot) {
				duplicate := snapshot.Rules[0]
				duplicate.ID = 2
				snapshot.Rules = append(snapshot.Rules, duplicate)
			},
			capabilities: []string{"wireguard", "egress_profiles"},
			wantCode:     revision.ErrorCodeConflict,
		},
		{
			name: "cross module listener conflict",
			mutate: func(snapshot *storage.Snapshot) {
				snapshot.L4Rules = append(snapshot.L4Rules, storage.L4Rule{
					ID: 2, AgentID: "edge-1", Protocol: "tcp", ListenHost: "0.0.0.0", ListenPort: 9443,
					Backends: []storage.L4Backend{{Host: "127.0.0.1", Port: 9000}},
				})
				snapshot.RelayListeners[0].BindHosts = []string{"0.0.0.0"}
				snapshot.RelayListeners[0].ListenPort = 9443
				snapshot.RelayListeners[0].TransportMode = "tls_tcp"
			},
			capabilities: []string{"wireguard", "egress_profiles"},
			wantCode:     revision.ErrorCodeConflict,
		},
	}

	validator := FullSnapshotValidator{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := validSnapshotForValidation()
			tt.mutate(&snapshot)
			err := validator.Validate(t.Context(), revision.SnapshotValidation{
				Target:   revision.Target{AgentID: "edge-1", Capabilities: tt.capabilities},
				Snapshot: snapshot,
			})
			if revision.ErrorCodeOf(err) != tt.wantCode {
				t.Fatalf("Validate() error = %v, code = %q, want %q", err, revision.ErrorCodeOf(err), tt.wantCode)
			}
		})
	}
}

func TestFullSnapshotValidatorRejectsDisabledRelayAndCertificateReferences(t *testing.T) {
	t.Parallel()
	validator := FullSnapshotValidator{}
	tests := []struct {
		name   string
		mutate func(*storage.Snapshot)
	}{
		{
			name: "disabled relay",
			mutate: func(snapshot *storage.Snapshot) {
				snapshot.RelayListeners[0].Enabled = false
			},
		},
		{
			name: "disabled certificate policy",
			mutate: func(snapshot *storage.Snapshot) {
				snapshot.Certificates = nil
				snapshot.CertificatePolicies[0].Enabled = false
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := validSnapshotForValidation()
			tt.mutate(&snapshot)
			err := validator.Validate(t.Context(), revision.SnapshotValidation{
				Target: revision.Target{
					AgentID: "edge-1", Capabilities: []string{"wireguard", "egress_profiles"},
				},
				Snapshot: snapshot,
			})
			if revision.ErrorCodeOf(err) != revision.ErrorCodeUnprocessable {
				t.Fatalf("Validate() error = %v, code = %q, want %q", err, revision.ErrorCodeOf(err), revision.ErrorCodeUnprocessable)
			}
		})
	}
}

func TestFullSnapshotValidatorAllowsHTTPVirtualHostsToShareIngress(t *testing.T) {
	t.Parallel()
	snapshot := validSnapshotForValidation()
	second := snapshot.Rules[0]
	second.ID = 2
	second.FrontendURL = "https://second.example.com"
	snapshot.Rules = append(snapshot.Rules, second)

	err := (FullSnapshotValidator{}).Validate(t.Context(), revision.SnapshotValidation{
		Target: revision.Target{
			AgentID: "edge-1", Capabilities: []string{"wireguard", "egress_profiles"},
		},
		Snapshot: snapshot,
	})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestFullSnapshotValidatorAllowsProxyEntryWithoutBackend(t *testing.T) {
	t.Parallel()
	snapshot := storage.Snapshot{L4Rules: []storage.L4Rule{{
		ID: 1, AgentID: "edge-1", Protocol: "tcp", ListenMode: "proxy",
		ListenHost: "127.0.0.1", ListenPort: 1080,
	}}}

	err := (FullSnapshotValidator{}).Validate(t.Context(), revision.SnapshotValidation{
		Target:   revision.Target{AgentID: "edge-1"},
		Snapshot: snapshot,
	})
	if err != nil {
		t.Fatalf("Validate(proxy entry) error = %v", err)
	}
}

func validSnapshotForValidation() storage.Snapshot {
	profileID := 7
	egressID := 8
	certificateID := 9
	return storage.Snapshot{
		Rules: []storage.HTTPRule{{
			ID: 1, AgentID: "edge-1", FrontendURL: "https://edge.example.com",
			Backends:    []storage.HTTPBackend{{URL: "http://127.0.0.1:8080"}},
			RelayLayers: [][]int{{5}}, WireGuardProfileID: &profileID, EgressProfileID: &egressID,
		}},
		L4Rules: []storage.L4Rule{{
			ID: 1, AgentID: "edge-1", Protocol: "tcp", ListenHost: "127.0.0.1", ListenPort: 9000,
			Backends: []storage.L4Backend{{Host: "127.0.0.1", Port: 9001}}, RelayLayers: [][]int{{5}},
		}},
		RelayListeners: []storage.RelayListener{{
			ID: 5, AgentID: "edge-1", Name: "relay", BindHosts: []string{"127.0.0.1"},
			ListenPort: 9443, Enabled: true, CertificateID: &certificateID, TransportMode: "tls_tcp",
		}},
		WireGuardProfiles: []storage.WireGuardProfile{{
			ID: profileID, AgentID: "edge-1", Name: "wg", ListenPort: 51820, Enabled: true,
		}},
		EgressProfiles: []storage.EgressProfile{{
			ID: egressID, Name: "proxy", Type: "http", ProxyURL: "http://127.0.0.1:3128", Enabled: true,
		}},
		Certificates:        []storage.ManagedCertificateBundle{{ID: certificateID, Domain: "edge.example.com", CertPEM: "cert", KeyPEM: "key"}},
		CertificatePolicies: []storage.ManagedCertificatePolicy{{ID: certificateID, Domain: "edge.example.com", Enabled: true}},
	}
}
