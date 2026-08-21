package rpcplugin

import (
	"context"
	"errors"
	"testing"
)

func TestGrantRequiredForHandleCreationAndUse(t *testing.T) {
	grants, err := NewGrants([]string{"resource.read"})
	if err != nil {
		t.Fatal(err)
	}
	generation := newGeneration("generation-1", grants, nil)
	if _, err := BindHandle(generation, "resource.write", "resource", nil); !errors.Is(err, ErrGrantDenied) {
		t.Fatalf("missing-grant handle creation error = %v", err)
	}

	handle, err := BindHandle(generation, "resource.read", "resource", nil)
	if err != nil {
		t.Fatal(err)
	}
	// A Handle never trusts its mere existence as authority. Even a stale or
	// corrupted handle must fail closed when Use rechecks its required scope.
	handle.requiredScope = "resource.write"
	called := false
	err = handle.Use(context.Background(), func(context.Context, string) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrGrantDenied) || called {
		t.Fatalf("missing-grant handle use error = %v, called = %v", err, called)
	}
}

func TestRevokeHandleRejectsLaterUse(t *testing.T) {
	grants, err := NewGrants([]string{"resource.read"})
	if err != nil {
		t.Fatal(err)
	}
	generation := newGeneration("generation-1", grants, nil)
	handle, err := BindHandle(generation, "resource.read", "resource", nil)
	if err != nil {
		t.Fatal(err)
	}
	handle.Revoke()
	if err := handle.Use(context.Background(), func(context.Context, string) error { return nil }); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked handle use error = %v", err)
	}
}
