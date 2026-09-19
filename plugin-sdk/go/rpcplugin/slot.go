package rpcplugin

import (
	"context"
	"sync"
)

// GenerationSlot owns one prepared-to-active Handle transition for a
// generation-scoped plugin service. Plugin hooks create business values while
// the slot centralizes binding, activation, use, detachment, and revoke races.
type GenerationSlot[T any] struct {
	mu sync.RWMutex

	prepared      *Handle[T]
	active        *Handle[T]
	preparedValue T
	activeValue   T
	revoke        func(T)
}

func NewGenerationSlot[T any](revoke func(T)) *GenerationSlot[T] {
	return &GenerationSlot[T]{revoke: revoke}
}

func (slot *GenerationSlot[T]) Prepare(generation *Generation, requiredScope string, value T) error {
	handle, err := BindHandle(generation, requiredScope, value, slot.revokeValue)
	if err != nil {
		return err
	}
	slot.mu.Lock()
	if handle.core.revoked.Load() {
		slot.mu.Unlock()
		return ErrRevoked
	}
	slot.prepared = handle
	slot.preparedValue = value
	slot.mu.Unlock()
	return nil
}

func (slot *GenerationSlot[T]) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.prepared == nil {
		return ErrRevoked
	}
	slot.active = slot.prepared
	slot.activeValue = slot.preparedValue
	return nil
}

func (slot *GenerationSlot[T]) UseActive(ctx context.Context, use func(context.Context, T) error) error {
	slot.mu.RLock()
	handle := slot.active
	slot.mu.RUnlock()
	if handle == nil {
		return ErrRevoked
	}
	return handle.Use(ctx, use)
}

func (slot *GenerationSlot[T]) ActiveValue() (T, bool) {
	slot.mu.RLock()
	defer slot.mu.RUnlock()
	return slot.activeValue, slot.active != nil
}

// Clear detaches the active value, or the prepared value when activation did
// not complete. Generation revoke still owns final Handle invalidation.
func (slot *GenerationSlot[T]) Clear() (T, bool) {
	slot.mu.Lock()
	defer slot.mu.Unlock()
	value, ok := slot.activeValue, slot.active != nil
	if !ok {
		value, ok = slot.preparedValue, slot.prepared != nil
	}
	slot.prepared, slot.active = nil, nil
	var zero T
	slot.preparedValue, slot.activeValue = zero, zero
	return value, ok
}

func (slot *GenerationSlot[T]) revokeValue(value T) {
	slot.mu.Lock()
	slot.prepared, slot.active = nil, nil
	var zero T
	slot.preparedValue, slot.activeValue = zero, zero
	slot.mu.Unlock()
	if slot.revoke != nil {
		slot.revoke(value)
	}
}
