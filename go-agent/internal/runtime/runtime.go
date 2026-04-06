package runtime

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type activationFunc func(previous, next model.Snapshot) error

type Runtime struct {
	mu             sync.RWMutex
	activeSnapshot model.Snapshot
	state          model.RuntimeState
	activator      activationFunc
}

func New() *Runtime {
	return newRuntimeWithActivator(defaultActivator)
}

func newRuntimeWithActivator(act activationFunc) *Runtime {
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

func defaultActivator(previous, next model.Snapshot) error {
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

	if err := r.activator(previous, next); err != nil {
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
		len(s.Certificates) == 0 &&
		len(s.CertificatePolicies) == 0
}

func snapshotEqual(left, right model.Snapshot) bool {
	return reflect.DeepEqual(left, right)
}

func cloneSnapshot(snapshot model.Snapshot) model.Snapshot {
	cloned := snapshot
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
