package wireguard

import (
	"context"
	"errors"
	"net"
	"reflect"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

type wireGuardGenerationFactory struct {
	generationID      string
	ingress           *wireGuardIngressManager
	registrar         WireGuardSessionRegistrar
	registrationReady bool
	base              Factory

	mu        sync.Mutex
	endpoints map[string]*wireGuardBindEndpoint
}

func newWireGuardGenerationFactory(generationID string, ingress *wireGuardIngressManager, registrar WireGuardSessionRegistrar, registrationReady bool, base Factory) *wireGuardGenerationFactory {
	if base == nil {
		base = NewRuntimeHandle
	}
	return &wireGuardGenerationFactory{
		generationID:      generationID,
		ingress:           ingress,
		registrar:         registrar,
		registrationReady: registrationReady,
		base:              base,
		endpoints:         make(map[string]*wireGuardBindEndpoint),
	}
}

func (f *wireGuardGenerationFactory) create(ctx context.Context, cfg Config) (RuntimeHandle, error) {
	if f == nil || f.ingress == nil {
		return nil, errors.New("wireguard generation factory is not configured")
	}
	lease, err := f.ingress.acquire(f.generationID, cfg, f.registrar, f.registrationReady)
	if err != nil {
		return nil, err
	}
	key := wireGuardBindingKey(cfg)
	f.mu.Lock()
	_, duplicate := f.endpoints[key]
	if !duplicate {
		f.endpoints[key] = lease.endpoint
	}
	f.mu.Unlock()
	if duplicate {
		_ = lease.release()
		return nil, errors.New("wireguard generation contains conflicting stable binding identities")
	}

	cfg.bind = lease.endpoint
	handle, err := f.base(ctx, cfg)
	if err != nil {
		f.mu.Lock()
		delete(f.endpoints, key)
		f.mu.Unlock()
		return nil, errors.Join(err, lease.release())
	}
	runtime := &wireGuardLeasedRuntime{RuntimeHandle: handle, lease: lease}
	lease.endpoint.setRuntimeCloser(runtime.Close)
	return runtime, nil
}

func (f *wireGuardGenerationFactory) endpoint(bindingKey string) *wireGuardBindEndpoint {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	endpoint := f.endpoints[bindingKey]
	f.mu.Unlock()
	return endpoint
}

func (f *wireGuardGenerationFactory) beginDrain() {
	if f == nil {
		return
	}
	f.mu.Lock()
	endpoints := make([]*wireGuardBindEndpoint, 0, len(f.endpoints))
	for _, endpoint := range f.endpoints {
		endpoints = append(endpoints, endpoint)
	}
	f.mu.Unlock()
	for _, endpoint := range endpoints {
		endpoint.beginDrain()
	}
}

type wireGuardLeasedRuntime struct {
	RuntimeHandle
	lease *wireGuardBindLease
	once  sync.Once
	err   error
}

func (r *wireGuardLeasedRuntime) Close() error {
	if r == nil {
		return nil
	}
	r.once.Do(func() {
		if r.RuntimeHandle != nil {
			r.err = r.RuntimeHandle.Close()
		}
		r.err = errors.Join(r.err, r.lease.release())
	})
	if r.lease != nil && r.lease.endpoint != nil {
		r.lease.endpoint.finishAssociations()
	}
	return r.err
}

type wireGuardGenerationOverlayProvider struct {
	runtime *Runtime
	factory *wireGuardGenerationFactory
}

func (p wireGuardGenerationOverlayProvider) DialContext(ctx context.Context, agentID string, profileID int, network string, address string) (net.Conn, error) {
	return overlayRuntimeProvider{runtime: p.runtime}.DialContext(ctx, agentID, profileID, network, address)
}

func (p wireGuardGenerationOverlayProvider) ListenTCP(ctx context.Context, agentID string, profileID int, address string) (net.Listener, error) {
	return overlayRuntimeProvider{runtime: p.runtime}.ListenTCP(ctx, agentID, profileID, address)
}

func (p wireGuardGenerationOverlayProvider) ListenUDP(ctx context.Context, agentID string, profileID int, address string) (net.PacketConn, error) {
	return overlayRuntimeProvider{runtime: p.runtime}.ListenUDP(ctx, agentID, profileID, address)
}

func (p wireGuardGenerationOverlayProvider) wireGuardBindEndpoint(bindingKey string) *wireGuardBindEndpoint {
	return p.factory.endpoint(bindingKey)
}

type wireGuardGenerationTransparentProvider struct{ runtime *Runtime }

func (p wireGuardGenerationTransparentProvider) ListenTransparentTCP(ctx context.Context, agentID string, profileID int) (net.Listener, error) {
	return transparentListenerProvider{runtime: p.runtime}.ListenTransparentTCP(ctx, agentID, profileID)
}

func (p wireGuardGenerationTransparentProvider) ListenTransparentUDP(ctx context.Context, agentID string, profileID int, address string) (module.TransparentUDPConn, error) {
	return transparentListenerProvider{runtime: p.runtime}.ListenTransparentUDP(ctx, agentID, profileID, address)
}

type wireGuardGenerationTransaction struct {
	module             *Module
	runtime            *Runtime
	previousRuntime    *Runtime
	factory            *wireGuardGenerationFactory
	previousFactory    *wireGuardGenerationFactory
	profiles           []model.WireGuardProfile
	previousProfiles   []model.WireGuardProfile
	generationID       string
	generationRevision int64
	entityChanges      []generation.EntityChange
	published          bool
	destroyed          bool
	finalized          bool
}

func (m *Module) prepareGeneration(ctx context.Context, req module.ApplyRequest) (module.ModuleTransaction, error) {
	generationContext, err := module.NewGenerationContext(req.Previous, req.Next)
	if err != nil {
		return nil, err
	}
	factory := newWireGuardGenerationFactory(generationContext.ID(), m.ingress, m.sessions, !m.manageDrain, m.factory)
	runtime := NewRuntime(factory.create)
	profiles := CloneWireGuardProfiles(req.Next.WireGuardProfiles)
	if err := runtime.Apply(ctx, profiles); err != nil {
		return nil, errors.Join(err, runtime.Close())
	}

	m.mu.Lock()
	previousRuntime := m.runtime
	previousFactory := m.generationFactory
	m.mu.Unlock()
	return &wireGuardGenerationTransaction{
		module:             m,
		runtime:            runtime,
		previousRuntime:    previousRuntime,
		factory:            factory,
		previousFactory:    previousFactory,
		profiles:           profiles,
		previousProfiles:   CloneWireGuardProfiles(req.Previous.WireGuardProfiles),
		generationID:       generationContext.ID(),
		generationRevision: generationContext.Revision(),
		entityChanges:      wireGuardProfileEntityChanges(req.Previous.WireGuardProfiles, req.Next.WireGuardProfiles),
	}, nil
}

func (t *wireGuardGenerationTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	if t == nil {
		return nil
	}
	if err := reg.Provide(module.ProviderOverlayRuntime, wireGuardGenerationOverlayProvider{runtime: t.runtime, factory: t.factory}); err != nil {
		return err
	}
	return reg.Provide(module.ProviderTransparentListener, wireGuardGenerationTransparentProvider{runtime: t.runtime})
}

func (*wireGuardGenerationTransaction) Ready(context.Context) error { return nil }

func (t *wireGuardGenerationTransaction) publish() error {
	if t == nil || t.module == nil || t.published {
		return nil
	}
	t.module.mu.Lock()
	t.module.runtime = t.runtime
	t.module.generationFactory = t.factory
	t.module.mu.Unlock()
	t.published = true
	return nil
}

func (t *wireGuardGenerationTransaction) Commit() error { return t.publish() }

func (t *wireGuardGenerationTransaction) Rollback() error {
	if t == nil || t.destroyed {
		return nil
	}
	if t.module != nil && t.published {
		t.module.mu.Lock()
		t.module.runtime = t.previousRuntime
		t.module.generationFactory = t.previousFactory
		t.module.mu.Unlock()
	}
	t.published = false
	return t.Destroy(context.Background())
}

func (t *wireGuardGenerationTransaction) Destroy(context.Context) error {
	if t == nil || t.destroyed {
		return nil
	}
	t.destroyed = true
	if t.runtime == nil {
		return nil
	}
	return t.runtime.Close()
}

func (t *wireGuardGenerationTransaction) FinalizeCommitSuccess() {
	if t == nil || !t.published || t.finalized {
		return
	}
	installed := true
	if t.module != nil && t.module.manageDrain && t.module.drain != nil {
		_ = t.module.drain.Activate(context.Background(), generation.Generation{
			ID: t.generationID, Revision: t.generationRevision, Resource: wireGuardDrainResource{runtime: t.runtime},
		}, t.entityChanges, t.module.drainTimeout)
		installed = wireGuardDrainGenerationIsActive(t.module.drain, t.generationID, t.generationRevision)
		if installed {
			for _, endpoint := range t.factory.endpointsSnapshot() {
				endpoint.enableRegistration()
			}
		}
	}
	if !installed {
		return
	}
	t.finalized = true
	if t.previousFactory != nil {
		t.previousFactory.beginDrain()
	}
}

func (f *wireGuardGenerationFactory) endpointsSnapshot() []*wireGuardBindEndpoint {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	endpoints := make([]*wireGuardBindEndpoint, 0, len(f.endpoints))
	for _, endpoint := range f.endpoints {
		endpoints = append(endpoints, endpoint)
	}
	f.mu.Unlock()
	return endpoints
}

type wireGuardDrainResource struct{ runtime *Runtime }

func (r wireGuardDrainResource) Destroy(context.Context) error {
	if r.runtime == nil {
		return nil
	}
	return r.runtime.Close()
}

func wireGuardDrainGenerationIsActive(controller *generation.DrainController, generationID string, revision int64) bool {
	if controller == nil {
		return false
	}
	snapshot := controller.Snapshot()
	if snapshot.ActiveGenerationID != generationID {
		return false
	}
	for _, status := range snapshot.Generations {
		if status.GenerationID == generationID && status.Revision == revision {
			return true
		}
	}
	return false
}

func wireGuardProfileEntityChanges(previous, next []model.WireGuardProfile) []generation.EntityChange {
	nextByEntity := make(map[generation.EntityKey]model.WireGuardProfile, len(next))
	for _, profile := range next {
		nextByEntity[wireGuardProfileEntityKey(profile)] = profile
	}
	changes := make([]generation.EntityChange, 0, len(previous))
	for _, profile := range previous {
		entity := wireGuardProfileEntityKey(profile)
		nextProfile, exists := nextByEntity[entity]
		switch {
		case !exists:
			changes = append(changes, generation.EntityChange{Entity: entity, Action: generation.EntityDeleted})
		case profile.Enabled && !nextProfile.Enabled:
			changes = append(changes, generation.EntityChange{Entity: entity, Action: generation.EntityDisabled})
		case !reflect.DeepEqual(profile, nextProfile):
			changes = append(changes, generation.EntityChange{Entity: entity, Action: generation.EntityModified})
		}
	}
	return changes
}

func wireGuardProfileEntityKey(profile model.WireGuardProfile) generation.EntityKey {
	cfg := Config{WireGuardProfile: profile}
	return wireGuardEntityKey(cfg)
}

var _ module.GenerationTransaction = (*wireGuardGenerationTransaction)(nil)
var _ module.OverlayRuntime = wireGuardGenerationOverlayProvider{}
var _ module.TransparentListener = wireGuardGenerationTransparentProvider{}
