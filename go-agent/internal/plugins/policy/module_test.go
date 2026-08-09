package policy

import (
	"context"
	"errors"
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

func TestPolicyModuleRejectsWAFOnL4(t *testing.T) {
	policyModule := NewModule(&testGenerationFactory{}, nil)
	next := model.Snapshot{Revision: 1, PluginPolicies: []model.PluginPolicy{testPolicy("waf", model.PolicyKindWAF)}}
	next.L4Rules = []model.L4Rule{{ID: 2, Enabled: true, PolicyRef: &model.PolicyRef{ID: "waf"}}}
	if _, err := policyModule.Prepare(context.Background(), module.ApplyRequest{Next: next}); err == nil {
		t.Fatal("Prepare() accepted an L4 WAF dependency")
	}
}

func TestPolicyModuleExcludesInvalidOptionalDefinitionWithoutBlockingRequiredPolicy(t *testing.T) {
	factory := &testGenerationFactory{}
	policyModule := NewModule(factory, nil)
	required := testPolicy("required", model.PolicyKindIP)
	optional := testPolicy("optional", model.PolicyKindWAF)
	optional.Stages[0].SignatureVerified = false
	next := model.Snapshot{Revision: 1, PluginPolicies: []model.PluginPolicy{required, optional}}
	next.Rules = []model.HTTPRule{{ID: 1, Enabled: true, PolicyRef: &model.PolicyRef{ID: "required"}}}
	transaction, err := policyModule.Prepare(context.Background(), module.ApplyRequest{Next: next})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	defer func() { _ = transaction.(module.GenerationTransaction).Destroy(context.Background()) }()
	if len(factory.spec.Policies) != 1 || factory.spec.Policies[0].ID != "required" {
		t.Fatalf("factory policies = %+v", factory.spec.Policies)
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
