package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

type GenerationCutover struct {
	Active   *module.GenerationView
	Previous *module.GenerationView
	DrainErr error
}

type GenerationIdentity struct {
	ID           string
	Revision     int64
	SnapshotHash string
}

// GenerationManager serializes prepare/readiness/cutover. The source owns the
// single atomic GenerationView slot; the manager retains prior views so a
// drain owner can release their resources after existing sessions finish.
type GenerationManager struct {
	mu              sync.Mutex
	source          module.GenerationPreparer
	retired         []*module.GenerationView
	drain           *GenerationDrain
	timeout         time.Duration
	publicationMu   sync.Mutex
	publicationDone chan struct{}
	publicationID   string
	sessions        generationSessionRegistrar
}

type generationSessionRegistrar interface {
	RegisterSession(string, generation.EntityKey, string, generation.Session) (*generation.SessionHandle, error)
}

func NewGenerationManager(source module.GenerationPreparer) *GenerationManager {
	return &GenerationManager{source: source}
}

func (m *GenerationManager) CandidateIdentity(previous, next model.Snapshot) (GenerationIdentity, error) {
	if m == nil || m.source == nil {
		return GenerationIdentity{}, errors.New("generation source is not configured")
	}
	generationContext, err := module.NewGenerationContext(previous, next)
	if err != nil {
		return GenerationIdentity{}, err
	}
	return GenerationIdentity{
		ID:           generationContext.ID(),
		Revision:     generationContext.Revision(),
		SnapshotHash: generationContext.SnapshotHash(),
	}, nil
}

func (m *GenerationManager) ActiveIdentity() GenerationIdentity {
	active := m.ActiveGeneration()
	if active == nil {
		return GenerationIdentity{}
	}
	return GenerationIdentity{ID: active.ID(), Revision: active.Revision(), SnapshotHash: active.SnapshotHash()}
}

func NewManagedGenerationManager(source module.GenerationPreparer, drain *GenerationDrain, timeout time.Duration) *GenerationManager {
	if drain == nil {
		drain = NewGenerationDrain(nil)
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	return &GenerationManager{source: source, drain: drain, timeout: timeout, sessions: drain.Controller()}
}

func (m *GenerationManager) apply(ctx context.Context, previous, next model.Snapshot, drainTimeout time.Duration, trafficRuntime *model.AgentConfig) (GenerationCutover, error) {
	if m == nil || m.source == nil {
		return GenerationCutover{}, errors.New("generation source is not configured")
	}
	if validator, ok := m.source.(interface{ ValidateGenerationCompatibility() error }); ok {
		if err := validator.ValidateGenerationCompatibility(); err != nil {
			return GenerationCutover{}, fmt.Errorf("generation compatibility: %w", err)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return GenerationCutover{}, fmt.Errorf("generation activation context: %w", err)
	}

	generationContext, err := module.NewGenerationContext(previous, next)
	if err != nil {
		return GenerationCutover{}, err
	}
	if trafficRuntime != nil {
		generationContext = generationContext.WithTrafficRuntimeConfig(*trafficRuntime)
	}
	if active := m.source.ActiveGeneration(); active != nil && active.ID() == generationContext.ID() {
		return GenerationCutover{Active: active}, nil
	}
	candidate, err := m.source.PrepareGeneration(ctx, generationContext)
	if err != nil {
		return GenerationCutover{}, fmt.Errorf("prepare generation %s: %w", generationContext.ID(), err)
	}
	if err := candidate.Ready(ctx); err != nil {
		destroyErr := candidate.Destroy(ctx)
		return GenerationCutover{}, errors.Join(
			fmt.Errorf("generation %s readiness: %w", generationContext.ID(), err),
			destroyErr,
		)
	}
	if m.drain != nil {
		if err := validateGenerationDrain(m.drain, generationContext, m.source.ActiveGeneration()); err != nil {
			destroyErr := candidate.Destroy(ctx)
			return GenerationCutover{}, errors.Join(
				fmt.Errorf("validate generation drain: %w", err),
				destroyErr,
			)
		}
	}
	if err := ctx.Err(); err != nil {
		destroyErr := candidate.Destroy(context.WithoutCancel(ctx))
		return GenerationCutover{}, errors.Join(
			fmt.Errorf("generation %s activation deadline: %w", generationContext.ID(), err),
			destroyErr,
		)
	}
	if preparer, ok := candidate.(interface {
		PreparePublication(context.Context) error
	}); ok {
		if err := preparer.PreparePublication(ctx); err != nil {
			destroyErr := candidate.Destroy(context.WithoutCancel(ctx))
			return GenerationCutover{}, errors.Join(
				fmt.Errorf("generation %s publication preparation: %w", generationContext.ID(), err),
				destroyErr,
			)
		}
	}
	publicationDone := m.beginPublication(generationContext.ID())
	active, retired := candidate.Publish()
	if retired != nil && m.drain == nil {
		m.retired = append(m.retired, retired)
	}
	cutover := GenerationCutover{Active: active, Previous: retired}
	if m.drain != nil {
		if drainTimeout <= 0 {
			drainTimeout = m.timeout
		}
		cutover.DrainErr = m.drain.Activate(ctx, cutover, generationEntityChanges(previous, next), drainTimeout)
	}
	m.endPublication(publicationDone)
	return cutover, nil
}

func (m *GenerationManager) ReconcileTrafficRuntime(ctx context.Context, config model.AgentConfig) (bool, error) {
	if m == nil || m.source == nil {
		return false, errors.New("generation source is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	active := m.source.ActiveGeneration()
	if active == nil {
		return false, nil
	}
	provider, found := active.Resolve(module.ProviderTrafficSink)
	if !found {
		return false, errors.New("active generation has no traffic provider")
	}
	reconciler, ok := provider.(module.TrafficRuntimeReconciler)
	if !ok {
		return false, errors.New("active traffic provider cannot reconcile runtime state")
	}
	if err := reconciler.ReconcileTrafficRuntime(ctx, config); err != nil {
		return false, err
	}
	return true, nil
}

func validateGenerationDrain(drain *GenerationDrain, next module.GenerationContext, previous *module.GenerationView) error {
	if drain == nil || drain.Controller() == nil {
		return errors.New("generation drain is not configured")
	}
	if next.ID() == "" {
		return errors.New("generation candidate has no id")
	}

	snapshot := drain.Controller().Snapshot()
	current := snapshot.ActiveGenerationID
	if previous == nil {
		if current != "" {
			return fmt.Errorf("generation candidate missing previous view for %s", current)
		}
	} else if current != previous.ID() {
		return fmt.Errorf("generation candidate previous view %s does not match drain owner %s", previous.ID(), current)
	}
	for _, status := range snapshot.Generations {
		if status.GenerationID == next.ID() {
			return fmt.Errorf("generation candidate duplicates %s", next.ID())
		}
		if status.GenerationID == current && next.Revision() <= status.Revision {
			return fmt.Errorf("generation revision %d must increase beyond %d", next.Revision(), status.Revision)
		}
	}
	return nil
}

func (m *GenerationManager) beginPublication(generationID string) chan struct{} {
	if m == nil || m.drain == nil {
		return nil
	}
	done := make(chan struct{})
	m.publicationMu.Lock()
	m.publicationDone = done
	m.publicationID = generationID
	m.publicationMu.Unlock()
	return done
}

func (m *GenerationManager) endPublication(done chan struct{}) {
	if done == nil {
		return
	}
	m.publicationMu.Lock()
	if m.publicationDone == done {
		m.publicationDone = nil
		m.publicationID = ""
	}
	close(done)
	m.publicationMu.Unlock()
}

func (m *GenerationManager) RegisterSession(generationID string, entity generation.EntityKey, sessionID string, session generation.Session) (*generation.SessionHandle, error) {
	if m == nil || m.sessions == nil {
		return nil, errors.New("generation session registrar is not configured")
	}
	for {
		m.publicationMu.Lock()
		done := m.publicationDone
		if done == nil || generationID != m.publicationID || m.drainGenerationPublished(generationID) {
			handle, err := m.sessions.RegisterSession(generationID, entity, sessionID, session)
			m.publicationMu.Unlock()
			return handle, err
		}
		m.publicationMu.Unlock()
		<-done
	}
}

func (m *GenerationManager) drainGenerationPublished(generationID string) bool {
	if m == nil || m.drain == nil || m.drain.Controller() == nil {
		return false
	}
	return m.drain.Controller().Snapshot().ActiveGenerationID == generationID
}

func (m *GenerationManager) ActiveGeneration() *module.GenerationView {
	if m == nil || m.source == nil {
		return nil
	}
	return m.source.ActiveGeneration()
}

func (m *GenerationManager) WithActiveGeneration(expected *module.GenerationView, use func() error) (bool, error) {
	if m == nil || m.source == nil || expected == nil {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if guarded, ok := m.source.(interface {
		WithActiveGeneration(*module.GenerationView, func() error) (bool, error)
	}); ok {
		return guarded.WithActiveGeneration(expected, use)
	}
	if m.source.ActiveGeneration() != expected {
		return false, nil
	}
	if use == nil {
		return true, nil
	}
	return true, use()
}

func (m *GenerationManager) DrainController() *generation.DrainController {
	if m == nil || m.drain == nil {
		return nil
	}
	return m.drain.Controller()
}

func (m *GenerationManager) RetiredGenerations() []*module.GenerationView {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*module.GenerationView(nil), m.retired...)
}

func (m *GenerationManager) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	if m.drain != nil {
		errs = append(errs, m.drain.Close(ctx))
	} else if m.source != nil {
		if active := m.source.ActiveGeneration(); active != nil {
			errs = append(errs, active.Destroy(ctx))
		}
	}
	for _, retired := range m.retired {
		if retired != nil {
			errs = append(errs, retired.Destroy(ctx))
		}
	}
	m.retired = nil
	return errors.Join(errs...)
}
