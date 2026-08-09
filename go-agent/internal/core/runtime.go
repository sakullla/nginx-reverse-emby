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
	if !r.UsesGenerationManager() {
		return GenerationIdentity{}, false, nil
	}
	identity, err := r.generations.CandidateIdentity(previous, next)
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
	if r != nil && r.generations != nil {
		if active := r.generations.ActiveGeneration(); active != nil {
			return active.Snapshot()
		}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneSnapshot(r.activeSnapshot)
}

func (r *Runtime) State() model.RuntimeState {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stateCopy := r.state
	stateCopy.Metadata = cloneStringMap(stateCopy.Metadata)
	if r.generations != nil {
		if active := r.generations.ActiveGeneration(); active != nil {
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
	return r.activate(ctx, previous, next, true, 0)
}

func (r *Runtime) ApplyWithDrainTimeout(ctx context.Context, previous, next model.Snapshot, drainTimeout time.Duration) error {
	return r.activate(ctx, previous, next, true, drainTimeout)
}

func (r *Runtime) Rollback(ctx context.Context, previous, next model.Snapshot) error {
	return r.activate(ctx, previous, next, false, 0)
}

func (r *Runtime) activate(ctx context.Context, previous, next model.Snapshot, checkPrevious bool, drainTimeout time.Duration) error {
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
		cutover, err := r.generations.ApplyWithDrainTimeout(ctx, previous, next, drainTimeout)
		if err != nil {
			r.state.Status = "error"
			return err
		}
		r.setActiveSnapshotLocked(cutover.Active.Snapshot())
	} else {
		if err := r.activator(ctx, previous, next); err != nil {
			r.state.Status = "error"
			return err
		}
		if err := ctx.Err(); err != nil {
			r.state.Status = "error"
			return err
		}
		r.setActiveSnapshotLocked(next)
	}

	return nil
}

func (r *Runtime) activeSnapshotLocked() model.Snapshot {
	if r.generations != nil {
		if active := r.generations.ActiveGeneration(); active != nil {
			return active.Snapshot()
		}
	}
	return r.activeSnapshot
}

func (r *Runtime) setActiveSnapshotLocked(next model.Snapshot) {
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
		len(s.PluginPolicies) == 0
}

func snapshotEqual(left, right model.Snapshot) bool {
	return reflect.DeepEqual(left, right)
}

func cloneSnapshot(snapshot model.Snapshot) model.Snapshot {
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
