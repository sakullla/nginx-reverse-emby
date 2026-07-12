package service

import (
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestFullSnapshotValidatorAcceptsCompleteResourceGraph(t *testing.T) {
	validator := FullSnapshotValidator{}
	snapshot := validSnapshotForValidation()
	if err := validator.Validate(t.Context(), revision.SnapshotValidation{
		Target:   revision.Target{AgentID: "edge-1", Capabilities: []string{"wireguard", "egress_profiles"}},
		Snapshot: snapshot,
	}); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestFullSnapshotValidatorClassifiesReferenceCapabilityAndConflictErrors(t *testing.T) {
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
		EgressProfiles:      []storage.EgressProfile{{ID: egressID, Name: "proxy", Type: "http", Enabled: true}},
		Certificates:        []storage.ManagedCertificateBundle{{ID: certificateID, Domain: "edge.example.com", CertPEM: "cert", KeyPEM: "key"}},
		CertificatePolicies: []storage.ManagedCertificatePolicy{{ID: certificateID, Domain: "edge.example.com", Enabled: true}},
	}
}
