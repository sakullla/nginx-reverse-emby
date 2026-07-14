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
}

func NewGenerationManager(source module.GenerationPreparer) *GenerationManager {
	return &GenerationManager{source: source}
}

func NewManagedGenerationManager(source module.GenerationPreparer, drain *GenerationDrain, timeout time.Duration) *GenerationManager {
	if drain == nil {
		drain = NewGenerationDrain(nil)
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	return &GenerationManager{source: source, drain: drain, timeout: timeout}
}

func (m *GenerationManager) Apply(ctx context.Context, previous, next model.Snapshot) (GenerationCutover, error) {
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

	generationContext, err := module.NewGenerationContext(previous, next)
	if err != nil {
		return GenerationCutover{}, err
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
	publicationDone := m.beginPublication()
	active, retired := candidate.Publish()
	if retired != nil && m.drain == nil {
		m.retired = append(m.retired, retired)
	}
	cutover := GenerationCutover{Active: active, Previous: retired}
	if m.drain != nil {
		cutover.DrainErr = m.drain.Activate(ctx, cutover, generationEntityChanges(previous, next), m.timeout)
	}
	m.endPublication(publicationDone)
	return cutover, nil
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

func (m *GenerationManager) beginPublication() chan struct{} {
	if m == nil || m.drain == nil {
		return nil
	}
	done := make(chan struct{})
	m.publicationMu.Lock()
	m.publicationDone = done
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
	}
	close(done)
	m.publicationMu.Unlock()
}

func (m *GenerationManager) RegisterSession(generationID string, entity generation.EntityKey, sessionID string, session generation.Session) (*generation.SessionHandle, error) {
	if m == nil || m.drain == nil || m.drain.Controller() == nil {
		return nil, errors.New("generation session registrar is not configured")
	}
	m.publicationMu.Lock()
	done := m.publicationDone
	m.publicationMu.Unlock()
	if done != nil {
		<-done
	}
	return m.drain.Controller().RegisterSession(generationID, entity, sessionID, session)
}

func (m *GenerationManager) ActiveGeneration() *module.GenerationView {
	if m == nil || m.source == nil {
		return nil
	}
	return m.source.ActiveGeneration()
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
	if m == nil || m.source == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	if active := m.source.ActiveGeneration(); active != nil {
		errs = append(errs, active.Destroy(ctx))
	}
	for _, retired := range m.retired {
		if retired != nil {
			errs = append(errs, retired.Destroy(ctx))
		}
	}
	m.retired = nil
	return errors.Join(errs...)
}
