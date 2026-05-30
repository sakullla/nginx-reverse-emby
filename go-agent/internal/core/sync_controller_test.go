package core

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	agentruntime "github.com/sakullla/nginx-reverse-emby/go-agent/internal/runtime"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
	agentsync "github.com/sakullla/nginx-reverse-emby/go-agent/internal/sync"
	agentupdate "github.com/sakullla/nginx-reverse-emby/go-agent/internal/update"
)

func TestSyncControllerSuccessfulSyncPersistsSnapshotsRuntimeStateAndClearsLastSyncError(t *testing.T) {
	st := newSyncControllerStore()
	previous := model.Snapshot{DesiredVersion: "stable", Revision: 7}
	if err := st.SaveAppliedSnapshot(previous); err != nil {
		t.Fatalf("SaveAppliedSnapshot() error = %v", err)
	}
	if err := st.SaveRuntimeState(store.RuntimeState{
		CurrentRevision: previous.Revision,
		Metadata: map[string]string{
			"current_revision": "7",
			"last_sync_error":  "previous failure",
			"unrelated":        "kept",
		},
	}); err != nil {
		t.Fatalf("SaveRuntimeState() error = %v", err)
	}
	rt := agentruntime.New()
	if err := rt.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	next := model.Snapshot{
		DesiredVersion: "next",
		Revision:       9,
		Rules: []model.HTTPRule{{
			FrontendURL: "https://next.example.test",
		}},
		AgentConfig: model.AgentConfig{TrafficStatsInterval: "30s"},
	}
	client := &syncControllerClient{snapshot: next}
	controller := &SyncController{Store: st, Runtime: rt, SyncClient: client}

	if err := controller.PerformSync(context.Background(), agentsync.SyncRequest{CurrentRevision: 7}); err != nil {
		t.Fatalf("PerformSync() error = %v", err)
	}

	if client.calls != 1 {
		t.Fatalf("Sync calls = %d, want 1", client.calls)
	}
	desired, err := st.LoadDesiredSnapshot()
	if err != nil {
		t.Fatalf("LoadDesiredSnapshot() error = %v", err)
	}
	if !reflect.DeepEqual(desired, next) {
		t.Fatalf("desired snapshot = %+v, want %+v", desired, next)
	}
	applied, err := st.LoadAppliedSnapshot()
	if err != nil {
		t.Fatalf("LoadAppliedSnapshot() error = %v", err)
	}
	if !reflect.DeepEqual(applied, next) {
		t.Fatalf("applied snapshot = %+v, want %+v", applied, next)
	}
	state, err := st.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState() error = %v", err)
	}
	if state.CurrentRevision != 9 || state.Status != "active" {
		t.Fatalf("runtime state = %+v, want active revision 9", state)
	}
	if _, ok := state.Metadata["last_sync_error"]; ok {
		t.Fatalf("last_sync_error not cleared: %+v", state.Metadata)
	}
	if state.Metadata["last_apply_revision"] != "9" || state.Metadata["last_apply_status"] != "success" || state.Metadata["last_apply_message"] != "" {
		t.Fatalf("apply metadata = %+v, want success at revision 9", state.Metadata)
	}
	if state.Metadata["traffic_stats_interval"] != "30s" || state.Metadata["unrelated"] != "kept" {
		t.Fatalf("runtime metadata = %+v", state.Metadata)
	}
}

func TestSyncControllerBuildSyncPlanIncludesApplyStatusStatsAndCertificateReports(t *testing.T) {
	st := newSyncControllerStore()
	applied := model.Snapshot{DesiredVersion: "1.0.0", Revision: 7}
	if err := st.SaveRuntimeState(store.RuntimeState{
		CurrentRevision: applied.Revision,
		Metadata: map[string]string{
			"last_apply_revision": "6",
			"last_apply_status":   "error",
			"last_apply_message":  "previous apply failed",
		},
	}); err != nil {
		t.Fatalf("SaveRuntimeState() error = %v", err)
	}
	controller := &SyncController{
		Store: st,
		Traffic: syncControllerTrafficReporter{report: TrafficReport{
			Stats:        map[string]any{"traffic": map[string]any{"total": map[string]uint64{"rx_bytes": 11, "tx_bytes": 22}}},
			StatsPresent: true,
		}},
		CertReports: syncControllerCertificateReporter{
			reports: []model.ManagedCertificateReport{{
				ID:           21,
				Domain:       "sync.example.com",
				Status:       "active",
				MaterialHash: "hash-21",
			}},
		},
	}

	plan, err := controller.BuildSyncPlan(context.Background(), applied)
	if err != nil {
		t.Fatalf("BuildSyncPlan() error = %v", err)
	}

	req := plan.Request
	if req.CurrentRevision != 7 {
		t.Fatalf("CurrentRevision = %d, want 7", req.CurrentRevision)
	}
	if req.LastApplyRevision != 6 || req.LastApplyStatus != "error" || req.LastApplyMessage != "previous apply failed" {
		t.Fatalf("apply metadata in request = %+v", req)
	}
	if req.Stats == nil || !req.StatsPresent {
		t.Fatalf("stats = %#v present=%v, want stats present", req.Stats, req.StatsPresent)
	}
	if len(req.ManagedCertificateReports) != 1 || req.ManagedCertificateReports[0].ID != 21 {
		t.Fatalf("managed certificate reports = %+v", req.ManagedCertificateReports)
	}
}

func TestSyncControllerMergesOmittedPayloadsAgainstDesiredAndAppliedSnapshots(t *testing.T) {
	st := newSyncControllerStore()
	previousApplied := model.Snapshot{
		DesiredVersion: "applied",
		Revision:       4,
		Rules: []model.HTTPRule{{
			FrontendURL: "http://applied.example.test:18080",
			Backends:    []model.HTTPBackend{{URL: "http://127.0.0.1:8096"}},
			Revision:    1,
		}},
		L4Rules: []model.L4Rule{{
			Protocol:   "tcp",
			ListenHost: "127.0.0.1",
			ListenPort: 9000,
			Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: 9001}},
			Revision:   1,
		}},
	}
	previousDesired := model.Snapshot{
		DesiredVersion: "desired",
		Revision:       4,
		Rules: []model.HTTPRule{{
			FrontendURL: "http://desired.example.test:18080",
			Backends:    []model.HTTPBackend{{URL: "http://127.0.0.1:8096"}},
			Revision:    2,
		}},
		L4Rules: previousApplied.L4Rules,
	}
	if err := st.SaveAppliedSnapshot(previousApplied); err != nil {
		t.Fatalf("SaveAppliedSnapshot() error = %v", err)
	}
	if err := st.SaveDesiredSnapshot(previousDesired); err != nil {
		t.Fatalf("SaveDesiredSnapshot() error = %v", err)
	}

	var appliedByRuntime model.Snapshot
	rt := agentruntime.NewWithActivator(func(_ context.Context, _, next model.Snapshot) error {
		if next.Revision == 5 {
			appliedByRuntime = next
		}
		return nil
	})
	if err := rt.Apply(context.Background(), model.Snapshot{}, previousApplied); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	synced := model.Snapshot{
		DesiredVersion: "next",
		Revision:       5,
		L4Rules: []model.L4Rule{{
			Protocol:   "tcp",
			ListenHost: "127.0.0.1",
			ListenPort: 9100,
			Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: 9101}},
			Revision:   2,
		}},
	}
	controller := &SyncController{Store: st, Runtime: rt, SyncClient: &syncControllerClient{snapshot: synced}}

	if err := controller.PerformSync(context.Background(), agentsync.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync() error = %v", err)
	}

	desired, err := st.LoadDesiredSnapshot()
	if err != nil {
		t.Fatalf("LoadDesiredSnapshot() error = %v", err)
	}
	if !reflect.DeepEqual(desired.Rules, previousDesired.Rules) {
		t.Fatalf("desired rules = %+v, want previous desired %+v", desired.Rules, previousDesired.Rules)
	}
	applied, err := st.LoadAppliedSnapshot()
	if err != nil {
		t.Fatalf("LoadAppliedSnapshot() error = %v", err)
	}
	if !reflect.DeepEqual(applied.Rules, previousApplied.Rules) {
		t.Fatalf("applied rules = %+v, want previous applied %+v", applied.Rules, previousApplied.Rules)
	}
	if !reflect.DeepEqual(applied.L4Rules, synced.L4Rules) {
		t.Fatalf("applied l4 rules = %+v, want synced %+v", applied.L4Rules, synced.L4Rules)
	}
	if !reflect.DeepEqual(appliedByRuntime.Rules, previousApplied.Rules) || !reflect.DeepEqual(appliedByRuntime.L4Rules, synced.L4Rules) {
		t.Fatalf("runtime applied snapshot = %+v", appliedByRuntime)
	}
}

func TestSyncControllerSyncFailureRecordsRuntimeErrorAndSuccessClearsIt(t *testing.T) {
	st := newSyncControllerStore()
	previous := model.Snapshot{DesiredVersion: "stable", Revision: 7}
	_ = st.SaveAppliedSnapshot(previous)
	_ = st.SaveRuntimeState(store.RuntimeState{Metadata: map[string]string{"foo": "bar"}})
	rt := agentruntime.New()
	if err := rt.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	client := &syncControllerClient{err: errors.New("boom")}
	controller := &SyncController{Store: st, Runtime: rt, SyncClient: client}

	if err := controller.PerformSync(context.Background(), agentsync.SyncRequest{}); err == nil || err.Error() != "boom" {
		t.Fatalf("PerformSync() error = %v, want boom", err)
	}
	state, err := st.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState() error = %v", err)
	}
	if state.Metadata["last_sync_error"] != "boom" || state.Metadata["foo"] != "bar" {
		t.Fatalf("runtime metadata after failure = %+v", state.Metadata)
	}

	client.err = nil
	client.snapshot = model.Snapshot{DesiredVersion: "next", Revision: 8}
	if err := controller.PerformSync(context.Background(), agentsync.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync() success error = %v", err)
	}
	state, err = st.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState() after success error = %v", err)
	}
	if _, ok := state.Metadata["last_sync_error"]; ok {
		t.Fatalf("last_sync_error not cleared: %+v", state.Metadata)
	}
}

func TestSyncControllerDesiredSnapshotSaveFailureRecordsRuntimeError(t *testing.T) {
	st := newSyncControllerStore()
	st.failOnDesiredSave = 1
	previous := model.Snapshot{DesiredVersion: "stable", Revision: 7}
	_ = st.SaveAppliedSnapshot(previous)
	_ = st.SaveRuntimeState(store.RuntimeState{Metadata: map[string]string{"foo": "bar"}})
	rt := agentruntime.New()
	if err := rt.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	controller := &SyncController{
		Store:      st,
		Runtime:    rt,
		SyncClient: &syncControllerClient{snapshot: model.Snapshot{DesiredVersion: "next", Revision: 8}},
	}

	if err := controller.PerformSync(context.Background(), agentsync.SyncRequest{}); err == nil || err.Error() != "desired persistence fail" {
		t.Fatalf("PerformSync() error = %v, want desired persistence fail", err)
	}

	state, err := st.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState() error = %v", err)
	}
	if state.Metadata["last_sync_error"] != "desired persistence fail" || state.Metadata["foo"] != "bar" {
		t.Fatalf("runtime metadata = %+v", state.Metadata)
	}
	if active := rt.ActiveSnapshot(); !reflect.DeepEqual(active, previous) {
		t.Fatalf("active snapshot = %+v, want previous %+v", active, previous)
	}
}

func TestSyncControllerApplyFailureRollsRuntimeBackAndRecordsCandidateError(t *testing.T) {
	st := newSyncControllerStore()
	previous := model.Snapshot{DesiredVersion: "stable", Revision: 7}
	_ = st.SaveAppliedSnapshot(previous)
	_ = st.SaveRuntimeState(store.RuntimeState{
		CurrentRevision: previous.Revision,
		Metadata:        map[string]string{"current_revision": "7"},
	})
	applyErr := errors.New("activation failed")
	rt := agentruntime.NewWithActivator(func(_ context.Context, _, next model.Snapshot) error {
		if next.Revision == 9 {
			return applyErr
		}
		return nil
	})
	if err := rt.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	controller := &SyncController{
		Store:      st,
		Runtime:    rt,
		SyncClient: &syncControllerClient{snapshot: model.Snapshot{DesiredVersion: "next", Revision: 9}},
	}

	if err := controller.PerformSync(context.Background(), agentsync.SyncRequest{}); !errors.Is(err, applyErr) {
		t.Fatalf("PerformSync() error = %v, want %v", err, applyErr)
	}

	if active := rt.ActiveSnapshot(); !reflect.DeepEqual(active, previous) {
		t.Fatalf("active snapshot = %+v, want rollback to %+v", active, previous)
	}
	state, err := st.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState() error = %v", err)
	}
	if state.CurrentRevision != 7 || state.Metadata["current_revision"] != "7" {
		t.Fatalf("runtime state advanced after failure: %+v", state)
	}
	if state.Metadata["last_sync_error"] != "activation failed" ||
		state.Metadata["last_apply_revision"] != "9" ||
		state.Metadata["last_apply_status"] != "error" ||
		state.Metadata["last_apply_message"] != "activation failed" {
		t.Fatalf("error metadata = %+v", state.Metadata)
	}
}

func TestSyncControllerAppliedSnapshotSaveFailureRollsRuntimeBackAndRecordsPersistedError(t *testing.T) {
	st := newSyncControllerStore()
	st.failOnAppliedSave = 2
	previous := model.Snapshot{DesiredVersion: "stable", Revision: 7}
	if err := st.SaveAppliedSnapshot(previous); err != nil {
		t.Fatalf("seed applied: %v", err)
	}
	_ = st.SaveRuntimeState(store.RuntimeState{
		CurrentRevision: previous.Revision,
		Metadata:        map[string]string{"current_revision": "7", "foo": "bar"},
	})
	rt := agentruntime.New()
	if err := rt.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	controller := &SyncController{
		Store:      st,
		Runtime:    rt,
		SyncClient: &syncControllerClient{snapshot: model.Snapshot{DesiredVersion: "next", Revision: 9}},
	}

	if err := controller.PerformSync(context.Background(), agentsync.SyncRequest{}); err == nil || err.Error() != "applied persistence fail" {
		t.Fatalf("PerformSync() error = %v, want applied persistence fail", err)
	}

	applied, _ := st.LoadAppliedSnapshot()
	if !reflect.DeepEqual(applied, previous) {
		t.Fatalf("applied snapshot = %+v, want previous %+v", applied, previous)
	}
	if active := rt.ActiveSnapshot(); !reflect.DeepEqual(active, previous) {
		t.Fatalf("active snapshot = %+v, want previous %+v", active, previous)
	}
	state, _ := st.LoadRuntimeState()
	if state.CurrentRevision != 7 || state.Metadata["current_revision"] != "7" || state.Metadata["foo"] != "bar" {
		t.Fatalf("runtime state changed unexpectedly: %+v", state)
	}
	if state.Metadata["last_sync_error"] != "applied persistence fail" ||
		state.Metadata["last_apply_revision"] != "9" ||
		state.Metadata["last_apply_status"] != "error" {
		t.Fatalf("error metadata = %+v", state.Metadata)
	}
}

func TestSyncControllerAppliedSnapshotSaveFailureRecordsPersistedErrorWhenRollbackFails(t *testing.T) {
	st := newSyncControllerStore()
	st.failOnAppliedSave = 2
	previous := model.Snapshot{DesiredVersion: "stable", Revision: 7}
	if err := st.SaveAppliedSnapshot(previous); err != nil {
		t.Fatalf("seed applied: %v", err)
	}
	_ = st.SaveRuntimeState(store.RuntimeState{
		CurrentRevision: previous.Revision,
		Metadata:        map[string]string{"current_revision": "7", "foo": "bar"},
	})
	rollbackErr := errors.New("rollback failed")
	rt := agentruntime.NewWithActivator(func(_ context.Context, previous, next model.Snapshot) error {
		if previous.Revision == 9 && next.Revision == 7 {
			return rollbackErr
		}
		return nil
	})
	if err := rt.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	controller := &SyncController{
		Store:      st,
		Runtime:    rt,
		SyncClient: &syncControllerClient{snapshot: model.Snapshot{DesiredVersion: "next", Revision: 9}},
	}

	if err := controller.PerformSync(context.Background(), agentsync.SyncRequest{}); err == nil || err.Error() != "applied persistence fail" {
		t.Fatalf("PerformSync() error = %v, want applied persistence fail", err)
	}

	applied, _ := st.LoadAppliedSnapshot()
	if !reflect.DeepEqual(applied, previous) {
		t.Fatalf("applied snapshot = %+v, want previous %+v", applied, previous)
	}
	state, _ := st.LoadRuntimeState()
	if state.CurrentRevision != 7 || state.Metadata["current_revision"] != "7" || state.Metadata["foo"] != "bar" {
		t.Fatalf("runtime state advanced unexpectedly: %+v", state)
	}
	if state.Metadata["last_sync_error"] != "applied persistence fail" ||
		state.Metadata["last_apply_revision"] != "9" ||
		state.Metadata["last_apply_status"] != "error" {
		t.Fatalf("error metadata = %+v", state.Metadata)
	}
	plan, err := controller.BuildSyncPlan(context.Background(), applied)
	if err != nil {
		t.Fatalf("BuildSyncPlan() error = %v", err)
	}
	if plan.Request.CurrentRevision != 7 {
		t.Fatalf("next CurrentRevision = %d, want fail-closed revision 7", plan.Request.CurrentRevision)
	}
}

func TestSyncControllerRuntimeStateSaveFailureRollsRuntimeBackAndRestoresPreviousAppliedSnapshot(t *testing.T) {
	st := newSyncControllerStore()
	previous := model.Snapshot{DesiredVersion: "stable", Revision: 7}
	next := model.Snapshot{DesiredVersion: "next", Revision: 9}
	_ = st.SaveAppliedSnapshot(previous)
	_ = st.SaveRuntimeState(store.RuntimeState{
		CurrentRevision: previous.Revision,
		Metadata:        map[string]string{"current_revision": "7"},
	})
	st.failOnRuntimeSave = 2
	rt := agentruntime.New()
	if err := rt.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	controller := &SyncController{
		Store:      st,
		Runtime:    rt,
		SyncClient: &syncControllerClient{snapshot: next},
	}

	if err := controller.PerformSync(context.Background(), agentsync.SyncRequest{}); err == nil || err.Error() != "runtime state persistence fail" {
		t.Fatalf("PerformSync() error = %v, want runtime state persistence fail", err)
	}

	applied, _ := st.LoadAppliedSnapshot()
	if !reflect.DeepEqual(applied, previous) {
		t.Fatalf("applied snapshot = %+v, want restored previous %+v", applied, previous)
	}
	if active := rt.ActiveSnapshot(); !reflect.DeepEqual(active, previous) {
		t.Fatalf("active snapshot = %+v, want previous %+v", active, previous)
	}
}

func TestSyncControllerClearsTrafficStatsIntervalAfterSuccessfulApply(t *testing.T) {
	st := newSyncControllerStore()
	previous := model.Snapshot{DesiredVersion: "stable", Revision: 7, AgentConfig: model.AgentConfig{TrafficStatsInterval: "30s"}}
	_ = st.SaveAppliedSnapshot(previous)
	_ = st.SaveRuntimeState(store.RuntimeState{
		CurrentRevision: previous.Revision,
		Metadata:        map[string]string{"current_revision": "7", "traffic_stats_interval": "30s"},
	})
	rt := agentruntime.New()
	if err := rt.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	var next model.Snapshot
	if err := json.Unmarshal([]byte(`{"desired_version":"next","desired_revision":8,"agent_config":{}}`), &next); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	controller := &SyncController{
		Store:      st,
		Runtime:    rt,
		SyncClient: &syncControllerClient{snapshot: next},
	}

	if err := controller.PerformSync(context.Background(), agentsync.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync() error = %v", err)
	}

	state, err := st.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState() error = %v", err)
	}
	if _, ok := state.Metadata["traffic_stats_interval"]; ok {
		t.Fatalf("traffic_stats_interval not cleared: %+v", state.Metadata)
	}
	if state.Metadata["last_apply_status"] != "success" || state.Metadata["last_apply_revision"] != "8" {
		t.Fatalf("apply metadata = %+v, want success at revision 8", state.Metadata)
	}
}

func TestSyncControllerPersistsTrafficBlockedStateFromAgentConfig(t *testing.T) {
	st := newSyncControllerStore()
	rt := agentruntime.New()
	controller := &SyncController{
		Store:   st,
		Runtime: rt,
		SyncClient: &syncControllerClient{snapshot: model.Snapshot{
			DesiredVersion: "next",
			Revision:       7,
			AgentConfig: model.AgentConfig{
				TrafficBlocked:     true,
				TrafficBlockReason: "monthly quota exceeded",
			},
		}},
	}

	if err := controller.PerformSync(context.Background(), agentsync.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync() error = %v", err)
	}

	state, err := st.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState() error = %v", err)
	}
	if state.Metadata["traffic_blocked"] != "true" {
		t.Fatalf("traffic_blocked = %q, want true", state.Metadata["traffic_blocked"])
	}
	if state.Metadata["traffic_block_reason"] != "monthly quota exceeded" {
		t.Fatalf("traffic_block_reason = %q", state.Metadata["traffic_block_reason"])
	}
}

func TestSyncControllerClearsTrafficBlockedStateFromAgentConfig(t *testing.T) {
	st := newSyncControllerStore()
	if err := st.SaveRuntimeState(store.RuntimeState{
		Metadata: map[string]string{
			"traffic_blocked":      "true",
			"traffic_block_reason": "monthly quota exceeded",
		},
	}); err != nil {
		t.Fatalf("SaveRuntimeState() error = %v", err)
	}
	rt := agentruntime.New()
	controller := &SyncController{
		Store:   st,
		Runtime: rt,
		SyncClient: &syncControllerClient{snapshot: model.Snapshot{
			DesiredVersion: "next",
			Revision:       8,
			AgentConfig:    model.AgentConfig{TrafficBlocked: false},
		}},
	}

	if err := controller.PerformSync(context.Background(), agentsync.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync() error = %v", err)
	}

	state, err := st.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState() error = %v", err)
	}
	if state.Metadata["traffic_blocked"] != "false" {
		t.Fatalf("traffic_blocked = %q, want false", state.Metadata["traffic_blocked"])
	}
	if _, ok := state.Metadata["traffic_block_reason"]; ok {
		t.Fatalf("traffic_block_reason not cleared: %+v", state.Metadata)
	}
}

func TestSyncControllerKeepsTrafficStatsIntervalWhenActivationFails(t *testing.T) {
	st := newSyncControllerStore()
	previous := model.Snapshot{DesiredVersion: "stable", Revision: 7, AgentConfig: model.AgentConfig{TrafficStatsInterval: "30s"}}
	_ = st.SaveAppliedSnapshot(previous)
	_ = st.SaveRuntimeState(store.RuntimeState{
		CurrentRevision: previous.Revision,
		Metadata:        map[string]string{"current_revision": "7", "traffic_stats_interval": "30s"},
	})
	applyErr := errors.New("activation failed")
	rt := agentruntime.NewWithActivator(func(_ context.Context, _, next model.Snapshot) error {
		if next.Revision == 9 {
			return applyErr
		}
		return nil
	})
	if err := rt.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	controller := &SyncController{
		Store:      st,
		Runtime:    rt,
		SyncClient: &syncControllerClient{snapshot: model.Snapshot{DesiredVersion: "next", Revision: 9, AgentConfig: model.AgentConfig{TrafficStatsInterval: "5m"}}},
	}

	if err := controller.PerformSync(context.Background(), agentsync.SyncRequest{}); !errors.Is(err, applyErr) {
		t.Fatalf("PerformSync() error = %v, want %v", err, applyErr)
	}

	state, err := st.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState() error = %v", err)
	}
	if state.Metadata["traffic_stats_interval"] != "30s" {
		t.Fatalf("traffic_stats_interval = %q, want previous 30s", state.Metadata["traffic_stats_interval"])
	}
	if state.Metadata["last_apply_revision"] != "9" || state.Metadata["last_apply_status"] != "error" {
		t.Fatalf("apply metadata = %+v, want error at revision 9", state.Metadata)
	}
}

func TestSyncControllerValidUpdatePackageReturnsRestartWithoutApplyingSnapshot(t *testing.T) {
	st := newSyncControllerStore()
	previous := model.Snapshot{DesiredVersion: "stable", Revision: 7}
	_ = st.SaveAppliedSnapshot(previous)
	_ = st.SaveRuntimeState(store.RuntimeState{
		CurrentRevision: previous.Revision,
		Metadata:        map[string]string{"current_revision": "7"},
	})
	rt := agentruntime.NewWithActivator(func(_ context.Context, _, next model.Snapshot) error {
		if next.Revision == 9 {
			t.Fatalf("snapshot was applied before update restart")
		}
		return nil
	})
	if err := rt.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	updater := &syncControllerUpdater{activateErr: agentupdate.ErrRestartRequested}
	controller := &SyncController{
		Store:                st,
		Runtime:              rt,
		SyncClient:           &syncControllerClient{snapshot: model.Snapshot{DesiredVersion: "2.0.0", Revision: 9, VersionPackage: &model.VersionPackage{URL: "https://example.test/nre-agent", SHA256: "new-sha"}}},
		Updater:              updater,
		CurrentPackageSHA256: "old-sha",
	}

	if err := controller.PerformSync(context.Background(), agentsync.SyncRequest{}); !errors.Is(err, agentupdate.ErrRestartRequested) {
		t.Fatalf("PerformSync() error = %v, want ErrRestartRequested", err)
	}
	if updater.stageCalls != 1 || updater.activateCalls != 1 {
		t.Fatalf("updater calls stage=%d activate=%d", updater.stageCalls, updater.activateCalls)
	}
	if updater.desiredVersions[0] != "2.0.0" {
		t.Fatalf("desired version = %q, want 2.0.0", updater.desiredVersions[0])
	}
	applied, _ := st.LoadAppliedSnapshot()
	if !reflect.DeepEqual(applied, previous) {
		t.Fatalf("applied snapshot = %+v, want previous %+v", applied, previous)
	}
	desired, _ := st.LoadDesiredSnapshot()
	if desired.VersionPackage == nil || desired.VersionPackage.SHA256 != "new-sha" {
		t.Fatalf("desired snapshot did not persist update package: %+v", desired)
	}
}

func TestSyncControllerHandlePendingUpdateUsesPackageSHAFallbacks(t *testing.T) {
	for _, tt := range []struct {
		name       string
		currentSHA string
		snapshot   model.Snapshot
		wantStage  bool
	}{
		{
			name:       "stages when desired version is empty and sha changed",
			currentSHA: "old-sha",
			snapshot: model.Snapshot{VersionPackage: &model.VersionPackage{
				URL:    "https://example.test/nre-agent",
				SHA256: "new-sha",
			}},
			wantStage: true,
		},
		{
			name:       "skips when desired version is empty and sha matches",
			currentSHA: "same-sha",
			snapshot: model.Snapshot{VersionPackage: &model.VersionPackage{
				URL:    "https://example.test/nre-agent",
				SHA256: "same-sha",
			}},
		},
		{
			name:       "skips desired version change when sha matches",
			currentSHA: "same-sha",
			snapshot: model.Snapshot{
				DesiredVersion: "2.0.0",
				VersionPackage: &model.VersionPackage{
					URL:    "https://example.test/nre-agent",
					SHA256: "same-sha",
				},
			},
		},
		{
			name:       "stages when desired version matches current version but sha changed",
			currentSHA: "old-sha",
			snapshot: model.Snapshot{
				DesiredVersion: "1.0.0",
				VersionPackage: &model.VersionPackage{
					URL:    "https://example.test/nre-agent",
					SHA256: "new-sha",
				},
			},
			wantStage: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			updater := &syncControllerUpdater{activateErr: agentupdate.ErrRestartRequested}
			controller := &SyncController{
				Store:                newSyncControllerStore(),
				Runtime:              agentruntime.New(),
				Updater:              updater,
				CurrentPackageSHA256: tt.currentSHA,
			}

			err := controller.HandlePendingUpdate(context.Background(), tt.snapshot)
			if tt.wantStage {
				if !errors.Is(err, agentupdate.ErrRestartRequested) {
					t.Fatalf("HandlePendingUpdate() error = %v, want ErrRestartRequested", err)
				}
				if updater.stageCalls != 1 {
					t.Fatalf("stage calls = %d, want 1", updater.stageCalls)
				}
				return
			}
			if err != nil {
				t.Fatalf("HandlePendingUpdate() error = %v, want nil", err)
			}
			if updater.stageCalls != 0 {
				t.Fatalf("stage calls = %d, want 0", updater.stageCalls)
			}
		})
	}
}

func TestSyncControllerUpdaterStageFailureRecordsErrorAndRetriesPersistedPackage(t *testing.T) {
	st := newSyncControllerStore()
	previous := model.Snapshot{DesiredVersion: "1.0.0", Revision: 7}
	_ = st.SaveAppliedSnapshot(previous)
	_ = st.SaveRuntimeState(store.RuntimeState{
		CurrentRevision: previous.Revision,
		Metadata:        map[string]string{"current_revision": "7", "foo": "bar"},
	})
	rt := agentruntime.New()
	if err := rt.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	client := &syncControllerSequenceClient{responses: []model.Snapshot{
		{
			DesiredVersion: "2.0.0",
			Revision:       9,
			VersionPackage: &model.VersionPackage{
				Platform: "linux-amd64",
				URL:      "https://example.test/nre-agent",
				SHA256:   "abc123",
			},
		},
		{DesiredVersion: "2.0.0", Revision: 9},
	}}
	updater := &syncControllerUpdater{stageErr: errors.New("stage failed")}
	controller := &SyncController{
		Store:                st,
		Runtime:              rt,
		SyncClient:           client,
		Updater:              updater,
		CurrentPackageSHA256: "old-sha",
	}

	for i := 0; i < 2; i++ {
		if err := controller.PerformSync(context.Background(), agentsync.SyncRequest{}); err == nil || err.Error() != "stage failed" {
			t.Fatalf("PerformSync(%d) error = %v, want stage failed", i+1, err)
		}
	}

	if updater.stageCalls != 2 {
		t.Fatalf("stage calls = %d, want retry on persisted package", updater.stageCalls)
	}
	desired, err := st.LoadDesiredSnapshot()
	if err != nil {
		t.Fatalf("LoadDesiredSnapshot() error = %v", err)
	}
	if desired.VersionPackage == nil || desired.VersionPackage.SHA256 != "abc123" {
		t.Fatalf("desired version package = %+v, want persisted package", desired.VersionPackage)
	}
	applied, _ := st.LoadAppliedSnapshot()
	if !reflect.DeepEqual(applied, previous) {
		t.Fatalf("applied snapshot = %+v, want previous %+v", applied, previous)
	}
	state, _ := st.LoadRuntimeState()
	if state.CurrentRevision != 7 || state.Metadata["current_revision"] != "7" || state.Metadata["last_sync_error"] != "stage failed" || state.Metadata["foo"] != "bar" {
		t.Fatalf("runtime state = %+v", state)
	}
}

func TestSyncControllerBuildSyncPlanCarriesRuntimeMetadata(t *testing.T) {
	st := newSyncControllerStore()
	_ = st.SaveRuntimeState(store.RuntimeState{
		Metadata: map[string]string{"traffic_stats_interval": "1s"},
	})
	reportMetadata := map[string]string{"last_traffic_stats_report_unix": "123"}
	controller := &SyncController{
		Store: st,
		Traffic: syncControllerTrafficReporter{report: TrafficReport{
			Stats:           map[string]any{"traffic": "present"},
			StatsPresent:    true,
			RuntimeMetadata: reportMetadata,
		}},
	}

	plan, err := controller.BuildSyncPlan(context.Background(), model.Snapshot{Revision: 7})
	if err != nil {
		t.Fatalf("BuildSyncPlan() error = %v", err)
	}

	if plan.Request.CurrentRevision != 7 {
		t.Fatalf("CurrentRevision = %d, want 7", plan.Request.CurrentRevision)
	}
	if plan.RuntimeMetadata["last_traffic_stats_report_unix"] != "123" {
		t.Fatalf("RuntimeMetadata = %+v, want report metadata", plan.RuntimeMetadata)
	}
	reportMetadata["last_traffic_stats_report_unix"] = "changed"
	if plan.RuntimeMetadata["last_traffic_stats_report_unix"] != "123" {
		t.Fatalf("RuntimeMetadata changed with source map: %+v", plan.RuntimeMetadata)
	}
}

func TestParseTrafficStatsIntervalRejectsInvalidValues(t *testing.T) {
	for _, raw := range []string{"not-a-duration", "0s", "-1s"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseTrafficStatsInterval(raw); err == nil {
				t.Fatal("ParseTrafficStatsInterval() error = nil, want invalid interval error")
			} else if !strings.Contains(err.Error(), "traffic_stats_interval") {
				t.Fatalf("ParseTrafficStatsInterval() error = %v, want traffic_stats_interval context", err)
			}
		})
	}
}

func TestMergeSnapshotPayloadPreservesOmittedFieldsAndExplicitEmptySlices(t *testing.T) {
	previous := model.Snapshot{
		DesiredVersion: "previous",
		Revision:       3,
		AgentConfig:    model.AgentConfig{OutboundProxyURL: "socks://127.0.0.1:1080"},
		Rules:          []model.HTTPRule{{FrontendURL: "https://previous.example.test"}},
		L4Rules:        []model.L4Rule{{Protocol: "tcp", ListenPort: 9000}},
	}
	var next model.Snapshot
	if err := json.Unmarshal([]byte(`{"desired_version":"next","desired_revision":4,"rules":[]}`), &next); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	merged := MergeSnapshotPayload(next, previous)
	if merged.DesiredVersion != "next" || merged.Revision != 4 {
		t.Fatalf("identity fields = %+v", merged)
	}
	if merged.Rules == nil || len(merged.Rules) != 0 {
		t.Fatalf("explicit empty rules not preserved: %+v", merged.Rules)
	}
	if !reflect.DeepEqual(merged.L4Rules, previous.L4Rules) {
		t.Fatalf("omitted l4 rules = %+v, want previous %+v", merged.L4Rules, previous.L4Rules)
	}
	if !reflect.DeepEqual(merged.AgentConfig, previous.AgentConfig) {
		t.Fatalf("omitted agent config = %+v, want previous %+v", merged.AgentConfig, previous.AgentConfig)
	}
}

func TestMergeSnapshotPayloadAppliesExplicitEmptyAgentConfig(t *testing.T) {
	previous := model.Snapshot{
		DesiredVersion: "previous",
		Revision:       4,
		AgentConfig: model.AgentConfig{
			OutboundProxyURL: "socks://127.0.0.1:1080",
		},
	}
	var next model.Snapshot
	if err := json.Unmarshal([]byte(`{"desired_version":"next","desired_revision":4,"agent_config":{}}`), &next); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	merged := MergeSnapshotPayload(next, previous)

	if merged.AgentConfig.OutboundProxyURL != "" {
		t.Fatalf("AgentConfig.OutboundProxyURL = %q, want cleared", merged.AgentConfig.OutboundProxyURL)
	}
}

func TestMergeSnapshotPayloadAppliesExplicitEmptyWireGuardAndEgressProfiles(t *testing.T) {
	previous := model.Snapshot{
		DesiredVersion: "previous",
		Revision:       7,
		WireGuardProfiles: []model.WireGuardProfile{{
			ID:         41,
			AgentID:    "remote-leaked",
			Name:       "leaked",
			PrivateKey: "leaked-private-key",
			Enabled:    true,
			Revision:   7,
		}},
		EgressProfiles: []model.EgressProfile{{
			ID:       42,
			Name:     "stale",
			Type:     "socks",
			ProxyURL: "socks5://127.0.0.1:1080",
			Enabled:  true,
			Revision: 7,
		}},
		Certificates: []model.ManagedCertificateBundle{{
			ID:       21,
			Domain:   "sync.example.com",
			Revision: 3,
			CertPEM:  "CERTIFICATE",
			KeyPEM:   "PRIVATEKEY",
		}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{
			ID:              21,
			Domain:          "sync.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			Status:          "issued",
			Revision:        3,
			Usage:           "relay_ca",
			CertificateType: "internal_ca",
			SelfSigned:      true,
		}},
	}
	var next model.Snapshot
	if err := json.Unmarshal([]byte(`{"desired_version":"cleanup","desired_revision":8,"wireguard_profiles":[],"egress_profiles":[],"certificates":[],"certificate_policies":[]}`), &next); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	merged := MergeSnapshotPayload(next, previous)

	if merged.WireGuardProfiles == nil || len(merged.WireGuardProfiles) != 0 {
		t.Fatalf("WireGuardProfiles = %+v, want explicit empty slice", merged.WireGuardProfiles)
	}
	if merged.EgressProfiles == nil || len(merged.EgressProfiles) != 0 {
		t.Fatalf("EgressProfiles = %+v, want explicit empty slice", merged.EgressProfiles)
	}
	if merged.Certificates == nil || len(merged.Certificates) != 0 {
		t.Fatalf("Certificates = %+v, want explicit empty slice", merged.Certificates)
	}
	if merged.CertificatePolicies == nil || len(merged.CertificatePolicies) != 0 {
		t.Fatalf("CertificatePolicies = %+v, want explicit empty slice", merged.CertificatePolicies)
	}
}

type syncControllerClient struct {
	snapshot model.Snapshot
	err      error
	calls    int
}

func (c *syncControllerClient) Sync(context.Context, agentsync.SyncRequest) (model.Snapshot, error) {
	c.calls++
	return c.snapshot, c.err
}

type syncControllerSequenceClient struct {
	responses []model.Snapshot
	err       error
	calls     int
}

func (c *syncControllerSequenceClient) Sync(context.Context, agentsync.SyncRequest) (model.Snapshot, error) {
	c.calls++
	if c.err != nil {
		return model.Snapshot{}, c.err
	}
	if c.calls <= len(c.responses) {
		return c.responses[c.calls-1], nil
	}
	return model.Snapshot{}, nil
}

type syncControllerUpdater struct {
	stageCalls      int
	activateCalls   int
	packages        []model.VersionPackage
	desiredVersions []string
	stageErr        error
	activateErr     error
}

func (u *syncControllerUpdater) Stage(_ context.Context, pkg model.VersionPackage) (string, error) {
	u.stageCalls++
	u.packages = append(u.packages, pkg)
	if u.stageErr != nil {
		return "", u.stageErr
	}
	return "staged/nre-agent", nil
}

func (u *syncControllerUpdater) Activate(_ string, desiredVersion string) error {
	u.activateCalls++
	u.desiredVersions = append(u.desiredVersions, desiredVersion)
	return u.activateErr
}

type syncControllerTrafficReporter struct {
	report TrafficReport
	err    error
}

func (r syncControllerTrafficReporter) TrafficReport(context.Context, map[string]string) (TrafficReport, error) {
	return r.report, r.err
}

type syncControllerCertificateReporter struct {
	reports []model.ManagedCertificateReport
	err     error
}

func (r syncControllerCertificateReporter) ManagedCertificateReports(context.Context) ([]model.ManagedCertificateReport, error) {
	return append([]model.ManagedCertificateReport(nil), r.reports...), r.err
}

type syncControllerStore struct {
	desired           model.Snapshot
	applied           model.Snapshot
	runtime           store.RuntimeState
	desiredSaveCount  int
	appliedSaveCount  int
	runtimeSaveCount  int
	failOnDesiredSave int
	failOnAppliedSave int
	failOnRuntimeSave int
}

func newSyncControllerStore() *syncControllerStore {
	return &syncControllerStore{}
}

func (s *syncControllerStore) SaveDesiredSnapshot(snapshot model.Snapshot) error {
	s.desiredSaveCount++
	if s.failOnDesiredSave > 0 && s.desiredSaveCount == s.failOnDesiredSave {
		return errors.New("desired persistence fail")
	}
	s.desired = snapshot
	return nil
}

func (s *syncControllerStore) LoadDesiredSnapshot() (model.Snapshot, error) {
	return s.desired, nil
}

func (s *syncControllerStore) SaveAppliedSnapshot(snapshot model.Snapshot) error {
	s.appliedSaveCount++
	if s.failOnAppliedSave > 0 && s.appliedSaveCount == s.failOnAppliedSave {
		return errors.New("applied persistence fail")
	}
	s.applied = snapshot
	return nil
}

func (s *syncControllerStore) LoadAppliedSnapshot() (model.Snapshot, error) {
	return s.applied, nil
}

func (s *syncControllerStore) SaveRuntimeState(state store.RuntimeState) error {
	s.runtimeSaveCount++
	if s.failOnRuntimeSave > 0 && s.runtimeSaveCount == s.failOnRuntimeSave {
		return errors.New("runtime state persistence fail")
	}
	s.runtime = copySyncControllerRuntimeState(state)
	return nil
}

func (s *syncControllerStore) LoadRuntimeState() (store.RuntimeState, error) {
	return copySyncControllerRuntimeState(s.runtime), nil
}

func copySyncControllerRuntimeState(state store.RuntimeState) store.RuntimeState {
	copied := state
	if state.Metadata != nil {
		copied.Metadata = make(map[string]string, len(state.Metadata))
		for key, value := range state.Metadata {
			copied.Metadata[key] = value
		}
	}
	return copied
}
