package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
)

type PluginLogOutboxStore interface {
	EnqueuePluginLogReports(string, []model.PluginRuntimeLogReport) ([]model.PluginRuntimeLogReport, error)
	PendingPluginLogReports() ([]model.PluginRuntimeLogReport, error)
	AcknowledgePluginLogReports([]model.PluginRuntimeLogReport) error
}

type PluginLogBackpressureStore interface {
	PluginLogOutboxStore
	WaitForPluginLogCapacity(context.Context) error
}

type PluginLogFenceRetirementStore interface {
	RetirePluginRuntimeLogFence(pluginprocess.RuntimeLogIdentity) error
}

type PluginLogRetirementIntentStore interface {
	PluginLogFenceRetirementStore
	StagePluginRuntimeLogRetirementIntent(string, int64, []pluginprocess.RuntimeLogIdentity) error
	CompletePluginRuntimeLogRetirementIntent(string) error
	AbortPluginRuntimeLogRetirementIntent(string) error
}

var ErrPluginLogOutboxFull = errors.New("plugin runtime log outbox is full")

const maxPluginLogRetirementIntents = 512

type pluginLogOutboxState struct {
	pending       []model.PluginRuntimeLogReport
	sequence      map[string]uint64
	sequenceOrder []string
	batches       map[string][]model.PluginRuntimeLogReport
	batchOrder    []string
	acks          map[string]struct{}
	ackOrder      []string
	retired       map[string]struct{}
	retireIntents map[string]pluginLogRetirementIntent
	records       int
}

func newPluginLogOutboxState() pluginLogOutboxState {
	return pluginLogOutboxState{
		sequence:      make(map[string]uint64),
		batches:       make(map[string][]model.PluginRuntimeLogReport),
		acks:          make(map[string]struct{}),
		retired:       make(map[string]struct{}),
		retireIntents: make(map[string]pluginLogRetirementIntent),
	}
}

type pluginLogRetirementFence struct {
	Revision             int64  `json:"revision"`
	ProviderGenerationID string `json:"generation_id"`
	InstanceID           string `json:"instance_id"`
	PluginID             string `json:"plugin_id"`
	AgentID              string `json:"agent_id"`
	PackageDigest        string `json:"package_digest"`
	ArtifactDigest       string `json:"artifact_digest"`
}

type pluginLogRetirementIntent struct {
	ID        string                     `json:"id"`
	Revision  int64                      `json:"revision"`
	SessionID string                     `json:"session_id,omitempty"`
	Drained   bool                       `json:"drained"`
	Fences    []pluginLogRetirementFence `json:"fences"`
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
		return nil, false, ErrPluginLogOutboxFull
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
	for index := range assigned {
		delete(state.retired, pluginRuntimeLogFenceIdentity(assigned[index]))
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
	sequenceOrder := state.sequenceOrder[:0]
	for _, identity := range state.sequenceOrder {
		if _, exists := state.sequence[identity]; !exists {
			continue
		}
		if _, keep := protectedSequences[identity]; keep {
			sequenceOrder = append(sequenceOrder, identity)
			continue
		}
		delete(state.sequence, identity)
		delete(state.retired, identity)
	}
	state.sequenceOrder = append([]string(nil), sequenceOrder...)
	for len(state.acks) > maxPluginLogReplayIdentities && len(state.ackOrder) > 0 {
		ackID := state.ackOrder[0]
		state.ackOrder = state.ackOrder[1:]
		delete(state.acks, ackID)
	}
}

func (state *pluginLogOutboxState) protectedSequenceHighWater() map[string]struct{} {
	protected := make(map[string]struct{}, len(state.sequence))
	for identity := range state.sequence {
		if _, retired := state.retired[identity]; !retired {
			protected[identity] = struct{}{}
		}
	}
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

func (state *pluginLogOutboxState) retire(identity pluginprocess.RuntimeLogIdentity) (string, bool, error) {
	fence, err := pluginRuntimeLogFenceIdentityFromRuntimeIdentity(identity)
	if err != nil {
		return "", false, err
	}
	if _, exists := state.sequence[fence]; !exists {
		return fence, false, nil
	}
	if _, exists := state.retired[fence]; exists {
		return fence, false, nil
	}
	state.retired[fence] = struct{}{}
	state.trimReplayHistory()
	return fence, true, nil
}

func (state *pluginLogOutboxState) stageRetirementIntent(id string, revision int64, identities []pluginprocess.RuntimeLogIdentity, sessionID string) (bool, error) {
	if state == nil || !validPluginLogOutboxID(id) || revision <= 0 || len(identities) == 0 {
		return false, errors.New("plugin runtime log retirement intent is invalid")
	}
	if sessionID != "" && !validPluginLogOutboxID(sessionID) {
		return false, errors.New("plugin runtime log retirement intent session is invalid")
	}
	intent := pluginLogRetirementIntent{ID: id, Revision: revision, SessionID: sessionID, Fences: make([]pluginLogRetirementFence, 0, len(identities))}
	seen := make(map[string]struct{}, len(identities))
	for _, identity := range identities {
		fenceID, err := pluginRuntimeLogFenceIdentityFromRuntimeIdentity(identity)
		if err != nil {
			return false, err
		}
		if _, duplicate := seen[fenceID]; duplicate {
			return false, errors.New("plugin runtime log retirement intent duplicates a fence")
		}
		seen[fenceID] = struct{}{}
		intent.Fences = append(intent.Fences, pluginLogRetirementFenceFromIdentity(identity))
	}
	if existing, ok := state.retireIntents[id]; ok {
		if !pluginLogRetirementIntentsEqual(existing, intent) {
			return false, errors.New("plugin runtime log retirement intent identity collision")
		}
		return false, nil
	}
	if len(state.retireIntents) >= maxPluginLogRetirementIntents {
		return false, errors.New("plugin runtime log retirement intents exceed Agent bounds")
	}
	state.retireIntents[id] = clonePluginLogRetirementIntent(intent)
	return true, nil
}

func (state *pluginLogOutboxState) markRetirementIntentDrained(id string) (bool, error) {
	if state == nil || !validPluginLogOutboxID(id) {
		return false, errors.New("plugin runtime log retirement intent identity is invalid")
	}
	intent, ok := state.retireIntents[id]
	if !ok || intent.Drained {
		return false, nil
	}
	intent.Drained = true
	state.retireIntents[id] = intent
	return true, nil
}

func (state *pluginLogOutboxState) completeRetirementIntent(id string) (bool, error) {
	if state == nil || !validPluginLogOutboxID(id) {
		return false, errors.New("plugin runtime log retirement intent identity is invalid")
	}
	intent, ok := state.retireIntents[id]
	if !ok {
		return false, nil
	}
	for _, fence := range intent.Fences {
		if _, _, err := state.retire(fence.runtimeIdentity()); err != nil {
			return false, err
		}
	}
	delete(state.retireIntents, id)
	return true, nil
}

func (state *pluginLogOutboxState) abortRetirementIntent(id string) (bool, error) {
	if state == nil || !validPluginLogOutboxID(id) {
		return false, errors.New("plugin runtime log retirement intent identity is invalid")
	}
	if _, ok := state.retireIntents[id]; !ok {
		return false, nil
	}
	delete(state.retireIntents, id)
	return true, nil
}

func (state *pluginLogOutboxState) recoverableRetirementIntents(applied model.Snapshot, sessionID string) []string {
	if state == nil || applied.Revision <= 0 {
		return nil
	}
	active := make(map[string]struct{}, len(applied.PluginGenerations))
	for _, generation := range applied.PluginGenerations {
		identity := pluginprocess.RuntimeLogIdentity{
			Revision: generation.Revision, ProviderGenerationID: generation.ID, InstanceID: generation.InstanceID,
			PluginID: generation.PluginID, AgentID: generation.Target.ID, PackageDigest: generation.PackageDigest,
			ArtifactDigest: generation.Artifact.SHA256,
		}
		if fence, err := pluginRuntimeLogFenceIdentityFromRuntimeIdentity(identity); err == nil {
			active[fence] = struct{}{}
		}
	}
	var result []string
	for id, intent := range state.retireIntents {
		if applied.Revision < intent.Revision {
			continue
		}
		if !intent.Drained && (sessionID == "" || intent.SessionID == "" || intent.SessionID == sessionID) {
			continue
		}
		removed := true
		for _, fence := range intent.Fences {
			identity, err := pluginRuntimeLogFenceIdentityFromRuntimeIdentity(fence.runtimeIdentity())
			if err != nil {
				removed = false
				break
			}
			if _, stillActive := active[identity]; stillActive {
				removed = false
				break
			}
		}
		if removed {
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
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

func pluginRuntimeLogFenceIdentityFromRuntimeIdentity(identity pluginprocess.RuntimeLogIdentity) (string, error) {
	report := model.PluginRuntimeLogReport{
		Revision: identity.Revision, GenerationID: identity.ProviderGenerationID, InstanceID: identity.InstanceID,
		PluginID: identity.PluginID, AgentID: identity.AgentID, PackageDigest: identity.PackageDigest,
		ArtifactDigest: identity.ArtifactDigest, Sequence: 1,
		Entries: []model.PluginRuntimeLogEntry{{Level: "info", Message: "retirement fence"}},
	}
	if err := report.Validate(); err != nil {
		return "", fmt.Errorf("retire plugin runtime log fence: %w", err)
	}
	return pluginRuntimeLogFenceIdentity(report), nil
}

func pluginRuntimeLogReportIdentity(report model.PluginRuntimeLogReport) string {
	return pluginRuntimeLogFenceIdentity(report) + "\x00" + fmt.Sprintf("%d", report.Sequence)
}

func pluginLogRetirementFenceFromIdentity(identity pluginprocess.RuntimeLogIdentity) pluginLogRetirementFence {
	return pluginLogRetirementFence{
		Revision: identity.Revision, ProviderGenerationID: identity.ProviderGenerationID, InstanceID: identity.InstanceID,
		PluginID: identity.PluginID, AgentID: identity.AgentID, PackageDigest: identity.PackageDigest, ArtifactDigest: identity.ArtifactDigest,
	}
}

func (fence pluginLogRetirementFence) runtimeIdentity() pluginprocess.RuntimeLogIdentity {
	return pluginprocess.RuntimeLogIdentity{
		Revision: fence.Revision, ProviderGenerationID: fence.ProviderGenerationID, InstanceID: fence.InstanceID,
		PluginID: fence.PluginID, AgentID: fence.AgentID, PackageDigest: fence.PackageDigest, ArtifactDigest: fence.ArtifactDigest,
	}
}

func clonePluginLogRetirementIntent(intent pluginLogRetirementIntent) pluginLogRetirementIntent {
	intent.Fences = append([]pluginLogRetirementFence(nil), intent.Fences...)
	return intent
}

func pluginLogRetirementIntentsEqual(left, right pluginLogRetirementIntent) bool {
	immutable := func(intent pluginLogRetirementIntent) interface{} {
		return struct {
			ID       string
			Revision int64
			Fences   []pluginLogRetirementFence
		}{intent.ID, intent.Revision, intent.Fences}
	}
	encodedLeft, _ := json.Marshal(immutable(left))
	encodedRight, _ := json.Marshal(immutable(right))
	return string(encodedLeft) == string(encodedRight)
}
