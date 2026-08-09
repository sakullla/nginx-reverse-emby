package policy

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
)

const ProviderEvaluator = module.ProviderPolicyEvaluator

type GenerationSpec struct {
	ID                string
	Revision          int64
	Policies          []model.PluginPolicy
	RequiredPolicyIDs []string
}

// GenerationFactory is the narrow process-to-generation bridge. A factory may
// reuse one process-scoped compiler/runtime, but every returned runtime owns
// only this generation's compiled modules, pools and state. Optional policy
// failures may be isolated inside the returned runtime; a required policy
// failure must return an error so the candidate generation is not published.
type GenerationFactory interface {
	PrepareGeneration(context.Context, GenerationSpec) (GenerationRuntime, error)
}

type GenerationRuntime interface {
	ModuleEvaluator
	Ready(context.Context) error
	Close(context.Context) error
}

type Module struct {
	factory  GenerationFactory
	observer observability.Observer

	mu         sync.Mutex
	standalone *transaction
}

func NewModule(factory GenerationFactory, observer observability.Observer) *Module {
	if observer == nil {
		observer = observability.Default()
	}
	return &Module{factory: factory, observer: observer}
}

func (*Module) Name() string { return "plugin-policy" }

func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.Name(), Provides: []module.ProviderRef{ProviderEvaluator}}
}

func (*Module) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(ProviderEvaluator, Evaluator(disabledEvaluator{}))
}

func (*Module) Capabilities(module.SnapshotView) []module.Capability {
	return []module.Capability{
		{Name: ExtensionHTTP, Enabled: true, Metadata: map[string]string{"abi": model.PolicyABIV1}},
		{Name: ExtensionL4, Enabled: true, Metadata: map[string]string{"abi": model.PolicyABIV1}},
	}
}

func (m *Module) Prepare(ctx context.Context, request module.ApplyRequest) (module.ModuleTransaction, error) {
	if m == nil {
		return nil, errors.New("policy module is nil")
	}
	generationContext, err := module.NewGenerationContext(request.Previous, request.Next)
	if err != nil {
		return nil, err
	}
	definitions, required, err := m.prepareSnapshotPolicies(ctx, request.Next)
	if err != nil {
		return nil, err
	}
	if _, err := NewGenerationEvaluator(generationContext.ID(), definitions, nil, m.observer); err != nil {
		return nil, fmt.Errorf("validate policy generation: %w", err)
	}
	var runtime GenerationRuntime
	if len(definitions) > 0 && m.factory != nil {
		runtime, err = m.factory.PrepareGeneration(ctx, GenerationSpec{
			ID: generationContext.ID(), Revision: generationContext.Revision(), Policies: definitions,
			RequiredPolicyIDs: append([]string(nil), required...),
		})
		if err != nil {
			if runtime != nil {
				_ = runtime.Close(context.WithoutCancel(ctx))
				runtime = nil
			}
			if len(required) == 0 {
				observeEvent(ctx, m.observer, observability.Event{
					Name: observability.PolicyDegraded, Outcome: "degraded", Reason: "optional-runtime-prepare-failed",
				})
				err = nil
			} else {
				return nil, fmt.Errorf("prepare policy generation runtime: %w", err)
			}
		}
	}
	if len(required) > 0 && runtime == nil {
		return nil, errors.New("required policy generation runtime is unavailable")
	}
	evaluator, err := NewGenerationEvaluator(generationContext.ID(), definitions, runtime, m.observer)
	if err != nil {
		if runtime != nil {
			_ = runtime.Close(context.WithoutCancel(ctx))
		}
		return nil, err
	}
	return &transaction{module: m, runtime: runtime, evaluator: evaluator}, nil
}

func (m *Module) Apply(ctx context.Context, request module.ApplyRequest) error {
	prepared, err := m.Prepare(ctx, request)
	if err != nil {
		return err
	}
	transaction, ok := prepared.(*transaction)
	if !ok {
		return errors.New("policy module prepared an incompatible transaction")
	}
	if err := transaction.Ready(ctx); err != nil {
		_ = transaction.Destroy(context.WithoutCancel(ctx))
		return err
	}
	return transaction.Commit()
}

func (m *Module) Stop(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	active := m.standalone
	m.standalone = nil
	m.mu.Unlock()
	if active == nil {
		return nil
	}
	return active.Destroy(ctx)
}

type transaction struct {
	module    *Module
	runtime   GenerationRuntime
	evaluator *GenerationEvaluator
	previous  *transaction

	mu        sync.Mutex
	committed bool
	closed    bool
}

func (transaction *transaction) RegisterProviders(reg module.ProviderRegistry) error {
	if transaction == nil || transaction.evaluator == nil {
		return errors.New("policy evaluator is unavailable")
	}
	return reg.Provide(ProviderEvaluator, Evaluator(transaction.evaluator))
}

func (transaction *transaction) Ready(ctx context.Context) error {
	if transaction == nil || transaction.evaluator == nil {
		return errors.New("policy generation transaction is incomplete")
	}
	if transaction.runtime != nil {
		return transaction.runtime.Ready(ctx)
	}
	return nil
}

func (transaction *transaction) Commit() error {
	if transaction == nil || transaction.module == nil {
		return errors.New("policy generation transaction is incomplete")
	}
	transaction.mu.Lock()
	if transaction.closed {
		transaction.mu.Unlock()
		return errors.New("policy generation transaction is closed")
	}
	if transaction.committed {
		transaction.mu.Unlock()
		return nil
	}
	transaction.module.mu.Lock()
	transaction.previous = transaction.module.standalone
	transaction.module.standalone = transaction
	transaction.committed = true
	transaction.module.mu.Unlock()
	transaction.mu.Unlock()
	return nil
}

func (transaction *transaction) Rollback() error {
	if transaction == nil {
		return nil
	}
	transaction.mu.Lock()
	if transaction.committed && transaction.module != nil {
		transaction.module.mu.Lock()
		if transaction.module.standalone == transaction {
			transaction.module.standalone = transaction.previous
		}
		transaction.module.mu.Unlock()
		transaction.committed = false
	}
	transaction.mu.Unlock()
	return transaction.Destroy(context.Background())
}

func (transaction *transaction) FinalizeCommitSuccess() {
	if transaction == nil {
		return
	}
	transaction.mu.Lock()
	previous := transaction.previous
	transaction.previous = nil
	transaction.mu.Unlock()
	if previous != nil {
		_ = previous.Destroy(context.Background())
	}
}

func (transaction *transaction) Destroy(ctx context.Context) error {
	if transaction == nil {
		return nil
	}
	transaction.mu.Lock()
	if transaction.closed {
		transaction.mu.Unlock()
		return nil
	}
	transaction.closed = true
	runtime := transaction.runtime
	transaction.runtime = nil
	transaction.evaluator = nil
	transaction.mu.Unlock()
	if runtime != nil {
		return runtime.Close(ctx)
	}
	return nil
}

func (m *Module) prepareSnapshotPolicies(ctx context.Context, snapshot model.Snapshot) ([]model.PluginPolicy, []string, error) {
	for _, rule := range snapshot.Rules {
		if !rule.Enabled || rule.PolicyRef == nil {
			continue
		}
		if _, err := NewTrustedPeerAllowlist(rule.TrustedProxyRanges); err != nil {
			return nil, nil, fmt.Errorf("http rule %d trusted proxy ranges: %w", rule.ID, err)
		}
	}
	for _, rule := range snapshot.L4Rules {
		if !rule.Enabled || rule.PolicyRef == nil || !rule.Tuning.ProxyProtocol.Decode {
			continue
		}
		if _, err := NewTrustedPeerAllowlist(rule.Tuning.ProxyProtocol.TrustedPeers); err != nil {
			return nil, nil, fmt.Errorf("l4 rule %d trusted PROXY peers: %w", rule.ID, err)
		}
	}
	rawDefinitions := make(map[string]model.PluginPolicy, len(snapshot.PluginPolicies))
	for _, definition := range snapshot.PluginPolicies {
		id := strings.TrimSpace(definition.ID)
		if !canonicalIdentity(definition.ID) {
			return nil, nil, fmt.Errorf("policy %q has a missing or non-canonical id", definition.ID)
		}
		if _, duplicate := rawDefinitions[id]; duplicate {
			return nil, nil, fmt.Errorf("duplicate policy id %q", id)
		}
		rawDefinitions[id] = definition
	}
	required := make(map[string]struct{})
	l4Required := make(map[string]struct{})
	for _, rule := range snapshot.Rules {
		if !rule.Enabled || rule.PolicyRef == nil {
			continue
		}
		if err := validatePolicyRef(rule.PolicyRef, rawDefinitions); err != nil {
			return nil, nil, fmt.Errorf("http rule %d: %w", rule.ID, err)
		}
		required[strings.TrimSpace(rule.PolicyRef.ID)] = struct{}{}
	}
	for _, rule := range snapshot.L4Rules {
		if !rule.Enabled || rule.PolicyRef == nil {
			continue
		}
		if err := validatePolicyRef(rule.PolicyRef, rawDefinitions); err != nil {
			return nil, nil, fmt.Errorf("l4 rule %d: %w", rule.ID, err)
		}
		id := strings.TrimSpace(rule.PolicyRef.ID)
		required[id] = struct{}{}
		l4Required[id] = struct{}{}
	}
	definitions := make([]model.PluginPolicy, 0, len(rawDefinitions))
	for _, definition := range snapshot.PluginPolicies {
		cloned, err := validateAndClonePolicy(definition)
		if err != nil {
			if _, directlyRequired := required[definition.ID]; directlyRequired {
				return nil, nil, fmt.Errorf("required policy %q: %w", definition.ID, err)
			}
			if m != nil && m.observer != nil {
				observeEvent(ctx, m.observer, observability.Event{
					Name: observability.PolicyDegraded, Outcome: "degraded", PolicyID: definition.ID, Reason: "invalid-optional-definition",
				})
			}
			continue
		}
		if _, usedByL4 := l4Required[cloned.ID]; usedByL4 && containsStage(cloned.Stages, model.PolicyKindWAF) {
			return nil, nil, fmt.Errorf("l4 policy %q cannot contain a waf stage", cloned.ID)
		}
		definitions = append(definitions, cloned)
	}
	ids := make([]string, 0, len(required))
	for id := range required {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return definitions, ids, nil
}

func validatePolicyRef(ref *model.PolicyRef, definitions map[string]model.PluginPolicy) error {
	if ref == nil || !canonicalIdentity(ref.ID) {
		return errors.New("policy ref is missing or non-canonical")
	}
	if len(ref.Overlay) > int(MaxPolicyInputBytes) {
		return errors.New("policy overlay exceeds host input ceiling")
	}
	if _, ok := definitions[ref.ID]; !ok {
		return fmt.Errorf("policy %q is unavailable", ref.ID)
	}
	return nil
}

func clonePolicies(policies []model.PluginPolicy) []model.PluginPolicy {
	cloned := make([]model.PluginPolicy, len(policies))
	for index, policy := range policies {
		cloned[index] = policy
		cloned[index].Stages = make([]model.PolicyStage, len(policy.Stages))
		for stageIndex, stage := range policy.Stages {
			cloned[index].Stages[stageIndex] = cloneStage(stage)
		}
	}
	return cloned
}

type disabledEvaluator struct{}

func (disabledEvaluator) Evaluate(_ context.Context, ref *model.PolicyRef, _ Input) Decision {
	if ref == nil || strings.TrimSpace(ref.ID) == "" {
		return Decision{Action: ActionAllow}
	}
	return unavailableDecision(strings.TrimSpace(ref.ID), "generation-policy-provider-unavailable")
}

var (
	_ module.Module                = (*Module)(nil)
	_ module.TransactionalModule   = (*Module)(nil)
	_ module.GenerationTransaction = (*transaction)(nil)
)
