package rpcplugin

import (
	"context"
	"errors"
	"testing"
)

func TestGenerationSlotOwnsPrepareActivateUseAndRevoke(t *testing.T) {
	grants, err := NewGrants([]string{"resource.read"})
	if err != nil {
		t.Fatal(err)
	}
	generation := newGeneration("generation", grants, nil)
	revoked := ""
	slot := NewGenerationSlot(func(value string) { revoked = value })
	if err := slot.Prepare(generation, "resource.read", "service"); err != nil {
		t.Fatal(err)
	}
	if err := slot.UseActive(t.Context(), func(context.Context, string) error { return nil }); !errors.Is(err, ErrRevoked) {
		t.Fatalf("use before activate = %v", err)
	}
	if err := slot.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := slot.UseActive(t.Context(), func(_ context.Context, value string) error {
		if value != "service" {
			t.Fatalf("value = %q", value)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	generation.Revoke()
	if revoked != "service" {
		t.Fatalf("revoked value = %q", revoked)
	}
	if _, ok := slot.ActiveValue(); ok {
		t.Fatal("revoked slot remained active")
	}
}
