package traffic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestRevisionSyncAppliesHeartbeatTrafficRuntimeWithoutRebuildingGeneration(t *testing.T) {
	previousEnabled := Enabled()
	t.Cleanup(func() { SetEnabled(previousEnabled) })

	registry := module.NewRegistry()
	trafficModule := NewModule(Config{GenerationSelector: registry})
	unrelated := &trafficRevisionUnrelatedModule{}
	if err := registry.Register(trafficModule); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(unrelated); err != nil {
		t.Fatal(err)
	}
	manager := core.NewGenerationManager(registry)
	runtime := core.NewRuntimeWithGenerationManager(manager)
	store := &trafficRevisionStore{InMemory: core.NewInMemory()}
	immutable := model.Snapshot{
		DesiredVersion: "v1", Revision: 1,
		AgentConfig: model.AgentConfig{TrafficStatsInterval: "10s"},
		Rules:       []model.HTTPRule{}, L4Rules: []model.L4Rule{}, RelayListeners: []model.RelayListener{},
		EgressProfiles: []model.EgressProfile{}, Certificates: []model.ManagedCertificateBundle{},
		CertificatePolicies: []model.ManagedCertificatePolicy{}, PluginPolicies: []model.PluginPolicy{},
	}
	payload, err := json.Marshal(immutable)
	if err != nil {
		t.Fatal(err)
	}
	digestBytes := sha256.Sum256(payload)
	digest := hex.EncodeToString(digestBytes[:])
	lease := model.RevisionLease{
		AgentID: "edge-1", Revision: 1, Attempt: 1, LeaseID: "lease-1", SnapshotDigest: digest,
		DesiredVersion: "v1", ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 30,
		DeadlineAt: time.Now().UTC().Add(time.Minute),
	}
	disabled := false
	enabled := true
	heartbeatFailure := errors.New("heartbeat unavailable")
	client := &trafficRevisionClient{
		heartbeats: []trafficHeartbeatResult{
			{snapshot: trafficHeartbeatSnapshot(disabled, true, "monthly quota exceeded")},
			{snapshot: trafficHeartbeatSnapshot(enabled, false, "")},
			{snapshot: trafficHeartbeatSnapshot(enabled, true, "daily quota exceeded")},
			{err: heartbeatFailure},
		},
		pulls: []model.RevisionPull{{
			HasUpdate: true, DesiredRevision: 1, Lease: &lease, Snapshot: &immutable, VerifiedSnapshotDigest: digest,
		}, {}, {}},
	}
	controller := &core.SyncController{Store: store, Runtime: runtime, SyncClient: client}
	AddHTTP(7, 11)
	if err := controller.PerformSyncPlan(t.Context(), core.SyncPlan{}); err != nil {
		t.Fatal(err)
	}
	initialReport, err := trafficModule.TrafficReport(t.Context(), nil)
	if err != nil || !initialReport.StatsPresent || len(initialReport.Stats) != 0 || !trafficModule.TrafficBlockState().Blocked {
		t.Fatalf("initial heartbeat traffic runtime report=%+v block=%+v error=%v", initialReport, trafficModule.TrafficBlockState(), err)
	}
	identity := manager.ActiveIdentity()
	if identity.SnapshotHash != digest || unrelated.prepareCalls != 1 {
		t.Fatalf("generation identity/prepares = %+v/%d, want immutable digest and one prepare", identity, unrelated.prepareCalls)
	}
	applied, err := store.LoadAppliedSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if applied.AgentConfig.TrafficStatsEnabled != nil || applied.AgentConfig.TrafficBlocked {
		t.Fatalf("runtime traffic polluted immutable applied snapshot: %+v", applied.AgentConfig)
	}

	if err := controller.PerformSyncPlan(t.Context(), core.SyncPlan{}); err != nil {
		t.Fatal(err)
	}
	enabledReport, err := trafficModule.TrafficReport(t.Context(), nil)
	if err != nil || !enabledReport.StatsPresent || enabledReport.Stats["traffic"] == nil || trafficModule.TrafficBlockState().Blocked || unrelated.prepareCalls != 1 {
		t.Fatalf("same-revision unblock rebuilt generation or missed runtime state: report=%+v block=%+v prepares=%d error=%v", enabledReport, trafficModule.TrafficBlockState(), unrelated.prepareCalls, err)
	}
	if current := manager.ActiveIdentity(); current != identity {
		t.Fatalf("same-revision traffic update changed generation identity: %+v -> %+v", identity, current)
	}

	if err := controller.PerformSyncPlan(t.Context(), core.SyncPlan{}); err != nil {
		t.Fatal(err)
	}
	if block := trafficModule.TrafficBlockState(); !block.Blocked || block.Reason != "daily quota exceeded" || unrelated.prepareCalls != 1 {
		t.Fatalf("same-revision block = %+v prepares=%d", block, unrelated.prepareCalls)
	}
	if err := controller.PerformSyncPlan(t.Context(), core.SyncPlan{}); !errors.Is(err, heartbeatFailure) {
		t.Fatalf("heartbeat failure error = %v", err)
	}
	if block := trafficModule.TrafficBlockState(); !block.Blocked || block.Reason != "daily quota exceeded" {
		t.Fatalf("heartbeat failure cleared last-known block: %+v", block)
	}

	restartedRegistry := module.NewRegistry()
	restartedTraffic := NewModule(Config{GenerationSelector: restartedRegistry})
	if err := restartedRegistry.Register(restartedTraffic); err != nil {
		t.Fatal(err)
	}
	if err := restartedRegistry.Register(&trafficRevisionUnrelatedModule{}); err != nil {
		t.Fatal(err)
	}
	restartedManager := core.NewGenerationManager(restartedRegistry)
	restarted := &core.SyncController{
		Store: store, Runtime: core.NewRuntimeWithGenerationManager(restartedManager),
		SyncClient: &trafficRevisionClient{heartbeats: []trafficHeartbeatResult{{err: heartbeatFailure}}},
	}
	if err := restarted.PerformSyncPlan(t.Context(), core.SyncPlan{}); !errors.Is(err, heartbeatFailure) {
		t.Fatalf("restart heartbeat failure error = %v", err)
	}
	if block := restartedTraffic.TrafficBlockState(); !block.Blocked || block.Reason != "daily quota exceeded" {
		t.Fatalf("durable restart cleared last-known block: %+v", block)
	}
	if current := restartedManager.ActiveIdentity(); current.ID != identity.ID || current.SnapshotHash != identity.SnapshotHash {
		t.Fatalf("runtime-only restore changed immutable generation identity: %+v -> %+v", identity, current)
	}
}

func trafficHeartbeatSnapshot(enabled bool, blocked bool, reason string) model.Snapshot {
	return model.Snapshot{AgentConfig: model.AgentConfig{
		TrafficStatsEnabled: &enabled, TrafficBlocked: blocked, TrafficBlockReason: reason,
	}}
}

type trafficHeartbeatResult struct {
	snapshot model.Snapshot
	err      error
}

type trafficRevisionClient struct {
	heartbeats []trafficHeartbeatResult
	pulls      []model.RevisionPull
}

func (c *trafficRevisionClient) Sync(context.Context, control.SyncRequest) (model.Snapshot, error) {
	result := c.heartbeats[0]
	c.heartbeats = c.heartbeats[1:]
	return result.snapshot, result.err
}

func (c *trafficRevisionClient) PullRevision(context.Context) (model.RevisionPull, error) {
	pull := c.pulls[0]
	c.pulls = c.pulls[1:]
	return pull, nil
}

func (*trafficRevisionClient) StartRevision(context.Context, model.RevisionStart) error   { return nil }
func (*trafficRevisionClient) ReportRevision(context.Context, model.RevisionReport) error { return nil }

type trafficRevisionStore struct {
	*core.InMemory
	journal model.GenerationJournal
	lkg     model.Snapshot
}

func (s *trafficRevisionStore) SaveGenerationJournal(journal model.GenerationJournal) error {
	s.journal = journal
	return nil
}
func (s *trafficRevisionStore) LoadGenerationJournal() (model.GenerationJournal, error) {
	return s.journal, nil
}
func (s *trafficRevisionStore) SaveLastKnownGoodSnapshot(snapshot model.Snapshot) error {
	s.lkg = snapshot
	return nil
}
func (s *trafficRevisionStore) LoadLastKnownGoodSnapshot() (model.Snapshot, error) {
	return s.lkg, nil
}

type trafficRevisionUnrelatedModule struct{ prepareCalls int }

func (*trafficRevisionUnrelatedModule) Name() string { return "unrelated" }
func (*trafficRevisionUnrelatedModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: "unrelated"}
}
func (*trafficRevisionUnrelatedModule) RegisterProviders(module.ProviderRegistry) error { return nil }
func (*trafficRevisionUnrelatedModule) Capabilities(module.SnapshotView) []module.Capability {
	return nil
}
func (*trafficRevisionUnrelatedModule) Apply(context.Context, module.ApplyRequest) error { return nil }
func (m *trafficRevisionUnrelatedModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	m.prepareCalls++
	return trafficRevisionTransaction{}, nil
}
func (*trafficRevisionUnrelatedModule) Stop(context.Context) error { return nil }

type trafficRevisionTransaction struct{}

func (trafficRevisionTransaction) Commit() error                 { return nil }
func (trafficRevisionTransaction) Rollback() error               { return nil }
func (trafficRevisionTransaction) Ready(context.Context) error   { return nil }
func (trafficRevisionTransaction) Destroy(context.Context) error { return nil }
