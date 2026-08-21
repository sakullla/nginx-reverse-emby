package rpcplugin

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

var (
	ErrDraining = errors.New("generation is draining")
	ErrRevoked  = errors.New("generation or handle is revoked")
)

// Grants is an immutable copy of the scopes granted during handshake.
type Grants struct {
	values map[string]struct{}
}

func NewGrants(scopes []string) (Grants, error) {
	values := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if scope == "" {
			return Grants{}, errors.New("granted scope must not be empty")
		}
		if _, exists := values[scope]; exists {
			return Grants{}, fmt.Errorf("granted scope %q is duplicated", scope)
		}
		values[scope] = struct{}{}
	}
	return Grants{values: values}, nil
}

func (grants Grants) Has(scope string) bool {
	_, ok := grants.values[scope]
	return ok
}

func (grants Grants) Require(scope string) error {
	if !grants.Has(scope) {
		return fmt.Errorf("%w: %s", ErrGrantDenied, scope)
	}
	return nil
}

func (grants Grants) Scopes() []string {
	result := make([]string, 0, len(grants.values))
	for value := range grants.values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// Generation owns admission, in-flight accounting, secrets, and handles for
// exactly one handshake generation.
type Generation struct {
	id       string
	grants   Grants
	redactor *Redactor
	logger   SafeLogger

	mu        sync.Mutex
	accepting bool
	revoked   bool
	inflight  uint64
	zero      chan struct{}
	handles   map[*handleCore]struct{}
}

func newGeneration(id string, grants Grants, sink LogSink) *Generation {
	zero := make(chan struct{})
	close(zero)
	redactor := NewRedactor()
	return &Generation{
		id:        id,
		grants:    grants,
		redactor:  redactor,
		logger:    SafeLogger{sink: sink, redactor: redactor},
		accepting: true,
		zero:      zero,
		handles:   make(map[*handleCore]struct{}),
	}
}

func (generation *Generation) ID() string { return generation.id }

func (generation *Generation) Grants() Grants { return generation.grants }

func (generation *Generation) BeginDrain() {
	generation.mu.Lock()
	generation.accepting = false
	generation.mu.Unlock()
}

// Wait waits only for operations admitted before BeginDrain.
func (generation *Generation) Wait(ctx context.Context) error {
	generation.mu.Lock()
	zero := generation.zero
	generation.mu.Unlock()
	select {
	case <-zero:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Revoke atomically rejects future operations and invalidates all handles and
// secret objects. Existing callback code must still honor its context.
func (generation *Generation) Revoke() {
	generation.mu.Lock()
	if generation.revoked {
		generation.mu.Unlock()
		return
	}
	generation.accepting = false
	generation.revoked = true
	handles := make([]*handleCore, 0, len(generation.handles))
	for handle := range generation.handles {
		handles = append(handles, handle)
	}
	generation.mu.Unlock()
	for _, handle := range handles {
		handle.revoke()
	}
}

func (generation *Generation) acquire() (func(), error) {
	generation.mu.Lock()
	defer generation.mu.Unlock()
	if generation.revoked {
		return nil, ErrRevoked
	}
	if !generation.accepting {
		return nil, ErrDraining
	}
	if generation.inflight == 0 {
		generation.zero = make(chan struct{})
	}
	generation.inflight++
	var once sync.Once
	return func() {
		once.Do(func() {
			generation.mu.Lock()
			generation.inflight--
			if generation.inflight == 0 {
				close(generation.zero)
			}
			generation.mu.Unlock()
		})
	}, nil
}

func (generation *Generation) register(handle *handleCore) error {
	generation.mu.Lock()
	defer generation.mu.Unlock()
	if generation.revoked || !generation.accepting {
		return ErrRevoked
	}
	generation.handles[handle] = struct{}{}
	return nil
}

// Secret creates a generation-owned secret. The material is copied, is never
// formatted by String, and is registered for status/log/error redaction.
func (generation *Generation) Secret(reference string, material []byte) (*Secret, error) {
	if reference == "" || len(material) == 0 {
		return nil, errors.New("secret reference and material are required")
	}
	secret := newSecret(reference, material)
	secret.generation = generation
	core := &handleCore{close: secret.close}
	secret.core = core
	if err := generation.register(core); err != nil {
		secret.close()
		return nil, err
	}
	generation.redactor.Add(material)
	return secret, nil
}

func (generation *Generation) Log(ctx context.Context, level, message string, fields map[string]string) {
	generation.logger.Log(ctx, Record{Level: level, Message: message, Fields: cloneFields(fields)})
}

func (generation *Generation) Status(message string, fields map[string]string) Record {
	return generation.redactor.Sanitize(Record{Level: "status", Message: message, Fields: cloneFields(fields)})
}

type handleCore struct {
	revoked atomic.Bool
	once    sync.Once
	close   func()
}

func (handle *handleCore) revoke() {
	handle.revoked.Store(true)
	handle.once.Do(func() {
		if handle.close != nil {
			handle.close()
		}
	})
}

// Handle wraps a process-local typed client/resource and binds its authority
// to a Generation. It carries no wire contract and cannot mint host grants.
type Handle[T any] struct {
	generation    *Generation
	requiredScope string
	core          *handleCore
	value         T
}

// BindHandle requires the generation to hold requiredScope before registering
// the handle. Use rechecks the same immutable grant snapshot for every call.
func BindHandle[T any](generation *Generation, requiredScope string, value T, close func(T)) (*Handle[T], error) {
	if generation == nil {
		return nil, errors.New("generation is required")
	}
	if requiredScope == "" {
		return nil, errors.New("handle required scope is required")
	}
	if err := generation.grants.Require(requiredScope); err != nil {
		return nil, err
	}
	handle := &Handle[T]{generation: generation, requiredScope: requiredScope, value: value}
	handle.core = &handleCore{close: func() {
		if close != nil {
			close(value)
		}
	}}
	if err := generation.register(handle.core); err != nil {
		handle.core.revoke()
		return nil, err
	}
	return handle, nil
}

// Use counts the call as in-flight and rejects stale/draining generations.
func (handle *Handle[T]) Use(ctx context.Context, call func(context.Context, T) error) error {
	if handle == nil || handle.core == nil || handle.core.revoked.Load() {
		return ErrRevoked
	}
	if call == nil {
		return errors.New("handle callback is required")
	}
	if err := handle.generation.grants.Require(handle.requiredScope); err != nil {
		return err
	}
	release, err := handle.generation.acquire()
	if err != nil {
		return err
	}
	defer release()
	if handle.core.revoked.Load() {
		return ErrRevoked
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return call(ctx, handle.value)
}

func (handle *Handle[T]) Revoke() {
	if handle != nil && handle.core != nil {
		handle.core.revoke()
	}
}

// Secret is an opaque, revocable secret value. WithBytes provides a transient
// copy so callers cannot retain the generation-owned backing storage.
type Secret struct {
	reference  string
	generation *Generation
	core       *handleCore
	mu         sync.Mutex
	material   []byte
}

func newSecret(reference string, material []byte) *Secret {
	return &Secret{reference: reference, material: append([]byte(nil), material...)}
}

func (secret *Secret) Reference() string { return secret.reference }

func (secret *Secret) String() string { return "[REDACTED]" }

func (secret *Secret) WithBytes(call func([]byte) error) error {
	if secret == nil || secret.core == nil || secret.core.revoked.Load() {
		return ErrRevoked
	}
	if call == nil {
		return errors.New("secret callback is required")
	}
	release, err := secret.generation.acquire()
	if err != nil {
		return err
	}
	defer release()
	secret.mu.Lock()
	if secret.core.revoked.Load() {
		secret.mu.Unlock()
		return ErrRevoked
	}
	copy := append([]byte(nil), secret.material...)
	secret.mu.Unlock()
	defer zeroBytes(copy)
	return call(copy)
}

func (secret *Secret) close() {
	secret.mu.Lock()
	zeroBytes(secret.material)
	secret.material = nil
	secret.mu.Unlock()
}

func zeroBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
