package service

import (
	"encoding/json"
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
		Target:   revision.Target{AgentID: "edge-1", Capabilities: []string{"egress_profiles"}},
		Snapshot: snapshot,
	}); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestFullSnapshotValidatorPluginGenerationCapabilityFailsClosed(t *testing.T) {
	digest := strings.Repeat("a", 64)
	generation := storage.PluginGeneration{
		InstanceID: "instance", OperationID: "operation", Revision: 7,
		PluginID: "runtime.rpc", PluginVersion: "1.0.0", PackageDigest: digest,
		Runtime:       storage.PluginGenerationRuntime{Kind: "rpc-service", ABI: "nre:rpc/v1", HostScope: "agent", Entry: "plugin"},
		Artifact:      storage.PluginGenerationArtifact{ArtifactID: digest, PackageIdentity: digest, RelativePath: "plugin", SHA256: digest, SizeBytes: 1, Mode: "executable", GOOS: "linux", GOARCH: "amd64", SignatureVerified: true, SignerKeyID: "release", SignerFingerprint: digest},
		ConfigVersion: 1, Config: json.RawMessage(`{}`), Grants: []storage.PluginGenerationGrant{}, SecretHandles: []storage.PluginGenerationSecretHandle{},
		ResourceBudget: storage.PluginGenerationResourceBudget{TimeoutMS: 10, MemoryBytes: 1024, Concurrency: 1, InputBytes: 128, OutputBytes: 128},
		Target:         storage.PluginGenerationTarget{Kind: "agent", ID: "edge-1", ResourceGroupID: "group", Version: 1},
		FailurePolicy:  storage.PluginGenerationFailurePolicy{OnError: "preserve-old", OnBudget: "preserve-old", Restart: "bounded", CoreFallback: "continue"},
	}
	generation.ID, _ = storage.PluginGenerationIdentity(generation)
	snapshot := storage.Snapshot{Revision: 7, PluginGenerations: []storage.PluginGeneration{generation}}
	validator := FullSnapshotValidator{}
	err := validator.Validate(t.Context(), revision.SnapshotValidation{Target: revision.Target{AgentID: "edge-1"}, Snapshot: snapshot})
	if revision.ErrorCodeOf(err) != revision.ErrorCodeUnprocessable || !strings.Contains(err.Error(), storage.PluginGenerationCapability) {
		t.Fatalf("missing capability error = %v", err)
	}
	if err := validator.Validate(t.Context(), revision.SnapshotValidation{Target: revision.Target{AgentID: "edge-1", Capabilities: []string{storage.PluginGenerationCapability}}, Snapshot: snapshot}); err != nil {
		t.Fatalf("capable Agent rejected generation: %v", err)
	}
}

func TestFullSnapshotValidatorRejectsInvalidPluginDependencyGraph(t *testing.T) {
	digest := strings.Repeat("b", 64)
	generation := storage.PluginGeneration{
		InstanceID: "provider", OperationID: "operation", Revision: 7, PluginID: "runtime.rpc", PluginVersion: "1.0.0", PackageDigest: digest,
		Runtime:         storage.PluginGenerationRuntime{Kind: "rpc-service", ABI: "nre:rpc/v1", HostScope: "agent", Entry: "plugin"},
		Artifact:        storage.PluginGenerationArtifact{ArtifactID: digest, PackageIdentity: digest, RelativePath: "plugin", SHA256: digest, SizeBytes: 1, Mode: "executable", GOOS: "linux", GOARCH: "amd64", SignatureVerified: true, SignerKeyID: "release", SignerFingerprint: digest},
		ExtensionPoints: []string{"http.request"}, ConfigVersion: 1, Config: json.RawMessage(`{}`), Grants: []storage.PluginGenerationGrant{}, SecretHandles: []storage.PluginGenerationSecretHandle{},
		ResourceBudget: storage.PluginGenerationResourceBudget{TimeoutMS: 10, MemoryBytes: 1024, Concurrency: 1, InputBytes: 128, OutputBytes: 128},
		Target:         storage.PluginGenerationTarget{Kind: "agent", ID: "edge-1", ResourceGroupID: "group", Version: 1},
		FailurePolicy:  storage.PluginGenerationFailurePolicy{OnError: "preserve-old", OnBudget: "preserve-old", Restart: "bounded", CoreFallback: "continue"},
	}
	generation.ID, _ = storage.PluginGenerationIdentity(generation)
	base := storage.Snapshot{
		Revision:           7,
		Rules:              []storage.HTTPRule{{ID: 1, AgentID: "edge-1", FrontendURL: "https://edge.example.test", Backends: []storage.HTTPBackend{{URL: "http://127.0.0.1:8096"}}}},
		PluginGenerations:  []storage.PluginGeneration{generation},
		PluginDependencies: []storage.PluginDependencyEdge{{Consumer: storage.PluginDependencyConsumer{Kind: "http_rule", ID: "1", ResourceGroupID: "group", Version: digest}, ProviderInstanceID: "provider", Target: storage.PluginDependencyTarget{AgentID: "edge-1", ResourceGroupID: "group", Version: 1}}},
	}
	validator := FullSnapshotValidator{}
	target := revision.Target{AgentID: "edge-1", Capabilities: []string{storage.PluginGenerationCapability}}
	if err := validator.Validate(t.Context(), revision.SnapshotValidation{Target: target, Snapshot: base}); err != nil {
		t.Fatalf("valid dependency graph rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*storage.Snapshot)
	}{
		{"dangling consumer", func(snapshot *storage.Snapshot) { snapshot.PluginDependencies[0].Consumer.ID = "2" }},
		{"dangling provider", func(snapshot *storage.Snapshot) { snapshot.PluginDependencies[0].ProviderInstanceID = "missing" }},
		{"duplicate", func(snapshot *storage.Snapshot) {
			snapshot.PluginDependencies = append(snapshot.PluginDependencies, snapshot.PluginDependencies[0])
		}},
		{"cross target", func(snapshot *storage.Snapshot) { snapshot.PluginDependencies[0].Target.AgentID = "edge-2" }},
		{"mismatched version", func(snapshot *storage.Snapshot) { snapshot.PluginDependencies[0].Target.Version = 2 }},
		{"consumer cross group", func(snapshot *storage.Snapshot) { snapshot.PluginDependencies[0].Consumer.ResourceGroupID = "other" }},
		{"consumer ownership version", func(snapshot *storage.Snapshot) {
			snapshot.PluginDependencies[0].Consumer.Version = strings.Repeat("A", 64)
		}},
		{"unsupported consumer", func(snapshot *storage.Snapshot) { snapshot.PluginDependencies[0].Consumer.Kind = "service" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			candidate.PluginDependencies = append([]storage.PluginDependencyEdge(nil), base.PluginDependencies...)
			test.mutate(&candidate)
			if err := validator.Validate(t.Context(), revision.SnapshotValidation{Target: target, Snapshot: candidate}); err == nil {
				t.Fatal("invalid plugin dependency graph was accepted")
			}
		})
	}
}

func TestFullSnapshotValidatorRejectsResourcesOwnedByAnotherAgent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*storage.Snapshot)
	}{
		{
			name: "HTTP rule",
			mutate: func(snapshot *storage.Snapshot) {
				snapshot.Rules[0].AgentID = "edge-2"
			},
		},
		{
			name: "L4 rule",
			mutate: func(snapshot *storage.Snapshot) {
				snapshot.L4Rules[0].AgentID = "edge-2"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := validSnapshotForValidation()
			tt.mutate(&snapshot)
			err := (FullSnapshotValidator{}).Validate(t.Context(), revision.SnapshotValidation{
				Target: revision.Target{
					AgentID: "edge-1", Capabilities: []string{"egress_profiles"},
				},
				Snapshot: snapshot,
			})
			if revision.ErrorCodeOf(err) != revision.ErrorCodeUnprocessable {
				t.Fatalf("Validate() error = %v, code = %q, want %q", err, revision.ErrorCodeOf(err), revision.ErrorCodeUnprocessable)
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
			capabilities: []string{"egress_profiles"},
			wantCode:     revision.ErrorCodeNotFound,
		},
		{
			name: "duplicate frontend",
			mutate: func(snapshot *storage.Snapshot) {
				duplicate := snapshot.Rules[0]
				duplicate.ID = 2
				snapshot.Rules = append(snapshot.Rules, duplicate)
			},
			capabilities: []string{"egress_profiles"},
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
			capabilities: []string{"egress_profiles"},
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
					AgentID: "edge-1", Capabilities: []string{"egress_profiles"},
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
			AgentID: "edge-1", Capabilities: []string{"egress_profiles"},
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
	egressID := 8
	certificateID := 9
	return storage.Snapshot{
		Rules: []storage.HTTPRule{{
			ID: 1, AgentID: "edge-1", FrontendURL: "https://edge.example.com",
			Backends:        []storage.HTTPBackend{{URL: "http://127.0.0.1:8080"}},
			RelayLayers:     [][]int{{5}},
			EgressProfileID: &egressID,
		}},
		L4Rules: []storage.L4Rule{{
			ID: 1, AgentID: "edge-1", Protocol: "tcp", ListenHost: "127.0.0.1", ListenPort: 9000,
			Backends: []storage.L4Backend{{Host: "127.0.0.1", Port: 9001}}, RelayLayers: [][]int{{5}},
		}},
		RelayListeners: []storage.RelayListener{{
			ID: 5, AgentID: "edge-1", Name: "relay", BindHosts: []string{"127.0.0.1"},
			ListenPort: 9443, Enabled: true, CertificateID: &certificateID, TransportMode: "tls_tcp",
		}},
		EgressProfiles: []storage.EgressProfile{{
			ID: egressID, Name: "proxy", Type: "http", ProxyURL: "http://127.0.0.1:3128", Enabled: true,
		}},
		Certificates:        []storage.ManagedCertificateBundle{{ID: certificateID, Domain: "edge.example.com", CertPEM: "cert", KeyPEM: "key"}},
		CertificatePolicies: []storage.ManagedCertificatePolicy{{ID: certificateID, Domain: "edge.example.com", Enabled: true}},
	}
}
