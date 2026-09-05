package core

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type Activator func(ctx context.Context, previous, next model.Snapshot) error

type Runtime struct {
	mu             sync.RWMutex
	activeSnapshot model.Snapshot
	trafficRuntime model.AgentConfig
	trafficSet     bool
	state          model.RuntimeState
	activator      Activator
	generations    *GenerationManager
}

func NewRuntime() *Runtime {
	return NewRuntimeWithActivator(nil)
}

func NewRuntimeWithActivator(act Activator) *Runtime {
	if act == nil {
		act = defaultActivator
	}
	return &Runtime{
		state: model.RuntimeState{
			Metadata: make(map[string]string),
		},
		activator: act,
	}
}

func NewRuntimeWithGenerationManager(manager *GenerationManager) *Runtime {
	runtime := NewRuntimeWithActivator(nil)
	runtime.generations = manager
	return runtime
}

func (r *Runtime) UsesGenerationManager() bool {
	return r != nil && r.generations != nil
}

func (r *Runtime) CandidateGenerationIdentity(previous, next model.Snapshot) (GenerationIdentity, bool, error) {
	return r.candidateGenerationIdentity(previous, next, "")
}

func (r *Runtime) CandidateGenerationIdentityWithSnapshotHash(previous, next model.Snapshot, snapshotHash string) (GenerationIdentity, bool, error) {
	return r.candidateGenerationIdentity(previous, next, snapshotHash)
}

func (r *Runtime) candidateGenerationIdentity(previous, next model.Snapshot, snapshotHash string) (GenerationIdentity, bool, error) {
	if !r.UsesGenerationManager() {
		return GenerationIdentity{}, false, nil
	}
	var identity GenerationIdentity
	var err error
	if snapshotHash == "" {
		identity, err = r.generations.CandidateIdentity(previous, next)
	} else {
		identity, err = r.generations.CandidateIdentityWithSnapshotHash(previous, next, snapshotHash)
	}
	return identity, true, err
}

func (r *Runtime) ActiveGenerationIdentity() (GenerationIdentity, bool) {
	if !r.UsesGenerationManager() {
		return GenerationIdentity{}, false
	}
	return r.generations.ActiveIdentity(), true
}

func (r *Runtime) GenerationDrainSnapshot() (model.GenerationDrainSnapshot, bool) {
	if !r.UsesGenerationManager() || r.generations.DrainController() == nil {
		return model.GenerationDrainSnapshot{}, false
	}
	return r.generations.DrainController().Snapshot(), true
}

func newRuntimeWithActivator(act Activator) *Runtime {
	return NewRuntimeWithActivator(act)
}

func defaultActivator(_ context.Context, previous, next model.Snapshot) error {
	_ = previous
	_ = next
	return nil
}

func (r *Runtime) ActiveSnapshot() model.Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r != nil && r.generations != nil {
		if active := r.generations.ActiveGeneration(); active != nil {
			return overlayTrafficRuntime(active.Snapshot(), r.trafficRuntime, r.trafficSet)
		}
	}
	return overlayTrafficRuntime(cloneSnapshot(r.activeSnapshot), r.trafficRuntime, r.trafficSet)
}

func (r *Runtime) State() model.RuntimeState {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stateCopy := r.state
	stateCopy.Metadata = cloneStringMap(stateCopy.Metadata)
	stateCopy.PluginStatuses = slices.Clone(stateCopy.PluginStatuses)
	stateCopy.PluginLogReports = model.ClonePluginRuntimeLogReports(stateCopy.PluginLogReports)
	if r.generations != nil {
		if active := r.generations.ActiveGeneration(); active != nil {
			stateCopy.PluginStatuses = active.PluginRuntimeStatuses()
			stateCopy.CurrentRevision = active.Revision()
			if stateCopy.Metadata == nil {
				stateCopy.Metadata = make(map[string]string)
			}
			stateCopy.Metadata["current_revision"] = strconv.FormatInt(active.Revision(), 10)
			stateCopy.Metadata["generation_id"] = active.ID()
			stateCopy.Metadata["snapshot_hash"] = active.SnapshotHash()
			stateCopy.Metadata["provider_hash"] = active.ProviderHash()
		}
	}
	return stateCopy
}

func (r *Runtime) Apply(ctx context.Context, previous, next model.Snapshot) error {
	return r.activate(ctx, previous, next, true, 0, nil, "")
}

func (r *Runtime) ApplyWithSnapshotHash(ctx context.Context, previous, next model.Snapshot, snapshotHash string) error {
	return r.activate(ctx, previous, next, true, 0, nil, snapshotHash)
}

func (r *Runtime) ApplyWithDrainTimeout(ctx context.Context, previous, next model.Snapshot, drainTimeout time.Duration) error {
	return r.activate(ctx, previous, next, true, drainTimeout, nil, "")
}

func (r *Runtime) ApplyWithDrainTimeoutAndSnapshotHash(ctx context.Context, previous, next model.Snapshot, drainTimeout time.Duration, snapshotHash string) error {
	return r.activate(ctx, previous, next, true, drainTimeout, nil, snapshotHash)
}

func (r *Runtime) ApplyWithTrafficRuntime(ctx context.Context, previous, next model.Snapshot, drainTimeout time.Duration, config model.AgentConfig) error {
	return r.activate(ctx, previous, next, true, drainTimeout, &config, "")
}

func (r *Runtime) ApplyWithTrafficRuntimeAndSnapshotHash(ctx context.Context, previous, next model.Snapshot, drainTimeout time.Duration, config model.AgentConfig, snapshotHash string) error {
	return r.activate(ctx, previous, next, true, drainTimeout, &config, snapshotHash)
}

func (r *Runtime) Rollback(ctx context.Context, previous, next model.Snapshot) error {
	return r.activate(ctx, previous, next, false, 0, nil, "")
}

func (r *Runtime) activate(ctx context.Context, previous, next model.Snapshot, checkPrevious bool, drainTimeout time.Duration, trafficRuntime *model.AgentConfig, snapshotHash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	activeSnapshot := r.activeSnapshotLocked()
	if checkPrevious && !isZeroSnapshot(previous) && !snapshotEqual(previous, activeSnapshot) {
		r.state.Status = "error"
		return fmt.Errorf(
			"previous snapshot mismatch: expected %s got %s",
			describeSnapshot(activeSnapshot),
			describeSnapshot(previous),
		)
	}
	if err := ctx.Err(); err != nil {
		r.state.Status = "error"
		return err
	}

	if r.generations != nil {
		var cutover GenerationCutover
		var err error
		if trafficRuntime != nil {
			if snapshotHash == "" {
				cutover, err = r.generations.ApplyWithTrafficRuntime(ctx, previous, next, drainTimeout, *trafficRuntime)
			} else {
				cutover, err = r.generations.ApplyWithTrafficRuntimeAndSnapshotHash(ctx, previous, next, drainTimeout, *trafficRuntime, snapshotHash)
			}
		} else {
			if snapshotHash == "" {
				cutover, err = r.generations.ApplyWithDrainTimeout(ctx, previous, next, drainTimeout)
			} else {
				cutover, err = r.generations.ApplyWithDrainTimeoutAndSnapshotHash(ctx, previous, next, drainTimeout, snapshotHash)
			}
		}
		if err != nil {
			r.state.Status = "error"
			return err
		}
		if trafficRuntime != nil {
			r.setTrafficRuntimeLocked(*trafficRuntime)
		}
		r.setActiveSnapshotLocked(cutover.Active.Snapshot())
	} else {
		runtimeNext := next
		if trafficRuntime != nil {
			runtimeNext = overlayTrafficRuntime(runtimeNext, *trafficRuntime, true)
		}
		if err := r.activator(ctx, previous, runtimeNext); err != nil {
			r.state.Status = "error"
			return err
		}
		if err := ctx.Err(); err != nil {
			r.state.Status = "error"
			return err
		}
		if trafficRuntime != nil {
			r.setTrafficRuntimeLocked(*trafficRuntime)
		}
		r.setActiveSnapshotLocked(runtimeNext)
	}

	return nil
}

func (r *Runtime) ReconcileTrafficRuntime(ctx context.Context, config model.AgentConfig) error {
	if r == nil || config.TrafficStatsEnabled == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.generations != nil {
		if _, err := r.generations.ReconcileTrafficRuntime(ctx, config); err != nil {
			if config.TrafficBlocked {
				r.setTrafficRuntimeLocked(config)
			}
			return err
		}
	} else if !isZeroSnapshot(r.activeSnapshot) {
		previous := overlayTrafficRuntime(cloneSnapshot(r.activeSnapshot), r.trafficRuntime, r.trafficSet)
		next := overlayTrafficRuntime(cloneSnapshot(r.activeSnapshot), config, true)
		if err := r.activator(ctx, previous, next); err != nil {
			return err
		}
		r.activeSnapshot = next
	}
	r.setTrafficRuntimeLocked(config)
	return nil
}

func (r *Runtime) setTrafficRuntimeLocked(config model.AgentConfig) {
	r.trafficRuntime = model.AgentConfig{
		TrafficStatsEnabled: clonePtr(config.TrafficStatsEnabled),
		TrafficBlocked:      config.TrafficBlocked,
		TrafficBlockReason:  config.TrafficBlockReason,
	}
	r.trafficSet = config.TrafficStatsEnabled != nil
}

func overlayTrafficRuntime(snapshot model.Snapshot, config model.AgentConfig, set bool) model.Snapshot {
	if !set {
		return snapshot
	}
	snapshot.AgentConfig.TrafficStatsEnabled = clonePtr(config.TrafficStatsEnabled)
	snapshot.AgentConfig.TrafficBlocked = config.TrafficBlocked
	snapshot.AgentConfig.TrafficBlockReason = config.TrafficBlockReason
	return snapshot
}

func (r *Runtime) activeSnapshotLocked() model.Snapshot {
	if r.generations != nil {
		if active := r.generations.ActiveGeneration(); active != nil {
			return overlayTrafficRuntime(active.Snapshot(), r.trafficRuntime, r.trafficSet)
		}
	}
	return overlayTrafficRuntime(r.activeSnapshot, r.trafficRuntime, r.trafficSet)
}

func (r *Runtime) setActiveSnapshotLocked(next model.Snapshot) {
	if next.AgentConfig.TrafficStatsEnabled != nil {
		r.setTrafficRuntimeLocked(next.AgentConfig)
	}
	r.activeSnapshot = cloneSnapshot(next)
	r.state.Status = "active"
	r.state.CurrentRevision = next.Revision

	if r.state.Metadata == nil {
		r.state.Metadata = make(map[string]string)
	}
	r.state.Metadata["current_revision"] = strconv.FormatInt(next.Revision, 10)
}

func isZeroSnapshot(s model.Snapshot) bool {
	return s.DesiredVersion == "" &&
		s.Revision == 0 &&
		s.VersionPackage == nil &&
		!s.HasAgentConfig() &&
		len(s.Rules) == 0 &&
		len(s.L4Rules) == 0 &&
		len(s.RelayListeners) == 0 &&
		len(s.EgressProfiles) == 0 &&
		len(s.Certificates) == 0 &&
		len(s.CertificatePolicies) == 0 &&
		len(s.PluginGenerations) == 0 &&
		len(s.PluginDependencies) == 0 &&
		len(s.PluginPolicies) == 0 && len(s.Datasets) == 0
}

func snapshotEqual(left, right model.Snapshot) bool {
	return reflect.DeepEqual(left, right)
}

func cloneSnapshot(snapshot model.Snapshot) model.Snapshot {
	snapshot.Datasets = model.CloneDatasetSnapshots(snapshot.Datasets)
	cloned := snapshot
	cloned.AgentConfig.TrafficStatsEnabled = clonePtr(snapshot.AgentConfig.TrafficStatsEnabled)
	cloned.VersionPackage = clonePtr(snapshot.VersionPackage)
	cloned.DDNSConfig = clonePtr(snapshot.DDNSConfig)
	if snapshot.Rules != nil {
		cloned.Rules = slices.Clone(snapshot.Rules)
		for i, rule := range snapshot.Rules {
			cloned.Rules[i].Backends = slices.Clone(rule.Backends)
			cloned.Rules[i].CustomHeaders = slices.Clone(rule.CustomHeaders)
			cloned.Rules[i].TrustedProxyRanges = slices.Clone(rule.TrustedProxyRanges)
			cloned.Rules[i].EgressProfileID = clonePtr(rule.EgressProfileID)
			cloned.Rules[i].RelayChain = slices.Clone(rule.RelayChain)
			cloned.Rules[i].RelayLayers = cloneRelayLayers(rule.RelayLayers)
			cloned.Rules[i].Tags = slices.Clone(rule.Tags)
			cloned.Rules[i].PolicyRef = clonePolicyRef(rule.PolicyRef)
		}
	}
	if snapshot.L4Rules != nil {
		cloned.L4Rules = slices.Clone(snapshot.L4Rules)
		for i, rule := range snapshot.L4Rules {
			cloned.L4Rules[i].Backends = slices.Clone(rule.Backends)
			cloned.L4Rules[i].Tuning.ProxyProtocol.TrustedPeers = slices.Clone(rule.Tuning.ProxyProtocol.TrustedPeers)
			cloned.L4Rules[i].EgressProfileID = clonePtr(rule.EgressProfileID)
			cloned.L4Rules[i].RelayChain = slices.Clone(rule.RelayChain)
			cloned.L4Rules[i].RelayLayers = cloneRelayLayers(rule.RelayLayers)
			cloned.L4Rules[i].Tags = slices.Clone(rule.Tags)
			cloned.L4Rules[i].PolicyRef = clonePolicyRef(rule.PolicyRef)
		}
	}
	if snapshot.RelayListeners != nil {
		cloned.RelayListeners = slices.Clone(snapshot.RelayListeners)
		for i, listener := range snapshot.RelayListeners {
			cloned.RelayListeners[i].BindHosts = slices.Clone(listener.BindHosts)
			cloned.RelayListeners[i].CertificateID = clonePtr(listener.CertificateID)
			cloned.RelayListeners[i].PinSet = slices.Clone(listener.PinSet)
			cloned.RelayListeners[i].TrustedCACertificateIDs = slices.Clone(listener.TrustedCACertificateIDs)
			cloned.RelayListeners[i].Tags = slices.Clone(listener.Tags)
		}
	}
	if snapshot.EgressProfiles != nil {
		cloned.EgressProfiles = slices.Clone(snapshot.EgressProfiles)
	}
	if snapshot.Certificates != nil {
		cloned.Certificates = slices.Clone(snapshot.Certificates)
	}
	if snapshot.CertificatePolicies != nil {
		cloned.CertificatePolicies = slices.Clone(snapshot.CertificatePolicies)
		for i, policy := range snapshot.CertificatePolicies {
			cloned.CertificatePolicies[i].Tags = slices.Clone(policy.Tags)
		}
	}
	if snapshot.PluginPolicies != nil {
		cloned.PluginPolicies = slices.Clone(snapshot.PluginPolicies)
		for i, policy := range snapshot.PluginPolicies {
			cloned.PluginPolicies[i].Stages = slices.Clone(policy.Stages)
			for stageIndex, stage := range policy.Stages {
				clonedStage := &cloned.PluginPolicies[i].Stages[stageIndex]
				clonedStage.ExtensionPoints = slices.Clone(stage.ExtensionPoints)
				clonedStage.GrantedScopes = slices.Clone(stage.GrantedScopes)
				clonedStage.Config = slices.Clone(stage.Config)
			}
		}
	}
	if snapshot.PluginGenerations != nil {
		cloned.PluginGenerations = slices.Clone(snapshot.PluginGenerations)
		for i, generation := range snapshot.PluginGenerations {
			clonedGeneration := &cloned.PluginGenerations[i]
			clonedGeneration.Config = slices.Clone(generation.Config)
			clonedGeneration.ExtensionPoints = slices.Clone(generation.ExtensionPoints)
			clonedGeneration.RequiredFeatures = slices.Clone(generation.RequiredFeatures)
			clonedGeneration.HTTPBackendProviders = slices.Clone(generation.HTTPBackendProviders)
			clonedGeneration.Grants = slices.Clone(generation.Grants)
			clonedGeneration.SecretHandles = slices.Clone(generation.SecretHandles)
		}
	}
	if snapshot.PluginDependencies != nil {
		cloned.PluginDependencies = slices.Clone(snapshot.PluginDependencies)
	}
	return cloned
}

func clonePolicyRef(ref *model.PolicyRef) *model.PolicyRef {
	if ref == nil {
		return nil
	}
	cloned := *ref
	cloned.Overlay = slices.Clone(ref.Overlay)
	return &cloned
}

func clonePtr[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneRelayLayers(layers [][]int) [][]int {
	if layers == nil {
		return nil
	}
	cloned := make([][]int, len(layers))
	for i, layer := range layers {
		cloned[i] = slices.Clone(layer)
	}
	return cloned
}

func describeSnapshot(snapshot model.Snapshot) string {
	return fmt.Sprintf(
		"revision=%d desired_version=%q has_version_package=%t certificates=%d certificate_policies=%d",
		snapshot.Revision,
		snapshot.DesiredVersion,
		snapshot.VersionPackage != nil,
		len(snapshot.Certificates),
		len(snapshot.CertificatePolicies),
	)
}
