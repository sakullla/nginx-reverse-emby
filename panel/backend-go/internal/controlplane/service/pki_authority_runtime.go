package service

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const (
	defaultPKIAuthorityHeartbeatInterval = 10 * time.Second
	pkiRotationDispatchErrorPrefix       = "CA rotation task dispatch: "
	pkiRotationDispatchRetryInterval     = time.Minute
)

type pkiAuthorityRuntimeStore interface {
	PKITransactionStore
	LoadPKICanonicalState(context.Context) (storage.PKICanonicalState, error)
	ListAgents(context.Context) ([]storage.AgentRow, error)
	ListRelayListeners(context.Context, string) ([]storage.RelayListenerRow, error)
	LoadLocalAgentState(context.Context) (storage.LocalAgentStateRow, error)
	LocalAgentID() string
}

type PKIAuthorityKeyDestroyer interface {
	DestroyCAKey(reference, pkiDomainID string, generation int64, purpose string) error
}

type PKIAuthorityRuntimeOptions struct {
	Store             pkiAuthorityRuntimeStore
	Lease             PKILeaseGate
	Generator         PKIAuthorityGenerator
	SnapshotSigner    PKIProjectedSecuritySnapshotSigner
	SnapshotPublisher PKISecuritySnapshotPublisher
	Tasks             *TaskService
	KeyDestroyer      PKIAuthorityKeyDestroyer
	RelayGate         PKIEmergencyRuntimeRelayGate
	Clock             func() time.Time
	HeartbeatInterval time.Duration
}

// PKIAuthorityRuntime is the production, restart-safe executor behind the
// panel CA actions. Queueing remains owned by InternalPKIService, while every
// phase mutation here is lease-fenced and persists job/runtime/snapshot/event
// together before any best-effort control-channel delivery.
type PKIAuthorityRuntime struct {
	store             pkiAuthorityRuntimeStore
	lease             PKILeaseGate
	generator         PKIAuthorityGenerator
	snapshotSigner    PKIProjectedSecuritySnapshotSigner
	snapshotPublisher PKISecuritySnapshotPublisher
	tasks             *TaskService
	keyDestroyer      PKIAuthorityKeyDestroyer
	relayGate         PKIEmergencyRuntimeRelayGate
	clock             func() time.Time
	heartbeatInterval time.Duration
	rotationDispatch  sync.Mutex
}

type PKIEmergencyRelayRevisionController interface {
	SetEmergencyPKIRelayAvailability(context.Context, bool, PKIRelayRevisionBarrier) (PKIRelayRevisionBarrier, error)
	ConfirmEmergencyPKIRelayBarrier(context.Context, *storage.PKITransaction, PKIRelayRevisionBarrier) (bool, error)
}

type PKIEmergencyRuntimeRelayGate interface {
	PKIEmergencyRelayGate
	EnablePKIRelay(context.Context, PKIRelayRevisionBarrier) (PKIRelayRevisionBarrier, error)
	ConfirmPKIRelayBarrier(context.Context, *storage.PKITransaction, PKIRelayRevisionBarrier) (bool, error)
}

type PKIEmergencyRevisionRelayGate struct {
	controller PKIEmergencyRelayRevisionController
}

func NewPKIEmergencyRevisionRelayGate(controller PKIEmergencyRelayRevisionController) (*PKIEmergencyRevisionRelayGate, error) {
	if controller == nil {
		return nil, fmt.Errorf("%w: emergency relay revision controller is required", ErrPKILifecycleInvalid)
	}
	return &PKIEmergencyRevisionRelayGate{controller: controller}, nil
}

func (g *PKIEmergencyRevisionRelayGate) DisablePKIRelay(
	ctx context.Context,
	previous PKIRelayRevisionBarrier,
) (PKIRelayRevisionBarrier, error) {
	return g.controller.SetEmergencyPKIRelayAvailability(ctx, false, previous)
}

func (g *PKIEmergencyRevisionRelayGate) EnablePKIRelay(
	ctx context.Context,
	previous PKIRelayRevisionBarrier,
) (PKIRelayRevisionBarrier, error) {
	return g.controller.SetEmergencyPKIRelayAvailability(ctx, true, previous)
}

func (g *PKIEmergencyRevisionRelayGate) ConfirmPKIRelayBarrier(
	ctx context.Context,
	tx *storage.PKITransaction,
	barrier PKIRelayRevisionBarrier,
) (bool, error) {
	return g.controller.ConfirmEmergencyPKIRelayBarrier(ctx, tx, barrier)
}

type pkiAuthorityRuntimePayload struct {
	Rotation   PKICARotationJob `json:"rotation"`
	Reason     string           `json:"reason"`
	OperatorID string           `json:"operator_id"`
}

type pkiAuthorityQueuedPayload struct {
	Reason     string `json:"reason"`
	OperatorID string `json:"operator_id"`
}

func marshalPKIAuthorityQueuedPayload(reason, operatorID string) (string, error) {
	payload := pkiAuthorityQueuedPayload{Reason: strings.TrimSpace(reason), OperatorID: strings.TrimSpace(operatorID)}
	if payload.Reason == "" || payload.OperatorID == "" {
		return "", fmt.Errorf("%w: queued CA operation context is incomplete", ErrPKILifecycleInvalid)
	}
	encoded, err := json.Marshal(payload)
	return string(encoded), err
}

func decodePKIAuthorityQueuedPayload(row storage.PKILifecycleJobRow) (pkiAuthorityQueuedPayload, error) {
	var payload pkiAuthorityQueuedPayload
	if err := json.Unmarshal([]byte(row.RuntimeJSON), &payload); err != nil || strings.TrimSpace(payload.Reason) == "" || strings.TrimSpace(payload.OperatorID) == "" {
		return pkiAuthorityQueuedPayload{}, fmt.Errorf("%w: queued CA operation context is invalid", ErrPKILifecycleInvalid)
	}
	return payload, nil
}

func NewPKIAuthorityRuntime(options PKIAuthorityRuntimeOptions) (*PKIAuthorityRuntime, error) {
	if options.Store == nil || options.Lease == nil || options.Generator == nil || options.SnapshotSigner == nil ||
		options.SnapshotPublisher == nil || options.Tasks == nil || options.KeyDestroyer == nil || options.RelayGate == nil {
		return nil, fmt.Errorf("%w: authority runtime dependencies are required", ErrPKILifecycleInvalid)
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.HeartbeatInterval == 0 {
		options.HeartbeatInterval = defaultPKIAuthorityHeartbeatInterval
	}
	if options.HeartbeatInterval <= 0 {
		return nil, fmt.Errorf("%w: authority heartbeat interval must be positive", ErrPKILifecycleInvalid)
	}
	return &PKIAuthorityRuntime{
		store: options.Store, lease: options.Lease, generator: options.Generator,
		snapshotSigner: options.SnapshotSigner, snapshotPublisher: options.SnapshotPublisher,
		tasks: options.Tasks, keyDestroyer: options.KeyDestroyer, relayGate: options.RelayGate,
		clock: options.Clock, heartbeatInterval: options.HeartbeatInterval,
	}, nil
}

func (r *PKIAuthorityRuntime) StartNormal(ctx context.Context, operationID, reason string) error {
	reason = strings.TrimSpace(reason)
	if strings.TrimSpace(operationID) == "" || reason == "" {
		return fmt.Errorf("%w: normal CA operation and reason are required", ErrPKILifecycleInvalid)
	}
	if err := r.initializeNormal(ctx, operationID, reason); err != nil {
		return err
	}
	return r.reconcileNormal(ctx, operationID)
}

func (r *PKIAuthorityRuntime) ReconcilePending(ctx context.Context) error {
	state, err := r.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return err
	}
	now := r.clock().UTC()
	var result error
	for _, row := range state.LifecycleJobs {
		if pkiLifecycleTerminal(row.State) || (row.NextAttemptAt != nil && row.NextAttemptAt.After(now)) {
			continue
		}
		switch row.Kind {
		case "ca_rotate":
			if row.Phase == "queued" {
				queued, decodeErr := decodePKIAuthorityQueuedPayload(row)
				if decodeErr != nil {
					result = errors.Join(result, decodeErr)
					continue
				}
				result = errors.Join(result, r.StartNormal(ctx, row.OperationID, queued.Reason))
				continue
			}
			result = errors.Join(result, r.reconcileNormal(ctx, row.OperationID))
		case "emergency_ca_rotate":
			if row.Phase == "queued" {
				queued, decodeErr := decodePKIAuthorityQueuedPayload(row)
				if decodeErr != nil {
					result = errors.Join(result, decodeErr)
					continue
				}
				result = errors.Join(result, r.StartEmergency(ctx, row.OperationID, queued.Reason, queued.OperatorID))
				continue
			}
			payload, decodeErr := decodePKIEmergencyRuntime(row)
			if decodeErr != nil {
				result = errors.Join(result, decodeErr)
				continue
			}
			result = errors.Join(result, r.StartEmergency(ctx, row.OperationID, payload.Reason, payload.OperatorID))
		}
	}
	result = errors.Join(result, r.reconcileAuthorityKeyDestruction(ctx, state.Authorities))
	return result
}

func (r *PKIAuthorityRuntime) initializeNormal(ctx context.Context, operationID, reason string) error {
	grant, err := r.lease.RequirePKILease(ctx)
	if err != nil {
		return err
	}
	state, err := r.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return err
	}
	row, found := findPKILifecycleRow(state, operationID)
	if !found || row.Kind != "ca_rotate" {
		return ErrPKIOperationNotFound
	}
	if row.Phase != "queued" || row.State != storage.PKILifecycleJobStatePending {
		return nil
	}
	if queued, queuedErr := decodePKIAuthorityQueuedPayload(row); queuedErr == nil {
		reason = queued.Reason
	}
	active, found := activePKIAuthority(state.Authorities)
	if !found {
		return fmt.Errorf("%w: active CA is unavailable", ErrPKILifecycleInvalid)
	}
	keyFingerprint, err := pkiAuthorityPublicKeyFingerprint(active.CertificatePEM)
	if err != nil {
		return err
	}
	payload := pkiAuthorityRuntimePayload{
		Rotation: PKICARotationJob{
			ID: row.ID, CurrentGeneration: active.Generation,
			CurrentKeyFingerprint: keyFingerprint, CurrentCertFingerprint: active.FingerprintSHA256,
		},
		Reason: reason, OperatorID: "panel",
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	now := r.clock().UTC()
	return r.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		if err := requirePKIAuthorityLeaseFence(ctx, tx, grant); err != nil {
			return err
		}
		previous, found, err := tx.GetPKILifecycleJobForUpdate(ctx, operationID)
		if err != nil {
			return err
		}
		if !found {
			return ErrPKIOperationNotFound
		}
		if previous.Phase != "queued" || previous.State != storage.PKILifecycleJobStatePending {
			return nil
		}
		next := previous
		next.Phase = PKICARotationPhasePrepare
		next.State = storage.PKILifecycleJobStateRunning
		next.Attempt++
		next.RuntimeJSON = string(encoded)
		next.UpdatedAt = now
		if err := tx.UpdatePKILifecycleJob(ctx, previous, next); err != nil {
			return err
		}
		return requirePKIAuthorityLeaseFence(ctx, tx, grant)
	})
}

// reconcileNormal advances multiple immediately-ready phases in one pass and
// stops only when it is waiting for an acknowledgement or overlap deadline.
func (r *PKIAuthorityRuntime) reconcileNormal(ctx context.Context, operationID string) error {
	for step := 0; step < 8; step++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		state, err := r.store.LoadPKICanonicalState(ctx)
		if err != nil {
			return err
		}
		row, found := findPKILifecycleRow(state, operationID)
		if !found {
			return ErrPKIOperationNotFound
		}
		if pkiLifecycleTerminal(row.State) {
			return nil
		}
		payload, err := decodePKIAuthorityRuntime(row)
		if err != nil {
			return r.failNormal(ctx, row, err)
		}
		if active, found := activePKIAuthority(state.Authorities); found &&
			normalRotationSupersededByActiveAuthority(payload.Rotation, active.Generation) {
			return r.cancelSupersededNormalRotation(ctx, state, row, payload, active.Generation)
		}
		if payload.Rotation.NewGeneration == 0 {
			material, generateErr := r.generator.GeneratePKIAuthority(ctx, nextPKIAuthorityGeneration(state.Authorities), payload.Reason)
			if generateErr != nil {
				return r.retryNormal(ctx, row, payload, generateErr)
			}
			if err := validatePreparedRuntimeAuthority(payload.Rotation, material, r.clock().UTC()); err != nil {
				return r.failNormal(ctx, row, err)
			}
			if err := r.persistStagedAuthority(ctx, row, payload, material); err != nil {
				return err
			}
			continue
		}

		newAuthority, newFound := authorityByGeneration(state.Authorities, payload.Rotation.NewGeneration)
		oldAuthority, oldFound := authorityByGeneration(state.Authorities, payload.Rotation.CurrentGeneration)
		if !newFound || !oldFound {
			return r.failNormal(ctx, row, fmt.Errorf("%w: rotation authority facts are incomplete", ErrPKILifecycleInvalid))
		}
		participants, err := r.rotationParticipants(ctx, state, payload.Rotation)
		if err != nil {
			return err
		}
		retired := oldAuthority.Status == "retired" && oldAuthority.PrivateKeyDestroyedAt != nil
		nextJob, action, err := AdvancePKICARotation(payload.Rotation, PKICARotationInput{
			Now: r.clock().UTC(), HeartbeatInterval: r.heartbeatInterval,
			Prepared: newAuthority.Status == "staged" || newAuthority.Status == "prepared" || newAuthority.Status == "active",
			Retired:  retired, Participants: participants,
		})
		if err != nil {
			return r.failNormal(ctx, row, err)
		}
		changed := !equalPKICARotationJob(payload.Rotation, nextJob)
		phaseChanged := payload.Rotation.Phase != nextJob.Phase || payload.Rotation.State != nextJob.State
		if changed {
			if err := r.commitNormalTransition(ctx, state, row, payload, nextJob, action, oldAuthority, newAuthority); err != nil {
				return err
			}
		}
		if action.DistributeTrust {
			r.publishCanonicalSnapshotBestEffort(ctx)
		}
		dispatchRequested := action.RequestReissue || action.RequestCutover
		var dispatchErr error
		if action.RequestReissue {
			dispatchErr = errors.Join(dispatchErr,
				r.dispatchRotation(ctx, operationID, state, nextJob.NewGeneration, PKICARotationPhaseReissue))
		}
		if action.RequestCutover {
			dispatchErr = errors.Join(dispatchErr,
				r.dispatchRotation(ctx, operationID, state, nextJob.NewGeneration, PKICARotationPhaseCutover))
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if dispatchErr != nil {
			if err := r.recordNormalRotationDispatchOutcome(ctx, operationID, dispatchErr); err != nil {
				return errors.Join(dispatchErr, err)
			}
			return nil
		}
		if dispatchRequested && !changed && strings.HasPrefix(row.LastError, pkiRotationDispatchErrorPrefix) {
			if err := r.recordNormalRotationDispatchOutcome(ctx, operationID, nil); err != nil {
				return err
			}
		}
		if action.DestroyOldPrivateKey && oldAuthority.PrivateKeyDestroyedAt == nil {
			if err := r.destroyRetiredAuthorityKey(ctx, oldAuthority); err != nil {
				return err
			}
			continue
		}
		if !changed || !phaseChanged || nextJob.Phase == PKICARotationPhaseOverlap || nextJob.State == PKICARotationStateBlocked {
			return nil
		}
	}
	return nil
}

func normalRotationSupersededByActiveAuthority(rotation PKICARotationJob, activeGeneration int64) bool {
	return rotation.NewGeneration > 0 && activeGeneration > 0 &&
		activeGeneration != rotation.CurrentGeneration && activeGeneration != rotation.NewGeneration
}

func (r *PKIAuthorityRuntime) cancelSupersededNormalRotation(
	ctx context.Context,
	state storage.PKICanonicalState,
	row storage.PKILifecycleJobRow,
	payload pkiAuthorityRuntimePayload,
	activeGeneration int64,
) error {
	grant, err := r.lease.RequirePKILease(ctx)
	if err != nil {
		return err
	}
	phase := pkiCARotationPhaseSupersededByAuthority
	reason := fmt.Sprintf("superseded by active CA generation %d", activeGeneration)
	operatorID := "scheduler"
	details := map[string]any{
		"active_generation":  activeGeneration,
		"current_generation": payload.Rotation.CurrentGeneration,
		"target_generation":  payload.Rotation.NewGeneration,
	}
	if emergencyOperationID, emergencyOperatorID, found := supersedingEmergencyOperation(state.LifecycleJobs, activeGeneration); found {
		phase = pkiCARotationPhaseSupersededByEmergency
		reason = "superseded by emergency CA rotation " + emergencyOperationID
		operatorID = emergencyOperatorID
		details["emergency_operation_id"] = emergencyOperationID
	}
	securityRevision := int64(0)
	if state.Settings != nil {
		securityRevision = state.Settings.SecurityRevision
	}
	now := r.clock().UTC()
	return r.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		if err := requirePKIAuthorityLeaseFence(ctx, tx, grant); err != nil {
			return err
		}
		previous, found, err := tx.GetPKILifecycleJobForUpdate(ctx, row.ID)
		if err != nil {
			return err
		}
		if !found {
			return ErrPKIOperationNotFound
		}
		if pkiLifecycleTerminal(previous.State) {
			return requirePKIAuthorityLeaseFence(ctx, tx, grant)
		}
		if previous.UpdatedAt != row.UpdatedAt || previous.RuntimeJSON != row.RuntimeJSON {
			return ErrPKILifecycleConflict
		}
		if err := markNormalRotationSuperseded(
			ctx, tx, previous, phase, reason, operatorID,
			activeGeneration, securityRevision, now, details,
		); err != nil {
			return err
		}
		return requirePKIAuthorityLeaseFence(ctx, tx, grant)
	})
}

func supersedingEmergencyOperation(
	jobs []storage.PKILifecycleJobRow,
	activeGeneration int64,
) (string, string, bool) {
	var selected storage.PKILifecycleJobRow
	var selectedPayload pkiEmergencyRuntimePayload
	found := false
	for _, job := range jobs {
		if job.Kind != "emergency_ca_rotate" || job.Phase == "queued" {
			continue
		}
		payload, err := decodePKIEmergencyRuntime(job)
		if err != nil || payload.ReplacementGeneration != activeGeneration {
			continue
		}
		if !found || job.UpdatedAt.After(selected.UpdatedAt) {
			selected = job
			selectedPayload = payload
			found = true
		}
	}
	if !found {
		return "", "", false
	}
	operatorID := strings.TrimSpace(selectedPayload.OperatorID)
	if operatorID == "" {
		operatorID = "scheduler"
	}
	return selected.OperationID, operatorID, true
}

func (r *PKIAuthorityRuntime) persistStagedAuthority(
	ctx context.Context,
	row storage.PKILifecycleJobRow,
	payload pkiAuthorityRuntimePayload,
	material PKIAuthorityMaterial,
) error {
	grant, err := r.lease.RequirePKILease(ctx)
	if err != nil {
		return err
	}
	payload.Rotation.NewGeneration = material.Generation
	payload.Rotation.NewKeyFingerprint = material.KeyFingerprint
	payload.Rotation.NewCertFingerprint = material.CertificateFingerprint
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	now := r.clock().UTC()
	keyReference := material.KeyReference
	authorityID := pkiRuntimeAuthorityID(row.PKIDomainID, material.Generation)
	return r.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		if err := requirePKIAuthorityLeaseFence(ctx, tx, grant); err != nil {
			return err
		}
		previous, found, err := tx.GetPKILifecycleJobForUpdate(ctx, row.ID)
		if err != nil {
			return err
		}
		if !found || previous.UpdatedAt != row.UpdatedAt || previous.RuntimeJSON != row.RuntimeJSON {
			return ErrPKILifecycleConflict
		}
		if err := tx.CreatePKIAuthority(ctx, storage.PKIAuthorityRow{
			ID: authorityID, PKIDomainID: row.PKIDomainID, Generation: material.Generation, Status: "staged",
			CertificatePEM: material.CertificatePEM, EncryptedKeyRef: &keyReference,
			FingerprintSHA256: material.CertificateFingerprint, NotBefore: material.NotBefore, NotAfter: material.NotAfter,
			CreatedReason: payload.Reason, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		next := previous
		next.RuntimeJSON = string(encoded)
		next.LastError = ""
		next.NextAttemptAt = nil
		next.UpdatedAt = now
		if err := tx.UpdatePKILifecycleJob(ctx, previous, next); err != nil {
			return err
		}
		if err := appendPKIAuthorityRuntimeEvent(ctx, tx, row.PKIDomainID, "pki.ca.rotation.prepared", row.ID,
			payload.OperatorID, payload.Reason, material.Generation, 0, now, map[string]any{"phase": PKICARotationPhasePrepare}); err != nil {
			return err
		}
		return requirePKIAuthorityLeaseFence(ctx, tx, grant)
	})
}

func (r *PKIAuthorityRuntime) commitNormalTransition(
	ctx context.Context,
	state storage.PKICanonicalState,
	row storage.PKILifecycleJobRow,
	payload pkiAuthorityRuntimePayload,
	nextJob PKICARotationJob,
	action PKICARotationAction,
	oldAuthority storage.PKIAuthorityRow,
	newAuthority storage.PKIAuthorityRow,
) error {
	previousJob := payload.Rotation
	grant, err := r.lease.RequirePKILease(ctx)
	if err != nil {
		return err
	}
	projected := slices.Clone(state.Authorities)
	needsSnapshot := false
	if action.DistributeTrust && newAuthority.Status == "staged" {
		setProjectedAuthorityStatus(projected, newAuthority.ID, "prepared", nil)
		needsSnapshot = true
	}
	if action.PromoteNewAuthority {
		setProjectedAuthorityStatus(projected, oldAuthority.ID, "retiring", &nextJob.RetireDeadline)
		setProjectedAuthorityStatus(projected, newAuthority.ID, "active", nil)
		needsSnapshot = true
	}
	if action.RemoveOldTrust {
		setProjectedAuthorityStatus(projected, oldAuthority.ID, "retired", nil)
		needsSnapshot = true
	}
	var signed *PKISignedSecuritySnapshot
	var projectedSigner storage.PKIAuthorityRow
	if needsSnapshot {
		if state.Settings == nil || state.Settings.SecurityRevision == int64(^uint64(0)>>1) {
			return fmt.Errorf("%w: security revision cannot be incremented", ErrPKILifecycleInvalid)
		}
		if action.PromoteNewAuthority || action.RemoveOldTrust {
			projectedSigner, _ = authorityByGeneration(projected, newAuthority.Generation)
		} else {
			projectedSigner, _ = activePKIAuthority(projected)
		}
		candidate, err := r.signProjectedSnapshot(ctx, state, projected, projectedSigner, state.Settings.SecurityRevision+1)
		if err != nil {
			return err
		}
		signed = &candidate
	}
	payload.Rotation = nextJob
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	now := r.clock().UTC()
	return r.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		if err := requirePKIAuthorityLeaseFence(ctx, tx, grant); err != nil {
			return err
		}
		previous, found, err := tx.GetPKILifecycleJobForUpdate(ctx, row.ID)
		if err != nil {
			return err
		}
		if !found || previous.UpdatedAt != row.UpdatedAt || previous.RuntimeJSON != row.RuntimeJSON {
			return ErrPKILifecycleConflict
		}
		if action.DistributeTrust && payload.Rotation.Phase == PKICARotationPhaseDistributeTrust && newAuthority.Status == "staged" {
			if err := tx.TransitionPKIAuthority(ctx, newAuthority.ID, "staged", "prepared", payload.Reason, nil, now); err != nil {
				return err
			}
		}
		if action.PromoteNewAuthority {
			deadline := nextJob.RetireDeadline
			if err := tx.TransitionPKIAuthority(ctx, oldAuthority.ID, "active", "retiring", payload.Reason, &deadline, now); err != nil {
				return err
			}
			if err := tx.TransitionPKIAuthority(ctx, newAuthority.ID, "prepared", "active", payload.Reason, nil, now); err != nil {
				return err
			}
		}
		if action.RemoveOldTrust && oldAuthority.Status == "retiring" {
			if err := tx.TransitionPKIAuthority(ctx, oldAuthority.ID, "retiring", "retired", payload.Reason, nil, now); err != nil {
				return err
			}
			if err := tx.ExpirePKICertificatesByGeneration(ctx, oldAuthority.Generation, "CA generation retired", now); err != nil {
				return err
			}
		}
		securityRevision := int64(0)
		if state.Settings != nil {
			securityRevision = state.Settings.SecurityRevision
		}
		if signed != nil {
			if err := tx.SetPKISecurityRevision(ctx, state.Settings.SecurityRevision, state.Settings.SecurityRevision+1, now); err != nil {
				return err
			}
			securityRevision++
			canonical, err := tx.LoadPKICanonicalState(ctx)
			if err != nil {
				return err
			}
			persisted, err := storagePKISecuritySnapshot(canonical, *signed)
			if err != nil {
				return err
			}
			encodedSnapshot, err := json.Marshal(persisted)
			if err != nil {
				return err
			}
			if err := tx.SavePKISecuritySnapshot(ctx, storage.PKISecuritySnapshotRow{
				PKIDomainID: row.PKIDomainID, PKIEpoch: state.Settings.PKIEpoch,
				SecurityRevision: securityRevision, SnapshotJSON: string(encodedSnapshot), UpdatedAt: now,
			}); err != nil {
				return err
			}
		}
		next := previous
		next.Phase = nextJob.Phase
		switch nextJob.State {
		case PKICARotationStateBlocked:
			next.State = storage.PKILifecycleJobStateBlocked
		case PKICARotationStateSucceeded:
			next.State = storage.PKILifecycleJobStateSucceeded
		default:
			next.State = storage.PKILifecycleJobStateRunning
		}
		next.RuntimeJSON = string(encoded)
		next.LastError = nextJob.LastError
		if strings.HasPrefix(previous.LastError, pkiRotationDispatchErrorPrefix) {
			next.NextAttemptAt = nil
		}
		next.UpdatedAt = now
		if !nextJob.AckDeadline.IsZero() {
			deadline := nextJob.AckDeadline
			next.Deadline = &deadline
		} else if !nextJob.RetireDeadline.IsZero() {
			deadline := nextJob.RetireDeadline
			next.Deadline = &deadline
		} else {
			next.Deadline = nil
		}
		if err := tx.UpdatePKILifecycleJob(ctx, previous, next); err != nil {
			return err
		}
		// Persisting a first missing-ACK set or deadline does not represent a
		// lifecycle transition. Suppressing that internal retry event also keeps
		// the stable audit ID unique when an injected clock has not advanced.
		if shouldAppendPKIAuthorityTransitionEvent(previousJob, nextJob) {
			eventType := "pki.ca.rotation." + nextJob.Phase
			result := "success"
			if nextJob.State == PKICARotationStateBlocked {
				eventType = "pki.ca.rotation.blocked"
				result = "blocked"
			}
			if nextJob.State == PKICARotationStateSucceeded {
				eventType = "pki.ca.rotation.succeeded"
			}
			if err := appendPKIAuthorityRuntimeEvent(ctx, tx, row.PKIDomainID, eventType, row.ID,
				payload.OperatorID, nextJob.LastError, nextJob.NewGeneration, securityRevision, now,
				map[string]any{"phase": nextJob.Phase, "result": result, "blocked_identity_ids": nextJob.BlockedIdentityIDs}); err != nil {
				return err
			}
		}
		return requirePKIAuthorityLeaseFence(ctx, tx, grant)
	})
}

func shouldAppendPKIAuthorityTransitionEvent(previous, next PKICARotationJob) bool {
	return previous.Phase != next.Phase || previous.State != next.State
}

func (r *PKIAuthorityRuntime) signProjectedSnapshot(
	ctx context.Context,
	state storage.PKICanonicalState,
	authorities []storage.PKIAuthorityRow,
	signer storage.PKIAuthorityRow,
	revision int64,
) (PKISignedSecuritySnapshot, error) {
	if state.Settings == nil {
		return PKISignedSecuritySnapshot{}, fmt.Errorf("%w: PKI settings are unavailable", ErrPKILifecycleInvalid)
	}
	trustGenerations, trustRoots := projectedPKITrustRoots(authorities)
	revokedIdentities, revokedSerials := canonicalPKIRevocations(state)
	return r.snapshotSigner.SignPKIProjectedSecuritySnapshot(ctx, PKIUnsignedSecuritySnapshot{
		PKIDomainID: state.Settings.PKIDomainID,
		Version: PKISecuritySnapshotVersion{
			Version: PKISecurityVersion{PKIEpoch: state.Settings.PKIEpoch, SecurityRevision: revision}, Full: true,
		},
		IssuedAt:         r.clock().UTC(),
		TrustGenerations: trustGenerations, TrustRoots: trustRoots,
		RevokedIdentityIDs: revokedIdentities, RevokedSerials: revokedSerials,
	}, signer)
}

func (r *PKIAuthorityRuntime) rotationParticipants(
	ctx context.Context,
	state storage.PKICanonicalState,
	job PKICARotationJob,
) ([]PKICARotationParticipant, error) {
	agents, err := r.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	listenerRows, err := r.store.ListRelayListeners(ctx, "")
	if err != nil {
		return nil, err
	}
	supportedListeners := make(map[string]struct{}, len(listenerRows))
	for _, listener := range supportedPKIRelayListenerRows(listenerRows) {
		supportedListeners[listener.AgentID+"\x00"+strconv.Itoa(listener.ID)] = struct{}{}
	}
	identitiesByAgent := make(map[string][]storage.PKIIdentityRow)
	certificates := make(map[string]storage.PKICertificateRow, len(state.Certificates))
	for _, certificate := range state.Certificates {
		certificates[certificate.ID] = certificate
	}
	for _, identity := range state.Identities {
		if identity.State != storage.PKIIdentityStateActive {
			continue
		}
		if identity.Kind == storage.PKIIdentityKindListener {
			if _, supported := supportedListeners[identity.AgentID+"\x00"+identity.ListenerID]; !supported {
				continue
			}
		}
		identitiesByAgent[identity.AgentID] = append(identitiesByAgent[identity.AgentID], identity)
	}
	localAgentID := strings.TrimSpace(r.store.LocalAgentID())
	if len(identitiesByAgent[localAgentID]) != 0 {
		localState, err := r.store.LoadLocalAgentState(ctx)
		if err != nil {
			return nil, err
		}
		localIndex := -1
		for index := range agents {
			if agents[index].ID == localAgentID {
				localIndex = index
				break
			}
		}
		if localIndex < 0 {
			agents = append(agents, storage.AgentRow{ID: localAgentID})
			localIndex = len(agents) - 1
		}
		agents[localIndex].IsLocal = true
		agents[localIndex].PKISecurityAckJSON = localState.PKISecurityAckJSON
		agents[localIndex].PKISecurityAckAt = localState.PKISecurityAckAt
	}
	participants := make([]PKICARotationParticipant, 0, len(agents))
	for _, agent := range agents {
		owned := identitiesByAgent[agent.ID]
		if len(owned) == 0 {
			continue
		}
		participant := PKICARotationParticipant{CanReceiveRevision: agent.IsLocal || strings.TrimSpace(agent.AgentToken) != ""}
		for _, identity := range owned {
			if identity.Kind == storage.PKIIdentityKindAgent {
				participant.IdentityID = identity.ID
				break
			}
		}
		if participant.IdentityID == "" {
			participant.IdentityID = "agent:" + agent.ID
		}
		participant.LastHeartbeatAt = parsePKIAgentHeartbeat(agent.LastSeenAt)
		if agent.IsLocal && participant.LastHeartbeatAt.IsZero() {
			participant.LastHeartbeatAt = r.clock().UTC()
		}
		var acknowledgement storage.PKISecurityAcknowledgement
		ackValid := json.Unmarshal([]byte(agent.PKISecurityAckJSON), &acknowledgement) == nil && state.Settings != nil &&
			acknowledgement.PKIDomainID == state.Settings.PKIDomainID && acknowledgement.PKIEpoch == state.Settings.PKIEpoch &&
			acknowledgement.SecurityRevision >= state.Settings.SecurityRevision && acknowledgement.Full
		participant.TrustAcked = ackValid && slices.Contains(acknowledgement.TrustGenerations, job.CurrentGeneration) &&
			slices.Contains(acknowledgement.TrustGenerations, job.NewGeneration)
		listenerAcknowledgements := make(map[string]storage.PKIListenerCredentialAcknowledgement, len(acknowledgement.ListenerCredentials))
		for _, listener := range acknowledgement.ListenerCredentials {
			listenerAcknowledgements[listener.IdentityID] = listener
		}
		participant.Reissued = true
		agentCertificateID := ""
		listenersCutoverAcked := true
		for _, identity := range owned {
			if identity.CurrentCertificateID == nil {
				participant.Reissued = false
				continue
			}
			certificate, found := certificates[*identity.CurrentCertificateID]
			if !found || certificate.Status != storage.PKICertificateStatusActive || certificate.CAGeneration != job.NewGeneration {
				participant.Reissued = false
			}
			if identity.Kind == storage.PKIIdentityKindAgent {
				agentCertificateID = *identity.CurrentCertificateID
			} else if identity.Kind == storage.PKIIdentityKindListener {
				listenerAck, found := listenerAcknowledgements[identity.ID]
				if !found || listenerAck.ListenerID != identity.ListenerID ||
					listenerAck.CertificateID != *identity.CurrentCertificateID || listenerAck.CAGeneration != job.NewGeneration {
					listenersCutoverAcked = false
				}
			}
		}
		participant.CutoverAcked = participant.TrustAcked && participant.Reissued && agentCertificateID != "" &&
			acknowledgement.CertificateID == agentCertificateID && listenersCutoverAcked
		participants = append(participants, participant)
	}
	return participants, nil
}

func (r *PKIAuthorityRuntime) dispatchRotation(
	ctx context.Context,
	operationID string,
	state storage.PKICanonicalState,
	generation int64,
	phase string,
) error {
	targets, err := r.pendingRotationDispatchTargets(ctx, state, generation, phase)
	if err != nil {
		return err
	}
	r.rotationDispatch.Lock()
	defer r.rotationDispatch.Unlock()

	var dispatchErr error
	for _, identity := range targets {
		if err := ctx.Err(); err != nil {
			return errors.Join(dispatchErr, err)
		}
		if r.rotationTaskDispatchDeferred(operationID, identity, generation, phase) {
			continue
		}
		if _, err := r.tasks.CreateAndDispatchContext(ctx, TaskCreateRequest{
			AgentID: identity.AgentID, Type: TaskTypePKIForceRotation,
			Payload: map[string]any{
				"operation_id": operationID, "identity_id": identity.ID,
				"identity_kind": identity.Kind, "listener_id": identity.ListenerID,
				"ca_generation": generation, "phase": phase,
			},
			TTL: PKICATrustAckTimeout,
		}); err != nil {
			dispatchErr = errors.Join(dispatchErr, fmt.Errorf(
				"identity %s on agent %s for %s: %w", identity.ID, identity.AgentID, phase, err,
			))
		}
	}
	return dispatchErr
}

func (r *PKIAuthorityRuntime) pendingRotationDispatchTargets(
	ctx context.Context,
	state storage.PKICanonicalState,
	generation int64,
	phase string,
) ([]storage.PKIIdentityRow, error) {
	if phase != PKICARotationPhaseReissue && phase != PKICARotationPhaseCutover {
		return nil, fmt.Errorf("%w: unsupported CA rotation dispatch phase %q", ErrPKILifecycleInvalid, phase)
	}
	listenerRows, err := r.store.ListRelayListeners(ctx, "")
	if err != nil {
		return nil, err
	}
	supportedListeners := make(map[string]struct{}, len(listenerRows))
	for _, listener := range supportedPKIRelayListenerRows(listenerRows) {
		supportedListeners[listener.AgentID+"\x00"+strconv.Itoa(listener.ID)] = struct{}{}
	}
	certificates := make(map[string]storage.PKICertificateRow, len(state.Certificates))
	for _, certificate := range state.Certificates {
		certificates[certificate.ID] = certificate
	}
	acknowledgements := make(map[string]storage.PKISecurityAcknowledgement)
	if phase == PKICARotationPhaseCutover {
		agents, err := r.store.ListAgents(ctx)
		if err != nil {
			return nil, err
		}
		for _, agent := range agents {
			var acknowledgement storage.PKISecurityAcknowledgement
			if json.Unmarshal([]byte(agent.PKISecurityAckJSON), &acknowledgement) == nil &&
				validPKIRotationCredentialAcknowledgement(state, acknowledgement) {
				acknowledgements[agent.ID] = acknowledgement
			}
		}
		localAgentID := strings.TrimSpace(r.store.LocalAgentID())
		if localAgentID != "" {
			localState, err := r.store.LoadLocalAgentState(ctx)
			if err != nil {
				return nil, err
			}
			var acknowledgement storage.PKISecurityAcknowledgement
			if json.Unmarshal([]byte(localState.PKISecurityAckJSON), &acknowledgement) == nil &&
				validPKIRotationCredentialAcknowledgement(state, acknowledgement) {
				acknowledgements[localAgentID] = acknowledgement
			}
		}
	}

	seen := make(map[string]struct{})
	targets := make([]storage.PKIIdentityRow, 0)
	for _, identity := range state.Identities {
		if identity.State != storage.PKIIdentityStateActive || strings.TrimSpace(identity.AgentID) == "" {
			continue
		}
		if identity.Kind == storage.PKIIdentityKindListener {
			if _, supported := supportedListeners[identity.AgentID+"\x00"+identity.ListenerID]; !supported {
				continue
			}
		}
		key := identity.AgentID + "\x00" + identity.ID
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		certificate, reissued := activeRotationCertificate(identity, certificates, generation)
		if phase == PKICARotationPhaseReissue && reissued {
			continue
		}
		if phase == PKICARotationPhaseCutover && reissued &&
			rotationCredentialAcknowledged(identity, certificate.ID, generation, acknowledgements[identity.AgentID]) {
			continue
		}
		targets = append(targets, identity)
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].AgentID != targets[j].AgentID {
			return targets[i].AgentID < targets[j].AgentID
		}
		return targets[i].ID < targets[j].ID
	})
	return targets, nil
}

func activeRotationCertificate(
	identity storage.PKIIdentityRow,
	certificates map[string]storage.PKICertificateRow,
	generation int64,
) (storage.PKICertificateRow, bool) {
	if identity.CurrentCertificateID == nil {
		return storage.PKICertificateRow{}, false
	}
	certificate, found := certificates[*identity.CurrentCertificateID]
	return certificate, found && certificate.Status == storage.PKICertificateStatusActive && certificate.CAGeneration == generation
}

func validPKIRotationCredentialAcknowledgement(
	state storage.PKICanonicalState,
	acknowledgement storage.PKISecurityAcknowledgement,
) bool {
	return state.Settings != nil && acknowledgement.PKIDomainID == state.Settings.PKIDomainID &&
		acknowledgement.PKIEpoch == state.Settings.PKIEpoch && acknowledgement.Full &&
		acknowledgement.SecurityRevision >= state.Settings.SecurityRevision
}

func rotationCredentialAcknowledged(
	identity storage.PKIIdentityRow,
	certificateID string,
	generation int64,
	acknowledgement storage.PKISecurityAcknowledgement,
) bool {
	if identity.Kind == storage.PKIIdentityKindAgent {
		return acknowledgement.CertificateID == certificateID
	}
	for _, listener := range acknowledgement.ListenerCredentials {
		if listener.IdentityID == identity.ID && listener.ListenerID == identity.ListenerID &&
			listener.CertificateID == certificateID && listener.CAGeneration == generation {
			return true
		}
	}
	return false
}

func (r *PKIAuthorityRuntime) rotationTaskDispatchDeferred(
	operationID string,
	identity storage.PKIIdentityRow,
	generation int64,
	phase string,
) bool {
	now := r.tasks.now().UTC()
	r.tasks.mu.Lock()
	defer r.tasks.mu.Unlock()
	for taskID, record := range r.tasks.tasks {
		if !matchesPKIRotationTask(record, operationID, identity, generation, phase) {
			continue
		}
		record = r.tasks.expireTaskIfDeadlineExceededLocked(record, now)
		r.tasks.tasks[taskID] = record
		if !isTerminalTaskState(record.State) || record.UpdatedAt.After(now.Add(-r.heartbeatInterval)) {
			return true
		}
	}
	session, connected := r.tasks.sessions[identity.AgentID]
	_, revoked := r.tasks.revoked[identity.AgentID]
	return revoked || !connected || session.session == nil
}

func matchesPKIRotationTask(
	record TaskRecord,
	operationID string,
	identity storage.PKIIdentityRow,
	generation int64,
	phase string,
) bool {
	if record.AgentID != identity.AgentID || record.Type != TaskTypePKIForceRotation ||
		strings.TrimSpace(taskPayloadString(record.Payload["operation_id"])) != strings.TrimSpace(operationID) ||
		strings.TrimSpace(taskPayloadString(record.Payload["identity_id"])) != identity.ID ||
		strings.TrimSpace(taskPayloadString(record.Payload["phase"])) != phase {
		return false
	}
	payloadGeneration, valid := taskPayloadInt64(record.Payload["ca_generation"])
	return valid && payloadGeneration == generation
}

func taskPayloadString(value any) string {
	valueString, _ := value.(string)
	return valueString
}

func taskPayloadInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		parsed := int64(typed)
		return parsed, float64(parsed) == typed
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func (r *PKIAuthorityRuntime) recordNormalRotationDispatchOutcome(
	ctx context.Context,
	operationID string,
	cause error,
) error {
	grant, err := r.lease.RequirePKILease(ctx)
	if err != nil {
		return err
	}
	now := r.clock().UTC()
	return r.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		if err := requirePKIAuthorityLeaseFence(ctx, tx, grant); err != nil {
			return err
		}
		previous, found, err := tx.GetPKILifecycleJobForUpdate(ctx, operationID)
		if err != nil {
			return err
		}
		if !found {
			return ErrPKIOperationNotFound
		}
		if pkiLifecycleTerminal(previous.State) {
			return requirePKIAuthorityLeaseFence(ctx, tx, grant)
		}
		next := previous
		if cause != nil {
			retryDelay := pkiRotationDispatchRetryInterval
			if r.heartbeatInterval > retryDelay {
				retryDelay = r.heartbeatInterval
			}
			retryAt := now.Add(retryDelay)
			next.NextAttemptAt = &retryAt
			next.LastError = truncatePKIRuntimeError(errors.New(
				pkiRotationDispatchErrorPrefix + strings.TrimSpace(cause.Error()),
			))
			next.Attempt++
		} else {
			if !strings.HasPrefix(previous.LastError, pkiRotationDispatchErrorPrefix) {
				return requirePKIAuthorityLeaseFence(ctx, tx, grant)
			}
			payload, err := decodePKIAuthorityRuntime(previous)
			if err != nil {
				return err
			}
			next.NextAttemptAt = nil
			next.LastError = payload.Rotation.LastError
		}
		next.UpdatedAt = now
		if err := tx.UpdatePKILifecycleJob(ctx, previous, next); err != nil {
			return err
		}
		return requirePKIAuthorityLeaseFence(ctx, tx, grant)
	})
}

func (r *PKIAuthorityRuntime) publishCanonicalSnapshotBestEffort(ctx context.Context) {
	state, err := r.store.LoadPKICanonicalState(ctx)
	if err != nil || state.SecuritySnapshot == nil {
		return
	}
	persisted, err := storage.ValidateCanonicalPKISecuritySnapshot(state)
	if err != nil {
		return
	}
	_ = r.snapshotPublisher.PublishPKISecuritySnapshot(ctx, signedPKISecuritySnapshotFromStorage(persisted), nil)
}

func (r *PKIAuthorityRuntime) destroyRetiredAuthorityKey(ctx context.Context, authority storage.PKIAuthorityRow) error {
	return r.destroyAuthorityKeyCoordinated(ctx, authority)
}

func (r *PKIAuthorityRuntime) destroyAuthorityKeyCoordinated(ctx context.Context, authority storage.PKIAuthorityRow) error {
	if strings.TrimSpace(authority.ID) == "" {
		return fmt.Errorf("%w: authority key destruction target is unavailable", ErrPKILifecycleInvalid)
	}
	grant, err := r.lease.RequirePKILease(ctx)
	if err != nil {
		return err
	}
	now := r.clock().UTC()
	alreadyDestroyed := false
	if err := r.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		if err := requirePKIAuthorityLeaseFence(ctx, tx, grant); err != nil {
			return err
		}
		current, found, err := tx.GetPKIAuthority(ctx, authority.ID)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: authority %q is unavailable", ErrPKILifecycleInvalid, authority.ID)
		}
		if current.PrivateKeyDestroyedAt != nil {
			if current.EncryptedKeyRef != nil {
				return fmt.Errorf("%w: destroyed authority %q retains a key reference", ErrPKILifecycleInvalid, current.ID)
			}
			alreadyDestroyed = true
			return requirePKIAuthorityLeaseFence(ctx, tx, grant)
		}
		if current.Status != "retired" && current.Status != "revoked" {
			return fmt.Errorf("%w: authority %q in state %q is not eligible for key destruction", ErrPKILifecycleInvalid, current.ID, current.Status)
		}
		if current.EncryptedKeyRef == nil || strings.TrimSpace(*current.EncryptedKeyRef) == "" {
			return fmt.Errorf("%w: retired authority key reference is unavailable", ErrPKILifecycleInvalid)
		}
		authority = current
		if err := tx.MarkPKIAuthorityKeyDestroyPending(ctx, authority.ID, now); err != nil {
			return err
		}
		return requirePKIAuthorityLeaseFence(ctx, tx, grant)
	}); err != nil {
		return err
	}
	if alreadyDestroyed {
		return nil
	}
	deleteGrant, err := r.lease.RequirePKILease(ctx)
	if err != nil || !samePKILeaseAuthority(grant, deleteGrant) {
		if err != nil {
			return err
		}
		return ErrPKILeaseNotHeld
	}
	if err := r.keyDestroyer.DestroyCAKey(*authority.EncryptedKeyRef, authority.PKIDomainID, authority.Generation, pkiBackupCAPurpose); err != nil {
		return err
	}
	destroyedAt := r.clock().UTC()
	return r.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		if err := requirePKIAuthorityLeaseFence(ctx, tx, deleteGrant); err != nil {
			return err
		}
		if err := tx.MarkPKIAuthorityKeyDestroyed(ctx, authority.ID, destroyedAt); err != nil {
			return err
		}
		return requirePKIAuthorityLeaseFence(ctx, tx, deleteGrant)
	})
}

func (r *PKIAuthorityRuntime) reconcileAuthorityKeyDestruction(ctx context.Context, authorities []storage.PKIAuthorityRow) error {
	var result error
	for _, authority := range authorities {
		if authority.PrivateKeyDestroyedAt != nil || authority.EncryptedKeyRef == nil {
			continue
		}
		if authority.Status != "retired" && authority.Status != "revoked" {
			continue
		}
		result = errors.Join(result, r.destroyAuthorityKeyCoordinated(ctx, authority))
	}
	return result
}

func (r *PKIAuthorityRuntime) retryNormal(ctx context.Context, row storage.PKILifecycleJobRow, payload pkiAuthorityRuntimePayload, cause error) error {
	grant, err := r.lease.RequirePKILease(ctx)
	if err != nil {
		return err
	}
	now := r.clock().UTC()
	retryAt := now.Add(time.Minute)
	return r.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		if err := requirePKIAuthorityLeaseFence(ctx, tx, grant); err != nil {
			return err
		}
		previous, found, err := tx.GetPKILifecycleJobForUpdate(ctx, row.ID)
		if err != nil {
			return err
		}
		if !found || previous.UpdatedAt != row.UpdatedAt {
			return ErrPKILifecycleConflict
		}
		next := previous
		next.State = storage.PKILifecycleJobStatePending
		next.NextAttemptAt = &retryAt
		next.LastError = truncatePKIRuntimeError(cause)
		if !json.Valid([]byte(next.RuntimeJSON)) {
			next.RuntimeJSON = "{}"
		}
		next.Attempt++
		next.UpdatedAt = now
		return tx.UpdatePKILifecycleJob(ctx, previous, next)
	})
}

func (r *PKIAuthorityRuntime) failNormal(ctx context.Context, row storage.PKILifecycleJobRow, cause error) error {
	grant, err := r.lease.RequirePKILease(ctx)
	if err != nil {
		return err
	}
	now := r.clock().UTC()
	return r.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		if err := requirePKIAuthorityLeaseFence(ctx, tx, grant); err != nil {
			return err
		}
		previous, found, err := tx.GetPKILifecycleJobForUpdate(ctx, row.ID)
		if err != nil {
			return err
		}
		if !found || pkiLifecycleTerminal(previous.State) {
			return nil
		}
		next := previous
		next.Phase = "failed"
		next.State = storage.PKILifecycleJobStateFailed
		next.LastError = truncatePKIRuntimeError(cause)
		if !json.Valid([]byte(next.RuntimeJSON)) {
			next.RuntimeJSON = "{}"
		}
		next.UpdatedAt = now
		return tx.UpdatePKILifecycleJob(ctx, previous, next)
	})
}

func decodePKIAuthorityRuntime(row storage.PKILifecycleJobRow) (pkiAuthorityRuntimePayload, error) {
	var payload pkiAuthorityRuntimePayload
	decoder := json.NewDecoder(strings.NewReader(row.RuntimeJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || payload.Rotation.ID != row.ID || strings.TrimSpace(payload.Reason) == "" {
		return pkiAuthorityRuntimePayload{}, fmt.Errorf("%w: CA rotation runtime is invalid", ErrPKILifecycleInvalid)
	}
	return payload, nil
}

func equalPKICARotationJob(left, right PKICARotationJob) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return slices.Equal(leftJSON, rightJSON)
}

func findPKILifecycleRow(state storage.PKICanonicalState, operationID string) (storage.PKILifecycleJobRow, bool) {
	operationID = strings.TrimSpace(operationID)
	for _, row := range state.LifecycleJobs {
		if row.ID == operationID || row.OperationID == operationID {
			return row, true
		}
	}
	return storage.PKILifecycleJobRow{}, false
}

func activePKIAuthority(authorities []storage.PKIAuthorityRow) (storage.PKIAuthorityRow, bool) {
	var selected storage.PKIAuthorityRow
	found := false
	for _, authority := range authorities {
		if authority.Status == "active" && (!found || authority.Generation > selected.Generation) {
			selected, found = authority, true
		}
	}
	return selected, found
}

func authorityByGeneration(authorities []storage.PKIAuthorityRow, generation int64) (storage.PKIAuthorityRow, bool) {
	for _, authority := range authorities {
		if authority.Generation == generation {
			return authority, true
		}
	}
	return storage.PKIAuthorityRow{}, false
}

func pkiAuthorityPublicKeyFingerprint(certificatePEM string) (string, error) {
	certificate, err := parsePKIAuthorityCertificate(certificatePEM)
	if err != nil {
		return "", err
	}
	publicDER, err := x509.MarshalPKIXPublicKey(certificate.PublicKey)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(publicDER)
	return hex.EncodeToString(digest[:]), nil
}

func validatePreparedRuntimeAuthority(job PKICARotationJob, material PKIAuthorityMaterial, now time.Time) error {
	if material.Generation <= job.CurrentGeneration ||
		strings.TrimSpace(material.CertificatePEM) == "" || strings.TrimSpace(material.KeyReference) == "" ||
		strings.TrimSpace(material.KeyFingerprint) == "" || strings.TrimSpace(material.CertificateFingerprint) == "" ||
		material.KeyFingerprint == job.CurrentKeyFingerprint || material.CertificateFingerprint == job.CurrentCertFingerprint ||
		material.NotBefore.After(now) || !material.NotAfter.After(now) {
		return fmt.Errorf("%w: staged CA replacement is invalid", ErrPKILifecycleInvalid)
	}
	return nil
}

func pkiRuntimeAuthorityID(domainID string, generation int64) string {
	return "authority-" + strings.TrimSpace(domainID) + "-g" + strconv.FormatInt(generation, 10)
}

func nextPKIAuthorityGeneration(authorities []storage.PKIAuthorityRow) int64 {
	var highest int64
	for _, authority := range authorities {
		if authority.Generation > highest {
			highest = authority.Generation
		}
	}
	if highest == int64(^uint64(0)>>1) {
		return 0
	}
	return highest + 1
}

func setProjectedAuthorityStatus(authorities []storage.PKIAuthorityRow, authorityID, status string, retireDeadline *time.Time) {
	for index := range authorities {
		if authorities[index].ID == authorityID {
			authorities[index].Status = status
			authorities[index].RetireDeadline = retireDeadline
			return
		}
	}
}

func projectedPKITrustRoots(authorities []storage.PKIAuthorityRow) ([]int64, []PKISecurityTrustRootDescriptor) {
	trusted := make([]storage.PKIAuthorityRow, 0, len(authorities))
	for _, authority := range authorities {
		switch authority.Status {
		case "active", "prepared", "retiring":
			trusted = append(trusted, authority)
		}
	}
	sort.Slice(trusted, func(i, j int) bool { return trusted[i].Generation < trusted[j].Generation })
	generations := make([]int64, 0, len(trusted))
	roots := make([]PKISecurityTrustRootDescriptor, 0, len(trusted))
	for _, authority := range trusted {
		generations = append(generations, authority.Generation)
		roots = append(roots, PKISecurityTrustRootDescriptor{
			AuthorityID: authority.ID, Generation: authority.Generation, Status: authority.Status,
			FingerprintSHA256: strings.ToLower(authority.FingerprintSHA256),
			NotBefore:         authority.NotBefore.UTC(), NotAfter: authority.NotAfter.UTC(),
		})
	}
	return generations, roots
}

func canonicalPKIRevocations(state storage.PKICanonicalState) ([]string, []string) {
	identities := make([]string, 0)
	serials := make([]string, 0)
	for _, identity := range state.Identities {
		if identity.State == storage.PKIIdentityStateRevoked {
			identities = append(identities, identity.ID)
		}
	}
	for _, certificate := range state.Certificates {
		if certificate.Status == storage.PKICertificateStatusRevoked {
			serials = append(serials, certificate.SerialHex)
		}
	}
	slices.Sort(identities)
	slices.Sort(serials)
	return identities, serials
}

func parsePKIAgentHeartbeat(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func requirePKIAuthorityLeaseFence(ctx context.Context, tx *storage.PKITransaction, grant PKILeaseGrant) error {
	err := tx.RequirePKILeaseFence(ctx, storage.PKILeaseFence{
		PKIDomainID: grant.PKIDomainID, PKIEpoch: grant.PKIEpoch, InstanceID: grant.InstanceID,
		LeaseTerm: grant.LeaseTerm, LeaseDeadline: grant.LeaseDeadline,
	})
	if errors.Is(err, storage.ErrPKILeaseFence) {
		return ErrPKILeaseNotHeld
	}
	return err
}

func appendPKIAuthorityRuntimeEvent(
	ctx context.Context,
	tx *storage.PKITransaction,
	domainID, eventType, objectID, operatorID, reason string,
	generation, securityRevision int64,
	occurredAt time.Time,
	details map[string]any,
) error {
	result := "succeeded"
	if strings.Contains(eventType, "failed") {
		result = "failed"
	} else if strings.Contains(eventType, "blocked") {
		result = "blocked"
	} else if strings.Contains(eventType, "superseded") {
		result = "cancelled"
	}
	event := NewPKIAuditEvent(eventType, "scheduler", objectID, result, reason, occurredAt)
	event.OperatorID = strings.TrimSpace(operatorID)
	event.ObjectType = "pki_lifecycle_job"
	event.CAGeneration = generation
	event.SecurityRevision = securityRevision
	event.Details = details
	event.ID = stablePKIAuditEventID(event)
	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return tx.AppendPKIEvent(ctx, storage.PKIEventRow{
		ID: event.ID, PKIDomainID: domainID, Type: event.Type, OccurredAt: occurredAt,
		Source: event.Source, OperatorID: event.OperatorID, ObjectType: event.ObjectType, ObjectID: objectID,
		CAGeneration: &generation, Result: event.Result, Reason: event.Reason,
		SecurityRevision: securityRevision, DetailsJSON: string(encoded),
	})
}

func truncatePKIRuntimeError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 1024 {
		message = message[:1024]
	}
	return message
}
