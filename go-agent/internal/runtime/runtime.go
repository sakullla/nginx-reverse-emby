package runtime

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/l4"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
)

type Activator func(ctx context.Context, previous, next model.Snapshot) error

type SnapshotActivationHandlers struct {
	ActivateManagedCertificates func(context.Context, []model.ManagedCertificateBundle, []model.ManagedCertificatePolicy) error
	ActivateHTTPRules           func(context.Context, []model.HTTPRule, []model.RelayListener) error
	ActivateRelayListeners      func(context.Context, []model.RelayListener) error
	ActivateL4Rules             func(context.Context, []model.L4Rule, []model.RelayListener) error
}

func NewSnapshotActivator(handlers SnapshotActivationHandlers) Activator {
	return func(ctx context.Context, previous, next model.Snapshot) error {
		if certificatesChanged(previous, next) && handlers.ActivateManagedCertificates != nil {
			if err := handlers.ActivateManagedCertificates(ctx, next.Certificates, next.CertificatePolicies); err != nil {
				return err
			}
		}

		if (httpRulesChanged(previous, next) || httpRelayInputsChanged(previous, next)) && handlers.ActivateHTTPRules != nil {
			if err := handlers.ActivateHTTPRules(ctx, next.Rules, next.RelayListeners); err != nil {
				return err
			}
		}

		if relay.ListenersChanged(previous.RelayListeners, next.RelayListeners) && handlers.ActivateRelayListeners != nil {
			if err := handlers.ActivateRelayListeners(ctx, next.RelayListeners); err != nil {
				return err
			}
		}

		if (l4RulesChanged(previous, next) || l4.RelayInputsChanged(next.L4Rules, previous.RelayListeners, next.RelayListeners)) &&
			handlers.ActivateL4Rules != nil {
			if err := handlers.ActivateL4Rules(ctx, next.L4Rules, next.RelayListeners); err != nil {
				return err
			}
		}

		return nil
	}
}

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
		s.VersionPackage == nil &&
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
			if rule.RelayChain != nil {
				cloned.Rules[i].RelayChain = make([]int, len(rule.RelayChain))
				copy(cloned.Rules[i].RelayChain, rule.RelayChain)
			}
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
			if listener.BindHosts != nil {
				cloned.RelayListeners[i].BindHosts = make([]string, len(listener.BindHosts))
				copy(cloned.RelayListeners[i].BindHosts, listener.BindHosts)
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
		"revision=%d desired_version=%q has_version_package=%t certificates=%d certificate_policies=%d",
		snapshot.Revision,
		snapshot.DesiredVersion,
		snapshot.VersionPackage != nil,
		len(snapshot.Certificates),
		len(snapshot.CertificatePolicies),
	)
}

func certificatesChanged(previous, next model.Snapshot) bool {
	return !reflect.DeepEqual(previous.Certificates, next.Certificates) ||
		!reflect.DeepEqual(previous.CertificatePolicies, next.CertificatePolicies)
}

func httpRulesChanged(previous, next model.Snapshot) bool {
	return !reflect.DeepEqual(previous.Rules, next.Rules)
}

func l4RulesChanged(previous, next model.Snapshot) bool {
	return !reflect.DeepEqual(previous.L4Rules, next.L4Rules)
}

func httpRelayInputsChanged(previous, next model.Snapshot) bool {
	for _, rule := range next.Rules {
		for _, listenerID := range rule.RelayChain {
			if relayListenerChangedByID(listenerID, previous.RelayListeners, next.RelayListeners) {
				return true
			}
		}
	}
	return false
}

func relayListenerChangedByID(listenerID int, previous, next []model.RelayListener) bool {
	previousListener, previousOK := relayListenerByID(listenerID, previous)
	nextListener, nextOK := relayListenerByID(listenerID, next)
	if previousOK != nextOK {
		return true
	}
	if !previousOK {
		return false
	}
	return !reflect.DeepEqual(previousListener, nextListener)
}

func relayListenerByID(listenerID int, listeners []model.RelayListener) (model.RelayListener, bool) {
	for _, listener := range listeners {
		if listener.ID == listenerID {
			return listener, true
		}
	}
	return model.RelayListener{}, false
}
