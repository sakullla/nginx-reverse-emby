package revision

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestSemanticSnapshotDigestIgnoresOrderingAndRevisionMetadata(t *testing.T) {
	t.Parallel()
	first := storage.Snapshot{
		Revision: 12,
		Rules: []storage.HTTPRule{
			{ID: 2, AgentID: "edge-1", FrontendURL: "https://b.example.com", Revision: 12},
			{ID: 1, AgentID: "edge-1", FrontendURL: "https://a.example.com", Revision: 11},
		},
		RelayListeners: []storage.RelayListener{
			{ID: 2, AgentID: "edge-1", BindHosts: []string{"127.0.0.2", "127.0.0.1"}, Tags: []string{"z", "a"}, Revision: 12},
		},
	}
	second := storage.Snapshot{
		Revision: 99,
		Rules: []storage.HTTPRule{
			{ID: 1, AgentID: "edge-1", FrontendURL: "https://a.example.com", Revision: 98},
			{ID: 2, AgentID: "edge-1", FrontendURL: "https://b.example.com", Revision: 99},
		},
		RelayListeners: []storage.RelayListener{
			{ID: 2, AgentID: "edge-1", BindHosts: []string{"127.0.0.1", "127.0.0.2"}, Tags: []string{"a", "z"}, Revision: 99},
		},
	}

	firstDigest, err := SemanticSnapshotDigest(first)
	if err != nil {
		t.Fatalf("SemanticSnapshotDigest(first) error = %v", err)
	}
	secondDigest, err := SemanticSnapshotDigest(second)
	if err != nil {
		t.Fatalf("SemanticSnapshotDigest(second) error = %v", err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("semantic digests differ: %s != %s", firstDigest, secondDigest)
	}

	payload, payloadDigest, err := CanonicalSnapshotPayload(first)
	if err != nil {
		t.Fatalf("CanonicalSnapshotPayload() error = %v", err)
	}
	if len(payload) == 0 || payloadDigest == "" {
		t.Fatalf("canonical payload len=%d digest=%q", len(payload), payloadDigest)
	}
	if payloadDigest == firstDigest {
		t.Fatal("artifact digest unexpectedly ignores revision metadata")
	}
}

func TestCanonicalSnapshotNormalizesPluginGenerations(t *testing.T) {
	t.Parallel()
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	generation := func(instance string, revision int64, extensions []string, config string, grants []storage.PluginGenerationGrant) storage.PluginGeneration {
		return storage.PluginGeneration{
			ID: digest, InstanceID: instance, OperationID: "operation", Revision: revision,
			PluginID: "runtime.rpc", PluginVersion: "1.0.0", PackageDigest: digest,
			Runtime:         storage.PluginGenerationRuntime{Kind: "rpc-service", ABI: "nre:rpc/v1", HostScope: "agent", Entry: "plugin"},
			Artifact:        storage.PluginGenerationArtifact{ArtifactID: digest, PackageIdentity: digest, RelativePath: "plugin", SHA256: digest, SizeBytes: 1, Mode: "executable", GOOS: "linux", GOARCH: "amd64", SignatureVerified: true, SignerKeyID: "release", SignerFingerprint: digest},
			ExtensionPoints: extensions, ConfigVersion: 2, Config: json.RawMessage(config), Grants: grants,
			SecretHandles: []storage.PluginGenerationSecretHandle{}, ResourceBudget: storage.PluginGenerationResourceBudget{TimeoutMS: 10},
			Target:        storage.PluginGenerationTarget{Kind: "agent", ID: "edge", ResourceGroupID: "group", Version: 2},
			FailurePolicy: storage.PluginGenerationFailurePolicy{OnError: "preserve-old"},
		}
	}
	first := storage.Snapshot{Revision: 4, PluginGenerations: []storage.PluginGeneration{
		generation("z", 4, []string{"relay.write", "relay.read", "relay.read"}, `{"b":2,"a":1}`, []storage.PluginGenerationGrant{{Name: "write"}, {Name: "read"}}),
		generation("a", 4, nil, `{}`, nil),
	}, PluginDependencies: []storage.PluginDependencyEdge{
		{Consumer: storage.PluginDependencyConsumer{Kind: "l4_rule", ID: "2"}, ProviderInstanceID: "z", Target: storage.PluginDependencyTarget{AgentID: "edge", ResourceGroupID: "group", Version: 2}},
		{Consumer: storage.PluginDependencyConsumer{Kind: "http_rule", ID: "1"}, ProviderInstanceID: "a", Target: storage.PluginDependencyTarget{AgentID: "edge", ResourceGroupID: "group", Version: 2}},
	}}
	second := storage.Snapshot{Revision: 99, PluginGenerations: []storage.PluginGeneration{
		generation("a", 99, []string{}, `{ }`, []storage.PluginGenerationGrant{}),
		generation("z", 99, []string{"relay.read", "relay.write"}, `{"a":1,"b":2}`, []storage.PluginGenerationGrant{{Name: "read"}, {Name: "write"}}),
	}, PluginDependencies: []storage.PluginDependencyEdge{
		{Consumer: storage.PluginDependencyConsumer{Kind: "http_rule", ID: "1"}, ProviderInstanceID: "a", Target: storage.PluginDependencyTarget{AgentID: "edge", ResourceGroupID: "group", Version: 2}},
		{Consumer: storage.PluginDependencyConsumer{Kind: "l4_rule", ID: "2"}, ProviderInstanceID: "z", Target: storage.PluginDependencyTarget{AgentID: "edge", ResourceGroupID: "group", Version: 2}},
	}}
	left, err := SemanticSnapshotDigest(first)
	if err != nil {
		t.Fatal(err)
	}
	right, err := SemanticSnapshotDigest(second)
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("equivalent plugin generation digests differ: %s != %s", left, right)
	}
	payload, _, err := CanonicalSnapshotPayload(first)
	if err != nil {
		t.Fatal(err)
	}
	var delivered storage.Snapshot
	if err := json.Unmarshal(payload, &delivered); err != nil {
		t.Fatal(err)
	}
	if delivered.PluginGenerations[0].InstanceID != "a" || !reflect.DeepEqual(delivered.PluginGenerations[1].ExtensionPoints, []string{"relay.read", "relay.write"}) {
		t.Fatalf("canonical plugin generations = %+v", delivered.PluginGenerations)
	}
	if delivered.PluginDependencies == nil || len(delivered.PluginDependencies) != 2 || delivered.PluginDependencies[0].Consumer.Kind != "http_rule" {
		t.Fatalf("canonical plugin dependencies = %+v", delivered.PluginDependencies)
	}
}

func TestCanonicalSnapshotPreservesConfiguredBackendOrder(t *testing.T) {
	t.Parallel()
	snapshot := storage.Snapshot{
		Revision: 7,
		Rules: []storage.HTTPRule{{
			ID: 1, Backends: []storage.HTTPBackend{{URL: "https://second.example"}, {URL: "https://first.example"}},
		}},
		L4Rules: []storage.L4Rule{{
			ID: 2, Backends: []storage.L4Backend{{Host: "second.example", Port: 9002}, {Host: "first.example", Port: 9001}},
		}},
	}
	payload, _, err := CanonicalSnapshotPayload(snapshot)
	if err != nil {
		t.Fatalf("CanonicalSnapshotPayload() error = %v", err)
	}
	var delivered storage.Snapshot
	if err := json.Unmarshal(payload, &delivered); err != nil {
		t.Fatalf("decode canonical payload: %v", err)
	}
	if !reflect.DeepEqual(delivered.Rules[0].Backends, snapshot.Rules[0].Backends) {
		t.Fatalf("HTTP backend order = %+v, want %+v", delivered.Rules[0].Backends, snapshot.Rules[0].Backends)
	}
	if !reflect.DeepEqual(delivered.L4Rules[0].Backends, snapshot.L4Rules[0].Backends) {
		t.Fatalf("L4 backend order = %+v, want %+v", delivered.L4Rules[0].Backends, snapshot.L4Rules[0].Backends)
	}

	reordered := snapshot
	reordered.Rules = append([]storage.HTTPRule(nil), snapshot.Rules...)
	reordered.Rules[0].Backends = []storage.HTTPBackend{{URL: "https://first.example"}, {URL: "https://second.example"}}
	reordered.L4Rules = append([]storage.L4Rule(nil), snapshot.L4Rules...)
	reordered.L4Rules[0].Backends = []storage.L4Backend{{Host: "first.example", Port: 9001}, {Host: "second.example", Port: 9002}}
	firstDigest, err := SemanticSnapshotDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := SemanticSnapshotDigest(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == secondDigest {
		t.Fatal("semantic digest ignored configured backend order")
	}
}

func TestCanonicalSnapshotNormalizesPluginPoliciesWithoutReorderingStages(t *testing.T) {
	t.Parallel()
	stage := func(_ string, kind string, extensionPoints, scopes []string, config string) storage.PolicyStage {
		return storage.PolicyStage{
			Kind: kind, PolicyID: kind, InstanceID: kind,
			ExtensionPoints: extensionPoints, GrantedScopes: scopes,
			Config: json.RawMessage(config),
		}
	}
	first := storage.Snapshot{PluginPolicies: []storage.PluginPolicy{
		{ID: "z-policy", Revision: 9, Stages: []storage.PolicyStage{stage("z-policy", "ip", []string{"http.request", "l4.accept", "http.request"}, []string{"state.write", "state.read"}, `{"limit":2,"enabled":true}`)}},
		{ID: "a-policy", Revision: 8, Stages: []storage.PolicyStage{
			stage("a-policy", "rate", []string{"http.request"}, nil, `{}`),
			stage("a-policy", "waf", []string{"http.request"}, []string{"request.read"}, `{"mode":"block"}`),
		}},
	}}
	second := storage.Snapshot{PluginPolicies: []storage.PluginPolicy{
		{ID: "a-policy", Revision: 100, Stages: []storage.PolicyStage{
			stage("a-policy", "rate", []string{"http.request"}, []string{}, `{}`),
			stage("a-policy", "waf", []string{"http.request"}, []string{"request.read"}, `{ "mode" : "block" }`),
		}},
		{ID: "z-policy", Revision: 101, Stages: []storage.PolicyStage{stage("z-policy", "ip", []string{"l4.accept", "http.request"}, []string{"state.read", "state.write"}, `{"enabled":true,"limit":2}`)}},
	}}
	firstDigest, err := SemanticSnapshotDigest(first)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := SemanticSnapshotDigest(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("equivalent plugin policy digests differ: %s != %s", firstDigest, secondDigest)
	}

	payload, _, err := CanonicalSnapshotPayload(storage.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	var delivered storage.Snapshot
	if err := json.Unmarshal(payload, &delivered); err != nil {
		t.Fatal(err)
	}
	if delivered.PluginPolicies == nil {
		t.Fatal("nil plugin policy catalog was not canonicalized to an explicit empty list")
	}

	reordered := first
	reordered.PluginPolicies = append([]storage.PluginPolicy(nil), first.PluginPolicies...)
	reordered.PluginPolicies[1].Stages = []storage.PolicyStage{first.PluginPolicies[1].Stages[1], first.PluginPolicies[1].Stages[0]}
	reorderedDigest, err := SemanticSnapshotDigest(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == reorderedDigest {
		t.Fatal("canonicalization ignored semantic policy stage order")
	}
}

func TestCanonicalSnapshotAllowsSharedStageAuthorityAcrossContainingChains(t *testing.T) {
	t.Parallel()
	shared := storage.PolicyStage{
		Kind: "ip", PolicyID: "shared-instance", InstanceID: "shared-instance",
		ExtensionPoints: []string{"l4.accept", "http.request"}, GrantedScopes: []string{"state.write", "state.read"},
		Config: json.RawMessage(`{"enabled":true}`),
	}
	first := storage.Snapshot{PluginPolicies: []storage.PluginPolicy{
		{ID: "chain-full", Stages: []storage.PolicyStage{shared}},
		{ID: "chain-lite", Stages: []storage.PolicyStage{shared}},
	}}
	second := storage.Snapshot{PluginPolicies: []storage.PluginPolicy{
		{ID: "chain-lite", Stages: []storage.PolicyStage{shared}},
		{ID: "chain-full", Stages: []storage.PolicyStage{shared}},
	}}
	firstDigest, err := SemanticSnapshotDigest(first)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := SemanticSnapshotDigest(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("shared stage catalog digests differ: %s != %s", firstDigest, secondDigest)
	}
	invalid := first
	invalid.PluginPolicies = append([]storage.PluginPolicy(nil), first.PluginPolicies...)
	invalid.PluginPolicies[0].Stages = append([]storage.PolicyStage(nil), first.PluginPolicies[0].Stages...)
	invalid.PluginPolicies[0].Stages[0].PolicyID = "different-authority"
	if _, err := SemanticSnapshotDigest(invalid); err == nil {
		t.Fatal("stage authority differing from InstanceID was accepted")
	}
}

func TestRequestFingerprintIsStableForEquivalentMaps(t *testing.T) {
	t.Parallel()
	first, err := RequestFingerprint(map[string]any{"enabled": true, "name": "edge", "ports": []int{443, 80}})
	if err != nil {
		t.Fatalf("RequestFingerprint(first) error = %v", err)
	}
	second, err := RequestFingerprint(map[string]any{"ports": []int{443, 80}, "name": "edge", "enabled": true})
	if err != nil {
		t.Fatalf("RequestFingerprint(second) error = %v", err)
	}
	if first != second {
		t.Fatalf("fingerprints differ: %s != %s", first, second)
	}
}

func TestSnapshotPackageRespectsTargetEligibility(t *testing.T) {
	packageInfo := &storage.VersionPackage{
		URL: "/downloads/nre-agent", SHA256: "digest", Platform: "linux-amd64",
	}
	tests := []struct {
		name        string
		target      Target
		wantPackage bool
	}{
		{
			name:        "eligible Linux agent",
			target:      Target{Platform: "linux-amd64", Capabilities: []string{"package_manifest_v1"}},
			wantPackage: true,
		},
		{
			name:   "unsupported platform",
			target: Target{Platform: "darwin-arm64", Capabilities: []string{"package_manifest_v1"}},
		},
		{
			name:   "missing manifest capability",
			target: Target{Platform: "linux-arm64", Capabilities: []string{"http_rules"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := snapshotForTargetPackageEligibility(storage.Snapshot{VersionPackage: packageInfo}, test.target)
			if got := snapshot.VersionPackage != nil; got != test.wantPackage {
				t.Fatalf("package retained = %v, want %v", got, test.wantPackage)
			}
		})
	}
}
