package runtime

import (
	"context"
	"fmt"
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
	return r.activeSnapshot
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

	if !isZeroSnapshot(previous) && previous != r.activeSnapshot {
		r.state.Status = "error"
		return fmt.Errorf("previous snapshot mismatch: expected %+v got %+v", r.activeSnapshot, previous)
	}

	if err := r.activator(previous, next); err != nil {
		r.state.Status = "error"
		return err
	}

	r.activeSnapshot = next
	r.state.Status = "active"
	r.state.CurrentRevision = next.Revision

	if r.state.Metadata == nil {
		r.state.Metadata = make(map[string]string)
	}
	r.state.Metadata["current_revision"] = strconv.FormatInt(next.Revision, 10)

	return nil
}

func isZeroSnapshot(s model.Snapshot) bool {
	return s.DesiredVersion == "" && s.Revision == 0
}
