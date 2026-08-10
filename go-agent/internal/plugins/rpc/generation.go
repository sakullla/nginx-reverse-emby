package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
)

const GenerationCapability = "plugin_generation_v1"

// GenerationModule attaches rpc-service processes to the same candidate view,
// readiness barrier, atomic publication, and predecessor drain as core modules.
type GenerationModule struct {
	mu     sync.RWMutex
	host   *Host
	active *generationTransaction
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

func (*GenerationModule) Name() string { return "plugin-rpc" }

func (*GenerationModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: "plugin-rpc"}
}

func (*GenerationModule) RegisterProviders(module.ProviderRegistry) error { return nil }

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
	generationContext, err := module.NewGenerationContext(request.Previous, request.Next)
	if err != nil {
		return nil, err
	}
	m.mu.RLock()
	host := m.host
	m.mu.RUnlock()
	if len(request.Next.PluginGenerations) > 0 && host == nil {
		return nil, errors.New("RPC plugin host is unavailable")
	}
	transaction := &generationTransaction{module: m, host: host, generationID: generationContext.ID(), specs: clonePluginGenerations(request.Next.PluginGenerations)}
	for _, generation := range request.Next.PluginGenerations {
		candidate, candidateErr := hostCandidateFromGeneration(generation, generationContext.ID())
		if candidateErr != nil {
			return nil, errors.Join(candidateErr, transaction.Destroy(context.WithoutCancel(ctx)))
		}
		instance, prepareErr := host.PrepareCandidate(ctx, candidate)
		if prepareErr != nil {
			return nil, errors.Join(fmt.Errorf("prepare RPC plugin instance %q: %w", generation.InstanceID, prepareErr), transaction.Destroy(context.WithoutCancel(ctx)))
		}
		transaction.instances = append(transaction.instances, instance)
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
	module       *GenerationModule
	host         *Host
	generationID string
	specs        []model.PluginGeneration
	instances    []*HostedInstance
	publication  *PreparedHostGeneration

	mu        sync.Mutex
	published bool
	closed    bool
}

func (t *generationTransaction) Ready(_ context.Context) error {
	if t == nil {
		return errors.New("RPC plugin generation transaction is nil")
	}
	for _, instance := range t.instances {
		if err := t.host.ReadyCandidate(instance); err != nil {
			return fmt.Errorf("RPC plugin candidate readiness: %w", err)
		}
	}
	return nil
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
	if t.host == nil && len(t.instances) == 0 {
		return nil
	}
	if t.publication != nil {
		return nil
	}
	for index, instance := range t.instances {
		if err := t.host.ActivatePreparedCandidate(ctx, instance); err != nil {
			return fmt.Errorf("activate RPC plugin instance %q: %w", t.specs[index].InstanceID, err)
		}
	}
	publication, err := t.host.PrepareGenerationPublication(t.generationID, t.instances)
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
	if t.module != nil {
		t.module.mu.Lock()
		t.module.active = t
		t.module.mu.Unlock()
	}
	t.mu.Unlock()
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
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	if t.publication != nil {
		t.publication.Abort()
		t.publication = nil
	}
	instances := append([]*HostedInstance(nil), t.instances...)
	t.instances = nil
	for index := range t.specs {
		t.specs[index].Config = nil
		t.specs[index].SecretHandles = nil
	}
	t.specs = nil
	t.mu.Unlock()
	var errs []error
	for _, instance := range instances {
		if t.host != nil {
			errs = append(errs, t.host.DestroyCandidate(instance))
		}
	}
	return errors.Join(errs...)
}

func (t *generationTransaction) PluginRuntimeStatuses() []model.PluginRuntimeStatus {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	statuses := make([]model.PluginRuntimeStatus, 0, len(t.instances))
	for index, instance := range t.instances {
		runtimeStatus := instance.Status()
		spec := t.specs[index]
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
		Artifact: pluginprocess.Artifact{CachePath: generation.Artifact.LocalPath, SHA256: generation.Artifact.SHA256,
			GOOS: generation.Artifact.GOOS, GOARCH: generation.Artifact.GOARCH},
		Requirement: requirement, Scopes: scopes, Config: append([]byte(nil), generation.Config...), Restart: generation.FailurePolicy.Restart,
		Process: pluginprocess.InstanceSpec{GracePeriod: grace, RestartLimit: generation.ResourceBudget.Restarts, RestartWindow: time.Minute},
		Dial:    DialConfig{Network: generationEndpointNetwork(), Deadline: deadline},
	}, nil
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
