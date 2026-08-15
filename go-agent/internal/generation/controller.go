package generation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
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

// Terminal statuses remain available for drain reporting and diagnostics, but
// their heavyweight runtime resources are released and history is bounded.
const maxRetainedTerminalGenerations = 16

const (
	cleanupRetryBase = time.Second
	cleanupRetryMax  = 30 * time.Second
)

type drainEntry struct {
	generation       Generation
	status           model.GenerationDrainStatus
	revoked          map[EntityKey]string
	timer            Timer
	cleanupRetry     Timer
	cleanupAttempts  int
	lifecycleMu      sync.Mutex
	finalState       string
	destroyed        bool
	destroyDone      chan error
	released         bool
	destroyErr       error
	cleanupTimeout   time.Duration
	observabilityCtx context.Context
}
type DrainController struct {
	mu       sync.Mutex
	clock    Clock
	registry *SessionRegistry
	active   string
	entries  map[string]*drainEntry
	order    []string
	closed   bool
}

func NewDrainController(clock Clock) *DrainController {
	if clock == nil {
		clock = realClock{}
	}
	c := &DrainController{clock: clock, entries: make(map[string]*drainEntry)}
	c.registry = NewSessionRegistry(c.onSessionEmpty)
	return c
}
func (c *DrainController) Registry() *SessionRegistry { return c.registry }
func (c *DrainController) RegisterSession(g string, e EntityKey, id string, s Session) (*SessionHandle, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("generation drain is closed")
	}
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

func (c *DrainController) activate(ctx context.Context, next Generation, changes []EntityChange, timeout time.Duration) error {
	if c == nil || next.ID == "" || next.Resource == nil {
		return errors.New("invalid generation")
	}
	now := c.clock.Now()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("generation drain is closed")
	}
	if _, exists := c.entries[next.ID]; exists {
		c.mu.Unlock()
		return errors.New("duplicate generation")
	}
	previous := c.entries[c.active]
	if previous != nil && next.Revision <= previous.generation.Revision {
		c.mu.Unlock()
		return errors.New("generation revision must increase")
	}
	entry := &drainEntry{generation: next, observabilityCtx: ctx, status: model.GenerationDrainStatus{GenerationID: next.ID, Revision: next.Revision, State: model.GenerationDrainStateApplied, AppliedAt: now}}
	c.entries[next.ID] = entry
	c.order = append(c.order, next.ID)
	c.active = next.ID
	if previous != nil {
		previous.status.State = model.GenerationDrainStateDraining
		previous.status.DrainStartedAt = now
		previous.revoked = revokedEntities(changes)
		progressiveProviderDrain := timeout < 0
		if progressiveProviderDrain {
			timeout = -timeout
		}
		if timeout == 0 {
			timeout = time.Minute
		}
		previous.cleanupTimeout = timeout
		id := previous.generation.ID
		forceCtx := previous.observabilityCtx
		if forceCtx == nil {
			forceCtx = context.Background()
		}
		forceCtx = context.WithoutCancel(forceCtx)
		previous.timer = c.clock.AfterFunc(timeout, func() {
			if progressiveProviderDrain {
				_ = c.forceNonProviderSessions(forceCtx, id)
				return
			}
			_ = c.force(forceCtx, id, model.GenerationForceReasonTimeout)
		})
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

func (c *DrainController) forceNonProviderSessions(ctx context.Context, id string) error {
	c.mu.Lock()
	entry := c.entries[id]
	if entry == nil || entry.released || entry.status.State != model.GenerationDrainStateDraining {
		c.mu.Unlock()
		return nil
	}
	entry.timer = nil
	c.mu.Unlock()
	count, err := c.registry.ForceGenerationExceptProgressive(ctx, id, model.GenerationForceReasonTimeout)
	c.mu.Lock()
	entry.status.ForcedSessionCount += count
	entry.status.SessionCount = c.registry.GenerationCount(id)
	c.mu.Unlock()
	c.onEmpty(id)
	return err
}

func (c *DrainController) retireActive(ctx context.Context, id string, timeout time.Duration) error {
	if c == nil || strings.TrimSpace(id) == "" {
		return errors.New("active generation id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := c.clock.Now()
	c.mu.Lock()
	entry := c.entries[id]
	if c.closed {
		c.mu.Unlock()
		return errors.New("generation drain is closed")
	}
	if entry == nil || c.active != id {
		c.mu.Unlock()
		return errors.New("generation is not active")
	}
	if entry.status.State != model.GenerationDrainStateApplied {
		c.mu.Unlock()
		return fmt.Errorf("active generation is in state %q", entry.status.State)
	}
	if timeout <= 0 {
		timeout = time.Minute
	}
	entry.status.State = model.GenerationDrainStateDraining
	entry.status.DrainStartedAt = now
	entry.cleanupTimeout = timeout
	c.active = ""
	forceCtx := context.WithoutCancel(ctx)
	entry.timer = c.clock.AfterFunc(timeout, func() { _ = c.force(forceCtx, id, model.GenerationForceReasonTimeout) })
	c.mu.Unlock()
	c.onEmpty(id)
	return nil
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

func (c *DrainController) onSessionEmpty(id string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	entry := c.entries[id]
	if entry == nil || entry.status.State != model.GenerationDrainStateDraining || c.registry.GenerationCount(id) != 0 {
		c.mu.Unlock()
		return
	}
	if entry.timer != nil {
		entry.timer.Stop()
	}
	entry.status.State = model.GenerationDrainStateDrained
	entry.status.SessionCount = 0
	entry.finalState = model.GenerationDrainStateDrained
	c.mu.Unlock()
	// Resource cleanup may gracefully stop the server handling this session. Let
	// Finish return first so server shutdown never waits on its own request stack.
	go c.completeNaturalCleanup(entry)
}

func (c *DrainController) completeNaturalCleanup(entry *drainEntry) {
	entry.lifecycleMu.Lock()
	defer entry.lifecycleMu.Unlock()
	c.mu.Lock()
	if entry.released || entry.status.State != model.GenerationDrainStateDrained {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	err := c.completeCleanup(context.Background(), entry)
	c.observeDrainCompletion(entry, err)
	if err == nil {
		c.clearObservabilityContext(entry)
	}
}

func (c *DrainController) onEmpty(id string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	entry := c.entries[id]
	if entry == nil || entry.status.State != model.GenerationDrainStateDraining {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	entry.lifecycleMu.Lock()
	defer entry.lifecycleMu.Unlock()
	c.mu.Lock()
	if c.registry.GenerationCount(id) != 0 {
		c.mu.Unlock()
		return
	}
	if entry.status.State != model.GenerationDrainStateDraining {
		c.mu.Unlock()
		return
	}
	if entry.timer != nil {
		entry.timer.Stop()
	}
	entry.status.State = model.GenerationDrainStateDrained
	entry.status.SessionCount = 0
	entry.finalState = model.GenerationDrainStateDrained
	c.mu.Unlock()
	err := c.completeCleanup(context.Background(), entry)
	c.observeDrainCompletion(entry, err)
	if err == nil {
		c.clearObservabilityContext(entry)
	}
}
func (c *DrainController) enforceLimit(ctx context.Context) error {
	attempted := make(map[string]bool)
	var forceErr error
	for {
		c.mu.Lock()
		live := 0
		oldest := ""
		for _, id := range c.order {
			entry := c.entries[id]
			if entry == nil || entry.released {
				continue
			}
			live++
			if id != c.active && oldest == "" && !attempted[id] {
				oldest = id
			}
		}
		c.mu.Unlock()
		if live <= 2 {
			return forceErr
		}
		if oldest == "" {
			return errors.Join(forceErr, errors.New("generation limit cannot release another retired generation"))
		}
		attempted[oldest] = true
		forceErr = errors.Join(forceErr, c.force(ctx, oldest, model.GenerationForceReasonGenerationLimit))
	}
}
func (c *DrainController) forceGeneration(ctx context.Context, id, reason string) error {
	c.mu.Lock()
	entry := c.entries[id]
	c.mu.Unlock()
	if entry == nil {
		return nil
	}
	entry.lifecycleMu.Lock()
	defer entry.lifecycleMu.Unlock()
	c.mu.Lock()
	if entry.released {
		c.mu.Unlock()
		return nil
	}
	if entry.status.State == model.GenerationDrainStateCleanupFailed && c.registry.GenerationCount(id) == 0 {
		c.mu.Unlock()
		return c.completeCleanup(ctx, entry)
	}
	shutdown := reason == model.GenerationForceReasonShutdown
	if !shutdown && entry.status.State != model.GenerationDrainStateDraining && entry.status.State != model.GenerationDrainStateCleanupFailed {
		err := entry.destroyErr
		c.mu.Unlock()
		return err
	}
	if entry.timer != nil {
		entry.timer.Stop()
	}
	entry.status.State = model.GenerationDrainStateForced
	entry.status.ForceReason = reason
	entry.finalState = model.GenerationDrainStateForced
	c.mu.Unlock()
	count, closeErr := c.registry.ForceGeneration(ctx, id, reason)
	remaining := c.registry.GenerationCount(id)
	c.mu.Lock()
	entry.status.ForcedSessionCount = count
	entry.status.SessionCount = remaining
	if remaining != 0 {
		err := fmt.Errorf("generation still owns %d sessions after terminal force", remaining)
		entry.status.State = model.GenerationDrainStateCleanupFailed
		entry.status.CleanupError = err.Error()
		c.mu.Unlock()
		return errors.Join(closeErr, err)
	}
	c.mu.Unlock()
	destroyErr := c.completeCleanup(ctx, entry)
	return errors.Join(closeErr, destroyErr)
}

func (c *DrainController) RetryCleanup(ctx context.Context, id string) error {
	if c == nil {
		return errors.New("generation drain is not configured")
	}
	c.mu.Lock()
	entry := c.entries[id]
	c.mu.Unlock()
	if entry == nil {
		return errors.New("unknown generation")
	}
	entry.lifecycleMu.Lock()
	defer entry.lifecycleMu.Unlock()
	c.mu.Lock()
	if entry.status.State != model.GenerationDrainStateCleanupFailed {
		c.mu.Unlock()
		return errors.New("generation cleanup is not retryable")
	}
	if count := c.registry.GenerationCount(id); count != 0 {
		c.mu.Unlock()
		return fmt.Errorf("generation still owns %d sessions", count)
	}
	if entry.cleanupRetry != nil {
		entry.cleanupRetry.Stop()
		entry.cleanupRetry = nil
	}
	c.mu.Unlock()
	err := c.completeCleanup(ctx, entry)
	c.observeDrainCompletion(entry, err)
	if err == nil {
		c.clearObservabilityContext(entry)
	}
	return err
}

func (c *DrainController) completeCleanup(ctx context.Context, entry *drainEntry) error {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := entry.cleanupTimeout
	if timeout <= 0 {
		timeout = time.Minute
	}
	cleanupContext, cancelCleanup := context.WithTimeout(ctx, timeout)
	defer cancelCleanup()
	if !entry.destroyed {
		entry.destroyErr = destroyGenerationResource(cleanupContext, entry)
		if entry.destroyErr == nil {
			entry.destroyed = true
		}
	}
	err := entry.destroyErr
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		entry.status.State = model.GenerationDrainStateCleanupFailed
		entry.status.CleanupError = err.Error()
		entry.status.CompletedAt = time.Time{}
		c.scheduleCleanupRetryLocked(entry)
		return err
	}
	entry.status.State = entry.finalState
	entry.status.CleanupError = ""
	entry.status.CompletedAt = c.clock.Now()
	entry.released = true
	entry.generation.Resource = nil
	entry.revoked = nil
	entry.timer = nil
	if entry.cleanupRetry != nil {
		entry.cleanupRetry.Stop()
	}
	entry.cleanupRetry = nil
	entry.cleanupAttempts = 0
	entry.destroyDone = nil
	entry.destroyErr = nil
	entry.cleanupTimeout = 0
	c.pruneReleasedLocked()
	return nil
}

func (c *DrainController) scheduleCleanupRetryLocked(entry *drainEntry) {
	if entry == nil || entry.released || entry.cleanupRetry != nil || c.closed {
		return
	}
	delay := cleanupRetryBase
	for attempt := 0; attempt < entry.cleanupAttempts && delay < cleanupRetryMax; attempt++ {
		delay *= 2
		if delay > cleanupRetryMax {
			delay = cleanupRetryMax
		}
	}
	entry.cleanupAttempts++
	id := entry.generation.ID
	entry.cleanupRetry = c.clock.AfterFunc(delay, func() {
		c.mu.Lock()
		current := c.entries[id]
		if current != entry || entry.released {
			c.mu.Unlock()
			return
		}
		entry.cleanupRetry = nil
		observeCtx := entry.observabilityCtx
		c.mu.Unlock()
		if observeCtx == nil {
			observeCtx = context.Background()
		} else {
			observeCtx = context.WithoutCancel(observeCtx)
		}
		_ = c.RetryCleanup(observeCtx, id)
	})
}

// Close stops accepting sessions, terminally closes every registered session,
// and releases all generation resources, including the currently active view.
func (c *DrainController) Close(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	c.mu.Lock()
	c.closed = true
	ids := append([]string(nil), c.order...)
	for _, id := range ids {
		entry := c.entries[id]
		if entry == nil || entry.released {
			continue
		}
		if entry.timer != nil {
			entry.timer.Stop()
		}
		if entry.cleanupRetry != nil {
			entry.cleanupRetry.Stop()
			entry.cleanupRetry = nil
		}
	}
	c.active = ""
	c.mu.Unlock()

	var closeErr error
	for _, id := range ids {
		closeErr = errors.Join(closeErr, c.force(ctx, id, model.GenerationForceReasonShutdown))
	}
	return closeErr
}

func (c *DrainController) clearObservabilityContext(entry *drainEntry) {
	if entry == nil {
		return
	}
	c.mu.Lock()
	if entry.released {
		entry.observabilityCtx = nil
	}
	c.mu.Unlock()
}

func (c *DrainController) pruneReleasedLocked() {
	released := 0
	for _, id := range c.order {
		if entry := c.entries[id]; entry != nil && entry.released && id != c.active {
			released++
		}
	}
	remove := released - maxRetainedTerminalGenerations
	if remove <= 0 {
		return
	}
	order := make([]string, 0, len(c.order)-remove)
	for _, id := range c.order {
		entry := c.entries[id]
		if remove > 0 && entry != nil && entry.released && id != c.active {
			delete(c.entries, id)
			remove--
			continue
		}
		if entry != nil {
			order = append(order, id)
		}
	}
	c.order = order
}

func destroyGenerationResource(ctx context.Context, entry *drainEntry) error {
	resuming := entry.destroyDone != nil
	for {
		if entry.destroyDone == nil {
			if err := ctx.Err(); err != nil {
				return err
			}
			entry.destroyDone = make(chan error, 1)
			done := entry.destroyDone
			go func() {
				done <- entry.generation.Resource.Destroy(ctx)
			}()
			resuming = false
		}

		done := entry.destroyDone
		var err error
		select {
		case err = <-done:
		case <-ctx.Done():
			select {
			case err = <-done:
			default:
				return ctx.Err()
			}
		}
		entry.destroyDone = nil
		if err == nil {
			return nil
		}
		if !resuming {
			return err
		}
		resuming = false
	}
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
		if e == nil {
			continue
		}
		s := e.status
		s.SessionCount = c.registry.GenerationCount(id)
		out.Generations = append(out.Generations, s)
	}
	sort.SliceStable(out.Generations, func(i, j int) bool { return out.Generations[i].Revision < out.Generations[j].Revision })
	return out
}
