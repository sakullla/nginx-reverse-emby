//go:build !integration

package policy

import (
	"bytes"
	"context"
	"errors"

	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

type testGenerationFactory struct {
	spec    GenerationSpec
	runtime *testGenerationRuntime
	err     error
}

func (factory *testGenerationFactory) PrepareGeneration(_ context.Context, spec GenerationSpec) (GenerationRuntime, error) {
	factory.spec = spec
	factory.runtime = &testGenerationRuntime{}
	return factory.runtime, factory.err
}

type testGenerationRuntime struct {
	ready  bool
	closed bool
}

func (runtime *testGenerationRuntime) Evaluate(context.Context, ModuleRequest) (ModuleResponse, error) {
	return ModuleResponse{Action: ActionAllow}, nil
}

func (runtime *testGenerationRuntime) Ready(context.Context) error {
	runtime.ready = true
	return nil
}

func (runtime *testGenerationRuntime) Close(context.Context) error {
	runtime.closed = true
	return nil
}

func TestPolicyModulePublishesGenerationOwnedEvaluatorAndClosesWithView(t *testing.T) {
	factory := &testGenerationFactory{}
	registry := module.NewRegistry()
	if err := registry.Register(NewModule(factory, nil)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	next := model.Snapshot{Revision: 7, PluginPolicies: []model.PluginPolicy{testPolicy("shared", model.PolicyKindIP)}}
	next.Rules = []model.HTTPRule{{ID: 1, Enabled: true, PolicyRef: &model.PolicyRef{ID: "shared"}}}
	generationContext, err := module.NewGenerationContext(model.Snapshot{}, next)
	if err != nil {
		t.Fatalf("NewGenerationContext() error = %v", err)
	}
	enabled := true
	generationContext = generationContext.WithTrafficRuntimeConfig(model.AgentConfig{
		TrafficStatsEnabled: &enabled,
		TrafficBlocked:      true,
		TrafficBlockReason:  "runtime-only",
	})
	candidate, err := registry.PrepareGeneration(context.Background(), generationContext)
	if err != nil {
		t.Fatalf("PrepareGeneration() error = %v", err)
	}
	if len(factory.spec.RequiredPolicyIDs) != 1 || factory.spec.RequiredPolicyIDs[0] != "shared" || factory.spec.ID != generationContext.ID() {
		t.Fatalf("factory spec = %+v", factory.spec)
	}
	if err := candidate.Ready(context.Background()); err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	view, _ := candidate.Publish()
	provider, ok := view.Resolve(module.ProviderPolicyEvaluator)
	if !ok {
		t.Fatal("generation evaluator provider is missing")
	}
	if _, ok := provider.(Evaluator); !ok {
		t.Fatalf("provider type = %T", provider)
	}
	if !factory.runtime.ready || factory.runtime.closed {
		t.Fatalf("runtime ready/closed = %v/%v", factory.runtime.ready, factory.runtime.closed)
	}
	if err := view.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	if !factory.runtime.closed {
		t.Fatal("generation runtime was not closed with GenerationView")
	}
}

func TestPolicyModuleRejectsMissingRequiredPolicyBeforeFactory(t *testing.T) {
	factory := &testGenerationFactory{}
	policyModule := NewModule(factory, nil)
	next := model.Snapshot{Revision: 1, Rules: []model.HTTPRule{{ID: 1, Enabled: true, PolicyRef: &model.PolicyRef{ID: "missing"}}}}
	if _, err := policyModule.Prepare(context.Background(), module.ApplyRequest{Next: next}); err == nil {
		t.Fatal("Prepare() error = nil")
	}
	if factory.runtime != nil {
		t.Fatal("factory was called for an invalid dependency graph")
	}
}

func TestPolicyValidationModuleAllowsOptionalCatalogAndRejectsRequiredPolicy(t *testing.T) {
	validation := NewValidationModule(nil)
	if capabilities := validation.Capabilities(model.Snapshot{}); len(capabilities) != 0 {
		t.Fatalf("validation-only module capabilities=%+v, want none", capabilities)
	}
	transaction, err := validation.Prepare(context.Background(), module.ApplyRequest{Next: model.Snapshot{Revision: 1, PluginPolicies: []model.PluginPolicy{}}})
	if err != nil {
		t.Fatalf("empty policy snapshot rejected: %v", err)
	}
	if err := transaction.(module.GenerationTransaction).Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	transaction, err = validation.Prepare(context.Background(), module.ApplyRequest{Next: model.Snapshot{
		Revision: 2, PluginPolicies: []model.PluginPolicy{testPolicy("unused", model.PolicyKindIP)},
	}})
	if err != nil {
		t.Fatalf("optional policy catalog rejected: %v", err)
	}
	if err := transaction.(module.GenerationTransaction).Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err = validation.Prepare(context.Background(), module.ApplyRequest{Next: model.Snapshot{
		Revision: 3, PluginPolicies: []model.PluginPolicy{testPolicy("required", model.PolicyKindIP)},
		Rules: []model.HTTPRule{{ID: 1, Enabled: true, PolicyRef: &model.PolicyRef{ID: "required"}}},
	}})
	if err == nil || !strings.Contains(err.Error(), "policy execution runtime is unavailable") {
		t.Fatalf("required policy snapshot error=%v, want unavailable validation failure", err)
	}
}

func TestPolicyModuleRejectsWAFOnL4(t *testing.T) {
	policyModule := NewModule(&testGenerationFactory{}, nil)
	next := model.Snapshot{Revision: 1, PluginPolicies: []model.PluginPolicy{testPolicy("waf", model.PolicyKindWAF)}}
	next.L4Rules = []model.L4Rule{{ID: 2, Enabled: true, PolicyRef: &model.PolicyRef{ID: "waf"}}}
	if _, err := policyModule.Prepare(context.Background(), module.ApplyRequest{Next: next}); err == nil {
		t.Fatal("Prepare() accepted an L4 WAF dependency")
	}
}

func TestPolicyModuleAdmitsExactEvaluateRequestFrameForEveryStage(t *testing.T) {
	overlay := bytes.Repeat([]byte("x"), 900)
	frameBytes, err := PolicyEvaluateRequestFrameBytes(ExtensionHTTP, strings.Repeat("r", MaxPolicyRequestIDBytes), overlay)
	if err != nil {
		t.Fatal(err)
	}
	definition := testPolicy("shared", model.PolicyKindIP, model.PolicyKindRate)
	for index := range definition.Stages {
		definition.Stages[index].ResourceBudget.InputBytes = int64(frameBytes)
	}
	snapshot := model.Snapshot{
		PluginPolicies: []model.PluginPolicy{definition},
		Rules: []model.HTTPRule{{ID: 1, Enabled: true, PolicyRef: &model.PolicyRef{
			ID: "shared", Overlay: overlay,
		}}},
	}
	policyModule := NewModule(nil, nil)
	if _, _, err := policyModule.prepareSnapshotPolicies(context.Background(), snapshot); err != nil {
		t.Fatalf("exact frame boundary rejected: %v", err)
	}

	snapshot.PluginPolicies[0].Stages[1].ResourceBudget.InputBytes--
	if _, _, err := policyModule.prepareSnapshotPolicies(context.Background(), snapshot); err == nil {
		t.Fatal("one-byte-over-budget frame accepted by second stage")
	}
}

func TestPolicyModuleOptionalRuntimeFailureDoesNotBlockCoreGeneration(t *testing.T) {
	factory := &testGenerationFactory{err: errors.New("optional compile failed")}
	policyModule := NewModule(factory, nil)
	next := model.Snapshot{Revision: 1, PluginPolicies: []model.PluginPolicy{testPolicy("unused", model.PolicyKindIP)}}
	prepared, err := policyModule.Prepare(context.Background(), module.ApplyRequest{Next: next})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	transaction := prepared.(module.GenerationTransaction)
	if err := transaction.Ready(context.Background()); err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	if !factory.runtime.closed {
		t.Fatal("failed optional runtime was not closed")
	}
	if err := transaction.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
}

func TestManagedEntryPolicyAdmissionRequiresExistingL4Policy(t *testing.T) {
	owner := NewModule(nil, nil)
	snapshot := model.Snapshot{PluginGenerations: []model.PluginGeneration{{InstanceID: "instance", ManagedNetworkPolicy: &model.PolicyRef{ID: "managed"}}}}
	if _, _, err := owner.prepareSnapshotPolicies(t.Context(), snapshot); err == nil {
		t.Fatal("missing managed entry policy accepted")
	}
	snapshot.PluginPolicies = []model.PluginPolicy{testPolicy("managed", model.PolicyKindWAF)}
	if _, _, err := owner.prepareSnapshotPolicies(t.Context(), snapshot); err == nil {
		t.Fatal("WAF attached to managed L4 entry")
	}
	snapshot.PluginPolicies = []model.PluginPolicy{testPolicy("managed", model.PolicyKindIP)}
	_, required, err := owner.prepareSnapshotPolicies(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(required) != 1 || required[0] != "managed" {
		t.Fatal("managed policy did not become required generation dependency")
	}
}
