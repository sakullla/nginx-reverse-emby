package model

import (
	"encoding/json"
	"runtime"
	"slices"
	"strings"
	"testing"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestPluginGenerationStrictValidationAndWirePresence(t *testing.T) {
	generation := validPluginGenerationForTest()
	snapshot := Snapshot{Revision: 7, PluginGenerations: []PluginGeneration{generation}}
	if err := ValidatePluginGenerations(snapshot, false); err != nil {
		t.Fatalf("ValidatePluginGenerations() error = %v", err)
	}
	wire, err := json.Marshal(Snapshot{PluginGenerations: []PluginGeneration{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"plugin_generations":[]`) {
		t.Fatalf("empty PluginGenerations wire = %s", wire)
	}

	for name, mutate := range map[string]func(*PluginGeneration){
		"operation": func(value *PluginGeneration) { value.OperationID = "" },
		"revision":  func(value *PluginGeneration) { value.Revision++ },
		"digest":    func(value *PluginGeneration) { value.PackageDigest = strings.Repeat("A", 64) },
		"path":      func(value *PluginGeneration) { value.Artifact.RelativePath = "../plugin" },
		"signature": func(value *PluginGeneration) { value.Artifact.SignatureVerified = false },
		"runtime":   func(value *PluginGeneration) { value.Runtime.Kind = PluginRuntimeWASMPolicy },
		"local":     func(value *PluginGeneration) { value.Artifact.LocalPath = "already-materialized" },
		"target":    func(value *PluginGeneration) { value.Target.Version++ },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := generation
			mutate(&invalid)
			if err := invalid.Validate(7, false); err == nil {
				t.Fatal("Validate() accepted invalid projection")
			}
		})
	}
}

func TestPluginGenerationMaterializedValidationRequiresLocalPath(t *testing.T) {
	generation := validPluginGenerationForTest()
	if err := generation.Validate(7, true); err == nil {
		t.Fatal("Validate(materialized) accepted empty local path")
	}
	generation.Artifact.LocalPath = "/agent/cache/plugin"
	if err := generation.Validate(7, true); err != nil {
		t.Fatalf("Validate(materialized) error = %v", err)
	}
}

func TestPluginGenerationAcceptsCanonicalHostCapabilitiesAndResourceOnlySelectors(t *testing.T) {
	generation := validPluginGenerationForTest()
	generation.Grants = []PluginGrantProjection{
		{Name: "policy.atomic-state", ResourceID: "tenant-a"},
		{Name: "policy.monotonic-clock"},
		{Name: "policy.trusted-source"},
		{Name: "service.revocable-resource-handle"},
		{Name: "ui.dynamic-actions"},
	}
	if err := generation.Validate(7, false); err != nil {
		t.Fatalf("Validate() rejected canonical capabilities: %v", err)
	}
	for _, legacy := range []string{"policy.atomic_state", "policy.monotonic_clock", "policy.trusted_source", "service.revocable_resource_handle", "ui.dynamic_actions"} {
		generation.Grants = []PluginGrantProjection{{Name: legacy}}
		if err := generation.Validate(7, false); err == nil {
			t.Fatalf("Validate() accepted noncanonical capability %q", legacy)
		}
	}
}

func TestPluginDependenciesValidateRealCoreConsumersAndDeriveRequiredProviders(t *testing.T) {
	httpProvider := validPluginGenerationForTest()
	httpProvider.InstanceID = "instance-b"
	l4Provider := validPluginGenerationForTest()
	l4Provider.ID, l4Provider.InstanceID, l4Provider.OperationID = "generation-l4", "instance-a", "operation-l4"
	l4Provider.ExtensionPoints = []string{"l4.accept"}
	l4Provider.RequiredFeatures = nil
	l4Provider.HTTPBackendProviders = nil
	snapshot := Snapshot{Revision: 7,
		Rules:             []HTTPRule{httpProviderRuleForTest(11, "edge-7", httpProvider)},
		L4Rules:           []L4Rule{{ID: 12, AgentID: "edge-7", Enabled: true}},
		PluginGenerations: []PluginGeneration{httpProvider, l4Provider},
		PluginDependencies: []PluginDependencyEdge{
			{Consumer: pluginDependencyConsumerForTest("http_rule", "11", httpProvider.Target.ResourceGroupID), ProviderInstanceID: httpProvider.InstanceID, Target: pluginDependencyTargetForTest(httpProvider)},
			{Consumer: pluginDependencyConsumerForTest("l4_rule", "12", l4Provider.Target.ResourceGroupID), ProviderInstanceID: l4Provider.InstanceID, Target: pluginDependencyTargetForTest(l4Provider)},
		},
	}
	if err := ValidatePluginGenerations(snapshot, false); err != nil {
		t.Fatalf("ValidatePluginGenerations() error = %v", err)
	}
	got, err := RequiredPluginInstanceIDs(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"instance-a", "instance-b"}) {
		t.Fatalf("RequiredPluginInstanceIDs() = %v", got)
	}
}

func TestPluginDependencyProductionWireRoundTripPreservesConsumerAuthority(t *testing.T) {
	provider := validPluginGenerationForTest()
	edge := PluginDependencyEdge{
		Consumer:           pluginDependencyConsumerForTest("http_rule", "11", provider.Target.ResourceGroupID),
		ProviderInstanceID: provider.InstanceID,
		Target:             pluginDependencyTargetForTest(provider),
	}
	snapshot := Snapshot{
		Revision:           7,
		Rules:              []HTTPRule{httpProviderRuleForTest(11, provider.Target.ID, provider)},
		PluginGenerations:  []PluginGeneration{provider},
		PluginDependencies: []PluginDependencyEdge{edge},
	}
	wire, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	expected := `"consumer":{"kind":"http_rule","id":"11","resource_group_id":"default","version":"` + strings.Repeat("e", 64) + `"}`
	if !strings.Contains(string(wire), expected) {
		t.Fatalf("production dependency wire = %s", wire)
	}
	var decoded Snapshot
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.PluginDependencies) != 1 || decoded.PluginDependencies[0].Consumer != edge.Consumer {
		t.Fatalf("round-tripped consumer authority = %+v", decoded.PluginDependencies)
	}
	if err := ValidatePluginGenerations(decoded, false); err != nil {
		t.Fatalf("round-tripped dependency validation error = %v", err)
	}
}

func TestPluginRuntimeLogReportStrictWireAndBounds(t *testing.T) {
	report := PluginRuntimeLogReport{
		Revision: 7, GenerationID: "generation-7", InstanceID: "instance-7", PluginID: "example.rpc", AgentID: "edge-7",
		PackageDigest: strings.Repeat("a", 64), ArtifactDigest: strings.Repeat("b", 64), Sequence: 3,
		Entries: []PluginRuntimeLogEntry{{Level: "error", Message: "safe message", Truncated: true}},
	}
	if err := report.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	wire, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"generation_id":"generation-7"`, `"artifact_digest":"` + strings.Repeat("b", 64) + `"`, `"sequence":3`, `"truncated":true`} {
		if !strings.Contains(string(wire), fragment) {
			t.Fatalf("plugin log wire %s lacks %s", wire, fragment)
		}
	}
	entryWire, err := json.Marshal(PluginRuntimeLogEntry{Level: "info", Message: "safe"})
	if err != nil || !strings.Contains(string(entryWire), `"truncated":false`) {
		t.Fatalf("non-truncated entry wire = %s, error = %v", entryWire, err)
	}

	for name, mutate := range map[string]func(*PluginRuntimeLogReport){
		"missing generation": func(value *PluginRuntimeLogReport) { value.GenerationID = "" },
		"missing agent":      func(value *PluginRuntimeLogReport) { value.AgentID = "" },
		"bad digest":         func(value *PluginRuntimeLogReport) { value.ArtifactDigest = strings.Repeat("B", 64) },
		"zero sequence":      func(value *PluginRuntimeLogReport) { value.Sequence = 0 },
		"bad level":          func(value *PluginRuntimeLogReport) { value.Entries[0].Level = "fatal" },
		"oversized message": func(value *PluginRuntimeLogReport) {
			value.Entries[0].Message = strings.Repeat("x", MaxPluginRuntimeLogMessage+1)
		},
		"empty entries": func(value *PluginRuntimeLogReport) { value.Entries = nil },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := report
			invalid.Entries = append([]PluginRuntimeLogEntry(nil), report.Entries...)
			mutate(&invalid)
			if err := invalid.Validate(); err == nil {
				t.Fatal("Validate() accepted invalid plugin runtime log report")
			}
		})
	}
}

func TestPluginSecretRedemptionRequestValidatesExactGenerationFence(t *testing.T) {
	request := PluginSecretRedemptionRequest{
		Revision: 7, GenerationID: "generation-a", InstanceID: "instance-a", PluginID: "example.rpc", OperationID: "operation-a",
		PackageDigest: strings.Repeat("a", 64), ArtifactDigest: strings.Repeat("b", 64),
		Handles: []PluginSecretHandle{{ID: "secret-a", Version: 2, Digest: strings.Repeat("c", 64), Purpose: "plugin-config:instance-a:/nested/token"}},
	}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"revision":7`, `"generation_id":"generation-a"`, `"operation_id":"operation-a"`, `"handles":[`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("request JSON %s does not contain %s", encoded, field)
		}
	}
	invalid := request
	invalid.Handles = append([]PluginSecretHandle(nil), request.Handles...)
	invalid.Handles[0].Purpose = "plugin-config:other-instance:/nested/token"
	if err := invalid.Validate(); err == nil {
		t.Fatal("cross-instance plugin secret purpose was accepted")
	}
	invalid = request
	invalid.Handles = append([]PluginSecretHandle(nil), request.Handles...)
	invalid.Handles[0].Purpose = "plugin-config:instance-a:/bad~2pointer"
	if err := invalid.Validate(); err == nil {
		t.Fatal("non-canonical plugin secret pointer was accepted")
	}
}

func TestPluginDependenciesRejectDanglingDuplicateCrossTargetAndInvalidConsumers(t *testing.T) {
	provider := validPluginGenerationForTest()
	base := Snapshot{Revision: 7, Rules: []HTTPRule{httpProviderRuleForTest(11, "edge-7", provider)}, PluginGenerations: []PluginGeneration{provider}}
	valid := PluginDependencyEdge{Consumer: pluginDependencyConsumerForTest("http_rule", "11", provider.Target.ResourceGroupID), ProviderInstanceID: provider.InstanceID, Target: pluginDependencyTargetForTest(provider)}
	for name, mutate := range map[string]func(*Snapshot, *PluginDependencyEdge){
		"dangling provider":          func(_ *Snapshot, edge *PluginDependencyEdge) { edge.ProviderInstanceID = "missing" },
		"cross target":               func(_ *Snapshot, edge *PluginDependencyEdge) { edge.Target.AgentID = "edge-8" },
		"stale target":               func(_ *Snapshot, edge *PluginDependencyEdge) { edge.Target.Version++ },
		"cross resource group":       func(_ *Snapshot, edge *PluginDependencyEdge) { edge.Consumer.ResourceGroupID = "other" },
		"missing consumer group":     func(_ *Snapshot, edge *PluginDependencyEdge) { edge.Consumer.ResourceGroupID = "" },
		"missing consumer authority": func(_ *Snapshot, edge *PluginDependencyEdge) { edge.Consumer.Version = "" },
		"stale consumer authority":   func(_ *Snapshot, edge *PluginDependencyEdge) { edge.Consumer.Version = strings.Repeat("f", 63) },
		"noncanonical authority":     func(_ *Snapshot, edge *PluginDependencyEdge) { edge.Consumer.Version = strings.Repeat("F", 64) },
		"disabled consumer":          func(snapshot *Snapshot, _ *PluginDependencyEdge) { snapshot.Rules[0].Enabled = false },
		"wrong agent":                func(snapshot *Snapshot, _ *PluginDependencyEdge) { snapshot.Rules[0].AgentID = "edge-8" },
		"unsupported kind":           func(_ *Snapshot, edge *PluginDependencyEdge) { edge.Consumer.Kind = "relay_listener" },
		"noncanonical id":            func(_ *Snapshot, edge *PluginDependencyEdge) { edge.Consumer.ID = "011" },
		"wrong extension": func(snapshot *Snapshot, _ *PluginDependencyEdge) {
			snapshot.PluginGenerations[0].ExtensionPoints = []string{"dns.provider"}
		},
		"duplicate": func(snapshot *Snapshot, edge *PluginDependencyEdge) {
			snapshot.PluginDependencies = append(snapshot.PluginDependencies, *edge)
		},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := base
			snapshot.Rules = append([]HTTPRule(nil), base.Rules...)
			snapshot.PluginGenerations = append([]PluginGeneration(nil), base.PluginGenerations...)
			edge := valid
			snapshot.PluginDependencies = []PluginDependencyEdge{edge}
			mutate(&snapshot, &snapshot.PluginDependencies[0])
			if err := ValidatePluginDependencies(snapshot); err == nil {
				t.Fatal("ValidatePluginDependencies() accepted invalid graph")
			}
			if _, err := RequiredPluginInstanceIDs(snapshot); err == nil {
				t.Fatal("RequiredPluginInstanceIDs() derived required state from an invalid graph")
			}
		})
	}
}

func pluginDependencyTargetForTest(generation PluginGeneration) PluginDependencyTarget {
	return PluginDependencyTarget{AgentID: generation.Target.ID, ResourceGroupID: generation.Target.ResourceGroupID, Version: generation.Target.Version}
}

func pluginDependencyConsumerForTest(kind, id, resourceGroupID string) PluginDependencyConsumer {
	return PluginDependencyConsumer{Kind: kind, ID: id, ResourceGroupID: resourceGroupID, Version: strings.Repeat("e", 64)}
}

func validPluginGenerationForTest() PluginGeneration {
	return PluginGeneration{
		ID: "generation-7", InstanceID: "instance-7", OperationID: "operation-7", Revision: 7,
		PluginID: "example.rpc", PluginVersion: "1.2.3", PackageDigest: strings.Repeat("a", 64),
		Runtime: PluginRuntimeDescriptor{Kind: PluginRuntimeRPCService, ABI: PluginRPCABIV1, HostScope: "agent", Entry: "artifacts/plugin"},
		Artifact: PluginArtifactDescriptor{ArtifactID: "artifact-7", PackageIdentity: "example.rpc@1.2.3", RelativePath: "artifacts/plugin",
			SHA256: strings.Repeat("b", 64), SizeBytes: 42, Mode: "executable", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
			SignatureVerified: true, SignerKeyID: "release-key", SignerFingerprint: strings.Repeat("c", 64)},
		ExtensionPoints: []string{pluginsdk.ExtensionHTTPBackendProvider}, RequiredFeatures: []string{pluginsdk.RPCFeatureHTTPBackendProviderV1},
		HTTPBackendProviders: []pluginsdk.HTTPBackendProviderDescriptor{{ID: "default", DisplayName: "Default"}}, ConfigVersion: 1, Config: json.RawMessage(`{"enabled":true}`),
		Grants:         []PluginGrantProjection{{Name: pluginsdk.PermissionHTTPOutbound}},
		SecretHandles:  []PluginSecretHandle{{ID: "secret-7", Version: 1, Digest: strings.Repeat("d", 64), Purpose: "upstream"}},
		ResourceBudget: PluginResourceBudget{TimeoutMS: 1000, MemoryBytes: 1 << 20, Concurrency: 2, InputBytes: 4096, OutputBytes: 4096, CPUMillis: 100, Restarts: 2},
		Target:         PluginTargetBinding{Kind: "agent", ID: "edge-7", ResourceGroupID: "default", Version: 1},
		FailurePolicy:  PluginFailurePolicy{OnError: "degraded", OnBudget: "fail-closed", Restart: "on-failure", CoreFallback: "preserve"},
	}
}

func httpProviderRuleForTest(id int, agentID string, provider PluginGeneration) HTTPRule {
	return HTTPRule{ID: id, AgentID: agentID, Enabled: true, Backends: []HTTPBackend{{
		Kind:           pluginsdk.HTTPBackendKindPluginProvider,
		PluginProvider: &pluginsdk.HTTPPluginProviderRef{InstanceID: provider.InstanceID, ProviderID: "default"},
	}}}
}
