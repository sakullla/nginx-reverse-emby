package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

const pluginLogOutboxFile = "plugin-log-outbox.jsonl"

type pluginLogOutboxRecord struct {
	Version    int                            `json:"version"`
	Operation  string                         `json:"operation"`
	BatchID    string                         `json:"batch_id"`
	Reports    []model.PluginRuntimeLogReport `json:"reports,omitempty"`
	Identities []string                       `json:"identities,omitempty"`
	Digest     string                         `json:"digest"`
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
	return model.ClonePluginRuntimeLogReports(assigned), nil
}

func (f *Filesystem) PendingPluginLogReports() ([]model.PluginRuntimeLogReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state, err := f.loadPluginLogOutboxLocked()
	if err != nil {
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
	return f.appendPluginLogOutboxRecordLocked(pluginLogOutboxRecord{Version: 1, Operation: "ack", BatchID: ackID, Identities: identities})
}

func (f *Filesystem) loadPluginLogOutboxLocked() (pluginLogOutboxState, error) {
	state := newPluginLogOutboxState()
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
		switch record.Operation {
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
				if report.Sequence <= state.sequence[identity] {
					return state, errors.New("plugin log outbox sequence is not monotonic")
				}
				state.sequence[identity] = report.Sequence
			}
			state.pending = append(state.pending, model.ClonePluginRuntimeLogReports(record.Reports)...)
			state.batches[record.BatchID] = model.ClonePluginRuntimeLogReports(record.Reports)
		case "ack":
			if _, exists := state.acks[record.BatchID]; exists {
				continue
			}
			state.acks[record.BatchID] = struct{}{}
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
		}
	}
	return state, nil
}

func (f *Filesystem) appendPluginLogOutboxRecordLocked(record pluginLogOutboxRecord) error {
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
	if record.Version != 1 || (record.Operation != "enqueue" && record.Operation != "ack") ||
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
	if (record.Operation == "enqueue" && (len(record.Reports) == 0 || len(record.Identities) != 0)) ||
		(record.Operation == "ack" && (len(record.Reports) != 0 || len(record.Identities) == 0)) {
		return errors.New("plugin log outbox record payload is invalid")
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
