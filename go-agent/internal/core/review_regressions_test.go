package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestUpdateManagerStagesLegacyPolicyWithoutManifestMetadata(t *testing.T) {
	payload := []byte("legacy-policy-agent")
	source := filepath.Join(t.TempDir(), "nre-agent-linux-amd64")
	if err := os.WriteFile(source, payload, 0o755); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	mgr := NewUpdateManager(t.TempDir(), "", nil, nil, nil, nil)
	mgr.platform = "linux-amd64"

	staged, err := mgr.Stage(t.Context(), model.VersionPackage{
		URL: "file:///" + filepath.ToSlash(source), SHA256: hex.EncodeToString(digest[:]), Platform: "linux-amd64",
	})
	if err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	manifestPayload, err := os.ReadFile(filepath.Join(filepath.Dir(staged), packageManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	var manifest model.PackageManifest
	if err := json.Unmarshal(manifestPayload, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Filename != filepath.Base(source) || manifest.Size != int64(len(payload)) {
		t.Fatalf("derived manifest = %+v", manifest)
	}
}

func TestRevisionSyncAppliesHeartbeatPackageWithoutRevision(t *testing.T) {
	store := &reviewJournalStore{InMemory: NewInMemory()}
	updater := &reviewUpdater{}
	client := &reviewRevisionClient{heartbeat: model.Snapshot{
		DesiredVersion: "bundled", VersionPackage: &model.VersionPackage{URL: "https://example.test/nre-agent", SHA256: "digest"},
	}}
	controller := &SyncController{
		Store: store, Runtime: NewRuntime(), SyncClient: client, Updater: updater,
	}

	err := controller.PerformSyncPlan(t.Context(), SyncPlan{Request: control.SyncRequest{}})
	if !errors.Is(err, ErrRestartRequested) {
		t.Fatalf("PerformSyncPlan() error = %v, want ErrRestartRequested", err)
	}
	if updater.preflightCalls != 1 || updater.stageCalls != 1 || updater.activateCalls != 1 {
		t.Fatalf("package calls = %d/%d/%d, want 1/1/1", updater.preflightCalls, updater.stageCalls, updater.activateCalls)
	}
}

func TestCompletedRuntimeDrainWaitsForCleanup(t *testing.T) {
	active := model.GenerationRecord{Revision: 2, RuntimeGenerationID: "runtime-2"}
	predecessor := model.GenerationRecord{Revision: 1, RuntimeGenerationID: "runtime-1"}
	snapshot := model.GenerationDrainSnapshot{Generations: []model.GenerationDrainStatus{{
		GenerationID: "runtime-1", Revision: 1, State: model.GenerationDrainStateDrained,
	}}}
	if _, ok := completedRuntimeDrain(snapshot, true, predecessor, active); ok {
		t.Fatal("drain reported before runtime cleanup completed")
	}
	snapshot.Generations[0].CompletedAt = time.Now().UTC()
	if _, ok := completedRuntimeDrain(snapshot, true, predecessor, active); !ok {
		t.Fatal("terminal runtime drain was not reportable")
	}
}

func TestCompletedDrainUsesActiveAuthoritativeLease(t *testing.T) {
	store := &reviewJournalStore{InMemory: NewInMemory()}
	client := &reviewRevisionClient{}
	journal := model.GenerationJournal{
		Version: 1,
		Active: &model.GenerationRecord{
			GenerationID: "protocol-2", Revision: 2, Phase: model.GenerationPhaseActive, Acknowledged: true,
			Lease: model.RevisionLease{AgentID: "edge-1", Revision: 2, RetryCycle: 3, Attempt: 4, LeaseID: "lease-2"},
		},
		Draining: []model.GenerationRecord{{GenerationID: "protocol-1", Revision: 1, Phase: model.GenerationPhaseActive}},
	}
	controller := &SyncController{Runtime: NewRuntime()}
	if err := controller.reportCompletedGenerationDrains(t.Context(), client, store, &journal); err != nil {
		t.Fatal(err)
	}
	if len(client.reports) != 1 {
		t.Fatalf("reports = %+v", client.reports)
	}
	report := client.reports[0]
	if report.Revision != 2 || report.RetryCycle != 3 || report.Attempt != 4 || report.LeaseID != "lease-2" ||
		report.GenerationID != "protocol-1" || report.Status != model.GenerationDrainStateDrained {
		t.Fatalf("drain report = %+v", report)
	}
	if len(store.journal.Draining) != 0 {
		t.Fatalf("persisted draining journal = %+v", store.journal.Draining)
	}
}

type reviewJournalStore struct {
	*InMemory
	journal model.GenerationJournal
	lkg     model.Snapshot
}

func (s *reviewJournalStore) SaveGenerationJournal(journal model.GenerationJournal) error {
	s.journal = journal
	return nil
}

func (s *reviewJournalStore) LoadGenerationJournal() (model.GenerationJournal, error) {
	return s.journal, nil
}

func (s *reviewJournalStore) SaveLastKnownGoodSnapshot(snapshot model.Snapshot) error {
	s.lkg = snapshot
	return nil
}

func (s *reviewJournalStore) LoadLastKnownGoodSnapshot() (model.Snapshot, error) {
	return s.lkg, nil
}

type reviewRevisionClient struct {
	heartbeat model.Snapshot
	reports   []model.RevisionReport
}

func (c *reviewRevisionClient) Sync(context.Context, control.SyncRequest) (model.Snapshot, error) {
	return c.heartbeat, nil
}

func (*reviewRevisionClient) PullRevision(context.Context) (model.RevisionPull, error) {
	return model.RevisionPull{}, nil
}

func (*reviewRevisionClient) StartRevision(context.Context, model.RevisionStart) error { return nil }
func (c *reviewRevisionClient) ReportRevision(_ context.Context, report model.RevisionReport) error {
	c.reports = append(c.reports, report)
	return nil
}

type reviewUpdater struct {
	preflightCalls int
	stageCalls     int
	activateCalls  int
}

func (u *reviewUpdater) Preflight(model.VersionPackage) error {
	u.preflightCalls++
	return nil
}

func (u *reviewUpdater) Stage(context.Context, model.VersionPackage) (string, error) {
	u.stageCalls++
	return "staged", nil
}

func (u *reviewUpdater) Activate(string, string) error {
	u.activateCalls++
	return ErrRestartRequested
}
