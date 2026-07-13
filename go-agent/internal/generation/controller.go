package generation

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type Resource interface{ Destroy(context.Context) error }
type Generation struct {
	ID       string
	Revision int64
	Resource Resource
}
type EntityAction string

const (
	EntityModified EntityAction = "modified"
	EntityDeleted  EntityAction = "deleted"
	EntityDisabled EntityAction = "disabled"
)

type EntityChange struct {
	Entity EntityKey
	Action EntityAction
}
type Timer interface{ Stop() bool }
type Clock interface {
	Now() time.Time
	AfterFunc(time.Duration, func()) Timer
}
type realClock struct{}

func (realClock) Now() time.Time                             { return time.Now() }
func (realClock) AfterFunc(d time.Duration, fn func()) Timer { return time.AfterFunc(d, fn) }

type drainEntry struct {
	generation  Generation
	status      model.GenerationDrainStatus
	revoked     map[EntityKey]string
	timer       Timer
	destroyOnce sync.Once
	destroyErr  error
}
type DrainController struct {
	mu       sync.Mutex
	clock    Clock
	registry *SessionRegistry
	active   string
	entries  map[string]*drainEntry
	order    []string
}

func NewDrainController(clock Clock) *DrainController {
	if clock == nil {
		clock = realClock{}
	}
	c := &DrainController{clock: clock, entries: make(map[string]*drainEntry)}
	c.registry = NewSessionRegistry(c.onEmpty)
	return c
}
func (c *DrainController) Registry() *SessionRegistry { return c.registry }
func (c *DrainController) RegisterSession(g string, e EntityKey, id string, s Session) (*SessionHandle, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[g]
	if entry == nil {
		return nil, errors.New("unknown generation")
	}
	if entry.status.State != model.GenerationDrainStateApplied && entry.status.State != model.GenerationDrainStateDraining {
		return nil, errors.New("generation no longer accepts sessions")
	}
	if reason := entry.revoked[e]; reason != "" {
		return nil, errors.New("entity no longer accepts sessions: " + reason)
	}
	return c.registry.Register(g, e, id, s)
}

func (c *DrainController) Activate(ctx context.Context, next Generation, changes []EntityChange, timeout time.Duration) error {
	if c == nil || next.ID == "" || next.Resource == nil {
		return errors.New("invalid generation")
	}
	now := c.clock.Now()
	c.mu.Lock()
	if _, exists := c.entries[next.ID]; exists {
		c.mu.Unlock()
		return errors.New("duplicate generation")
	}
	previous := c.entries[c.active]
	if previous != nil && next.Revision <= previous.generation.Revision {
		c.mu.Unlock()
		return errors.New("generation revision must increase")
	}
	entry := &drainEntry{generation: next, status: model.GenerationDrainStatus{GenerationID: next.ID, Revision: next.Revision, State: model.GenerationDrainStateApplied, AppliedAt: now}}
	c.entries[next.ID] = entry
	c.order = append(c.order, next.ID)
	c.active = next.ID
	if previous != nil {
		previous.status.State = model.GenerationDrainStateDraining
		previous.status.DrainStartedAt = now
		previous.revoked = revokedEntities(changes)
		if timeout <= 0 {
			timeout = time.Minute
		}
		id := previous.generation.ID
		previous.timer = c.clock.AfterFunc(timeout, func() { _ = c.force(context.Background(), id, model.GenerationForceReasonTimeout) })
	}
	c.mu.Unlock()
	var drainErr error
	if previous != nil {
		if len(previous.revoked) > 0 {
			_, drainErr = c.registry.ForceEntities(ctx, previous.generation.ID, previous.revoked)
		}
		c.onEmpty(previous.generation.ID)
	}
	return errors.Join(drainErr, c.enforceLimit(ctx))
}

func revokedEntities(changes []EntityChange) map[EntityKey]string {
	var revoked map[EntityKey]string
	for _, change := range changes {
		var reason string
		switch change.Action {
		case EntityDeleted:
			reason = model.GenerationForceReasonEntityDeleted
		case EntityDisabled:
			reason = model.GenerationForceReasonEntityDisabled
		}
		if reason != "" {
			if revoked == nil {
				revoked = make(map[EntityKey]string)
			}
			revoked[change.Entity] = reason
		}
	}
	return revoked
}

func (c *DrainController) onEmpty(id string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.registry.GenerationCount(id) != 0 {
		c.mu.Unlock()
		return
	}
	entry := c.entries[id]
	if entry == nil || entry.status.State != model.GenerationDrainStateDraining {
		c.mu.Unlock()
		return
	}
	if entry.timer != nil {
		entry.timer.Stop()
	}
	entry.status.State = model.GenerationDrainStateDrained
	entry.status.SessionCount = 0
	c.mu.Unlock()
	_ = c.destroy(context.Background(), entry)
	c.mu.Lock()
	entry.status.CompletedAt = c.clock.Now()
	c.mu.Unlock()
}
func (c *DrainController) enforceLimit(ctx context.Context) error {
	c.mu.Lock()
	var draining []string
	for _, id := range c.order {
		if e := c.entries[id]; e != nil && e.status.State == model.GenerationDrainStateDraining {
			draining = append(draining, id)
		}
	}
	c.mu.Unlock()
	var err error
	for len(draining) > 1 {
		err = errors.Join(err, c.force(ctx, draining[0], model.GenerationForceReasonGenerationLimit))
		draining = draining[1:]
	}
	return err
}
func (c *DrainController) force(ctx context.Context, id, reason string) error {
	c.mu.Lock()
	entry := c.entries[id]
	if entry == nil || entry.status.State != model.GenerationDrainStateDraining {
		c.mu.Unlock()
		return nil
	}
	if entry.timer != nil {
		entry.timer.Stop()
	}
	entry.status.State = model.GenerationDrainStateForced
	entry.status.ForceReason = reason
	c.mu.Unlock()
	count, closeErr := c.registry.ForceGeneration(ctx, id, reason)
	destroyErr := c.destroy(ctx, entry)
	c.mu.Lock()
	entry.status.ForcedSessionCount = count
	entry.status.SessionCount = 0
	entry.status.CompletedAt = c.clock.Now()
	c.mu.Unlock()
	return errors.Join(closeErr, destroyErr)
}
func (c *DrainController) destroy(ctx context.Context, e *drainEntry) error {
	e.destroyOnce.Do(func() { e.destroyErr = e.generation.Resource.Destroy(ctx) })
	return e.destroyErr
}
func (c *DrainController) Snapshot() model.GenerationDrainSnapshot {
	if c == nil {
		return model.GenerationDrainSnapshot{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := model.GenerationDrainSnapshot{ActiveGenerationID: c.active}
	for _, id := range c.order {
		e := c.entries[id]
		s := e.status
		s.SessionCount = c.registry.GenerationCount(id)
		out.Generations = append(out.Generations, s)
	}
	sort.SliceStable(out.Generations, func(i, j int) bool { return out.Generations[i].Revision < out.Generations[j].Revision })
	return out
}
