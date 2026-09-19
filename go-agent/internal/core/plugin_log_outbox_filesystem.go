package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
)

const pluginLogOutboxFile = "plugin-log-outbox.jsonl"

const maxPluginLogOutboxJournalRecords = 512

type pluginLogOutboxBatch struct {
	ID      string                         `json:"id"`
	Reports []model.PluginRuntimeLogReport `json:"reports"`
}

type pluginLogOutboxRecord struct {
	Version       int                            `json:"version"`
	Operation     string                         `json:"operation"`
	BatchID       string                         `json:"batch_id"`
	Reports       []model.PluginRuntimeLogReport `json:"reports,omitempty"`
	Identities    []string                       `json:"identities,omitempty"`
	Sequences     map[string]uint64              `json:"sequences,omitempty"`
	SequenceOrder []string                       `json:"sequence_order,omitempty"`
	Batches       []pluginLogOutboxBatch         `json:"batches,omitempty"`
	Acks          []string                       `json:"acks,omitempty"`
	Retired       []string                       `json:"retired,omitempty"`
	Fence         *pluginLogRetirementFence      `json:"fence,omitempty"`
	Intent        *pluginLogRetirementIntent     `json:"intent,omitempty"`
	Intents       []pluginLogRetirementIntent    `json:"intents,omitempty"`
	Digest        string                         `json:"digest"`
}

func (f *Filesystem) EnqueuePluginLogReports(batchID string, drafts []model.PluginRuntimeLogReport) ([]model.PluginRuntimeLogReport, error) {
	if len(drafts) == 0 {
		return nil, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	state, err := f.loadPluginLogOutboxLocked()
	if err != nil {
		return nil, err
	}
	assigned, existing, err := state.enqueue(batchID, drafts)
	if err != nil {
		return nil, err
	}
	if existing {
		if f.syncDirectory != nil {
			if err := f.syncDirectory(f.root); err != nil {
				return nil, err
			}
		}
		return model.ClonePluginRuntimeLogReports(assigned), nil
	}
	record := pluginLogOutboxRecord{Version: 1, Operation: "enqueue", BatchID: batchID, Reports: assigned}
	if err := f.appendPluginLogOutboxRecordLocked(record); err != nil {
		return nil, err
	}
	state.records++
	if err := f.maybeCompactPluginLogOutboxLocked(&state); err != nil {
		return nil, err
	}
	return model.ClonePluginRuntimeLogReports(assigned), nil
}

func (f *Filesystem) PendingPluginLogReports() ([]model.PluginRuntimeLogReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state, err := f.loadPluginLogOutboxLocked()
	if err != nil {
		return nil, err
	}
	if err := f.retryPluginLogRetirementDrainsLocked(&state); err != nil {
		return nil, err
	}
	return model.ClonePluginRuntimeLogReports(state.pending), nil
}

func (f *Filesystem) AcknowledgePluginLogReports(sent []model.PluginRuntimeLogReport) error {
	if len(sent) == 0 {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	state, err := f.loadPluginLogOutboxLocked()
	if err != nil {
		return err
	}
	ackID, identities, changed, err := state.acknowledge(sent)
	if err != nil || !changed {
		return err
	}
	if err := f.appendPluginLogOutboxRecordLocked(pluginLogOutboxRecord{Version: 1, Operation: "ack", BatchID: ackID, Identities: identities}); err != nil {
		return err
	}
	state.records++
	err = f.maybeCompactPluginLogOutboxLocked(&state)
	f.logCapacity.notify()
	return err
}

func (f *Filesystem) WaitForPluginLogCapacity(ctx context.Context) error {
	return waitPluginLogCapacity(ctx, &f.logCapacity, func() (int, error) {
		pending, err := f.PendingPluginLogReports()
		return len(pending), err
	})
}

func (f *Filesystem) RetirePluginRuntimeLogFence(identity pluginprocess.RuntimeLogIdentity) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	state, err := f.loadPluginLogOutboxLocked()
	if err != nil {
		return err
	}
	fenceID, changed, err := state.retire(identity)
	if err != nil || !changed {
		return err
	}
	digest := sha256.Sum256([]byte(fenceID))
	record := pluginLogOutboxRecord{
		Version: 1, Operation: "retire", BatchID: hex.EncodeToString(digest[:]), Fence: ptrPluginLogRetirementFence(pluginLogRetirementFenceFromIdentity(identity)),
	}
	if err := f.appendPluginLogOutboxRecordLocked(record); err != nil {
		return err
	}
	state.records++
	return f.maybeCompactPluginLogOutboxLocked(&state)
}

func (f *Filesystem) StagePluginRuntimeLogRetirementIntent(id string, revision int64, identities []pluginprocess.RuntimeLogIdentity) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	state, err := f.loadPluginLogOutboxLocked()
	if err != nil {
		return err
	}
	changed, err := state.stageRetirementIntent(id, revision, identities, f.pluginLogSessionID)
	if err != nil || !changed {
		return err
	}
	intent := clonePluginLogRetirementIntent(state.retireIntents[id])
	if err := f.appendPluginLogOutboxRecordLocked(pluginLogOutboxRecord{Version: 1, Operation: "retire_intent", BatchID: id, Intent: &intent}); err != nil {
		return err
	}
	state.records++
	return f.maybeCompactPluginLogOutboxLocked(&state)
}

func (f *Filesystem) CompletePluginRuntimeLogRetirementIntent(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	state, err := f.loadPluginLogOutboxLocked()
	if err != nil {
		return err
	}
	return f.completePluginLogRetirementIntentLocked(&state, id)
}

func (f *Filesystem) MarkPluginRuntimeLogRetirementIntentDrained(id string) error {
	if !validPluginLogOutboxID(id) {
		return errors.New("plugin runtime log retirement intent identity is invalid")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	state, err := f.loadPluginLogOutboxLocked()
	if err != nil {
		f.rememberPluginLogDrainRetryLocked(id)
		return nil
	}
	if err := f.markPluginLogRetirementIntentDrainedLocked(&state, id, true); err != nil {
		// Process destruction has already succeeded. Persistence retry ownership
		// belongs to the store from this point so a once-only GenerationView does
		// not turn a transient journal failure into a terminal resource failure.
		f.rememberPluginLogDrainRetryLocked(id)
		return nil
	}
	return nil
}

func (f *Filesystem) AbortPluginRuntimeLogRetirementIntent(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	state, err := f.loadPluginLogOutboxLocked()
	if err != nil {
		return err
	}
	changed, err := state.abortRetirementIntent(id)
	if err != nil || !changed {
		return err
	}
	if err := f.appendPluginLogOutboxRecordLocked(pluginLogOutboxRecord{Version: 1, Operation: "retire_intent_abort", BatchID: id}); err != nil {
		return err
	}
	state.records++
	return f.maybeCompactPluginLogOutboxLocked(&state)
}

func (f *Filesystem) completePluginLogRetirementIntentLocked(state *pluginLogOutboxState, id string) error {
	changed, err := state.completeRetirementIntent(id)
	if err != nil || !changed {
		return err
	}
	if err := f.appendPluginLogOutboxRecordLocked(pluginLogOutboxRecord{Version: 1, Operation: "retire_intent_complete", BatchID: id}); err != nil {
		return err
	}
	state.records++
	return f.maybeCompactPluginLogOutboxLocked(state)
}

func (f *Filesystem) markPluginLogRetirementIntentDrainedLocked(state *pluginLogOutboxState, id string, rememberFailure bool) error {
	changed, err := state.markRetirementIntentDrained(id)
	if err != nil {
		return err
	}
	if !changed {
		delete(f.pluginLogDrainRetries, id)
		return nil
	}
	if err := f.appendPluginLogOutboxRecordLocked(pluginLogOutboxRecord{Version: 1, Operation: "retire_intent_drained", BatchID: id}); err != nil {
		if rememberFailure {
			f.rememberPluginLogDrainRetryLocked(id)
		}
		return err
	}
	delete(f.pluginLogDrainRetries, id)
	state.records++
	return f.maybeCompactPluginLogOutboxLocked(state)
}

func (f *Filesystem) rememberPluginLogDrainRetryLocked(id string) {
	if f.pluginLogDrainRetries == nil {
		f.pluginLogDrainRetries = make(map[string]struct{})
	}
	f.pluginLogDrainRetries[id] = struct{}{}
}

func (f *Filesystem) retryPluginLogRetirementDrainsLocked(state *pluginLogOutboxState) error {
	ids := make([]string, 0, len(f.pluginLogDrainRetries))
	for id := range f.pluginLogDrainRetries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := f.markPluginLogRetirementIntentDrainedLocked(state, id, true); err != nil {
			return err
		}
	}
	return nil
}

func (f *Filesystem) AuthorizePluginRuntimeLogRetirementIntents(applied model.Snapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	state, err := f.loadPluginLogOutboxLocked()
	if err != nil {
		return err
	}
	if err := f.retryPluginLogRetirementDrainsLocked(&state); err != nil {
		return err
	}
	for _, id := range state.restartDrainableRetirementIntents(applied, f.pluginLogSessionID) {
		if err := f.markPluginLogRetirementIntentDrainedLocked(&state, id, false); err != nil {
			return err
		}
	}
	for _, id := range state.recoverableRetirementIntents(applied) {
		if err := f.completePluginLogRetirementIntentLocked(&state, id); err != nil {
			return err
		}
	}
	return nil
}

func (f *Filesystem) loadPluginLogOutboxLocked() (pluginLogOutboxState, error) {
	state := newPluginLogOutboxState()
	if f.pluginLogLoadFailure != nil {
		if err := f.pluginLogLoadFailure(); err != nil {
			return state, err
		}
	}
	data, err := os.ReadFile(filepath.Join(f.root, pluginLogOutboxFile))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	complete := bytes.HasSuffix(data, []byte("\n"))
	if !complete && len(data) > 0 {
		lastNewline := bytes.LastIndexByte(data, '\n')
		recoveredLength := int64(lastNewline + 1)
		file, truncateErr := os.OpenFile(filepath.Join(f.root, pluginLogOutboxFile), os.O_WRONLY, 0o600)
		if truncateErr != nil {
			return state, truncateErr
		}
		truncateErr = file.Truncate(recoveredLength)
		if truncateErr == nil {
			truncateErr = file.Sync()
		}
		closeErr := file.Close()
		if truncateErr != nil || closeErr != nil {
			return state, errors.Join(truncateErr, closeErr)
		}
		data = data[:recoveredLength]
		complete = true
	}
	lines := bytes.Split(data, []byte("\n"))
	for index, line := range lines {
		if len(line) == 0 {
			continue
		}
		if index == len(lines)-1 && !complete {
			break
		}
		var record pluginLogOutboxRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return state, fmt.Errorf("decode plugin log outbox record %d: %w", index+1, err)
		}
		if err := validatePluginLogOutboxRecord(record); err != nil {
			return state, fmt.Errorf("validate plugin log outbox record %d: %w", index+1, err)
		}
		state.records++
		switch record.Operation {
		case "checkpoint":
			checkpoint, err := pluginLogOutboxStateFromCheckpoint(record)
			if err != nil {
				return state, fmt.Errorf("restore plugin log outbox checkpoint %d: %w", index+1, err)
			}
			checkpoint.records = 1
			state = checkpoint
		case "enqueue":
			if existing, exists := state.batches[record.BatchID]; exists {
				if !pluginLogReportsEqual(existing, record.Reports) {
					return state, errors.New("plugin log outbox batch identity collision")
				}
				continue
			}
			if len(state.pending)+len(record.Reports) > model.MaxPendingPluginLogReports {
				return state, errors.New("plugin log outbox exceeds Agent bounds")
			}
			for _, report := range record.Reports {
				if err := report.Validate(); err != nil {
					return state, err
				}
				identity := pluginRuntimeLogFenceIdentity(report)
				delete(state.retired, identity)
				if report.Sequence <= state.sequence[identity] {
					return state, errors.New("plugin log outbox sequence is not monotonic")
				}
				if _, exists := state.sequence[identity]; !exists {
					state.sequenceOrder = append(state.sequenceOrder, identity)
				}
				state.sequence[identity] = report.Sequence
			}
			state.pending = append(state.pending, model.ClonePluginRuntimeLogReports(record.Reports)...)
			state.batches[record.BatchID] = model.ClonePluginRuntimeLogReports(record.Reports)
			state.batchOrder = append(state.batchOrder, record.BatchID)
		case "ack":
			if _, exists := state.acks[record.BatchID]; exists {
				continue
			}
			state.acks[record.BatchID] = struct{}{}
			state.ackOrder = append(state.ackOrder, record.BatchID)
			acked := make(map[string]struct{}, len(record.Identities))
			for _, identity := range record.Identities {
				acked[identity] = struct{}{}
			}
			kept := state.pending[:0]
			for _, pending := range state.pending {
				if _, ok := acked[pluginRuntimeLogReportIdentity(pending)]; !ok {
					kept = append(kept, pending)
				}
			}
			state.pending = model.ClonePluginRuntimeLogReports(kept)
		case "retire":
			if record.Fence == nil {
				return state, errors.New("plugin log retirement fence is missing")
			}
			if _, _, err := state.retire(record.Fence.runtimeIdentity()); err != nil {
				return state, err
			}
		case "retire_intent":
			if record.Intent == nil {
				return state, errors.New("plugin log retirement intent is missing")
			}
			identities := make([]pluginprocess.RuntimeLogIdentity, 0, len(record.Intent.Fences))
			for _, fence := range record.Intent.Fences {
				identities = append(identities, fence.runtimeIdentity())
			}
			if _, err := state.stageRetirementIntent(record.Intent.ID, record.Intent.Revision, identities, record.Intent.SessionID); err != nil {
				return state, err
			}
		case "retire_intent_drained":
			if changed, err := state.markRetirementIntentDrained(record.BatchID); err != nil {
				return state, err
			} else if !changed {
				return state, errors.New("plugin log retirement drained marker is dangling")
			}
		case "retire_intent_complete":
			if changed, err := state.completeRetirementIntent(record.BatchID); err != nil {
				return state, err
			} else if !changed {
				return state, errors.New("plugin log retirement completion is dangling")
			}
		case "retire_intent_abort":
			if changed, err := state.abortRetirementIntent(record.BatchID); err != nil {
				return state, err
			} else if !changed {
				return state, errors.New("plugin log retirement abort is dangling")
			}
		}
		state.trimReplayHistory()
	}
	return state, nil
}

func (f *Filesystem) maybeCompactPluginLogOutboxLocked(state *pluginLogOutboxState) error {
	if state == nil || state.records <= maxPluginLogOutboxJournalRecords {
		return nil
	}
	state.trimReplayHistory()
	record := pluginLogOutboxRecord{
		Version: 1, Operation: "checkpoint", BatchID: pluginLogCheckpointID(*state),
		Reports: model.ClonePluginRuntimeLogReports(state.pending), Sequences: make(map[string]uint64, len(state.sequence)),
		Acks:          append([]string(nil), state.ackOrder...),
		SequenceOrder: append([]string(nil), state.sequenceOrder...),
	}
	intentIDs := make([]string, 0, len(state.retireIntents))
	for id := range state.retireIntents {
		intentIDs = append(intentIDs, id)
	}
	sort.Strings(intentIDs)
	for _, id := range intentIDs {
		record.Intents = append(record.Intents, clonePluginLogRetirementIntent(state.retireIntents[id]))
	}
	for identity, sequence := range state.sequence {
		record.Sequences[identity] = sequence
	}
	for _, identity := range state.sequenceOrder {
		if _, retired := state.retired[identity]; retired {
			record.Retired = append(record.Retired, identity)
		}
	}
	for _, batchID := range state.batchOrder {
		reports, ok := state.batches[batchID]
		if !ok {
			continue
		}
		record.Batches = append(record.Batches, pluginLogOutboxBatch{ID: batchID, Reports: model.ClonePluginRuntimeLogReports(reports)})
	}
	sealed, err := sealPluginLogOutboxRecord(record)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(f.root, pluginLogOutboxFile+".checkpoint-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	removeTemp := func() { _ = os.Remove(tempPath) }
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		removeTemp()
		return err
	}
	writeErr := writePluginLogOutboxAll(temp, append(sealed, '\n'))
	if writeErr == nil {
		writeErr = temp.Sync()
	}
	closeErr := temp.Close()
	if writeErr != nil || closeErr != nil {
		removeTemp()
		return errors.Join(writeErr, closeErr)
	}
	if err := os.Rename(tempPath, filepath.Join(f.root, pluginLogOutboxFile)); err != nil {
		removeTemp()
		return err
	}
	state.records = 1
	if f.syncDirectory != nil {
		if err := f.syncDirectory(f.root); err != nil {
			return &filesystemCommitUncertainError{err: err}
		}
	}
	return nil
}

func pluginLogCheckpointID(state pluginLogOutboxState) string {
	payload, _ := json.Marshal(struct {
		Pending       []model.PluginRuntimeLogReport
		Sequence      map[string]uint64
		Retired       map[string]struct{}
		RetireIntents map[string]pluginLogRetirementIntent
	}{state.pending, state.sequence, state.retired, state.retireIntents})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func pluginLogOutboxStateFromCheckpoint(record pluginLogOutboxRecord) (pluginLogOutboxState, error) {
	state := newPluginLogOutboxState()
	state.pending = model.ClonePluginRuntimeLogReports(record.Reports)
	if len(record.SequenceOrder) != len(record.Sequences) {
		return state, errors.New("checkpoint sequence order is invalid")
	}
	seenSequences := make(map[string]struct{}, len(record.SequenceOrder))
	for _, identity := range record.SequenceOrder {
		if _, ok := record.Sequences[identity]; !ok {
			return state, errors.New("checkpoint sequence order is dangling")
		}
		if _, duplicate := seenSequences[identity]; duplicate {
			return state, errors.New("checkpoint sequence order is duplicated")
		}
		seenSequences[identity] = struct{}{}
		state.sequenceOrder = append(state.sequenceOrder, identity)
	}
	for identity, sequence := range record.Sequences {
		if identity == "" || sequence == 0 {
			return state, errors.New("checkpoint sequence is invalid")
		}
		state.sequence[identity] = sequence
	}
	for _, identity := range record.Retired {
		if _, exists := state.sequence[identity]; !exists {
			return state, errors.New("checkpoint retired fence is dangling")
		}
		if _, duplicate := state.retired[identity]; duplicate {
			return state, errors.New("checkpoint retired fence is duplicated")
		}
		state.retired[identity] = struct{}{}
	}
	for _, report := range state.pending {
		if err := report.Validate(); err != nil || state.sequence[pluginRuntimeLogFenceIdentity(report)] < report.Sequence {
			return state, errors.New("checkpoint pending report exceeds sequence high-water")
		}
	}
	for _, batch := range record.Batches {
		if !validPluginLogOutboxID(batch.ID) || len(batch.Reports) == 0 {
			return state, errors.New("checkpoint replay batch is invalid")
		}
		if _, duplicate := state.batches[batch.ID]; duplicate {
			return state, errors.New("checkpoint replay batch is duplicated")
		}
		for _, report := range batch.Reports {
			if err := report.Validate(); err != nil {
				return state, err
			}
		}
		state.batches[batch.ID] = model.ClonePluginRuntimeLogReports(batch.Reports)
		state.batchOrder = append(state.batchOrder, batch.ID)
	}
	for _, ackID := range record.Acks {
		if !validPluginLogOutboxID(ackID) {
			return state, errors.New("checkpoint ACK identity is invalid")
		}
		if _, duplicate := state.acks[ackID]; duplicate {
			return state, errors.New("checkpoint ACK identity is duplicated")
		}
		state.acks[ackID] = struct{}{}
		state.ackOrder = append(state.ackOrder, ackID)
	}
	for _, intent := range record.Intents {
		identities := make([]pluginprocess.RuntimeLogIdentity, 0, len(intent.Fences))
		for _, fence := range intent.Fences {
			identities = append(identities, fence.runtimeIdentity())
		}
		if changed, err := state.stageRetirementIntent(intent.ID, intent.Revision, identities, intent.SessionID); err != nil {
			return state, err
		} else if !changed {
			return state, errors.New("checkpoint retirement intent is duplicated")
		}
		if intent.Drained {
			if _, err := state.markRetirementIntentDrained(intent.ID); err != nil {
				return state, err
			}
		}
	}
	state.trimReplayHistory()
	if len(state.pending) > model.MaxPendingPluginLogReports || len(state.batches) > maxPluginLogReplayIdentities || len(state.acks) > maxPluginLogReplayIdentities {
		return state, errors.New("checkpoint exceeds Agent bounds")
	}
	return state, nil
}

func (f *Filesystem) appendPluginLogOutboxRecordLocked(record pluginLogOutboxRecord) error {
	if f.pluginLogAppendFailure != nil {
		if err := f.pluginLogAppendFailure(record.Operation); err != nil {
			return err
		}
	}
	sealed, err := sealPluginLogOutboxRecord(record)
	if err != nil {
		return err
	}
	path := filepath.Join(f.root, pluginLogOutboxFile)
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	writeErr := writePluginLogOutboxAll(file, sealed)
	if writeErr == nil {
		_, writeErr = file.Write([]byte("\n"))
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return errors.Join(writeErr, closeErr)
	}
	if created && f.syncDirectory != nil {
		return f.syncDirectory(f.root)
	}
	return nil
}

func sealPluginLogOutboxRecord(record pluginLogOutboxRecord) ([]byte, error) {
	record.Digest = ""
	unsigned, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(unsigned)
	record.Digest = hex.EncodeToString(digest[:])
	return json.Marshal(record)
}

func validatePluginLogOutboxRecord(record pluginLogOutboxRecord) error {
	if record.Version != 1 || !validPluginLogOutboxOperation(record.Operation) ||
		!validPluginLogOutboxID(record.BatchID) || !validPluginLogOutboxID(record.Digest) {
		return errors.New("plugin log outbox record identity is invalid")
	}
	want := record.Digest
	record.Digest = ""
	unsigned, err := json.Marshal(record)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(unsigned)
	if hex.EncodeToString(digest[:]) != want {
		return errors.New("plugin log outbox record digest mismatch")
	}
	if (record.Operation == "enqueue" && (len(record.Reports) == 0 || pluginLogOutboxRecordHasMetadata(record))) ||
		(record.Operation == "ack" && (len(record.Reports) != 0 || len(record.Identities) == 0 || pluginLogOutboxRecordHasState(record) || record.Fence != nil || record.Intent != nil)) ||
		(record.Operation == "retire" && (pluginLogOutboxRecordHasContent(record) || record.Fence == nil || record.Intent != nil)) ||
		(record.Operation == "retire_intent" && (pluginLogOutboxRecordHasContent(record) || record.Fence != nil || record.Intent == nil || record.Intent.ID != record.BatchID)) ||
		((record.Operation == "retire_intent_drained" || record.Operation == "retire_intent_complete" || record.Operation == "retire_intent_abort") && (pluginLogOutboxRecordHasContent(record) || record.Fence != nil || record.Intent != nil)) ||
		(record.Operation == "checkpoint" && (len(record.Identities) != 0 || record.Sequences == nil || len(record.SequenceOrder) != len(record.Sequences) || len(record.Retired) > len(record.Sequences) || len(record.Batches) > maxPluginLogReplayIdentities || len(record.Acks) > maxPluginLogReplayIdentities || len(record.Intents) > maxPluginLogRetirementIntents || record.Fence != nil || record.Intent != nil)) {
		return errors.New("plugin log outbox record payload is invalid")
	}
	if record.Operation == "retire" {
		fenceID, err := pluginRuntimeLogFenceIdentityFromRuntimeIdentity(record.Fence.runtimeIdentity())
		if err != nil {
			return err
		}
		digest := sha256.Sum256([]byte(fenceID))
		if record.BatchID != hex.EncodeToString(digest[:]) {
			return errors.New("plugin log retirement identity mismatch")
		}
	}
	if record.Operation == "retire_intent" {
		if record.Intent.Drained {
			return errors.New("plugin log retirement intent cannot be staged as drained")
		}
		identities := make([]pluginprocess.RuntimeLogIdentity, 0, len(record.Intent.Fences))
		for _, fence := range record.Intent.Fences {
			identities = append(identities, fence.runtimeIdentity())
		}
		state := newPluginLogOutboxState()
		if changed, err := state.stageRetirementIntent(record.Intent.ID, record.Intent.Revision, identities, record.Intent.SessionID); err != nil || !changed {
			return errors.Join(errors.New("plugin log retirement intent payload is invalid"), err)
		}
	}
	seen := make(map[string]struct{}, len(record.Identities))
	for _, identity := range record.Identities {
		if identity == "" {
			return errors.New("plugin log outbox ACK identity is invalid")
		}
		if _, duplicate := seen[identity]; duplicate {
			return errors.New("plugin log outbox ACK identity is duplicated")
		}
		seen[identity] = struct{}{}
	}
	return nil
}

func validPluginLogOutboxOperation(operation string) bool {
	switch operation {
	case "enqueue", "ack", "checkpoint", "retire", "retire_intent", "retire_intent_drained", "retire_intent_complete", "retire_intent_abort":
		return true
	default:
		return false
	}
}

func pluginLogOutboxRecordHasContent(record pluginLogOutboxRecord) bool {
	return len(record.Reports) != 0 || len(record.Identities) != 0 || pluginLogOutboxRecordHasState(record)
}

func pluginLogOutboxRecordHasState(record pluginLogOutboxRecord) bool {
	return len(record.Sequences) != 0 || len(record.SequenceOrder) != 0 || len(record.Batches) != 0 || len(record.Acks) != 0 || len(record.Retired) != 0 || len(record.Intents) != 0
}

func pluginLogOutboxRecordHasMetadata(record pluginLogOutboxRecord) bool {
	return len(record.Identities) != 0 || pluginLogOutboxRecordHasState(record) || record.Fence != nil || record.Intent != nil
}

func ptrPluginLogRetirementFence(fence pluginLogRetirementFence) *pluginLogRetirementFence {
	return &fence
}

func writePluginLogOutboxAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func pluginLogReportsEqual(left, right []model.PluginRuntimeLogReport) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return bytes.Equal(leftJSON, rightJSON)
}
