package revision

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const (
	defaultIdempotencyScope = "panel"
	defaultIdempotencyTTL   = 24 * time.Hour
	defaultApplyTimeout     = 60
	defaultDrainTimeout     = 600
)

type Store interface {
	WithRevisionMutation(context.Context, storage.RevisionMutationFunc) error
	GetIdempotencyRecord(context.Context, string, string) (storage.IdempotencyRecordRow, bool, error)
}

type Target struct {
	AgentID             string                  `json:"agent_id"`
	Local               bool                    `json:"local,omitempty"`
	DesiredVersion      string                  `json:"desired_version,omitempty"`
	Platform            string                  `json:"platform,omitempty"`
	Capabilities        []string                `json:"capabilities,omitempty"`
	ApplyTimeoutSeconds int                     `json:"apply_timeout_seconds,omitempty"`
	DrainTimeoutSeconds int                     `json:"drain_timeout_seconds,omitempty"`
	IntentResources     IntentResourceSelection `json:"intent_resources,omitempty"`
	persistedDesired    int64
	persistedCurrent    int64
}

// IntentResourceSelection identifies global resources that this mutation
// associates with one target even before the target's runtime graph references them.
type IntentResourceSelection struct {
	EgressProfileIDs []int `json:"egress_profile_ids,omitempty"`
}

type SnapshotValidation struct {
	Target         Target
	Snapshot       storage.Snapshot
	IntentSnapshot *storage.Snapshot
}

type SnapshotValidator interface {
	Validate(context.Context, SnapshotValidation) error
}

type ValidatorFunc func(context.Context, SnapshotValidation) error

func (fn ValidatorFunc) Validate(ctx context.Context, input SnapshotValidation) error {
	return fn(ctx, input)
}

type SnapshotBuilder interface {
	Build(context.Context, *storage.GormStore, Target) (storage.Snapshot, error)
}

type SnapshotBuilderFunc func(context.Context, *storage.GormStore, Target) (storage.Snapshot, error)

func (fn SnapshotBuilderFunc) Build(ctx context.Context, store *storage.GormStore, target Target) (storage.Snapshot, error) {
	return fn(ctx, store, target)
}

type RuntimeSnapshotTransform func(context.Context, *storage.GormStore, Target, storage.Snapshot) (storage.Snapshot, error)

type MutationValidation struct {
	Request any
	Targets []Target
}

type MutationValidator interface {
	ValidateMutation(context.Context, *storage.GormStore, MutationValidation) error
}

type MutationValidatorFunc func(context.Context, *storage.GormStore, MutationValidation) error

func (fn MutationValidatorFunc) ValidateMutation(ctx context.Context, store *storage.GormStore, input MutationValidation) error {
	return fn(ctx, store, input)
}

// ResourceMutation may only read or write through the transaction-scoped store.
// Runtime notifications and other external side effects belong after Execute returns.
type ResourceMutation func(context.Context, *storage.GormStore, map[string]int64) error

// ResourceStateReader returns the target's canonical resource state without
// volatile revision metadata. It covers persisted intent omitted from runtime snapshots.
type ResourceStateReader func(context.Context, *storage.GormStore, Target) (any, error)

type MutationRequest struct {
	OperationID            string
	Kind                   string
	DependencyAction       DependencyAction
	IdempotencyScope       string
	IdempotencyKey         string
	IdempotencyTTL         time.Duration
	Request                any
	Targets                []Target
	ResourceState          ResourceStateReader
	Mutate                 ResourceMutation
	ReplayResourceField    string
	ReplayResource         func() any
	ReplayExtra            func() map[string]any
	httpRequestFingerprint string
}

type mutationFingerprintEnvelope struct {
	Kind             string           `json:"kind"`
	DependencyAction DependencyAction `json:"dependency_action,omitempty"`
	Targets          []Target         `json:"targets"`
	Request          any              `json:"request"`
}

type AgentMutationResult struct {
	AgentID         string `json:"agent_id"`
	DesiredRevision int64  `json:"desired_revision"`
	SnapshotDigest  string `json:"snapshot_digest,omitempty"`
	NoOp            bool   `json:"no_op"`
}

type MutationResult struct {
	Operation              storage.OperationRow  `json:"operation"`
	Agents                 []AgentMutationResult `json:"agents"`
	NoOp                   bool                  `json:"no_op"`
	Replayed               bool                  `json:"replayed"`
	HTTPRequestFingerprint string                `json:"http_request_fingerprint,omitempty"`
	ReplayResourceField    string                `json:"replay_resource_field,omitempty"`
	ReplayResource         json.RawMessage       `json:"replay_resource,omitempty"`
	ReplayExtra            json.RawMessage       `json:"replay_extra,omitempty"`
}

type Executor struct {
	store                 Store
	snapshotBuilder       SnapshotBuilder
	intentSnapshotBuilder SnapshotBuilder
	runtimeTransforms     []RuntimeSnapshotTransform
	mutationValidators    []MutationValidator
	validators            []SnapshotValidator
	now                   func() time.Time
	operationID           func() (string, error)
	idempotencyTTL        time.Duration
}

type Option func(*Executor)

func WithClock(now func() time.Time) Option {
	return func(executor *Executor) {
		if now != nil {
			executor.now = now
		}
	}
}

func WithOperationIDGenerator(generate func() (string, error)) Option {
	return func(executor *Executor) {
		if generate != nil {
			executor.operationID = generate
		}
	}
}

func WithSnapshotBuilder(builder SnapshotBuilder) Option {
	return func(executor *Executor) {
		if builder != nil {
			executor.snapshotBuilder = builder
			executor.intentSnapshotBuilder = builder
		}
	}
}

func WithSnapshotValidator(validator SnapshotValidator) Option {
	return func(executor *Executor) {
		if validator != nil {
			executor.validators = append(executor.validators, validator)
		}
	}
}

func WithRuntimeSnapshotTransform(transform RuntimeSnapshotTransform) Option {
	return func(executor *Executor) {
		if transform != nil {
			executor.runtimeTransforms = append(executor.runtimeTransforms, transform)
		}
	}
}

func WithMutationValidator(validator MutationValidator) Option {
	return func(executor *Executor) {
		if validator != nil {
			executor.mutationValidators = append(executor.mutationValidators, validator)
		}
	}
}

func WithIdempotencyTTL(ttl time.Duration) Option {
	return func(executor *Executor) {
		if ttl > 0 {
			executor.idempotencyTTL = ttl
		}
	}
}

func NewExecutor(store Store, options ...Option) *Executor {
	executor := &Executor{
		store:                 store,
		snapshotBuilder:       SnapshotBuilderFunc(buildStorageSnapshot),
		intentSnapshotBuilder: SnapshotBuilderFunc(buildStorageIntentSnapshot),
		now:                   time.Now,
		operationID:           newOperationID,
		idempotencyTTL:        defaultIdempotencyTTL,
	}
	for _, option := range options {
		option(executor)
	}
	return executor
}

func (e *Executor) Execute(ctx context.Context, request MutationRequest) (MutationResult, error) {
	if e == nil || e.store == nil {
		return MutationResult{}, NewError(ErrorCodeInternal, "revision mutation store is unavailable", nil)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request = applyMutationContext(ctx, request)
	kind := strings.TrimSpace(request.Kind)
	if kind == "" {
		return MutationResult{}, wrapError(ErrorCodeInvalidRequest, "operation kind is required")
	}
	if request.Mutate == nil {
		return MutationResult{}, wrapError(ErrorCodeInvalidRequest, "resource mutation is required")
	}
	if request.ResourceState == nil {
		return MutationResult{}, wrapError(ErrorCodeInvalidRequest, "resource state reader is required")
	}
	if err := validateDependencyAction(request.DependencyAction); err != nil {
		return MutationResult{}, err
	}
	targets, err := normalizeTargets(request.Targets)
	if err != nil {
		return MutationResult{}, err
	}
	fingerprint, err := RequestFingerprint(mutationFingerprintEnvelope{
		Kind: kind, DependencyAction: request.DependencyAction,
		Targets: idempotencyFingerprintTargets(targets), Request: request.Request,
	})
	if err != nil {
		return MutationResult{}, err
	}

	now := e.now().UTC()
	scope := strings.TrimSpace(request.IdempotencyScope)
	key := strings.TrimSpace(request.IdempotencyKey)
	if key != "" && scope == "" {
		scope = defaultIdempotencyScope
	}
	if key != "" {
		if replay, found, err := e.loadReplay(ctx, scope, key, fingerprint, now); err != nil {
			return MutationResult{}, err
		} else if found {
			return publishMutationResult(ctx, replay), nil
		}
	}

	operationID := strings.TrimSpace(request.OperationID)
	if operationID == "" {
		operationID, err = e.operationID()
		if err != nil {
			return MutationResult{}, NewError(ErrorCodeInternal, "operation id could not be generated", err)
		}
		operationID = strings.TrimSpace(operationID)
	}
	if operationID == "" {
		return MutationResult{}, NewError(ErrorCodeInternal, "operation id generator returned an empty id", nil)
	}

	var result MutationResult
	err = e.store.WithRevisionMutation(ctx, func(tx *storage.GormStore) (storage.RevisionMutationDecision, error) {
		var expiredIdempotencyRecord *storage.IdempotencyRecordRow
		if key != "" {
			replay, found, expired, replayErr := loadReplayFromStore(ctx, tx, scope, key, fingerprint, now)
			if replayErr != nil {
				return storage.RevisionMutationDecision{}, replayErr
			}
			if found {
				result = replay
				return storage.RevisionMutationDecision{}, nil
			}
			expiredIdempotencyRecord = expired
		}

		pointers := make(map[string]storage.AgentRevisionPointerRow, len(targets))
		for _, target := range targets {
			pointer, pointerErr := tx.LockAgentRevisionPointer(ctx, target.AgentID, now)
			if pointerErr != nil {
				return storage.RevisionMutationDecision{}, pointerErr
			}
			pointers[target.AgentID] = pointer
		}

		resolvedTargets := make([]Target, len(targets))
		for i, target := range targets {
			resolvedTarget, resolveErr := resolveTargetMetadata(ctx, tx, target)
			if resolveErr != nil {
				return storage.RevisionMutationDecision{}, resolveErr
			}
			resolvedTargets[i] = resolvedTarget
		}
		for _, validator := range e.mutationValidators {
			if validateErr := validator.ValidateMutation(ctx, tx, MutationValidation{
				Request: request.Request,
				Targets: append([]Target(nil), resolvedTargets...),
			}); validateErr != nil {
				return storage.RevisionMutationDecision{}, validateErr
			}
		}

		before := make(map[string]storage.Snapshot, len(resolvedTargets))
		beforeResourceDigests := make(map[string]string, len(resolvedTargets))
		allocated := make(map[string]int64, len(resolvedTargets))
		for _, target := range resolvedTargets {
			snapshot, buildErr := e.snapshotBuilder.Build(ctx, tx, target)
			if buildErr != nil {
				return storage.RevisionMutationDecision{}, buildErr
			}
			snapshot = snapshotForTargetPackageEligibility(snapshot, target)
			snapshot, buildErr = e.transformRuntimeSnapshot(ctx, tx, target, snapshot)
			if buildErr != nil {
				return storage.RevisionMutationDecision{}, buildErr
			}
			before[target.AgentID] = snapshot
			resourceState, stateErr := request.ResourceState(ctx, tx, target)
			if stateErr != nil {
				return storage.RevisionMutationDecision{}, stateErr
			}
			resourceDigest, stateErr := RequestFingerprint(resourceState)
			if stateErr != nil {
				return storage.RevisionMutationDecision{}, stateErr
			}
			beforeResourceDigests[target.AgentID] = resourceDigest
			pointer := pointers[target.AgentID]
			floor := maxRevision(
				snapshot.Revision,
				pointer.DesiredRevision,
				pointer.AppliedRevision,
				pointer.LastKnownGoodRevision,
				target.persistedDesired,
				target.persistedCurrent,
			)
			if floor == math.MaxInt64 {
				return storage.RevisionMutationDecision{}, wrapError(ErrorCodeConflict, "agent %q revision space is exhausted", target.AgentID)
			}
			allocated[target.AgentID] = floor + 1
		}

		if mutateErr := request.Mutate(ctx, tx, cloneRevisionMap(allocated)); mutateErr != nil {
			return storage.RevisionMutationDecision{}, mutateErr
		}

		ledger := storage.RevisionLedgerWrite{}
		allNoOp := true
		allApplied := true
		changedTargets := 0
		after := make(map[string]storage.Snapshot, len(resolvedTargets))
		agentResults := make([]AgentMutationResult, 0, len(resolvedTargets))
		for _, target := range resolvedTargets {
			snapshot, buildErr := e.snapshotBuilder.Build(ctx, tx, target)
			if buildErr != nil {
				return storage.RevisionMutationDecision{}, buildErr
			}
			snapshot = snapshotForTargetPackageEligibility(snapshot, target)
			snapshot, buildErr = e.transformRuntimeSnapshot(ctx, tx, target, snapshot)
			if buildErr != nil {
				return storage.RevisionMutationDecision{}, buildErr
			}
			validationSnapshot := snapshot
			if len(e.validators) > 0 && e.intentSnapshotBuilder != nil {
				validationSnapshot, buildErr = e.intentSnapshotBuilder.Build(ctx, tx, target)
				if buildErr != nil {
					return storage.RevisionMutationDecision{}, buildErr
				}
				validationSnapshot, buildErr = includeSelectedIntentResources(ctx, tx, target, validationSnapshot)
				if buildErr != nil {
					return storage.RevisionMutationDecision{}, buildErr
				}
				validationSnapshot = snapshotForTargetPackageEligibility(validationSnapshot, target)
			}
			for _, validator := range e.validators {
				if validateErr := validator.Validate(ctx, SnapshotValidation{
					Target: target, Snapshot: snapshot, IntentSnapshot: &validationSnapshot,
				}); validateErr != nil {
					return storage.RevisionMutationDecision{}, validateErr
				}
			}
			after[target.AgentID] = snapshot

			beforeDigest, digestErr := SemanticSnapshotDigest(before[target.AgentID])
			if digestErr != nil {
				return storage.RevisionMutationDecision{}, digestErr
			}
			afterDigest, digestErr := SemanticSnapshotDigest(snapshot)
			if digestErr != nil {
				return storage.RevisionMutationDecision{}, digestErr
			}
			resourceState, stateErr := request.ResourceState(ctx, tx, target)
			if stateErr != nil {
				return storage.RevisionMutationDecision{}, stateErr
			}
			resourceDigest, stateErr := RequestFingerprint(resourceState)
			if stateErr != nil {
				return storage.RevisionMutationDecision{}, stateErr
			}
			resourceChanged := resourceDigest != beforeResourceDigests[target.AgentID]
			pointer := pointers[target.AgentID]
			if beforeDigest == afterDigest && !resourceChanged {
				desired := maxRevision(
					before[target.AgentID].Revision,
					pointer.DesiredRevision,
					pointer.AppliedRevision,
					pointer.LastKnownGoodRevision,
					target.persistedDesired,
					target.persistedCurrent,
				)
				applied := maxRevision(pointer.AppliedRevision, pointer.LastKnownGoodRevision, target.persistedCurrent)
				if desired != applied {
					allApplied = false
				}
				agentResults = append(agentResults, AgentMutationResult{
					AgentID: target.AgentID, DesiredRevision: desired, NoOp: true,
				})
				ledger.Events = append(ledger.Events, mutationEvent(operationID, target.AgentID, desired, true, "", now))
				continue
			}

			allNoOp = false
			allApplied = false
			changedTargets++
			revision := allocated[target.AgentID]
			snapshot.Revision = revision
			payload, digest, payloadErr := CanonicalSnapshotPayload(snapshot)
			if payloadErr != nil {
				return storage.RevisionMutationDecision{}, payloadErr
			}
			artifactID := "snapshot-" + digest
			ledger.Artifacts = append(ledger.Artifacts, storage.GenerationArtifactRow{
				ID: artifactID, Kind: "agent_snapshot", SHA256: digest,
				Payload: payload, SizeBytes: int64(len(payload)), CreatedAt: now,
			})
			ledger.Revisions = append(ledger.Revisions, storage.AgentRevisionRow{
				AgentID: target.AgentID, Revision: revision, OperationID: operationID,
				State: storage.AgentRevisionStatePending, SnapshotArtifactID: artifactID, SnapshotDigest: digest,
				DesiredVersion:      snapshot.DesiredVersion,
				ApplyTimeoutSeconds: resolvedApplyTimeout(target), DrainTimeoutSeconds: resolvedDrainTimeout(target),
				CreatedAt: now, UpdatedAt: now,
			})
			ledger.Pointers = append(ledger.Pointers, storage.AgentRevisionPointerRow{
				AgentID: target.AgentID, DesiredRevision: revision,
				AppliedRevision: pointer.AppliedRevision, LastKnownGoodRevision: pointer.LastKnownGoodRevision,
				UpdatedAt: now,
			})
			agentResults = append(agentResults, AgentMutationResult{
				AgentID: target.AgentID, DesiredRevision: revision, SnapshotDigest: digest,
			})
			ledger.Events = append(ledger.Events, mutationEvent(operationID, target.AgentID, revision, false, digest, now))
		}
		if changedTargets > 0 && changedTargets != len(resolvedTargets) {
			return storage.RevisionMutationDecision{}, NewError(
				ErrorCodeUnprocessable,
				"affected agent set contains both changed and unchanged final states",
				nil,
			)
		}
		if err := appendDependencyPlan(
			&ledger, operationID, request.DependencyAction, resolvedTargets,
			allocated, before, after, now,
		); err != nil {
			return storage.RevisionMutationDecision{}, err
		}

		status := storage.OperationStatusPending
		var completedAt *time.Time
		if allNoOp && allApplied {
			status = storage.OperationStatusApplied
			completed := now
			completedAt = &completed
		}
		operation := storage.OperationRow{
			ID: operationID, Kind: kind, Status: status, PrimaryAgentID: resolvedTargets[0].AgentID,
			RequestFingerprint: fingerprint, NoOp: allNoOp,
			CreatedAt: now, UpdatedAt: now, CompletedAt: completedAt,
		}
		result = MutationResult{Operation: operation, Agents: agentResults, NoOp: allNoOp}
		ledger.Operation = operation
		if key != "" {
			resourceField := strings.TrimSpace(request.ReplayResourceField)
			if resourceField != "" {
				if request.ReplayResource == nil {
					return storage.RevisionMutationDecision{}, NewError(
						ErrorCodeInternal,
						fmt.Sprintf("mutation %q does not provide its durable replay resource", kind),
						nil,
					)
				}
				replayResource, marshalErr := json.Marshal(request.ReplayResource())
				if marshalErr != nil {
					return storage.RevisionMutationDecision{}, NewError(ErrorCodeInternal, "mutation replay resource could not be persisted", marshalErr)
				}
				result.ReplayResourceField = resourceField
				result.ReplayResource = replayResource
			}
			if request.ReplayExtra != nil {
				replayExtra, marshalErr := json.Marshal(request.ReplayExtra())
				if marshalErr != nil {
					return storage.RevisionMutationDecision{}, NewError(ErrorCodeInternal, "mutation replay response fields could not be persisted", marshalErr)
				}
				result.ReplayExtra = replayExtra
			}
			result.HTTPRequestFingerprint = strings.TrimSpace(request.httpRequestFingerprint)
			responseJSON, marshalErr := json.Marshal(result)
			if marshalErr != nil {
				return storage.RevisionMutationDecision{}, NewError(ErrorCodeInternal, "mutation result could not be persisted", marshalErr)
			}
			ttl := request.IdempotencyTTL
			if ttl <= 0 {
				ttl = e.idempotencyTTL
			}
			ledger.IdempotencyRecords = append(ledger.IdempotencyRecords, storage.IdempotencyRecordRow{
				Scope: scope, Key: key, RequestFingerprint: fingerprint, OperationID: operationID,
				ResponseJSON: string(responseJSON), CreatedAt: now, ExpiresAt: now.Add(ttl),
			})
		}
		decision := storage.RevisionMutationDecision{Ledger: &ledger, RollbackResources: allNoOp}
		if expiredIdempotencyRecord != nil {
			decision.DeleteIdempotencyRecords = append(decision.DeleteIdempotencyRecords, storage.IdempotencyRecordMatch{
				Scope:              expiredIdempotencyRecord.Scope,
				Key:                expiredIdempotencyRecord.Key,
				RequestFingerprint: expiredIdempotencyRecord.RequestFingerprint,
				OperationID:        expiredIdempotencyRecord.OperationID,
				ExpiresAt:          expiredIdempotencyRecord.ExpiresAt,
			})
		}
		return decision, nil
	})
	if err != nil {
		if key != "" {
			if replay, found, replayErr := e.loadReplay(ctx, scope, key, fingerprint, now); replayErr != nil {
				return MutationResult{}, replayErr
			} else if found {
				return publishMutationResult(ctx, replay), nil
			}
		}
		return MutationResult{}, err
	}
	return publishMutationResult(ctx, result), nil
}

func (e *Executor) transformRuntimeSnapshot(ctx context.Context, store *storage.GormStore, target Target, snapshot storage.Snapshot) (storage.Snapshot, error) {
	var err error
	for _, transform := range e.runtimeTransforms {
		snapshot, err = transform(ctx, store, target, snapshot)
		if err != nil {
			return storage.Snapshot{}, err
		}
	}
	return snapshot, nil
}

func snapshotForTargetPackageEligibility(snapshot storage.Snapshot, target Target) storage.Snapshot {
	if snapshot.VersionPackage == nil {
		return snapshot
	}
	switch strings.ToLower(strings.TrimSpace(target.Platform)) {
	case "linux-amd64", "linux-arm64":
	default:
		snapshot.VersionPackage = nil
		return snapshot
	}
	for _, capability := range target.Capabilities {
		if strings.EqualFold(strings.TrimSpace(capability), "package_manifest_v1") {
			return snapshot
		}
	}
	snapshot.VersionPackage = nil
	return snapshot
}

func (e *Executor) loadReplay(ctx context.Context, scope, key, fingerprint string, now time.Time) (MutationResult, bool, error) {
	record, found, err := e.store.GetIdempotencyRecord(ctx, scope, key)
	if err != nil {
		return MutationResult{}, false, err
	}
	if !found || !record.ExpiresAt.After(now) {
		return MutationResult{}, false, nil
	}
	return decodeReplay(record, fingerprint)
}

func loadReplayFromStore(ctx context.Context, store *storage.GormStore, scope, key, fingerprint string, now time.Time) (MutationResult, bool, *storage.IdempotencyRecordRow, error) {
	record, found, err := store.LockIdempotencyRecord(ctx, scope, key)
	if err != nil {
		return MutationResult{}, false, nil, err
	}
	if !found {
		return MutationResult{}, false, nil, nil
	}
	if !record.ExpiresAt.After(now) {
		return MutationResult{}, false, &record, nil
	}
	result, replayed, err := decodeReplay(record, fingerprint)
	return result, replayed, nil, err
}

func decodeReplay(record storage.IdempotencyRecordRow, fingerprint string) (MutationResult, bool, error) {
	if record.RequestFingerprint != fingerprint {
		return MutationResult{}, false, NewError(
			ErrorCodeConflict,
			"idempotency key was already used with a different request",
			nil,
		)
	}
	var result MutationResult
	if err := json.Unmarshal([]byte(record.ResponseJSON), &result); err != nil {
		return MutationResult{}, false, NewError(ErrorCodeInternal, "persisted idempotency response is invalid", err)
	}
	if result.Operation.ID == "" || result.Operation.ID != record.OperationID {
		return MutationResult{}, false, NewError(ErrorCodeInternal, "persisted idempotency response does not match its operation", nil)
	}
	result.Replayed = true
	return result, true, nil
}

func normalizeTargets(input []Target) ([]Target, error) {
	if len(input) == 0 {
		return nil, wrapError(ErrorCodeInvalidRequest, "at least one affected agent is required")
	}
	result := make([]Target, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, target := range input {
		target.AgentID = strings.TrimSpace(target.AgentID)
		if target.AgentID == "" {
			return nil, wrapError(ErrorCodeInvalidRequest, "affected agent id is required")
		}
		if _, exists := seen[target.AgentID]; exists {
			return nil, wrapError(ErrorCodeInvalidRequest, "affected agent %q is duplicated", target.AgentID)
		}
		seen[target.AgentID] = struct{}{}
		if target.Local {
			target.Capabilities = normalizedCapabilities(target.Capabilities)
		} else {
			target.Capabilities = nil
		}
		intentEgressProfileIDs, err := normalizedIntentResourceIDs(target.IntentResources.EgressProfileIDs)
		if err != nil {
			return nil, wrapError(ErrorCodeInvalidRequest, "affected agent %q %v", target.AgentID, err)
		}
		target.IntentResources.EgressProfileIDs = intentEgressProfileIDs
		result = append(result, target)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].AgentID < result[j].AgentID })
	return result, nil
}

func normalizedIntentResourceIDs(input []int) ([]int, error) {
	seen := make(map[int]struct{}, len(input))
	result := make([]int, 0, len(input))
	for _, id := range input {
		if id <= 0 {
			return nil, fmt.Errorf("intent egress profile id must be positive")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	sort.Ints(result)
	return result, nil
}

func normalizedCapabilities(input []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(input))
	for _, capability := range input {
		capability = strings.ToLower(strings.TrimSpace(capability))
		if capability == "" {
			continue
		}
		if _, exists := seen[capability]; exists {
			continue
		}
		seen[capability] = struct{}{}
		result = append(result, capability)
	}
	sort.Strings(result)
	return result
}

func idempotencyFingerprintTargets(input []Target) []Target {
	result := append([]Target(nil), input...)
	for i := range result {
		result[i].DesiredVersion = strings.TrimSpace(result[i].DesiredVersion)
		result[i].Platform = strings.TrimSpace(result[i].Platform)
		result[i].ApplyTimeoutSeconds = resolvedApplyTimeout(result[i])
		result[i].DrainTimeoutSeconds = resolvedDrainTimeout(result[i])
	}
	return result
}

func buildStorageSnapshot(ctx context.Context, store *storage.GormStore, target Target) (storage.Snapshot, error) {
	return buildStorageSnapshotMode(ctx, store, target, false)
}

func buildStorageIntentSnapshot(ctx context.Context, store *storage.GormStore, target Target) (storage.Snapshot, error) {
	return buildStorageSnapshotMode(ctx, store, target, true)
}

func includeSelectedIntentResources(
	ctx context.Context,
	store *storage.GormStore,
	target Target,
	snapshot storage.Snapshot,
) (storage.Snapshot, error) {
	if len(target.IntentResources.EgressProfileIDs) == 0 {
		return snapshot, nil
	}

	selectedIDs := make(map[int]struct{}, len(target.IntentResources.EgressProfileIDs))
	for _, id := range target.IntentResources.EgressProfileIDs {
		selectedIDs[id] = struct{}{}
	}
	rows, err := store.ListEgressProfiles(ctx)
	if err != nil {
		return storage.Snapshot{}, err
	}
	selectedRows := make([]storage.EgressProfileRow, 0, len(selectedIDs))
	for _, row := range rows {
		if _, selected := selectedIDs[row.ID]; selected {
			selectedRows = append(selectedRows, row)
		}
	}

	existingIDs := make(map[int]struct{}, len(snapshot.EgressProfiles))
	for _, profile := range snapshot.EgressProfiles {
		existingIDs[profile.ID] = struct{}{}
	}
	for _, profile := range storage.SnapshotEgressProfilesForIntent(selectedRows) {
		if _, exists := existingIDs[profile.ID]; exists {
			continue
		}
		snapshot.EgressProfiles = append(snapshot.EgressProfiles, profile)
		existingIDs[profile.ID] = struct{}{}
	}
	sort.Slice(snapshot.EgressProfiles, func(i, j int) bool {
		return snapshot.EgressProfiles[i].ID < snapshot.EgressProfiles[j].ID
	})
	return snapshot, nil
}

func buildStorageSnapshotMode(ctx context.Context, store *storage.GormStore, target Target, intent bool) (storage.Snapshot, error) {
	if target.Local {
		if intent {
			return store.LoadLocalIntentSnapshot(ctx, target.AgentID)
		}
		return store.LoadLocalSnapshot(ctx, target.AgentID)
	}
	agents, err := store.ListAgents(ctx)
	if err != nil {
		return storage.Snapshot{}, err
	}
	for _, agent := range agents {
		if agent.ID != target.AgentID {
			continue
		}
		desiredVersion := strings.TrimSpace(target.DesiredVersion)
		if desiredVersion == "" {
			desiredVersion = agent.DesiredVersion
		}
		platform := strings.TrimSpace(target.Platform)
		if platform == "" {
			platform = agent.Platform
		}
		input := storage.AgentSnapshotInput{
			DesiredVersion: desiredVersion, DesiredRevision: agent.DesiredRevision,
			CurrentRevision: agent.CurrentRevision, Platform: platform,
		}
		if intent {
			return store.LoadAgentIntentSnapshot(ctx, target.AgentID, input)
		}
		return store.LoadAgentSnapshot(ctx, target.AgentID, input)
	}
	return storage.Snapshot{}, wrapError(ErrorCodeNotFound, "agent %q was not found", target.AgentID)
}

func resolveTargetMetadata(ctx context.Context, store *storage.GormStore, target Target) (Target, error) {
	if target.Local {
		state, err := store.LoadLocalAgentState(ctx)
		if err != nil {
			return Target{}, err
		}
		target.persistedDesired = int64(state.DesiredRevision)
		target.persistedCurrent = int64(state.CurrentRevision)
		return target, nil
	}
	agents, err := store.ListAgents(ctx)
	if err != nil {
		return Target{}, err
	}
	for _, agent := range agents {
		if agent.ID != target.AgentID {
			continue
		}
		if strings.TrimSpace(target.DesiredVersion) == "" {
			target.DesiredVersion = agent.DesiredVersion
		}
		if strings.TrimSpace(target.Platform) == "" {
			target.Platform = agent.Platform
		}
		target.persistedDesired = int64(agent.DesiredRevision)
		target.persistedCurrent = int64(agent.CurrentRevision)
		target.Capabilities = nil
		if strings.TrimSpace(agent.CapabilitiesJSON) != "" {
			if err := json.Unmarshal([]byte(agent.CapabilitiesJSON), &target.Capabilities); err != nil {
				return Target{}, NewError(ErrorCodeInternal, fmt.Sprintf("agent %q capabilities are invalid", target.AgentID), err)
			}
		}
		target.Capabilities = normalizedCapabilities(target.Capabilities)
		return target, nil
	}
	return Target{}, wrapError(ErrorCodeNotFound, "agent %q was not found", target.AgentID)
}

func mutationEvent(operationID, agentID string, revisionNumber int64, noOp bool, digest string, now time.Time) storage.RevisionEventRow {
	eventType := "revision_pending"
	if noOp {
		eventType = "mutation_no_op"
	}
	payload, _ := json.Marshal(map[string]any{
		"desired_revision": revisionNumber,
		"no_op":            noOp,
		"snapshot_digest":  digest,
	})
	return storage.RevisionEventRow{
		OperationID: operationID,
		AgentID:     agentID,
		Revision:    revisionNumber,
		EventType:   eventType,
		PayloadJSON: string(payload),
		CreatedAt:   now,
	}
}

func resolvedApplyTimeout(target Target) int {
	if target.ApplyTimeoutSeconds > 0 {
		return target.ApplyTimeoutSeconds
	}
	return defaultApplyTimeout
}

func resolvedDrainTimeout(target Target) int {
	if target.DrainTimeoutSeconds > 0 {
		return target.DrainTimeoutSeconds
	}
	return defaultDrainTimeout
}

func cloneRevisionMap(input map[string]int64) map[string]int64 {
	result := make(map[string]int64, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func maxRevision(values ...int64) int64 {
	var result int64
	for _, value := range values {
		if value > result {
			result = value
		}
	}
	return result
}

func newOperationID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "op-" + hex.EncodeToString(bytes), nil
}

func (result MutationResult) String() string {
	return fmt.Sprintf("operation=%s agents=%d no_op=%t replayed=%t", result.Operation.ID, len(result.Agents), result.NoOp, result.Replayed)
}
