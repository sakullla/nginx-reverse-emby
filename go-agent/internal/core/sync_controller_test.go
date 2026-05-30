package core

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
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
	applied, _ := st.LoadAppliedSnapshot()
	if !reflect.DeepEqual(applied, previous) {
		t.Fatalf("applied snapshot = %+v, want previous %+v", applied, previous)
	}
	desired, _ := st.LoadDesiredSnapshot()
	if desired.VersionPackage == nil || desired.VersionPackage.SHA256 != "new-sha" {
		t.Fatalf("desired snapshot did not persist update package: %+v", desired)
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

type syncControllerClient struct {
	snapshot model.Snapshot
	err      error
	calls    int
}

func (c *syncControllerClient) Sync(context.Context, agentsync.SyncRequest) (model.Snapshot, error) {
	c.calls++
	return c.snapshot, c.err
}

type syncControllerUpdater struct {
	stageCalls    int
	activateCalls int
	stageErr      error
	activateErr   error
}

func (u *syncControllerUpdater) Stage(context.Context, model.VersionPackage) (string, error) {
	u.stageCalls++
	if u.stageErr != nil {
		return "", u.stageErr
	}
	return "staged/nre-agent", nil
}

func (u *syncControllerUpdater) Activate(string, string) error {
	u.activateCalls++
	return u.activateErr
}

type syncControllerTrafficReporter struct {
	report TrafficReport
	err    error
}

func (r syncControllerTrafficReporter) TrafficReport(context.Context, map[string]string) (TrafficReport, error) {
	return r.report, r.err
}

type syncControllerStore struct {
	desired           model.Snapshot
	applied           model.Snapshot
	runtime           store.RuntimeState
	appliedSaveCount  int
	runtimeSaveCount  int
	failOnAppliedSave int
	failOnRuntimeSave int
}

func newSyncControllerStore() *syncControllerStore {
	return &syncControllerStore{}
}

func (s *syncControllerStore) SaveDesiredSnapshot(snapshot model.Snapshot) error {
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
	s.runtime = state
	return nil
}

func (s *syncControllerStore) LoadRuntimeState() (store.RuntimeState, error) {
	return s.runtime, nil
}
