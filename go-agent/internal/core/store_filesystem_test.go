package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
)

func TestFilesystemStorePersistsGenerationJournalAndLastKnownGood(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem() error = %v", err)
	}
	lease := model.RevisionLease{
		AgentID: "edge-1", Revision: 7, Attempt: 1, LeaseID: "lease-7",
		SnapshotDigest: "digest-7", DeadlineAt: time.Now().Add(time.Minute).UTC().Truncate(time.Second),
	}
	journal := model.GenerationJournal{
		Version: 1,
		AgentID: "edge-1",
		Candidate: &model.GenerationRecord{
			GenerationID: "generation-7", RuntimeGenerationID: "runtime-generation-7",
			RuntimeSnapshotHash: "runtime-hash-7", Revision: 7, SnapshotDigest: "digest-7",
			Phase: model.GenerationPhaseStarted, Lease: lease, UpdatedAt: time.Now().UTC().Truncate(time.Second),
		},
	}
	lastKnownGood := model.Snapshot{DesiredVersion: "stable", Revision: 6}
	if err := store.SaveGenerationJournal(journal); err != nil {
		t.Fatalf("SaveGenerationJournal() error = %v", err)
	}
	if err := store.SaveLastKnownGoodSnapshot(lastKnownGood); err != nil {
		t.Fatalf("SaveLastKnownGoodSnapshot() error = %v", err)
	}

	reopened, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("reopen NewFilesystem() error = %v", err)
	}
	gotJournal, err := reopened.LoadGenerationJournal()
	if err != nil {
		t.Fatalf("LoadGenerationJournal() error = %v", err)
	}
	if gotJournal.Candidate == nil || gotJournal.Candidate.Phase != model.GenerationPhaseStarted ||
		gotJournal.Candidate.Lease.LeaseID != lease.LeaseID ||
		gotJournal.Candidate.RuntimeGenerationID != journal.Candidate.RuntimeGenerationID ||
		gotJournal.Candidate.RuntimeSnapshotHash != journal.Candidate.RuntimeSnapshotHash {
		t.Fatalf("generation journal = %+v, want persisted started candidate", gotJournal)
	}
	gotLastKnownGood, err := reopened.LoadLastKnownGoodSnapshot()
	if err != nil {
		t.Fatalf("LoadLastKnownGoodSnapshot() error = %v", err)
	}
	if gotLastKnownGood.Revision != lastKnownGood.Revision {
		t.Fatalf("LKG revision = %d, want %d", gotLastKnownGood.Revision, lastKnownGood.Revision)
	}
}

func TestFilesystemStoreFreshRevisionStateLoadsAsEmpty(t *testing.T) {
	store, err := NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystem() error = %v", err)
	}
	applied, err := store.LoadAppliedSnapshot()
	if err != nil {
		t.Fatalf("LoadAppliedSnapshot() on fresh store error = %v", err)
	}
	journal, err := store.LoadGenerationJournal()
	if err != nil {
		t.Fatalf("LoadGenerationJournal() on fresh store error = %v", err)
	}
	lastKnownGood, err := store.LoadLastKnownGoodSnapshot()
	if err != nil {
		t.Fatalf("LoadLastKnownGoodSnapshot() on fresh store error = %v", err)
	}
	if !isZeroSnapshot(applied) || journal.Version != 0 || !isZeroSnapshot(lastKnownGood) {
		t.Fatalf("fresh applied/journal/LKG = %+v/%+v/%+v, want empty state", applied, journal, lastKnownGood)
	}
}

func TestFilesystemStoreFallsBackToLastKnownGoodWhenAppliedIsCorrupt(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem() error = %v", err)
	}
	lastKnownGood := model.Snapshot{DesiredVersion: "stable", Revision: 6}
	if err := store.SaveLastKnownGoodSnapshot(lastKnownGood); err != nil {
		t.Fatalf("SaveLastKnownGoodSnapshot() error = %v", err)
	}
	if err := store.SaveAppliedSnapshot(model.Snapshot{DesiredVersion: "candidate", Revision: 7}); err != nil {
		t.Fatalf("SaveAppliedSnapshot() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, appliedSnapshotFile), []byte(`{"truncated":`), 0600); err != nil {
		t.Fatalf("corrupt applied snapshot: %v", err)
	}

	got, err := store.LoadAppliedSnapshot()
	if err != nil {
		t.Fatalf("LoadAppliedSnapshot() error = %v", err)
	}
	if got.Revision != lastKnownGood.Revision || got.DesiredVersion != lastKnownGood.DesiredVersion {
		t.Fatalf("LoadAppliedSnapshot() = %+v, want LKG %+v", got, lastKnownGood)
	}
}

func TestFilesystemStoreWritesPrivateStateFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX permission enforcement")
	}
	dir := t.TempDir()
	store, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem() error = %v", err)
	}
	if err := store.SaveDesiredSnapshot(model.Snapshot{Revision: 1}); err != nil {
		t.Fatalf("SaveDesiredSnapshot() error = %v", err)
	}
	if err := store.SaveAppliedSnapshot(model.Snapshot{Revision: 1}); err != nil {
		t.Fatalf("SaveAppliedSnapshot() error = %v", err)
	}
	if err := store.SaveLastKnownGoodSnapshot(model.Snapshot{Revision: 1}); err != nil {
		t.Fatalf("SaveLastKnownGoodSnapshot() error = %v", err)
	}
	if err := store.SaveGenerationJournal(model.GenerationJournal{Version: 1}); err != nil {
		t.Fatalf("SaveGenerationJournal() error = %v", err)
	}
	if err := store.SaveRuntimeState(model.RuntimeState{CurrentRevision: 1}); err != nil {
		t.Fatalf("SaveRuntimeState() error = %v", err)
	}

	for _, name := range []string{
		desiredSnapshotFile, appliedSnapshotFile, lastKnownGoodSnapshotFile,
		generationJournalFile, runtimeStateFile,
	} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("Stat(%s) error = %v", name, err)
		}
		if got := info.Mode().Perm(); got != 0600 {
			t.Fatalf("%s permissions = %o, want 600", name, got)
		}
	}
}

func TestFilesystemStoreRepeatedAtomicJournalSavesLeaveLatestCompleteValue(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem() error = %v", err)
	}
	for revision := int64(1); revision <= 10; revision++ {
		journal := model.GenerationJournal{
			Version: 1,
			AgentID: "edge-1",
			Candidate: &model.GenerationRecord{
				GenerationID: "generation", Revision: revision, Phase: model.GenerationPhasePrepared,
			},
		}
		if err := store.SaveGenerationJournal(journal); err != nil {
			t.Fatalf("SaveGenerationJournal(%d) error = %v", revision, err)
		}
	}
	got, err := store.LoadGenerationJournal()
	if err != nil {
		t.Fatalf("LoadGenerationJournal() error = %v", err)
	}
	if got.Candidate == nil || got.Candidate.Revision != 10 {
		t.Fatalf("latest journal = %+v, want revision 10", got)
	}
	temps, err := filepath.Glob(filepath.Join(dir, generationJournalFile+".tmp*"))
	if err != nil {
		t.Fatalf("Glob temporary journals: %v", err)
	}
	if len(temps) != 0 {
		t.Fatalf("temporary journal files remain: %v", temps)
	}
}

func TestFilesystemStoreMarksDirectorySyncFailureAsCommitUncertain(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem() error = %v", err)
	}
	store.syncDirectory = func(string) error { return errors.New("directory sync unavailable") }

	err = store.SaveAppliedSnapshot(model.Snapshot{DesiredVersion: "candidate", Revision: 7})
	if err == nil || !isFilesystemCommitUncertain(err) {
		t.Fatalf("SaveAppliedSnapshot() error = %v, want commit-uncertain error", err)
	}
	reopened, openErr := NewFilesystem(dir)
	if openErr != nil {
		t.Fatalf("reopen NewFilesystem() error = %v", openErr)
	}
	got, loadErr := reopened.LoadAppliedSnapshot()
	if loadErr != nil {
		t.Fatalf("LoadAppliedSnapshot() after uncertain commit error = %v", loadErr)
	}
	if got.Revision != 7 {
		t.Fatalf("visible applied revision = %d, want committed revision 7", got.Revision)
	}
}

func TestFilesystemStorePersistsAppliedSnapshot(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem returned error: %v", err)
	}

	err = s.SaveAppliedSnapshot(model.Snapshot{DesiredVersion: "1.2.3"})
	if err != nil {
		t.Fatalf("SaveAppliedSnapshot returned error: %v", err)
	}

	s2, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem returned error: %v", err)
	}

	got, err := s2.LoadAppliedSnapshot()
	if err != nil {
		t.Fatalf("LoadAppliedSnapshot returned error: %v", err)
	}

	if got.DesiredVersion != "1.2.3" {
		t.Fatalf("expected applied desired version 1.2.3, got %q", got.DesiredVersion)
	}
}

func TestFilesystemStorePersistsDesiredSnapshot(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem returned error: %v", err)
	}

	err = s.SaveDesiredSnapshot(model.Snapshot{DesiredVersion: "9.9.9"})
	if err != nil {
		t.Fatalf("SaveDesiredSnapshot returned error: %v", err)
	}

	s2, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem returned error: %v", err)
	}

	got, err := s2.LoadDesiredSnapshot()
	if err != nil {
		t.Fatalf("LoadDesiredSnapshot returned error: %v", err)
	}

	if got.DesiredVersion != "9.9.9" {
		t.Fatalf("expected desired version 9.9.9, got %q", got.DesiredVersion)
	}
}

func TestFilesystemStorePreservesExplicitEmptyRevisionSlices(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem returned error: %v", err)
	}

	expected := model.Snapshot{
		DesiredVersion:      "9.9.9",
		Revision:            12,
		Certificates:        []model.ManagedCertificateBundle{},
		CertificatePolicies: []model.ManagedCertificatePolicy{},
		PluginGenerations:   []model.PluginGeneration{},
		PluginDependencies:  []model.PluginDependencyEdge{},
		PluginPolicies:      []model.PluginPolicy{},
	}
	if err := s.SaveDesiredSnapshot(expected); err != nil {
		t.Fatalf("SaveDesiredSnapshot returned error: %v", err)
	}

	s2, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem returned error: %v", err)
	}

	got, err := s2.LoadDesiredSnapshot()
	if err != nil {
		t.Fatalf("LoadDesiredSnapshot returned error: %v", err)
	}
	if got.Certificates == nil || len(got.Certificates) != 0 {
		t.Fatalf("expected explicit empty certificates slice, got %+v", got.Certificates)
	}
	if got.CertificatePolicies == nil || len(got.CertificatePolicies) != 0 {
		t.Fatalf("expected explicit empty certificate policies slice, got %+v", got.CertificatePolicies)
	}
	if got.PluginGenerations == nil || got.PluginDependencies == nil || got.PluginPolicies == nil {
		t.Fatalf("expected explicit empty plugin graph slices, got generations=%#v dependencies=%#v policies=%#v", got.PluginGenerations, got.PluginDependencies, got.PluginPolicies)
	}
}

func TestFilesystemStorePersistsRuntimeState(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem returned error: %v", err)
	}

	expected := model.RuntimeState{
		NodeID: "agent-42",
		PluginLogReports: []model.PluginRuntimeLogReport{{
			Revision: 7, GenerationID: "generation-7", InstanceID: "instance-7", PluginID: "example.rpc", AgentID: "edge-7",
			PackageDigest: strings.Repeat("a", 64), ArtifactDigest: strings.Repeat("b", 64), Sequence: 2,
			Entries: []model.PluginRuntimeLogEntry{{Level: "error", Message: "persisted", Truncated: true}},
		}},
		Metadata: map[string]string{
			"session": "abc123",
		},
	}

	if err := s.SaveRuntimeState(expected); err != nil {
		t.Fatalf("SaveRuntimeState returned error: %v", err)
	}

	s2, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem returned error: %v", err)
	}

	got, err := s2.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState returned error: %v", err)
	}

	if got.NodeID != expected.NodeID {
		t.Fatalf("expected node ID %q, got %q", expected.NodeID, got.NodeID)
	}

	if val := got.Metadata["session"]; val != "abc123" {
		t.Fatalf("expected metadata session=abc123, got %q", val)
	}
	if !reflect.DeepEqual(got.PluginLogReports, expected.PluginLogReports) {
		t.Fatalf("restarted filesystem outbox = %+v, want %+v", got.PluginLogReports, expected.PluginLogReports)
	}
	pluginprocess.DrainRuntimeLogEvents()
	t.Cleanup(func() { pluginprocess.DrainRuntimeLogEvents() })
	persisted := got.PluginLogReports[0]
	pluginprocess.RestoreRuntimeLogEvents([]pluginprocess.RuntimeLogEvent{{
		Identity: pluginprocess.RuntimeLogIdentity{
			Revision: persisted.Revision, ProviderGenerationID: persisted.GenerationID, InstanceID: persisted.InstanceID,
			PluginID: persisted.PluginID, AgentID: persisted.AgentID, PackageDigest: persisted.PackageDigest, ArtifactDigest: persisted.ArtifactDigest,
		},
		Entry: pluginprocess.RuntimeLogEntry{Level: "info", Message: "after restart"},
	}})
	plan, err := (&SyncController{Store: s2}).BuildSyncPlan(t.Context(), model.Snapshot{Revision: 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Request.PluginLogs) != 2 || plan.Request.PluginLogs[1].Sequence != 3 || plan.Request.PluginLogs[1].Entries[0].Message != "after restart" {
		t.Fatalf("restarted outbox continuation = %+v", plan.Request.PluginLogs)
	}
}

func TestFilesystemStoreWritesSeparateFiles(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem returned error: %v", err)
	}

	if err := s.SaveDesiredSnapshot(model.Snapshot{DesiredVersion: "desired"}); err != nil {
		t.Fatalf("SaveDesiredSnapshot returned error: %v", err)
	}
	if err := s.SaveAppliedSnapshot(model.Snapshot{DesiredVersion: "applied"}); err != nil {
		t.Fatalf("SaveAppliedSnapshot returned error: %v", err)
	}
	expected := model.RuntimeState{
		NodeID: "node-b",
	}
	if err := s.SaveRuntimeState(expected); err != nil {
		t.Fatalf("SaveRuntimeState returned error: %v", err)
	}

	readSnapshot := func(name string, dest interface{}) {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("ReadFile %s failed: %v", name, err)
		}
		if err := json.Unmarshal(data, dest); err != nil {
			t.Fatalf("Unmarshal %s failed: %v", name, err)
		}
	}

	var desired model.Snapshot
	readSnapshot(desiredSnapshotFile, &desired)
	if desired.DesiredVersion != "desired" {
		t.Fatalf("desired file content mismatch: %s", desired.DesiredVersion)
	}

	var applied model.Snapshot
	readSnapshot(appliedSnapshotFile, &applied)
	if applied.DesiredVersion != "applied" {
		t.Fatalf("applied file content mismatch: %s", applied.DesiredVersion)
	}

	var runtime model.RuntimeState
	readSnapshot(runtimeStateFile, &runtime)
	if runtime.NodeID != expected.NodeID {
		t.Fatalf("runtime file content mismatch: %s", runtime.NodeID)
	}
}

func TestInMemoryRuntimeStateCopiesMetadata(t *testing.T) {
	s := NewInMemory()
	original := map[string]string{
		"key": "value",
	}
	if err := s.SaveRuntimeState(RuntimeState{
		NodeID:   "node-x",
		Metadata: original,
	}); err != nil {
		t.Fatalf("SaveRuntimeState returned error: %v", err)
	}

	original["key"] = "mutated"

	loaded, err := s.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState returned error: %v", err)
	}
	if got := loaded.Metadata["key"]; got != "value" {
		t.Fatalf("metadata aliasing detected on load: %s", got)
	}

	loaded.Metadata["key"] = "changed-after-load"
	newLoad, err := s.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState returned error: %v", err)
	}
	if got := newLoad.Metadata["key"]; got != "value" {
		t.Fatalf("metadata aliasing detected on subsequent load: %s", got)
	}
}
