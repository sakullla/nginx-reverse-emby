package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type PluginLogOutboxStore interface {
	EnqueuePluginLogReports(string, []model.PluginRuntimeLogReport) ([]model.PluginRuntimeLogReport, error)
	PendingPluginLogReports() ([]model.PluginRuntimeLogReport, error)
	AcknowledgePluginLogReports([]model.PluginRuntimeLogReport) error
}

type pluginLogOutboxState struct {
	pending  []model.PluginRuntimeLogReport
	sequence map[string]uint64
	batches  map[string][]model.PluginRuntimeLogReport
	acks     map[string]struct{}
}

func newPluginLogOutboxState() pluginLogOutboxState {
	return pluginLogOutboxState{
		sequence: make(map[string]uint64),
		batches:  make(map[string][]model.PluginRuntimeLogReport),
		acks:     make(map[string]struct{}),
	}
}

func (state *pluginLogOutboxState) enqueue(batchID string, drafts []model.PluginRuntimeLogReport) ([]model.PluginRuntimeLogReport, bool, error) {
	if state == nil || len(drafts) == 0 {
		return nil, false, nil
	}
	if state.sequence == nil {
		*state = newPluginLogOutboxState()
	}
	if !validPluginLogOutboxID(batchID) {
		return nil, false, errors.New("plugin runtime log capture batch identity is invalid")
	}
	if existing, ok := state.batches[batchID]; ok {
		existingDrafts := model.ClonePluginRuntimeLogReports(existing)
		for index := range existingDrafts {
			existingDrafts[index].Sequence = 0
		}
		existingDigest, existingErr := pluginLogBatchDigest(existingDrafts)
		draftDigest, draftErr := pluginLogBatchDigest(drafts)
		if existingErr != nil || draftErr != nil || existingDigest != draftDigest {
			return nil, false, errors.New("plugin runtime log capture batch identity collision")
		}
		return model.ClonePluginRuntimeLogReports(existing), true, nil
	}
	if len(state.pending)+len(drafts) > model.MaxPendingPluginLogReports {
		return nil, false, errors.New("plugin runtime log outbox is full")
	}
	assigned := model.ClonePluginRuntimeLogReports(drafts)
	nextSequence := make(map[string]uint64, len(state.sequence))
	for identity, sequence := range state.sequence {
		nextSequence[identity] = sequence
	}
	for index := range assigned {
		if assigned[index].Sequence != 0 {
			return nil, false, errors.New("plugin runtime log draft already has a sequence")
		}
		identity := pluginRuntimeLogFenceIdentity(assigned[index])
		sequence := nextSequence[identity] + 1
		if sequence == 0 {
			return nil, false, errors.New("plugin runtime log sequence exhausted")
		}
		assigned[index].Sequence = sequence
		if err := assigned[index].Validate(); err != nil {
			return nil, false, fmt.Errorf("enqueue plugin runtime log report: %w", err)
		}
		nextSequence[identity] = sequence
	}
	state.sequence = nextSequence
	state.pending = append(state.pending, model.ClonePluginRuntimeLogReports(assigned)...)
	state.batches[batchID] = model.ClonePluginRuntimeLogReports(assigned)
	return assigned, false, nil
}

func validPluginLogOutboxID(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func (state *pluginLogOutboxState) acknowledge(sent []model.PluginRuntimeLogReport) (string, []string, bool, error) {
	if state == nil || len(sent) == 0 {
		return "", nil, false, nil
	}
	ackID, err := pluginLogBatchDigest(sent)
	if err != nil {
		return "", nil, false, err
	}
	if _, ok := state.acks[ackID]; ok {
		return ackID, nil, false, nil
	}
	acked := make(map[string]struct{}, len(sent))
	identities := make([]string, 0, len(sent))
	for _, report := range sent {
		if err := report.Validate(); err != nil {
			return "", nil, false, fmt.Errorf("acknowledge plugin runtime log report: %w", err)
		}
		identity := pluginRuntimeLogReportIdentity(report)
		if _, duplicate := acked[identity]; duplicate {
			return "", nil, false, errors.New("acknowledge plugin runtime logs contains duplicate report")
		}
		acked[identity] = struct{}{}
		identities = append(identities, identity)
	}
	kept := state.pending[:0]
	changed := false
	for _, report := range state.pending {
		if _, ok := acked[pluginRuntimeLogReportIdentity(report)]; ok {
			changed = true
			continue
		}
		kept = append(kept, report)
	}
	state.pending = model.ClonePluginRuntimeLogReports(kept)
	state.acks[ackID] = struct{}{}
	return ackID, identities, changed, nil
}

func pluginLogBatchDigest(reports []model.PluginRuntimeLogReport) (string, error) {
	encoded, err := json.Marshal(reports)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func pluginRuntimeLogFenceIdentity(report model.PluginRuntimeLogReport) string {
	return strings.Join([]string{
		fmt.Sprintf("%d", report.Revision), report.GenerationID, report.InstanceID, report.PluginID,
		report.AgentID, report.PackageDigest, report.ArtifactDigest,
	}, "\x00")
}

func pluginRuntimeLogReportIdentity(report model.PluginRuntimeLogReport) string {
	return pluginRuntimeLogFenceIdentity(report) + "\x00" + fmt.Sprintf("%d", report.Sequence)
}
