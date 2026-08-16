//go:build !integration

package core

import (
	"context"

	"errors"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type syncControllerClient struct {
	snapshot model.Snapshot
	err      error
	calls    int
}

func (c *syncControllerClient) Sync(context.Context, control.SyncRequest) (model.Snapshot, error) {
	c.calls++
	return c.snapshot, c.err
}

type syncControllerSequenceClient struct {
	responses []model.Snapshot
	err       error
	calls     int
}

func (c *syncControllerSequenceClient) Sync(context.Context, control.SyncRequest) (model.Snapshot, error) {
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
	preflightCalls  int
	stageCalls      int
	activateCalls   int
	packages        []model.VersionPackage
	desiredVersions []string
	preflightErr    error
	stageErr        error
	activateErr     error
}

func (u *syncControllerUpdater) Preflight(model.VersionPackage) error {
	u.preflightCalls++
	return u.preflightErr
}

func (u *syncControllerUpdater) Stage(_ context.Context, pkg model.VersionPackage) (string, error) {
	u.stageCalls++
	u.packages = append(u.packages, pkg)
	if u.stageErr != nil {
		return "", u.stageErr
	}
	return "staged/nre-agent", nil
}

func (u *syncControllerUpdater) Activate(_ context.Context, _ string, desiredVersion string) error {
	u.activateCalls++
	u.desiredVersions = append(u.desiredVersions, desiredVersion)
	return u.activateErr
}

type syncControllerStore struct {
	desired           model.Snapshot
	applied           model.Snapshot
	runtime           RuntimeState
	desiredSaveCount  int
	appliedSaveCount  int
	runtimeSaveCount  int
	failOnDesiredSave int
	failOnAppliedSave int
	failOnRuntimeSave int
	pluginLogs        pluginLogOutboxState
}

func newSyncControllerStore() *syncControllerStore {
	return &syncControllerStore{pluginLogs: newPluginLogOutboxState()}
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

func (s *syncControllerStore) SaveRuntimeState(state RuntimeState) error {
	s.runtimeSaveCount++
	if s.failOnRuntimeSave > 0 && s.runtimeSaveCount == s.failOnRuntimeSave {
		return errors.New("runtime state persistence fail")
	}
	s.runtime = copySyncControllerRuntimeState(state)
	return nil
}

func (s *syncControllerStore) LoadRuntimeState() (RuntimeState, error) {
	return copySyncControllerRuntimeState(s.runtime), nil
}

func (s *syncControllerStore) EnqueuePluginLogReports(batchID string, drafts []model.PluginRuntimeLogReport) ([]model.PluginRuntimeLogReport, error) {
	reports, _, err := s.pluginLogs.enqueue(batchID, drafts)
	return reports, err
}

func (s *syncControllerStore) PendingPluginLogReports() ([]model.PluginRuntimeLogReport, error) {
	return model.ClonePluginRuntimeLogReports(s.pluginLogs.pending), nil
}

func (s *syncControllerStore) AcknowledgePluginLogReports(sent []model.PluginRuntimeLogReport) error {
	_, _, _, err := s.pluginLogs.acknowledge(sent)
	return err
}

func copySyncControllerRuntimeState(state RuntimeState) RuntimeState {
	copied := state
	copied.PluginLogReports = model.ClonePluginRuntimeLogReports(state.PluginLogReports)
	if state.Metadata != nil {
		copied.Metadata = make(map[string]string, len(state.Metadata))
		for key, value := range state.Metadata {
			copied.Metadata[key] = value
		}
	}
	return copied
}
