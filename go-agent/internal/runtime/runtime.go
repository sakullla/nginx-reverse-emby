package runtime

import (
	"context"
	"fmt"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type Runtime struct {
	mu             sync.RWMutex
	activeSnapshot model.Snapshot
	state          model.RuntimeState
	failNextApply  error
}

func New() *Runtime {
	return &Runtime{}
}

func (r *Runtime) ActiveSnapshot() model.Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.activeSnapshot
}

func (r *Runtime) State() model.RuntimeState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state
}

func (r *Runtime) SetFailNextApply(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failNextApply = err
}

func (r *Runtime) Apply(ctx context.Context, previous, next model.Snapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	_ = ctx

	if !isZeroSnapshot(previous) && previous != r.activeSnapshot {
		return fmt.Errorf("previous snapshot mismatch: expected %+v got %+v", r.activeSnapshot, previous)
	}

	if fail := r.failNextApply; fail != nil {
		r.failNextApply = nil
		r.state.Status = "error"
		return fail
	}

	r.activeSnapshot = next
	r.state.Status = "active"
	r.state.CurrentRevision = next.Revision

	return nil
}

func isZeroSnapshot(s model.Snapshot) bool {
	return s.DesiredVersion == "" && s.Revision == 0
}
