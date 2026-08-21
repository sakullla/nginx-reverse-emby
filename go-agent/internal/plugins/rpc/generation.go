package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const GenerationCapability = "plugin_generation_v1"

// GenerationModule attaches rpc-service processes to the same candidate view,
// readiness barrier, atomic publication, and predecessor drain as core modules.
type GenerationModule struct {
	mu      sync.RWMutex
	host    *Host
	retirer RuntimeLogFenceRetirer
	active  *generationTransaction
}

type RuntimeLogFenceRetirer interface {
	StagePluginRuntimeLogRetirementIntent(string, int64, []pluginprocess.RuntimeLogIdentity) error
	MarkPluginRuntimeLogRetirementIntentDrained(string) error
	AbortPluginRuntimeLogRetirementIntent(string) error
}

func NewGenerationModule(host *Host) *GenerationModule { return &GenerationModule{host: host} }

func (m *GenerationModule) SetHost(host *Host) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.host = host
	m.mu.Unlock()
}

func (m *GenerationModule) SetRuntimeLogFenceRetirer(retirer RuntimeLogFenceRetirer) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.retirer = retirer
	m.mu.Unlock()
}

func (m *GenerationModule) RuntimeLogFenceRetirementReady() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.retirer != nil
}

func (*GenerationModule) Name() string { return "plugin-rpc" }

func (*GenerationModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: "plugin-rpc", Provides: []module.ProviderRef{ProviderHTTPBackendProviders}}
}

func (*GenerationModule) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(ProviderHTTPBackendProviders, NewHTTPBackendProviderSet(nil))
}

func (*GenerationModule) Capabilities(module.SnapshotView) []module.Capability {
	return []module.Capability{{Name: GenerationCapability, Enabled: true, Metadata: map[string]string{"abi": model.PluginRPCABIV1}}}
}

func (m *GenerationModule) Prepare(ctx context.Context, request module.ApplyRequest) (module.ModuleTransaction, error) {
	if m == nil {
		return nil, errors.New("RPC plugin generation module is nil")
	}
	if err := model.ValidatePluginGenerations(request.Next, true); err != nil {
		return nil, err
	}
	generationContext, err := request.ResolvedGenerationContext()
	if err != nil {
		return nil, err
	}
	m.mu.RLock()
	host := m.host
	retirer := m.retirer
	m.mu.RUnlock()
	requiredInstances := make(map[string]struct{})
	requiredIDs, err := model.RequiredPluginInstanceIDs(request.Next)
	if err != nil {
		return nil, err
	}
	for _, instanceID := range requiredIDs {
		requiredInstances[instanceID] = struct{}{}
	}
	retirements := retiredRuntimeLogIdentities(request.Previous.PluginGenerations, request.Next.PluginGenerations)
	transaction := &generationTransaction{module: m, host: host, generationID: generationContext.ID()}
	if len(retirements) > 0 && retirer != nil {
		transaction.retirementIntentID = runtimeLogRetirementIntentID(generationContext.ID(), request.Next.Revision, retirements)
		if err := retirer.StagePluginRuntimeLogRetirementIntent(transaction.retirementIntentID, request.Next.Revision, retirements); err != nil {
			return nil, fmt.Errorf("stage RPC plugin runtime log retirement intent: %w", err)
		}
		transaction.retirementIntentStaged = true
	}
	for _, generation := range clonePluginGenerations(request.Next.PluginGenerations) {
		_, required := requiredInstances[generation.InstanceID]
		transaction.candidates = append(transaction.candidates, generationCandidate{spec: generation, required: required})
		index := len(transaction.candidates) - 1
		if host == nil {
			if required {
				return nil, errors.Join(fmt.Errorf("required RPC plugin instance %q: host is unavailable", generation.InstanceID), transaction.Destroy(context.WithoutCancel(ctx)))
			}
			transaction.failOptionalCandidate(index, "prepare")
			continue
		}
		candidate, candidateErr := hostCandidateFromGeneration(generation, generationContext.ID())
		if candidateErr != nil {
			if required {
				return nil, errors.Join(fmt.Errorf("required RPC plugin instance %q: %w", generation.InstanceID, candidateErr), transaction.Destroy(context.WithoutCancel(ctx)))
			}
			transaction.failOptionalCandidate(index, "prepare")
			continue
		}
		instance, prepareErr := host.PrepareCandidate(ctx, candidate)
		if prepareErr != nil {
			if required {
				return nil, errors.Join(fmt.Errorf("prepare required RPC plugin instance %q: %w", generation.InstanceID, prepareErr), transaction.Destroy(context.WithoutCancel(ctx)))
			}
			transaction.failOptionalCandidate(index, "prepare")
			continue
		}
		transaction.candidates[index].instance = instance
	}
	return transaction, nil
}

func (m *GenerationModule) Apply(ctx context.Context, request module.ApplyRequest) error {
	prepared, err := m.Prepare(ctx, request)
	if err != nil {
		return err
	}
	transaction := prepared.(*generationTransaction)
	if err := transaction.Ready(ctx); err != nil {
		return errors.Join(err, transaction.Destroy(context.WithoutCancel(ctx)))
	}
	if err := transaction.PrepareGenerationPublication(ctx); err != nil {
		return errors.Join(err, transaction.Destroy(context.WithoutCancel(ctx)))
	}
	transaction.FinalizeGenerationPublication()
	return nil
}

func (m *GenerationModule) Stop(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	active := m.active
	m.active = nil
	m.mu.Unlock()
	if active == nil {
		return nil
	}
	return active.Destroy(ctx)
}

type generationTransaction struct {
	module                     *GenerationModule
	host                       *Host
	generationID               string
	candidates                 []generationCandidate
	publication                *PreparedHostGeneration
	retirementIntentID         string
	retirementIntentStaged     bool
	committedRetirementIntents []string
	providerHandles            []*HTTPBackendProviderHandle

	mu        sync.Mutex
	published bool
	closed    bool
}

type generationCandidate struct {
	spec     model.PluginGeneration
	instance *HostedInstance
	required bool
	failure  *model.PluginRuntimeStatus
}

func (t *generationTransaction) Ready(_ context.Context) error {
	if t == nil {
		return errors.New("RPC plugin generation transaction is nil")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errors.New("RPC plugin generation transaction is closed")
	}
	for index := range t.candidates {
		candidate := &t.candidates[index]
		if candidate.instance == nil {
			continue
		}
		if err := t.host.ReadyCandidate(candidate.instance); err != nil {
			if candidate.required {
				return fmt.Errorf("required RPC plugin instance %q readiness: %w", candidate.spec.InstanceID, err)
			}
			if cleanupErr := t.host.DestroyCandidate(candidate.instance); cleanupErr != nil {
				return errors.Join(fmt.Errorf("optional RPC plugin instance %q readiness: %w", candidate.spec.InstanceID, err), cleanupErr)
			}
			candidate.instance = nil
			t.failOptionalCandidate(index, "readiness")
		}
	}
	return nil
}

func (t *generationTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	if t == nil {
		return reg.Provide(ProviderHTTPBackendProviders, NewHTTPBackendProviderSet(nil))
	}
	handles := make([]*HTTPBackendProviderHandle, 0)
	for index := range t.candidates {
		candidate := &t.candidates[index]
		if candidate.instance == nil {
			continue
		}
		for _, descriptor := range candidate.spec.HTTPBackendProviders {
			handles = append(handles, newHTTPBackendProviderHandle(candidate.instance, descriptor.ID))
		}
	}
	t.providerHandles = handles
	return reg.Provide(ProviderHTTPBackendProviders, NewHTTPBackendProviderSet(handles))
}

func (t *generationTransaction) PrepareGenerationPublication(ctx context.Context) error {
	if t == nil {
		return errors.New("RPC plugin generation transaction is nil")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errors.New("RPC plugin generation transaction is closed")
	}
	if t.host == nil {
		return nil
	}
	if t.publication != nil {
		return nil
	}
	instances := make([]*HostedInstance, 0, len(t.candidates))
	for index := range t.candidates {
		candidate := &t.candidates[index]
		if candidate.instance == nil {
			continue
		}
		if err := t.host.ActivatePreparedCandidate(ctx, candidate.instance); err != nil {
			if candidate.required {
				return fmt.Errorf("activate required RPC plugin instance %q: %w", candidate.spec.InstanceID, err)
			}
			if cleanupErr := t.host.DestroyCandidate(candidate.instance); cleanupErr != nil {
				return errors.Join(fmt.Errorf("activate optional RPC plugin instance %q: %w", candidate.spec.InstanceID, err), cleanupErr)
			}
			candidate.instance = nil
			t.failOptionalCandidate(index, "activation")
			continue
		}
		instances = append(instances, candidate.instance)
	}
	publication, err := t.host.PrepareGenerationPublication(t.generationID, instances)
	if err != nil {
		return err
	}
	t.publication = publication
	return nil
}

func (t *generationTransaction) FinalizeGenerationPublication() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.publication == nil || t.closed || t.published {
		t.mu.Unlock()
		return
	}
	t.publication.Publish()
	t.publication = nil
	t.published = true
	intentID := t.retirementIntentID
	t.retirementIntentStaged = false
	var previous *generationTransaction
	if t.module != nil {
		t.module.mu.Lock()
		previous = t.module.active
		t.module.active = t
		t.module.mu.Unlock()
	}
	t.mu.Unlock()
	if intentID == "" {
		return
	}
	if previous != nil && previous != t {
		previous.addCommittedRetirementIntent(intentID)
		return
	}
	// With no prior in-memory RPC transaction (for example after an Agent
	// restart), the previous process lifecycle is already absent. Record only
	// that drain fact here; sync_revision authorizes fence completion after its
	// cutover journal and applied snapshot are durable.
	if err := t.markRuntimeLogRetirementIntentDrained(intentID); err != nil {
		t.mu.Lock()
		t.committedRetirementIntents = append(t.committedRetirementIntents, intentID)
		t.mu.Unlock()
	}
}

func (t *generationTransaction) Commit() error {
	if err := t.PrepareGenerationPublication(context.Background()); err != nil {
		return err
	}
	t.FinalizeGenerationPublication()
	return nil
}

func (t *generationTransaction) Rollback() error { return t.Destroy(context.Background()) }

func (t *generationTransaction) Destroy(ctx context.Context) error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	if t.closed {
		intentIDs := append([]string(nil), t.committedRetirementIntents...)
		t.mu.Unlock()
		if err := t.markRuntimeLogRetirementIntentsDrained(intentIDs); err != nil {
			return err
		}
		t.mu.Lock()
		t.committedRetirementIntents = nil
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	if t.publication != nil {
		t.publication.Abort()
		t.publication = nil
	}
	instances := make([]*HostedInstance, 0, len(t.candidates))
	for index := range t.candidates {
		candidate := &t.candidates[index]
		if candidate.instance != nil {
			instances = append(instances, candidate.instance)
		}
		candidate.instance = nil
		candidate.spec.Config = nil
		candidate.spec.SecretHandles = nil
	}
	intentIDs := append([]string(nil), t.committedRetirementIntents...)
	abortIntentID := ""
	if !t.published && t.retirementIntentStaged {
		abortIntentID = t.retirementIntentID
	}
	t.candidates = nil
	handles := append([]*HTTPBackendProviderHandle(nil), t.providerHandles...)
	t.providerHandles = nil
	t.mu.Unlock()
	var destroyErr error
	for _, handle := range handles {
		destroyErr = errors.Join(destroyErr, handle.drain(ctx))
	}
	for _, instance := range instances {
		if t.host != nil {
			destroyErr = errors.Join(destroyErr, t.host.DestroyCandidate(instance))
		}
	}
	if abortIntentID != "" {
		destroyErr = errors.Join(destroyErr, t.abortRuntimeLogRetirementIntent(abortIntentID))
	}
	if destroyErr != nil {
		return destroyErr
	}
	if err := t.markRuntimeLogRetirementIntentsDrained(intentIDs); err != nil {
		return err
	}
	t.mu.Lock()
	t.committedRetirementIntents = nil
	t.mu.Unlock()
	return nil
}

func (t *generationTransaction) addCommittedRetirementIntent(id string) {
	if t == nil || id == "" {
		return
	}
	t.mu.Lock()
	t.committedRetirementIntents = append(t.committedRetirementIntents, id)
	t.mu.Unlock()
}

func (t *generationTransaction) markRuntimeLogRetirementIntentsDrained(ids []string) error {
	var err error
	for _, id := range ids {
		err = errors.Join(err, t.markRuntimeLogRetirementIntentDrained(id))
	}
	return err
}

func (t *generationTransaction) markRuntimeLogRetirementIntentDrained(id string) error {
	if t == nil || id == "" || t.module == nil {
		return nil
	}
	t.module.mu.RLock()
	retirer := t.module.retirer
	t.module.mu.RUnlock()
	if retirer == nil {
		return nil
	}
	return retirer.MarkPluginRuntimeLogRetirementIntentDrained(id)
}

func (t *generationTransaction) abortRuntimeLogRetirementIntent(id string) error {
	if t == nil || id == "" || t.module == nil {
		return nil
	}
	t.module.mu.RLock()
	retirer := t.module.retirer
	t.module.mu.RUnlock()
	if retirer == nil {
		return nil
	}
	return retirer.AbortPluginRuntimeLogRetirementIntent(id)
}

func (t *generationTransaction) PluginRuntimeStatuses() []model.PluginRuntimeStatus {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	statuses := make([]model.PluginRuntimeStatus, 0, len(t.candidates))
	for index := range t.candidates {
		candidate := &t.candidates[index]
		if candidate.failure != nil {
			statuses = append(statuses, clonePluginRuntimeStatus(*candidate.failure))
			continue
		}
		if candidate.instance == nil {
			continue
		}
		runtimeStatus := candidate.instance.Status()
		spec := candidate.spec
		details, _ := json.Marshal(struct {
			SandboxProvider string `json:"sandbox_provider,omitempty"`
			RestartCount    int    `json:"restart_count,omitempty"`
			CircuitOpen     bool   `json:"circuit_open,omitempty"`
		}{runtimeStatus.SandboxProvider, runtimeStatus.RestartCount, runtimeStatus.CircuitOpen})
		budget, _ := json.Marshal(spec.ResourceBudget)
		status := model.PluginRuntimeStatus{
			InstanceID: spec.InstanceID, PluginID: spec.PluginID, OperationID: spec.OperationID, Revision: spec.Revision,
			GenerationID: spec.ID, PackageDigest: spec.PackageDigest, ArtifactDigest: spec.Artifact.SHA256, ConfigVersion: spec.ConfigVersion,
			RuntimeKind: spec.Runtime.Kind, State: pluginRuntimeReportState(runtimeStatus.State), Sequence: 1,
			Details: details, Budget: budget, SandboxProvider: runtimeStatus.SandboxProvider,
			RestartCount: runtimeStatus.RestartCount, CircuitOpen: runtimeStatus.CircuitOpen,
		}
		if runtimeStatus.LastError != "" {
			status.ErrorCode = "rpc_runtime_failed"
			status.SafeDetail = pluginRuntimeSafeDetail(runtimeStatus.State)
		}
		statuses = append(statuses, status)
	}
	return statuses
}

func (t *generationTransaction) failOptionalCandidate(index int, phase string) {
	if index < 0 || index >= len(t.candidates) {
		return
	}
	candidate := &t.candidates[index]
	state := "failed"
	if candidate.spec.FailurePolicy.OnError == "degraded" || candidate.spec.FailurePolicy.OnError == "fail-open" {
		state = "degraded"
	}
	details, _ := json.Marshal(struct {
		Phase    string `json:"phase"`
		Required bool   `json:"required"`
	}{Phase: phase, Required: false})
	budget, _ := json.Marshal(candidate.spec.ResourceBudget)
	status := model.PluginRuntimeStatus{
		InstanceID: candidate.spec.InstanceID, PluginID: candidate.spec.PluginID, OperationID: candidate.spec.OperationID,
		Revision: candidate.spec.Revision, GenerationID: candidate.spec.ID, PackageDigest: candidate.spec.PackageDigest,
		ArtifactDigest: candidate.spec.Artifact.SHA256, ConfigVersion: candidate.spec.ConfigVersion, RuntimeKind: candidate.spec.Runtime.Kind,
		State: state, Sequence: 1, ErrorCode: "rpc_" + phase + "_failed",
		SafeDetail: "Optional RPC plugin candidate failed and was excluded from publication", Details: details, Budget: budget,
	}
	candidate.failure = &status
}

func clonePluginRuntimeStatus(status model.PluginRuntimeStatus) model.PluginRuntimeStatus {
	status.Details = append([]byte(nil), status.Details...)
	status.Budget = append([]byte(nil), status.Budget...)
	return status
}

func pluginRuntimeSafeDetail(state string) string {
	switch strings.TrimSpace(state) {
	case "backoff":
		return "RPC plugin runtime is waiting for a bounded restart"
	case "failed":
		return "RPC plugin runtime stopped after a bounded failure"
	default:
		return "RPC plugin runtime is unavailable"
	}
}

func pluginRuntimeReportState(state string) string {
	switch state {
	case "healthy":
		return "active"
	case "backoff":
		return "degraded"
	case "failed":
		return "failed"
	case "stopping", "stopped":
		return "drained"
	default:
		return "prepared"
	}
}

func hostCandidateFromGeneration(generation model.PluginGeneration, generationID string) (HostCandidate, error) {
	permissions := make([]pluginprocess.SandboxPermission, 0, len(generation.Grants))
	scopes := make([]string, 0, len(generation.Grants))
	for _, grant := range generation.Grants {
		permissions = append(permissions, pluginprocess.SandboxPermission(grant.Name))
		scopes = append(scopes, grant.Name)
	}
	extensions := make([]pluginprocess.SandboxExtensionPoint, len(generation.ExtensionPoints))
	for index, extension := range generation.ExtensionPoints {
		extensions[index] = pluginprocess.SandboxExtensionPoint(extension)
	}
	requirement, err := pluginprocess.NewSandboxRequirement(pluginprocess.SandboxRequirementProjection{
		PackageDigest: generation.PackageDigest, Permissions: permissions, ExtensionPoints: extensions,
		ResourceBudget: pluginprocess.ManifestResourceBudget{
			TimeoutMS: generation.ResourceBudget.TimeoutMS, MemoryBytes: generation.ResourceBudget.MemoryBytes,
			Concurrency: generation.ResourceBudget.Concurrency, InputBytes: generation.ResourceBudget.InputBytes,
			OutputBytes: generation.ResourceBudget.OutputBytes, CPUMillis: generation.ResourceBudget.CPUMillis,
			Restarts: generation.ResourceBudget.Restarts,
		},
	})
	if err != nil {
		return HostCandidate{}, err
	}
	deadline := time.Duration(generation.ResourceBudget.TimeoutMS) * time.Millisecond
	grace := deadline
	if grace < time.Second {
		grace = time.Second
	}
	if grace > 30*time.Second {
		grace = 30 * time.Second
	}
	return HostCandidate{
		InstanceID: generation.InstanceID, PluginID: generation.PluginID, PluginVersion: generation.PluginVersion,
		PackageDigest: generation.PackageDigest, Generation: generationID, OperationID: generation.OperationID, Revision: generation.Revision,
		ProviderGenerationID: generation.ID, AgentID: generation.Target.ID,
		Artifact: pluginprocess.Artifact{CachePath: generation.Artifact.LocalPath, SHA256: generation.Artifact.SHA256,
			GOOS: generation.Artifact.GOOS, GOARCH: generation.Artifact.GOARCH},
		Requirement: requirement, Scopes: scopes, SecretHandles: append([]model.PluginSecretHandle(nil), generation.SecretHandles...),
		Config: append([]byte(nil), generation.Config...), Restart: generation.FailurePolicy.Restart,
		HTTPBackendProviders: append([]pluginsdk.HTTPBackendProviderDescriptor(nil), generation.HTTPBackendProviders...),
		Process:              pluginprocess.InstanceSpec{GracePeriod: grace, RestartLimit: generation.ResourceBudget.Restarts, RestartWindow: time.Minute},
		Dial: DialConfig{Network: generationEndpointNetwork(), Deadline: deadline,
			UIRoute:              hasUIRoute(generation.ExtensionPoints),
			HTTPBackendProviders: httpBackendProviderIdentities(generation.InstanceID, generationID, generation.HTTPBackendProviders)},
	}, nil
}

func hasUIRoute(points []string) bool {
	for _, point := range points {
		if point == pluginsdk.ExtensionUIRoute {
			return true
		}
	}
	return false
}

func runtimeLogIdentityFromGeneration(generation model.PluginGeneration) pluginprocess.RuntimeLogIdentity {
	return pluginprocess.RuntimeLogIdentity{
		Revision: generation.Revision, ProviderGenerationID: generation.ID, InstanceID: generation.InstanceID,
		PluginID: generation.PluginID, AgentID: generation.Target.ID, PackageDigest: generation.PackageDigest,
		ArtifactDigest: generation.Artifact.SHA256,
	}
}

func retiredRuntimeLogIdentities(previous, next []model.PluginGeneration) []pluginprocess.RuntimeLogIdentity {
	nextFences := make(map[pluginprocess.RuntimeLogIdentity]struct{}, len(next))
	for _, generation := range next {
		if generation.Runtime.Kind != model.PluginRuntimeRPCService {
			continue
		}
		nextFences[runtimeLogIdentityFromGeneration(generation)] = struct{}{}
	}
	retired := make([]pluginprocess.RuntimeLogIdentity, 0, len(previous))
	seen := make(map[pluginprocess.RuntimeLogIdentity]struct{}, len(previous))
	for _, generation := range previous {
		if generation.Runtime.Kind != model.PluginRuntimeRPCService {
			continue
		}
		identity := runtimeLogIdentityFromGeneration(generation)
		if _, unchanged := nextFences[identity]; unchanged {
			continue
		}
		if _, duplicate := seen[identity]; duplicate {
			continue
		}
		seen[identity] = struct{}{}
		retired = append(retired, identity)
	}
	return retired
}

func runtimeLogRetirementIntentID(generationID string, revision int64, identities []pluginprocess.RuntimeLogIdentity) string {
	canonical := append([]pluginprocess.RuntimeLogIdentity(nil), identities...)
	sort.Slice(canonical, func(left, right int) bool {
		leftJSON, _ := json.Marshal(canonical[left])
		rightJSON, _ := json.Marshal(canonical[right])
		return string(leftJSON) < string(rightJSON)
	})
	payload, _ := json.Marshal(struct {
		Domain       string                             `json:"domain"`
		GenerationID string                             `json:"generation_id"`
		Revision     int64                              `json:"revision"`
		Fences       []pluginprocess.RuntimeLogIdentity `json:"fences"`
	}{"nre.plugin-log-retirement.v1", generationID, revision, canonical})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func generationEndpointNetwork() string {
	if runtime.GOOS == "windows" {
		return "tcp"
	}
	return "unix"
}

func clonePluginGenerations(generations []model.PluginGeneration) []model.PluginGeneration {
	cloned := make([]model.PluginGeneration, len(generations))
	for index, generation := range generations {
		cloned[index] = generation
		cloned[index].Config = append([]byte(nil), generation.Config...)
		cloned[index].ExtensionPoints = append([]string(nil), generation.ExtensionPoints...)
		cloned[index].Grants = append([]model.PluginGrantProjection(nil), generation.Grants...)
		cloned[index].SecretHandles = append([]model.PluginSecretHandle(nil), generation.SecretHandles...)
	}
	return cloned
}

var (
	_ module.Module                = (*GenerationModule)(nil)
	_ module.TransactionalModule   = (*GenerationModule)(nil)
	_ module.GenerationTransaction = (*generationTransaction)(nil)
)
