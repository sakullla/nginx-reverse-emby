package core

import (
	"context"
	"fmt"
	"reflect"
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
	if len(stateCopy.Metadata) > 0 {
		copied := make(map[string]string, len(stateCopy.Metadata))
		for k, v := range stateCopy.Metadata {
			copied[k] = v
		}
		stateCopy.Metadata = copied
	} else {
		stateCopy.Metadata = nil
	}

	return stateCopy
}

func (r *Runtime) Apply(ctx context.Context, previous, next model.Snapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	_ = ctx

	if !isZeroSnapshot(previous) && !snapshotEqual(previous, r.activeSnapshot) {
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

func (r *Runtime) Rollback(ctx context.Context, previous, next model.Snapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()

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
	if snapshot.AgentConfig.TrafficStatsEnabled != nil {
		value := *snapshot.AgentConfig.TrafficStatsEnabled
		cloned.AgentConfig.TrafficStatsEnabled = &value
	}
	if snapshot.VersionPackage != nil {
		copyValue := *snapshot.VersionPackage
		cloned.VersionPackage = &copyValue
	}
	if snapshot.Rules != nil {
		cloned.Rules = make([]model.HTTPRule, len(snapshot.Rules))
		copy(cloned.Rules, snapshot.Rules)
		for i, rule := range snapshot.Rules {
			if rule.Backends != nil {
				cloned.Rules[i].Backends = make([]model.HTTPBackend, len(rule.Backends))
				copy(cloned.Rules[i].Backends, rule.Backends)
			}
			if rule.CustomHeaders != nil {
				cloned.Rules[i].CustomHeaders = make([]model.HTTPHeader, len(rule.CustomHeaders))
				copy(cloned.Rules[i].CustomHeaders, rule.CustomHeaders)
			}
			if rule.WireGuardProfileID != nil {
				value := *rule.WireGuardProfileID
				cloned.Rules[i].WireGuardProfileID = &value
			}
			if rule.EgressProfileID != nil {
				value := *rule.EgressProfileID
				cloned.Rules[i].EgressProfileID = &value
			}
			cloned.Rules[i].RelayChain = append([]int(nil), rule.RelayChain...)
			cloned.Rules[i].RelayLayers = cloneRelayLayers(rule.RelayLayers)
			cloned.Rules[i].Tags = append([]string(nil), rule.Tags...)
		}
	}
	if snapshot.L4Rules != nil {
		cloned.L4Rules = make([]model.L4Rule, len(snapshot.L4Rules))
		copy(cloned.L4Rules, snapshot.L4Rules)
		for i, rule := range snapshot.L4Rules {
			if rule.Backends != nil {
				cloned.L4Rules[i].Backends = make([]model.L4Backend, len(rule.Backends))
				copy(cloned.L4Rules[i].Backends, rule.Backends)
			}
			if rule.WireGuardProfileID != nil {
				value := *rule.WireGuardProfileID
				cloned.L4Rules[i].WireGuardProfileID = &value
			}
			if rule.EgressProfileID != nil {
				value := *rule.EgressProfileID
				cloned.L4Rules[i].EgressProfileID = &value
			}
			cloned.L4Rules[i].RelayChain = append([]int(nil), rule.RelayChain...)
			cloned.L4Rules[i].RelayLayers = cloneRelayLayers(rule.RelayLayers)
			cloned.L4Rules[i].Tags = append([]string(nil), rule.Tags...)
		}
	}
	if snapshot.RelayListeners != nil {
		cloned.RelayListeners = make([]model.RelayListener, len(snapshot.RelayListeners))
		copy(cloned.RelayListeners, snapshot.RelayListeners)
		for i, listener := range snapshot.RelayListeners {
			if listener.BindHosts != nil {
				cloned.RelayListeners[i].BindHosts = make([]string, len(listener.BindHosts))
				copy(cloned.RelayListeners[i].BindHosts, listener.BindHosts)
			}
			if listener.CertificateID != nil {
				value := *listener.CertificateID
				cloned.RelayListeners[i].CertificateID = &value
			}
			if listener.WireGuardProfileID != nil {
				value := *listener.WireGuardProfileID
				cloned.RelayListeners[i].WireGuardProfileID = &value
			}
			if listener.PinSet != nil {
				cloned.RelayListeners[i].PinSet = make([]model.RelayPin, len(listener.PinSet))
				copy(cloned.RelayListeners[i].PinSet, listener.PinSet)
			}
			if listener.TrustedCACertificateIDs != nil {
				cloned.RelayListeners[i].TrustedCACertificateIDs = make([]int, len(listener.TrustedCACertificateIDs))
				copy(cloned.RelayListeners[i].TrustedCACertificateIDs, listener.TrustedCACertificateIDs)
			}
			if listener.Tags != nil {
				cloned.RelayListeners[i].Tags = make([]string, len(listener.Tags))
				copy(cloned.RelayListeners[i].Tags, listener.Tags)
			}
		}
	}
	if snapshot.WireGuardProfiles != nil {
		cloned.WireGuardProfiles = make([]model.WireGuardProfile, len(snapshot.WireGuardProfiles))
		copy(cloned.WireGuardProfiles, snapshot.WireGuardProfiles)
		for i, profile := range snapshot.WireGuardProfiles {
			if profile.BindAddresses != nil {
				cloned.WireGuardProfiles[i].BindAddresses = append([]string(nil), profile.BindAddresses...)
			}
			if profile.Addresses != nil {
				cloned.WireGuardProfiles[i].Addresses = append([]string(nil), profile.Addresses...)
			}
			if profile.DNS != nil {
				cloned.WireGuardProfiles[i].DNS = append([]string(nil), profile.DNS...)
			}
			if profile.Tags != nil {
				cloned.WireGuardProfiles[i].Tags = append([]string(nil), profile.Tags...)
			}
			if profile.Peers != nil {
				cloned.WireGuardProfiles[i].Peers = make([]model.WireGuardPeer, len(profile.Peers))
				copy(cloned.WireGuardProfiles[i].Peers, profile.Peers)
				for j, peer := range profile.Peers {
					if peer.AllowedIPs != nil {
						cloned.WireGuardProfiles[i].Peers[j].AllowedIPs = append([]string(nil), peer.AllowedIPs...)
					}
					if peer.Reserved != nil {
						cloned.WireGuardProfiles[i].Peers[j].Reserved = append([]byte(nil), peer.Reserved...)
					}
				}
			}
		}
	}
	if snapshot.EgressProfiles != nil {
		cloned.EgressProfiles = make([]model.EgressProfile, len(snapshot.EgressProfiles))
		copy(cloned.EgressProfiles, snapshot.EgressProfiles)
		for i, profile := range snapshot.EgressProfiles {
			if profile.WireGuardConfig == nil {
				continue
			}
			cfg := *profile.WireGuardConfig
			cfg.Addresses = append([]string(nil), profile.WireGuardConfig.Addresses...)
			cfg.Peers = make([]model.WireGuardPeer, len(profile.WireGuardConfig.Peers))
			copy(cfg.Peers, profile.WireGuardConfig.Peers)
			for j, peer := range profile.WireGuardConfig.Peers {
				if peer.AllowedIPs != nil {
					cfg.Peers[j].AllowedIPs = append([]string(nil), peer.AllowedIPs...)
				}
				if peer.Reserved != nil {
					cfg.Peers[j].Reserved = append([]byte(nil), peer.Reserved...)
				}
			}
			cfg.DNS = append([]string(nil), profile.WireGuardConfig.DNS...)
			cloned.EgressProfiles[i].WireGuardConfig = &cfg
		}
	}
	if snapshot.Certificates != nil {
		cloned.Certificates = make([]model.ManagedCertificateBundle, len(snapshot.Certificates))
		copy(cloned.Certificates, snapshot.Certificates)
	}
	if snapshot.CertificatePolicies != nil {
		cloned.CertificatePolicies = make([]model.ManagedCertificatePolicy, len(snapshot.CertificatePolicies))
		for i, policy := range snapshot.CertificatePolicies {
			clonedPolicy := policy
			if policy.Tags != nil {
				clonedPolicy.Tags = make([]string, len(policy.Tags))
				copy(clonedPolicy.Tags, policy.Tags)
			}
			cloned.CertificatePolicies[i] = clonedPolicy
		}
	}
	return cloned
}

func cloneRelayLayers(layers [][]int) [][]int {
	if layers == nil {
		return nil
	}
	cloned := make([][]int, len(layers))
	for i, layer := range layers {
		cloned[i] = append([]int(nil), layer...)
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
