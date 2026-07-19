package localagent

import (
	"context"
	"testing"
	"time"

	goagentembedded "github.com/sakullla/nginx-reverse-emby/go-agent/embedded"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestEmbeddedSnapshotCarriesDDNSConfig(t *testing.T) {
	got := toEmbeddedSnapshot(storage.Snapshot{DDNSConfig: &storage.DDNSConfig{
		Enabled: true, Domain: "media.example.com",
		IPv4: storage.DDNSFamily{Enabled: true, Source: "interface", Interface: "eth0"},
	}})
	if got.DDNSConfig == nil || !got.DDNSConfig.Enabled || got.DDNSConfig.Domain != "media.example.com" ||
		got.DDNSConfig.IPv4.Interface != "eth0" {
		t.Fatalf("embedded DDNS config = %+v", got.DDNSConfig)
	}
}

func TestEmbeddedDrainWaitsForRuntimeCompletion(t *testing.T) {
	snapshot := goagentembedded.GenerationDrainSnapshot{Generations: []goagentembedded.GenerationDrainStatus{{
		Revision: 1, State: goagentembedded.GenerationDrainStateDrained,
	}}}
	if _, ok := completedEmbeddedDrain(snapshot, 1, 2); ok {
		t.Fatal("embedded predecessor acknowledged before cleanup completed")
	}
	snapshot.Generations[0].CompletedAt = time.Now().UTC()
	if _, ok := completedEmbeddedDrain(snapshot, 1, 2); !ok {
		t.Fatal("completed embedded predecessor was not acknowledged")
	}
}

func TestEmbeddedRevisionApplyReceivesLeaseTiming(t *testing.T) {
	runtime := &timedRevisionApplier{}
	deadline := time.Now().UTC().Add(time.Minute)
	lease := service.RemoteRevisionLease{
		AgentID: "local", Revision: 3, Attempt: 1, LeaseID: "lease-3",
		ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 37, DeadlineAt: deadline,
	}
	if err := applyRevisionWithinLease(t.Context(), runtime, storage.Snapshot{Revision: 3}, lease); err != nil {
		t.Fatal(err)
	}
	if !runtime.deadline.Equal(deadline) || runtime.drainTimeout != 37*time.Second {
		t.Fatalf("embedded lease timing = deadline:%s drain:%s", runtime.deadline, runtime.drainTimeout)
	}
}

func TestSyncSourcePersistsLocalDDNSAddresses(t *testing.T) {
	store := &localDDNSStoreStub{rows: []storage.AgentRow{{ID: "local", IsLocal: true, Mode: "local"}}}
	source := NewSyncSource(store, "local")
	reconciled := false
	source.SetDDNSReconciler(func(context.Context, string) { reconciled = true })
	if _, err := source.Sync(t.Context(), SyncRequest{LastSeenIPv4: "203.0.113.30"}); err != nil {
		t.Fatal(err)
	}
	if store.saved.LastSeenIPv4 != "203.0.113.30" || !reconciled {
		t.Fatalf("saved heartbeat = %+v, reconciled = %t", store.saved, reconciled)
	}
}

type localDDNSStoreStub struct {
	rows  []storage.AgentRow
	saved storage.AgentRow
}

type timedRevisionApplier struct {
	deadline     time.Time
	drainTimeout time.Duration
}

func (a *timedRevisionApplier) ApplyRevisionWithDrainTimeout(ctx context.Context, _ storage.Snapshot, drainTimeout time.Duration) error {
	a.deadline, _ = ctx.Deadline()
	a.drainTimeout = drainTimeout
	return nil
}

func (*localDDNSStoreStub) LoadLocalSnapshot(context.Context, string) (storage.Snapshot, error) {
	return storage.Snapshot{}, nil
}

func (s *localDDNSStoreStub) ListAgents(context.Context) ([]storage.AgentRow, error) {
	return append([]storage.AgentRow(nil), s.rows...), nil
}

func (s *localDDNSStoreStub) SaveAgentHeartbeat(_ context.Context, row storage.AgentRow) error {
	s.saved = row
	return nil
}
