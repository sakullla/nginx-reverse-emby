package runtime

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

func New() *Runtime {
	return NewWithActivator(nil)
}

func NewWithActivator(act Activator) *Runtime {
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
	return NewWithActivator(act)
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

	r.activeSnapshot = cloneSnapshot(next)
	r.state.Status = "active"
	r.state.CurrentRevision = next.Revision

	if r.state.Metadata == nil {
		r.state.Metadata = make(map[string]string)
	}
	r.state.Metadata["current_revision"] = strconv.FormatInt(next.Revision, 10)

	return nil
}

func isZeroSnapshot(s model.Snapshot) bool {
	return s.DesiredVersion == "" &&
		s.Revision == 0 &&
		len(s.Rules) == 0 &&
		len(s.L4Rules) == 0 &&
		len(s.RelayListeners) == 0 &&
		len(s.Certificates) == 0 &&
		len(s.CertificatePolicies) == 0
}

func snapshotEqual(left, right model.Snapshot) bool {
	return reflect.DeepEqual(left, right)
}

func cloneSnapshot(snapshot model.Snapshot) model.Snapshot {
	cloned := snapshot
	if snapshot.Rules != nil {
		cloned.Rules = make([]model.HTTPRule, len(snapshot.Rules))
		copy(cloned.Rules, snapshot.Rules)
		for i, rule := range snapshot.Rules {
			if rule.CustomHeaders != nil {
				cloned.Rules[i].CustomHeaders = make([]model.HTTPHeader, len(rule.CustomHeaders))
				copy(cloned.Rules[i].CustomHeaders, rule.CustomHeaders)
			}
		}
	}
	if snapshot.L4Rules != nil {
		cloned.L4Rules = make([]model.L4Rule, len(snapshot.L4Rules))
		copy(cloned.L4Rules, snapshot.L4Rules)
		for i, rule := range snapshot.L4Rules {
			if rule.RelayChain != nil {
				cloned.L4Rules[i].RelayChain = make([]int, len(rule.RelayChain))
				copy(cloned.L4Rules[i].RelayChain, rule.RelayChain)
			}
		}
	}
	if snapshot.RelayListeners != nil {
		cloned.RelayListeners = make([]model.RelayListener, len(snapshot.RelayListeners))
		copy(cloned.RelayListeners, snapshot.RelayListeners)
		for i, listener := range snapshot.RelayListeners {
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

func describeSnapshot(snapshot model.Snapshot) string {
	return fmt.Sprintf(
		"revision=%d desired_version=%q certificates=%d certificate_policies=%d",
		snapshot.Revision,
		snapshot.DesiredVersion,
		len(snapshot.Certificates),
		len(snapshot.CertificatePolicies),
	)
}
