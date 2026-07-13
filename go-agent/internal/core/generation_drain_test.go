package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestGenerationDrainOwnsPublishedAndRetiredViews(t *testing.T) {
	registry := module.NewRegistry()
	mod := &runtimeGenerationModule{name: "generation-drain", providerRef: module.ProviderRef("test.generation-drain")}
	mustRegister(t, registry, mod)
	manager := core.NewGenerationManager(registry)
	drain := core.NewGenerationDrain(nil)

	first, err := manager.Apply(context.Background(), model.Snapshot{}, model.Snapshot{Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := drain.Activate(context.Background(), first, nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	handle, err := drain.Controller().RegisterSession(first.Active.ID(), generation.EntityKey{Module: "http", ID: "rule-1"}, "session-1", &coreDrainSession{})
	if err != nil {
		t.Fatal(err)
	}

	second, err := manager.Apply(context.Background(), model.Snapshot{Revision: 1}, model.Snapshot{Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	if second.Previous != first.Active {
		t.Fatal("cutover did not return the retired view owned by the drain controller")
	}
	if err := drain.Activate(context.Background(), second, []generation.EntityChange{{
		Entity: generation.EntityKey{Module: "http", ID: "rule-1"},
		Action: generation.EntityModified,
	}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if len(mod.destroyed) != 0 {
		t.Fatalf("destroyed revisions before drain = %v", mod.destroyed)
	}

	handle.Finish()
	if len(mod.destroyed) != 1 || mod.destroyed[0] != 1 {
		t.Fatalf("destroyed revisions after drain = %v, want [1]", mod.destroyed)
	}
}

func TestGenerationDrainRejectsMismatchedRetiredView(t *testing.T) {
	registry := module.NewRegistry()
	mod := &runtimeGenerationModule{name: "generation-drain-mismatch", providerRef: module.ProviderRef("test.generation-drain-mismatch")}
	mustRegister(t, registry, mod)
	manager := core.NewGenerationManager(registry)
	drain := core.NewGenerationDrain(nil)

	first, err := manager.Apply(context.Background(), model.Snapshot{}, model.Snapshot{Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := drain.Activate(context.Background(), first, nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	second, err := manager.Apply(context.Background(), model.Snapshot{Revision: 1}, model.Snapshot{Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	second.Previous = second.Active
	if err := drain.Activate(context.Background(), second, nil, time.Minute); err == nil {
		t.Fatal("Activate accepted a cutover whose retired view was not drain-owned")
	}
}

type coreDrainSession struct{}

func (*coreDrainSession) ForceClose(context.Context, string) error { return nil }
