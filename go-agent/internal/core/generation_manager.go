package core

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

type GenerationCutover struct {
	Active   *module.GenerationView
	Previous *module.GenerationView
}

// GenerationManager serializes prepare/readiness/cutover. The source owns the
// single atomic GenerationView slot; the manager retains prior views so a
// drain owner can release their resources after existing sessions finish.
type GenerationManager struct {
	mu      sync.Mutex
	source  module.GenerationPreparer
	retired []*module.GenerationView
}

func NewGenerationManager(source module.GenerationPreparer) *GenerationManager {
	return &GenerationManager{source: source}
}

func (m *GenerationManager) Apply(ctx context.Context, previous, next model.Snapshot) (GenerationCutover, error) {
	if m == nil || m.source == nil {
		return GenerationCutover{}, errors.New("generation source is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	generationContext, err := module.NewGenerationContext(previous, next)
	if err != nil {
		return GenerationCutover{}, err
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
	active, retired := candidate.Publish()
	if retired != nil {
		m.retired = append(m.retired, retired)
	}
	return GenerationCutover{Active: active, Previous: retired}, nil
}

func (m *GenerationManager) ActiveGeneration() *module.GenerationView {
	if m == nil || m.source == nil {
		return nil
	}
	return m.source.ActiveGeneration()
}

func (m *GenerationManager) RetiredGenerations() []*module.GenerationView {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*module.GenerationView(nil), m.retired...)
}
