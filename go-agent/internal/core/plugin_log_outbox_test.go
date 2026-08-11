package core

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestFilesystemPluginLogOutboxSurvivesRestartAndExactMultiBatchACK(t *testing.T) {
	directory := t.TempDir()
	store, err := NewFilesystem(directory)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.EnqueuePluginLogReports(pluginLogTestBatchID(1), []model.PluginRuntimeLogReport{pluginLogTestDraft("same")})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.EnqueuePluginLogReports(pluginLogTestBatchID(2), []model.PluginRuntimeLogReport{pluginLogTestDraft("same")})
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Sequence != 1 || second[0].Sequence != 2 {
		t.Fatalf("sequences = %d, %d", first[0].Sequence, second[0].Sequence)
	}
	restarted, err := NewFilesystem(directory)
	if err != nil {
		t.Fatal(err)
	}
	third, err := restarted.EnqueuePluginLogReports(pluginLogTestBatchID(3), []model.PluginRuntimeLogReport{pluginLogTestDraft("later")})
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.AcknowledgePluginLogReports(append(first, second...)); err != nil {
		t.Fatal(err)
	}
	if err := restarted.AcknowledgePluginLogReports(append(first, second...)); err != nil {
		t.Fatal(err)
	}
	afterACK, err := NewFilesystem(directory)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := afterACK.PendingPluginLogReports()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Sequence != 3 || pending[0].Entries[0].Message != third[0].Entries[0].Message {
		t.Fatalf("pending after exact ACK = %+v", pending)
	}
}

func TestFilesystemPluginLogOutboxRecoversPartialCrashTail(t *testing.T) {
	directory := t.TempDir()
	store, err := NewFilesystem(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueuePluginLogReports(pluginLogTestBatchID(1), []model.PluginRuntimeLogReport{pluginLogTestDraft("before")}); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(directory, pluginLogOutboxFile), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"version":1`); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewFilesystem(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.EnqueuePluginLogReports(pluginLogTestBatchID(2), []model.PluginRuntimeLogReport{pluginLogTestDraft("after")}); err != nil {
		t.Fatal(err)
	}
	final, err := NewFilesystem(directory)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := final.PendingPluginLogReports()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 || pending[0].Sequence != 1 || pending[1].Sequence != 2 {
		t.Fatalf("recovered pending = %+v", pending)
	}
}

func TestFilesystemPluginLogOutboxRetryAfterDirectorySyncFailureIsIdempotent(t *testing.T) {
	store, err := NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	store.syncDirectory = func(string) error {
		if calls.Add(1) == 1 {
			return fmt.Errorf("injected directory sync failure")
		}
		return nil
	}
	draft := []model.PluginRuntimeLogReport{pluginLogTestDraft("uncertain")}
	if _, err := store.EnqueuePluginLogReports(pluginLogTestBatchID(1), draft); err == nil {
		t.Fatal("enqueue accepted uncertain directory durability")
	}
	retried, err := store.EnqueuePluginLogReports(pluginLogTestBatchID(1), draft)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingPluginLogReports()
	if err != nil {
		t.Fatal(err)
	}
	if len(retried) != 1 || retried[0].Sequence != 1 || len(pending) != 1 || pending[0].Sequence != 1 {
		t.Fatalf("idempotent retry = retried:%+v pending:%+v", retried, pending)
	}
	if _, err := store.EnqueuePluginLogReports(pluginLogTestBatchID(1), []model.PluginRuntimeLogReport{pluginLogTestDraft("different capture")}); err == nil {
		t.Fatal("capture batch identity collision was accepted")
	}
}

func TestFilesystemPluginLogOutboxSerializesConcurrentEnqueue(t *testing.T) {
	store, err := NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const count = 16
	var wait sync.WaitGroup
	errorsByWorker := make(chan error, count)
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, err := store.EnqueuePluginLogReports(pluginLogTestBatchID(index+1), []model.PluginRuntimeLogReport{pluginLogTestDraft(fmt.Sprintf("event-%d", index))})
			errorsByWorker <- err
		}(index)
	}
	wait.Wait()
	close(errorsByWorker)
	for err := range errorsByWorker {
		if err != nil {
			t.Fatal(err)
		}
	}
	pending, err := store.PendingPluginLogReports()
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[uint64]struct{}, count)
	for _, report := range pending {
		seen[report.Sequence] = struct{}{}
	}
	if len(pending) != count || len(seen) != count {
		t.Fatalf("concurrent pending sequences = %+v", pending)
	}
	for sequence := uint64(1); sequence <= count; sequence++ {
		if _, ok := seen[sequence]; !ok {
			t.Fatalf("missing sequence %d", sequence)
		}
	}
}

func TestFilesystemPluginLogOutboxCompactsLongRunningReplayState(t *testing.T) {
	directory := t.TempDir()
	store, err := NewFilesystem(directory)
	if err != nil {
		t.Fatal(err)
	}
	const cycles = 300
	for index := 1; index <= cycles; index++ {
		assigned, err := store.EnqueuePluginLogReports(pluginLogTestBatchID(index), []model.PluginRuntimeLogReport{pluginLogTestDraft(fmt.Sprintf("cycle-%d", index))})
		if err != nil {
			t.Fatal(err)
		}
		if assigned[0].Sequence != uint64(index) {
			t.Fatalf("cycle %d sequence = %d", index, assigned[0].Sequence)
		}
		if err := store.AcknowledgePluginLogReports(assigned); err != nil {
			t.Fatal(err)
		}
	}
	journal, err := os.ReadFile(filepath.Join(directory, pluginLogOutboxFile))
	if err != nil {
		t.Fatal(err)
	}
	if records := bytes.Count(journal, []byte("\n")); records > maxPluginLogOutboxJournalRecords {
		t.Fatalf("compacted journal retained %d records", records)
	}
	restarted, err := NewFilesystem(directory)
	if err != nil {
		t.Fatal(err)
	}
	next, err := restarted.EnqueuePluginLogReports(pluginLogTestBatchID(cycles+1), []model.PluginRuntimeLogReport{pluginLogTestDraft("after checkpoint")})
	if err != nil {
		t.Fatal(err)
	}
	if next[0].Sequence != cycles+1 {
		t.Fatalf("post-checkpoint sequence = %d", next[0].Sequence)
	}
	restarted.mu.Lock()
	state, err := restarted.loadPluginLogOutboxLocked()
	restarted.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.batches) > maxPluginLogReplayIdentities || len(state.acks) > maxPluginLogReplayIdentities {
		t.Fatalf("replay identities are unbounded: batches=%d ACKs=%d", len(state.batches), len(state.acks))
	}
}

func TestFilesystemPluginLogOutboxCheckpointCommitUncertaintyRestartsExactly(t *testing.T) {
	directory := t.TempDir()
	store, err := NewFilesystem(directory)
	if err != nil {
		t.Fatal(err)
	}
	batchID := pluginLogTestBatchID(1)
	assigned, err := store.EnqueuePluginLogReports(batchID, []model.PluginRuntimeLogReport{pluginLogTestDraft("pending")})
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	state, err := store.loadPluginLogOutboxLocked()
	if err != nil {
		store.mu.Unlock()
		t.Fatal(err)
	}
	state.records = maxPluginLogOutboxJournalRecords + 1
	store.syncDirectory = func(string) error { return errors.New("injected checkpoint directory sync failure") }
	err = store.maybeCompactPluginLogOutboxLocked(&state)
	store.mu.Unlock()
	if !isFilesystemCommitUncertain(err) {
		t.Fatalf("checkpoint error = %v", err)
	}
	restarted, err := NewFilesystem(directory)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := restarted.PendingPluginLogReports()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Sequence != assigned[0].Sequence {
		t.Fatalf("checkpoint restart pending = %+v", pending)
	}
	retried, err := restarted.EnqueuePluginLogReports(batchID, []model.PluginRuntimeLogReport{pluginLogTestDraft("pending")})
	if err != nil || len(retried) != 1 || retried[0].Sequence != 1 {
		t.Fatalf("checkpoint retry = %+v, %v", retried, err)
	}
}

func TestFilesystemPluginLogOutboxIgnoresPreRenameCheckpointCrashFile(t *testing.T) {
	directory := t.TempDir()
	store, err := NewFilesystem(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueuePluginLogReports(pluginLogTestBatchID(1), []model.PluginRuntimeLogReport{pluginLogTestDraft("authoritative")}); err != nil {
		t.Fatal(err)
	}
	crashFile, err := os.CreateTemp(directory, pluginLogOutboxFile+".checkpoint-")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := crashFile.WriteString(`{"partial_checkpoint":`); err != nil {
		t.Fatal(err)
	}
	if err := crashFile.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewFilesystem(directory)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := restarted.PendingPluginLogReports()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Entries[0].Message != "authoritative" {
		t.Fatalf("pre-rename crash file replaced journal: %+v", pending)
	}
}

func TestPluginLogOutboxBoundsHistoricalFenceHighWater(t *testing.T) {
	state := newPluginLogOutboxState()
	for index := 1; index <= maxPluginLogSequenceHighWater+100; index++ {
		draft := pluginLogTestDraft("historical")
		draft.GenerationID = fmt.Sprintf("generation-%d", index)
		assigned, _, err := state.enqueue(pluginLogTestBatchID(index), []model.PluginRuntimeLogReport{draft})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := state.acknowledge(assigned); err != nil {
			t.Fatal(err)
		}
	}
	if len(state.sequence) > maxPluginLogSequenceHighWater || len(state.batches) > maxPluginLogReplayIdentities || len(state.acks) > maxPluginLogReplayIdentities {
		t.Fatalf("historical replay state is unbounded: sequences=%d batches=%d ACKs=%d", len(state.sequence), len(state.batches), len(state.acks))
	}
}

func pluginLogTestDraft(message string) model.PluginRuntimeLogReport {
	return model.PluginRuntimeLogReport{
		Revision: 7, GenerationID: "generation-7", InstanceID: "instance-7", PluginID: "example.rpc", AgentID: "edge-7",
		PackageDigest: strings.Repeat("a", 64), ArtifactDigest: strings.Repeat("b", 64),
		Entries: []model.PluginRuntimeLogEntry{{Level: "info", Message: message}},
	}
}

func pluginLogTestBatchID(value int) string { return fmt.Sprintf("%064x", value) }
