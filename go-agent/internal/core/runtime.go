package core

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type Activator func(ctx context.Context, previous, next model.Snapshot) error

type Runtime struct {
	mu             sync.RWMutex
	activeSnapshot model.Snapshot
	state          model.RuntimeState
	activator      Activator
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
	return cloneSnapshot(r.activeSnapshot)
}

func (r *Runtime) State() model.RuntimeState {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stateCopy := r.state
	stateCopy.Metadata = cloneStringMap(stateCopy.Metadata)
	return stateCopy
}

func (r *Runtime) Apply(ctx context.Context, previous, next model.Snapshot) error {
	return r.activate(ctx, previous, next, true)
}

func (r *Runtime) Rollback(ctx context.Context, previous, next model.Snapshot) error {
	return r.activate(ctx, previous, next, false)
}

func (r *Runtime) activate(ctx context.Context, previous, next model.Snapshot, checkPrevious bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if checkPrevious && !isZeroSnapshot(previous) && !snapshotEqual(previous, r.activeSnapshot) {
		r.state.Status = "error"
		return fmt.Errorf(
			"previous snapshot mismatch: expected %s got %s",
			describeSnapshot(r.activeSnapshot),
			describeSnapshot(previous),
		)
	}

	if err := r.activator(ctx, previous, next); err != nil {
		r.state.Status = "error"
		return err
	}

	r.setActiveSnapshotLocked(next)

	return nil
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
		len(s.WireGuardProfiles) == 0 &&
		len(s.EgressProfiles) == 0 &&
		len(s.Certificates) == 0 &&
		len(s.CertificatePolicies) == 0
}

func snapshotEqual(left, right model.Snapshot) bool {
	return reflect.DeepEqual(left, right)
}

func cloneSnapshot(snapshot model.Snapshot) model.Snapshot {
	cloned := snapshot
	cloned.AgentConfig.TrafficStatsEnabled = clonePtr(snapshot.AgentConfig.TrafficStatsEnabled)
	cloned.VersionPackage = clonePtr(snapshot.VersionPackage)
	if snapshot.Rules != nil {
		cloned.Rules = slices.Clone(snapshot.Rules)
		for i, rule := range snapshot.Rules {
			cloned.Rules[i].Backends = slices.Clone(rule.Backends)
			cloned.Rules[i].CustomHeaders = slices.Clone(rule.CustomHeaders)
			cloned.Rules[i].WireGuardProfileID = clonePtr(rule.WireGuardProfileID)
			cloned.Rules[i].EgressProfileID = clonePtr(rule.EgressProfileID)
			cloned.Rules[i].RelayChain = slices.Clone(rule.RelayChain)
			cloned.Rules[i].RelayLayers = cloneRelayLayers(rule.RelayLayers)
			cloned.Rules[i].Tags = slices.Clone(rule.Tags)
		}
	}
	if snapshot.L4Rules != nil {
		cloned.L4Rules = slices.Clone(snapshot.L4Rules)
		for i, rule := range snapshot.L4Rules {
			cloned.L4Rules[i].Backends = slices.Clone(rule.Backends)
			cloned.L4Rules[i].WireGuardProfileID = clonePtr(rule.WireGuardProfileID)
			cloned.L4Rules[i].EgressProfileID = clonePtr(rule.EgressProfileID)
			cloned.L4Rules[i].RelayChain = slices.Clone(rule.RelayChain)
			cloned.L4Rules[i].RelayLayers = cloneRelayLayers(rule.RelayLayers)
			cloned.L4Rules[i].Tags = slices.Clone(rule.Tags)
		}
	}
	if snapshot.RelayListeners != nil {
		cloned.RelayListeners = slices.Clone(snapshot.RelayListeners)
		for i, listener := range snapshot.RelayListeners {
			cloned.RelayListeners[i].BindHosts = slices.Clone(listener.BindHosts)
			cloned.RelayListeners[i].CertificateID = clonePtr(listener.CertificateID)
			cloned.RelayListeners[i].WireGuardProfileID = clonePtr(listener.WireGuardProfileID)
			cloned.RelayListeners[i].PinSet = slices.Clone(listener.PinSet)
			cloned.RelayListeners[i].TrustedCACertificateIDs = slices.Clone(listener.TrustedCACertificateIDs)
			cloned.RelayListeners[i].Tags = slices.Clone(listener.Tags)
		}
	}
	if snapshot.WireGuardProfiles != nil {
		cloned.WireGuardProfiles = slices.Clone(snapshot.WireGuardProfiles)
		for i, profile := range snapshot.WireGuardProfiles {
			cloned.WireGuardProfiles[i].BindAddresses = slices.Clone(profile.BindAddresses)
			cloned.WireGuardProfiles[i].Addresses = slices.Clone(profile.Addresses)
			cloned.WireGuardProfiles[i].DNS = slices.Clone(profile.DNS)
			cloned.WireGuardProfiles[i].Tags = slices.Clone(profile.Tags)
			cloned.WireGuardProfiles[i].Peers = cloneWireGuardPeers(profile.Peers)
		}
	}
	if snapshot.EgressProfiles != nil {
		cloned.EgressProfiles = slices.Clone(snapshot.EgressProfiles)
		for i, profile := range snapshot.EgressProfiles {
			cloned.EgressProfiles[i].WireGuardConfig = cloneEgressWireGuardConfig(profile.WireGuardConfig)
		}
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
	return cloned
}

func clonePtr[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneWireGuardPeers(peers []model.WireGuardPeer) []model.WireGuardPeer {
	cloned := slices.Clone(peers)
	for i, peer := range peers {
		cloned[i].AllowedIPs = slices.Clone(peer.AllowedIPs)
		cloned[i].Reserved = slices.Clone(peer.Reserved)
	}
	return cloned
}

func cloneEgressWireGuardConfig(config *model.EgressWireGuardConfig) *model.EgressWireGuardConfig {
	if config == nil {
		return nil
	}
	cloned := *config
	cloned.Addresses = slices.Clone(config.Addresses)
	cloned.Peers = cloneWireGuardPeers(config.Peers)
	cloned.DNS = slices.Clone(config.DNS)
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
