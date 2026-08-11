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
	pending       []model.PluginRuntimeLogReport
	sequence      map[string]uint64
	sequenceOrder []string
	batches       map[string][]model.PluginRuntimeLogReport
	batchOrder    []string
	acks          map[string]struct{}
	ackOrder      []string
	records       int
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
	nextSequenceOrder := append([]string(nil), state.sequenceOrder...)
	for identity, sequence := range state.sequence {
		nextSequence[identity] = sequence
	}
	for index := range assigned {
		if assigned[index].Sequence != 0 {
			return nil, false, errors.New("plugin runtime log draft already has a sequence")
		}
		identity := pluginRuntimeLogFenceIdentity(assigned[index])
		if _, exists := nextSequence[identity]; !exists {
			nextSequenceOrder = append(nextSequenceOrder, identity)
		}
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
	state.sequenceOrder = nextSequenceOrder
	state.pending = append(state.pending, model.ClonePluginRuntimeLogReports(assigned)...)
	state.batches[batchID] = model.ClonePluginRuntimeLogReports(assigned)
	state.batchOrder = append(state.batchOrder, batchID)
	state.trimReplayHistory()
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
	state.ackOrder = append(state.ackOrder, ackID)
	state.trimReplayHistory()
	return ackID, identities, changed, nil
}

const maxPluginLogReplayIdentities = 512
const maxPluginLogSequenceHighWater = 1024

func (state *pluginLogOutboxState) trimReplayHistory() {
	if state == nil {
		return
	}
	pending := make(map[string]struct{}, len(state.pending))
	for _, report := range state.pending {
		pending[pluginRuntimeLogReportIdentity(report)] = struct{}{}
	}
	protectedBatches := make(map[string]struct{})
	for batchID, reports := range state.batches {
		for _, report := range reports {
			if _, ok := pending[pluginRuntimeLogReportIdentity(report)]; ok {
				protectedBatches[batchID] = struct{}{}
				break
			}
		}
	}
	for len(state.batches) > maxPluginLogReplayIdentities && len(state.batchOrder) > 0 {
		batchID := state.batchOrder[0]
		state.batchOrder = state.batchOrder[1:]
		if _, keep := protectedBatches[batchID]; keep {
			state.batchOrder = append(state.batchOrder, batchID)
			if len(protectedBatches) == len(state.batches) {
				break
			}
			continue
		}
		delete(state.batches, batchID)
	}
	protectedSequences := state.protectedSequenceHighWater()
	for len(protectedSequences) > maxPluginLogSequenceHighWater && len(state.batchOrder) > 0 {
		batchID := state.batchOrder[0]
		state.batchOrder = state.batchOrder[1:]
		if _, keep := protectedBatches[batchID]; keep {
			state.batchOrder = append(state.batchOrder, batchID)
			if len(protectedBatches) == len(state.batches) {
				break
			}
			continue
		}
		delete(state.batches, batchID)
		protectedSequences = state.protectedSequenceHighWater()
	}
	for len(state.sequence) > maxPluginLogSequenceHighWater && len(state.sequenceOrder) > 0 {
		identity := state.sequenceOrder[0]
		state.sequenceOrder = state.sequenceOrder[1:]
		if _, keep := protectedSequences[identity]; keep {
			state.sequenceOrder = append(state.sequenceOrder, identity)
			if len(protectedSequences) == len(state.sequence) {
				break
			}
			continue
		}
		delete(state.sequence, identity)
	}
	for len(state.acks) > maxPluginLogReplayIdentities && len(state.ackOrder) > 0 {
		ackID := state.ackOrder[0]
		state.ackOrder = state.ackOrder[1:]
		delete(state.acks, ackID)
	}
}

func (state *pluginLogOutboxState) protectedSequenceHighWater() map[string]struct{} {
	protected := make(map[string]struct{})
	for _, report := range state.pending {
		protected[pluginRuntimeLogFenceIdentity(report)] = struct{}{}
	}
	for _, reports := range state.batches {
		for _, report := range reports {
			protected[pluginRuntimeLogFenceIdentity(report)] = struct{}{}
		}
	}
	return protected
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
