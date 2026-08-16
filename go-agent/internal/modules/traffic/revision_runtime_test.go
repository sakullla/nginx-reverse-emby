//go:build !integration

package traffic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestRevisionSyncAppliesHeartbeatTrafficRuntimeWithoutRebuildingGeneration(t *testing.T) {
	previousEnabled := Enabled()
	t.Cleanup(func() {
		SetEnabled(previousEnabled)
		Reset()
	})

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
		PluginGenerations: []model.PluginGeneration{}, PluginDependencies: []model.PluginDependencyEdge{},
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
	AddHTTP(5, 9)
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

func TestRevisionSyncCommitsAuthenticatedBlockBeforePlanAndPullFailures(t *testing.T) {
	previousEnabled := Enabled()
	t.Cleanup(func() {
		SetEnabled(previousEnabled)
		Reset()
	})
	tests := []struct {
		name             string
		plan             core.SyncPlan
		pull             model.RevisionPull
		pullErr          error
		failRuntimeSaves map[int]error
		providerErr      error
		wantErr          string
	}{
		{name: "pull network", pullErr: errors.New("pull unavailable"), wantErr: "pull unavailable"},
		{name: "bad pull digest", pull: model.RevisionPull{HasUpdate: true}, wantErr: "revision pull"},
		{
			name: "plan metadata persistence", plan: core.SyncPlan{RuntimeMetadata: map[string]string{"plan": "metadata"}},
			failRuntimeSaves: map[int]error{2: errors.New("plan metadata persistence failed")},
			wantErr:          "plan metadata persistence failed",
		},
		{
			name: "traffic persistence retries after fail closed", failRuntimeSaves: map[int]error{1: errors.New("traffic persistence failed")},
		},
		{name: "provider reconcile failure is forced closed", providerErr: errors.New("provider reconcile failed"), wantErr: "provider reconcile failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, runtime, trafficModule, immutable := newTrafficRevisionRuntime(t, true, false, "")
			store.failRuntimeSaves = tt.failRuntimeSaves
			if tt.providerErr != nil {
				trafficModule.reconcileTrafficRuntime = func(context.Context, model.AgentConfig) error { return tt.providerErr }
			}
			controller := &core.SyncController{
				Store: store, Runtime: runtime,
				SyncClient: &trafficRevisionClient{
					heartbeats: []trafficHeartbeatResult{{snapshot: trafficHeartbeatSnapshot(false, true, "quota exceeded")}},
					pulls:      []model.RevisionPull{tt.pull}, pullErr: tt.pullErr,
				},
			}
			err := controller.PerformSyncPlan(t.Context(), tt.plan)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("PerformSyncPlan() error = %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("PerformSyncPlan() error = %v, want %q", err, tt.wantErr)
			}
			if block := trafficModule.TrafficBlockState(); !block.Blocked || block.Reason != "quota exceeded" {
				t.Fatalf("active block after later failure = %+v", block)
			}
			state, loadErr := store.LoadRuntimeState()
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if state.Metadata["traffic_blocked"] != "true" || state.Metadata["traffic_block_reason"] != "quota exceeded" {
				t.Fatalf("durable traffic runtime = %+v", state.Metadata)
			}
			assertTrafficRestartState(t, store, immutable, true, "quota exceeded")
		})
	}
}

func TestRevisionSyncTrafficPersistenceFailureRollsForwardBlockAndPreservesUnblock(t *testing.T) {
	previousEnabled := Enabled()
	t.Cleanup(func() {
		SetEnabled(previousEnabled)
		Reset()
	})
	t.Run("authenticated block retries durable state", func(t *testing.T) {
		store, runtime, trafficModule, immutable := newTrafficRevisionRuntime(t, true, false, "")
		store.failRuntimeSaves = map[int]error{1: errors.New("injected first write failure")}
		controller := &core.SyncController{
			Store: store, Runtime: runtime,
			SyncClient: &trafficRevisionClient{
				heartbeats: []trafficHeartbeatResult{{snapshot: trafficHeartbeatSnapshot(true, true, "quota exceeded")}},
				pulls:      []model.RevisionPull{{}},
			},
		}
		if err := controller.PerformSyncPlan(t.Context(), core.SyncPlan{}); err != nil {
			t.Fatal(err)
		}
		if block := trafficModule.TrafficBlockState(); !block.Blocked {
			t.Fatalf("active block = %+v", block)
		}
		assertTrafficRestartState(t, store, immutable, true, "quota exceeded")
	})

	t.Run("failed unblock persistence leaves prior block", func(t *testing.T) {
		store, runtime, trafficModule, immutable := newTrafficRevisionRuntime(t, true, true, "existing block")
		store.failRuntimeSaves = map[int]error{1: errors.New("injected unblock write failure")}
		controller := &core.SyncController{
			Store: store, Runtime: runtime,
			SyncClient: &trafficRevisionClient{
				heartbeats: []trafficHeartbeatResult{{snapshot: trafficHeartbeatSnapshot(true, false, "")}},
				pulls:      []model.RevisionPull{{}},
			},
		}
		if err := controller.PerformSyncPlan(t.Context(), core.SyncPlan{}); err == nil || !strings.Contains(err.Error(), "injected unblock write failure") {
			t.Fatalf("PerformSyncPlan() error = %v", err)
		}
		if block := trafficModule.TrafficBlockState(); !block.Blocked || block.Reason != "existing block" {
			t.Fatalf("failed unblock changed active state: %+v", block)
		}
		assertTrafficRestartState(t, store, immutable, true, "existing block")
	})

	t.Run("two failed block writes converge through error persistence", func(t *testing.T) {
		store, runtime, trafficModule, immutable := newTrafficRevisionRuntime(t, true, false, "")
		store.failRuntimeSaves = map[int]error{
			1: errors.New("injected block intent write failure"),
			2: errors.New("injected block retry write failure"),
		}
		controller := &core.SyncController{
			Store: store, Runtime: runtime,
			SyncClient: &trafficRevisionClient{heartbeats: []trafficHeartbeatResult{{
				snapshot: trafficHeartbeatSnapshot(true, true, "safe block"),
			}}},
		}
		if err := controller.PerformSyncPlan(t.Context(), core.SyncPlan{}); err == nil || !strings.Contains(err.Error(), "block retry write failure") {
			t.Fatalf("PerformSyncPlan() error = %v", err)
		}
		if block := trafficModule.TrafficBlockState(); !block.Blocked || block.Reason != "safe block" {
			t.Fatalf("active block = %+v", block)
		}
		state, err := store.LoadRuntimeState()
		if err != nil {
			t.Fatal(err)
		}
		if state.Metadata["traffic_blocked"] != "true" || state.Metadata["traffic_block_reason"] != "safe block" {
			t.Fatalf("error persistence lost safe block: %+v", state.Metadata)
		}
		assertTrafficRestartState(t, store, immutable, true, "safe block")
	})

	t.Run("failed unblock rollback converges through error persistence", func(t *testing.T) {
		store, runtime, trafficModule, immutable := newTrafficRevisionRuntime(t, true, true, "existing block")
		store.failRuntimeSaves = map[int]error{2: errors.New("injected unblock rollback failure")}
		trafficModule.reconcileTrafficRuntime = func(context.Context, model.AgentConfig) error {
			return errors.New("injected provider unblock failure")
		}
		controller := &core.SyncController{
			Store: store, Runtime: runtime,
			SyncClient: &trafficRevisionClient{heartbeats: []trafficHeartbeatResult{{
				snapshot: trafficHeartbeatSnapshot(true, false, ""),
			}}},
		}
		if err := controller.PerformSyncPlan(t.Context(), core.SyncPlan{}); err == nil || !strings.Contains(err.Error(), "unblock rollback failure") {
			t.Fatalf("PerformSyncPlan() error = %v", err)
		}
		if block := trafficModule.TrafficBlockState(); !block.Blocked || block.Reason != "existing block" {
			t.Fatalf("failed unblock changed active state: %+v", block)
		}
		state, err := store.LoadRuntimeState()
		if err != nil {
			t.Fatal(err)
		}
		if state.Metadata["traffic_blocked"] != "true" || state.Metadata["traffic_block_reason"] != "existing block" {
			t.Fatalf("error persistence retained unsafe unblock: %+v", state.Metadata)
		}
		assertTrafficRestartState(t, store, immutable, true, "existing block")
	})
}

func TestRevisionSyncMigratesLegacyTrafficEnabledFromAppliedArtifact(t *testing.T) {
	previousEnabled := Enabled()
	t.Cleanup(func() {
		SetEnabled(previousEnabled)
		Reset()
	})
	disabled, enabled := false, true
	for _, tt := range []struct {
		name           string
		appliedEnabled *bool
		wantEnabled    bool
	}{
		{name: "disabled artifact", appliedEnabled: &disabled, wantEnabled: false},
		{name: "enabled artifact", appliedEnabled: &enabled, wantEnabled: true},
		{name: "unknown artifact fails safe", appliedEnabled: nil, wantEnabled: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			immutable := model.Snapshot{
				DesiredVersion: "v1", Revision: 1,
				AgentConfig: model.AgentConfig{TrafficStatsInterval: "10s", TrafficStatsEnabled: tt.appliedEnabled},
				Rules:       []model.HTTPRule{}, L4Rules: []model.L4Rule{}, RelayListeners: []model.RelayListener{},
				EgressProfiles: []model.EgressProfile{}, Certificates: []model.ManagedCertificateBundle{},
				CertificatePolicies: []model.ManagedCertificatePolicy{}, PluginPolicies: []model.PluginPolicy{},
				PluginGenerations: []model.PluginGeneration{}, PluginDependencies: []model.PluginDependencyEdge{},
			}
			store := &trafficRevisionStore{InMemory: core.NewInMemory()}
			if err := store.SaveAppliedSnapshot(immutable); err != nil {
				t.Fatal(err)
			}
			if err := store.InMemory.SaveRuntimeState(model.RuntimeState{CurrentRevision: 1, Metadata: map[string]string{
				"traffic_blocked": "false",
			}}); err != nil {
				t.Fatal(err)
			}

			registry := module.NewRegistry()
			trafficModule := NewModule(Config{GenerationSelector: registry})
			if err := registry.Register(trafficModule); err != nil {
				t.Fatal(err)
			}
			manager := core.NewGenerationManager(registry)
			restarted := &core.SyncController{
				Store: store, Runtime: core.NewRuntimeWithGenerationManager(manager),
				SyncClient: &trafficRevisionClient{heartbeats: []trafficHeartbeatResult{{err: errors.New("heartbeat unavailable")}}},
			}
			if err := restarted.PerformSyncPlan(t.Context(), core.SyncPlan{}); err == nil {
				t.Fatal("PerformSyncPlan() error = nil")
			}
			active := manager.ActiveGeneration()
			if active == nil {
				t.Fatal("restored active generation = nil")
			}
			provider, found := active.Resolve(module.ProviderTrafficSink)
			tx, ok := provider.(*transaction)
			if !found || !ok {
				t.Fatalf("restored traffic provider = %T, found=%t", provider, found)
			}
			tx.mu.RLock()
			runtimeEnabled := tx.nextEnabled
			tx.mu.RUnlock()
			if runtimeEnabled != tt.wantEnabled {
				t.Fatalf("legacy enabled migration runtime=%t, want %t", runtimeEnabled, tt.wantEnabled)
			}
			migrated, err := store.LoadRuntimeState()
			if err != nil {
				t.Fatal(err)
			}
			if migrated.Metadata["traffic_stats_enabled"] != fmt.Sprintf("%t", tt.wantEnabled) {
				t.Fatalf("migrated metadata = %+v", migrated.Metadata)
			}
			if applied, err := store.LoadAppliedSnapshot(); err != nil || applied.Revision != immutable.Revision {
				t.Fatalf("applied snapshot = %+v, error=%v", applied, err)
			}
		})
	}
}

func newTrafficRevisionRuntime(t *testing.T, enabled, blocked bool, reason string) (*trafficRevisionStore, *core.Runtime, *Module, model.Snapshot) {
	t.Helper()
	registry := module.NewRegistry()
	trafficModule := NewModule(Config{GenerationSelector: registry})
	if err := registry.Register(trafficModule); err != nil {
		t.Fatal(err)
	}
	manager := core.NewGenerationManager(registry)
	runtime := core.NewRuntimeWithGenerationManager(manager)
	immutable := model.Snapshot{
		DesiredVersion: "v1", Revision: 1,
		AgentConfig: model.AgentConfig{TrafficStatsInterval: "10s", TrafficStatsEnabled: &enabled},
		Rules:       []model.HTTPRule{}, L4Rules: []model.L4Rule{}, RelayListeners: []model.RelayListener{},
		EgressProfiles: []model.EgressProfile{}, Certificates: []model.ManagedCertificateBundle{},
		CertificatePolicies: []model.ManagedCertificatePolicy{}, PluginPolicies: []model.PluginPolicy{},
		PluginGenerations: []model.PluginGeneration{}, PluginDependencies: []model.PluginDependencyEdge{},
	}
	config := model.AgentConfig{TrafficStatsEnabled: &enabled, TrafficBlocked: blocked, TrafficBlockReason: reason}
	if err := runtime.ApplyWithTrafficRuntime(t.Context(), model.Snapshot{}, immutable, 0, config); err != nil {
		t.Fatal(err)
	}
	store := &trafficRevisionStore{InMemory: core.NewInMemory()}
	if err := store.SaveAppliedSnapshot(immutable); err != nil {
		t.Fatal(err)
	}
	if err := store.InMemory.SaveRuntimeState(model.RuntimeState{CurrentRevision: immutable.Revision, Metadata: map[string]string{
		"traffic_stats_enabled": fmt.Sprintf("%t", enabled),
		"traffic_blocked":       fmt.Sprintf("%t", blocked),
		"traffic_block_reason":  reason,
	}}); err != nil {
		t.Fatal(err)
	}
	return store, runtime, trafficModule, immutable
}

func assertTrafficRestartState(t *testing.T, store *trafficRevisionStore, immutable model.Snapshot, blocked bool, reason string) {
	t.Helper()
	registry := module.NewRegistry()
	trafficModule := NewModule(Config{GenerationSelector: registry})
	if err := registry.Register(trafficModule); err != nil {
		t.Fatal(err)
	}
	restarted := &core.SyncController{
		Store: store, Runtime: core.NewRuntimeWithGenerationManager(core.NewGenerationManager(registry)),
		SyncClient: &trafficRevisionClient{heartbeats: []trafficHeartbeatResult{{err: errors.New("heartbeat unavailable")}}},
	}
	store.failRuntimeSaves = nil
	if err := restarted.PerformSyncPlan(t.Context(), core.SyncPlan{}); err == nil {
		t.Fatal("restart heartbeat error = nil")
	}
	if block := trafficModule.TrafficBlockState(); block.Blocked != blocked || block.Reason != reason {
		t.Fatalf("restart block = %+v, want blocked=%t reason=%q (revision=%d)", block, blocked, reason, immutable.Revision)
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
	pullErr    error
}

func (c *trafficRevisionClient) Sync(context.Context, control.SyncRequest) (model.Snapshot, error) {
	result := c.heartbeats[0]
	c.heartbeats = c.heartbeats[1:]
	return result.snapshot, result.err
}

func (c *trafficRevisionClient) PullRevision(context.Context) (model.RevisionPull, error) {
	if c.pullErr != nil {
		return model.RevisionPull{}, c.pullErr
	}
	if len(c.pulls) == 0 {
		return model.RevisionPull{}, nil
	}
	pull := c.pulls[0]
	c.pulls = c.pulls[1:]
	return pull, nil
}

func (*trafficRevisionClient) StartRevision(context.Context, model.RevisionStart) error   { return nil }
func (*trafficRevisionClient) ReportRevision(context.Context, model.RevisionReport) error { return nil }

type trafficRevisionStore struct {
	*core.InMemory
	journal          model.GenerationJournal
	lkg              model.Snapshot
	runtimeSaveCount int
	failRuntimeSaves map[int]error
}

func (s *trafficRevisionStore) SaveRuntimeState(state model.RuntimeState) error {
	s.runtimeSaveCount++
	if err := s.failRuntimeSaves[s.runtimeSaveCount]; err != nil {
		return err
	}
	return s.InMemory.SaveRuntimeState(state)
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
