package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type pkiEmergencyRuntimePayload struct {
	PreviousGeneration           int64                   `json:"previous_generation"`
	PreviousKey                  string                  `json:"previous_key_fingerprint"`
	PreviousCertificate          string                  `json:"previous_certificate_fingerprint"`
	ReplacementGeneration        int64                   `json:"replacement_generation"`
	Reason                       string                  `json:"reason"`
	OperatorID                   string                  `json:"operator_id"`
	RequiredReenrollmentAgentIDs []string                `json:"required_reenrollment_agent_ids,omitempty"`
	RelayDisableBarrier          PKIRelayRevisionBarrier `json:"relay_disable_barrier,omitempty"`
	RelayEnableBarrier           PKIRelayRevisionBarrier `json:"relay_enable_barrier,omitempty"`
}

func (r *PKIAuthorityRuntime) StartEmergency(ctx context.Context, operationID, reason, operatorID string) error {
	operationID = strings.TrimSpace(operationID)
	reason = strings.TrimSpace(reason)
	operatorID = strings.TrimSpace(operatorID)
	if operationID == "" || reason == "" || operatorID == "" {
		return fmt.Errorf("%w: emergency CA operation fields are incomplete", ErrPKILifecycleInvalid)
	}
	state, row, payload, err := r.ensureEmergencyFailClosed(ctx, operationID, reason, operatorID)
	if err != nil {
		return err
	}
	if pkiLifecycleTerminal(row.State) {
		return nil
	}
	if row.Phase == "relay_enable_pending" {
		r.destroyEmergencyKeysBestEffort(ctx, state.Authorities, payload.ReplacementGeneration)
		return r.completeEmergencyRelayEnable(ctx, operationID, payload)
	}
	disableBarrier, err := r.relayGate.DisablePKIRelay(ctx, payload.RelayDisableBarrier)
	payload.RelayDisableBarrier = disableBarrier
	if err != nil {
		return r.failEmergency(ctx, row, payload, err)
	}
	if err := r.recordEmergencyRelayDisableBarrier(ctx, row.ID, payload); err != nil {
		return err
	}
	if !disableBarrier.Converged {
		return nil
	}
	state, err = r.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return err
	}
	row, found := findPKILifecycleRow(state, operationID)
	if !found {
		return ErrPKIOperationNotFound
	}
	r.publishCanonicalSnapshotBestEffort(ctx)
	material, err := r.generator.GeneratePKIAuthority(ctx, payload.ReplacementGeneration, reason)
	if err != nil {
		return r.failEmergency(ctx, row, payload, err)
	}
	if err := validateEmergencyPKIAuthority(PKIEmergencyAuthorityState{
		PKIDomainID: state.Settings.PKIDomainID, ActiveGeneration: payload.PreviousGeneration,
		ActiveKeyFingerprint: payload.PreviousKey, ActiveCertFingerprint: payload.PreviousCertificate,
		SecurityRevision: state.Settings.SecurityRevision,
	}, material, r.clock().UTC()); err != nil {
		return r.failEmergency(ctx, row, payload, err)
	}
	replaced, err := r.commitEmergencyReplacement(ctx, state, row, &payload, material)
	if err != nil {
		return err
	}
	if !replaced {
		return nil
	}
	r.publishCanonicalSnapshotBestEffort(ctx)
	r.destroyEmergencyKeysBestEffort(ctx, state.Authorities, payload.ReplacementGeneration)
	return r.completeEmergencyRelayEnable(ctx, operationID, payload)
}

func (r *PKIAuthorityRuntime) ensureEmergencyFailClosed(
	ctx context.Context,
	operationID, reason, operatorID string,
) (storage.PKICanonicalState, storage.PKILifecycleJobRow, pkiEmergencyRuntimePayload, error) {
	state, err := r.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return storage.PKICanonicalState{}, storage.PKILifecycleJobRow{}, pkiEmergencyRuntimePayload{}, err
	}
	row, found := findPKILifecycleRow(state, operationID)
	if !found || row.Kind != "emergency_ca_rotate" {
		return storage.PKICanonicalState{}, storage.PKILifecycleJobRow{}, pkiEmergencyRuntimePayload{}, ErrPKIOperationNotFound
	}
	if row.Phase != "queued" {
		payload, err := decodePKIEmergencyRuntime(row)
		return state, row, payload, err
	}
	if queued, queuedErr := decodePKIAuthorityQueuedPayload(row); queuedErr == nil {
		reason = queued.Reason
		operatorID = queued.OperatorID
	}
	if state.Settings == nil || state.Settings.RelayFailClosed || state.Settings.SecurityRevision == int64(^uint64(0)>>1) {
		return storage.PKICanonicalState{}, storage.PKILifecycleJobRow{}, pkiEmergencyRuntimePayload{},
			fmt.Errorf("%w: emergency fail-closed state cannot be initialized", ErrPKILifecycleInvalid)
	}
	active, found := activePKIAuthority(state.Authorities)
	if !found || active.Generation == int64(^uint64(0)>>1) {
		return storage.PKICanonicalState{}, storage.PKILifecycleJobRow{}, pkiEmergencyRuntimePayload{},
			fmt.Errorf("%w: emergency active authority is unavailable", ErrPKILifecycleInvalid)
	}
	keyFingerprint, err := pkiAuthorityPublicKeyFingerprint(active.CertificatePEM)
	if err != nil {
		return storage.PKICanonicalState{}, storage.PKILifecycleJobRow{}, pkiEmergencyRuntimePayload{}, err
	}
	payload := pkiEmergencyRuntimePayload{
		PreviousGeneration: active.Generation, PreviousKey: keyFingerprint,
		PreviousCertificate: active.FingerprintSHA256, ReplacementGeneration: nextPKIAuthorityGeneration(state.Authorities),
		Reason: reason, OperatorID: operatorID,
	}
	if payload.ReplacementGeneration <= payload.PreviousGeneration {
		return storage.PKICanonicalState{}, storage.PKILifecycleJobRow{}, pkiEmergencyRuntimePayload{},
			fmt.Errorf("%w: emergency CA generation cannot be incremented", ErrPKILifecycleInvalid)
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return storage.PKICanonicalState{}, storage.PKILifecycleJobRow{}, pkiEmergencyRuntimePayload{}, err
	}
	signed, err := r.signProjectedSnapshot(ctx, state, state.Authorities, active, state.Settings.SecurityRevision+1)
	if err != nil {
		return storage.PKICanonicalState{}, storage.PKILifecycleJobRow{}, pkiEmergencyRuntimePayload{}, err
	}
	grant, err := r.lease.RequirePKILease(ctx)
	if err != nil {
		return storage.PKICanonicalState{}, storage.PKILifecycleJobRow{}, pkiEmergencyRuntimePayload{}, err
	}
	now := r.clock().UTC()
	err = r.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		if err := requirePKIAuthorityLeaseFence(ctx, tx, grant); err != nil {
			return err
		}
		previous, found, err := tx.GetPKILifecycleJobForUpdate(ctx, row.ID)
		if err != nil {
			return err
		}
		if !found || previous.Phase != "queued" || previous.State != storage.PKILifecycleJobStatePending {
			return ErrPKILifecycleConflict
		}
		if err := tx.SetPKISecurityState(ctx, state.Settings.SecurityRevision, state.Settings.SecurityRevision+1, false, true, now); err != nil {
			return err
		}
		canonical, err := tx.LoadPKICanonicalState(ctx)
		if err != nil {
			return err
		}
		persisted, err := storagePKISecuritySnapshot(canonical, signed)
		if err != nil {
			return err
		}
		encodedSnapshot, err := json.Marshal(persisted)
		if err != nil {
			return err
		}
		if err := tx.SavePKISecuritySnapshot(ctx, storage.PKISecuritySnapshotRow{
			PKIDomainID: state.Settings.PKIDomainID, PKIEpoch: state.Settings.PKIEpoch,
			SecurityRevision: state.Settings.SecurityRevision + 1, SnapshotJSON: string(encodedSnapshot), UpdatedAt: now,
		}); err != nil {
			return err
		}
		next := previous
		next.Phase = "relay_disable_pending"
		next.State = storage.PKILifecycleJobStateRunning
		next.RuntimeJSON = string(encodedPayload)
		next.Attempt++
		next.UpdatedAt = now
		if err := tx.UpdatePKILifecycleJob(ctx, previous, next); err != nil {
			return err
		}
		if err := appendPKIAuthorityRuntimeEvent(ctx, tx, state.Settings.PKIDomainID,
			"pki.ca.emergency.fail_closed", row.ID, operatorID, reason, active.Generation,
			state.Settings.SecurityRevision+1, now, map[string]any{"relay_fail_closed": true}); err != nil {
			return err
		}
		return requirePKIAuthorityLeaseFence(ctx, tx, grant)
	})
	if err != nil {
		return storage.PKICanonicalState{}, storage.PKILifecycleJobRow{}, pkiEmergencyRuntimePayload{}, err
	}
	state, err = r.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return storage.PKICanonicalState{}, storage.PKILifecycleJobRow{}, pkiEmergencyRuntimePayload{}, err
	}
	row, found = findPKILifecycleRow(state, operationID)
	if !found {
		return storage.PKICanonicalState{}, storage.PKILifecycleJobRow{}, pkiEmergencyRuntimePayload{}, ErrPKIOperationNotFound
	}
	return state, row, payload, nil
}

func (r *PKIAuthorityRuntime) recordEmergencyRelayDisableBarrier(
	ctx context.Context,
	operationID string,
	payload pkiEmergencyRuntimePayload,
) error {
	grant, err := r.lease.RequirePKILease(ctx)
	if err != nil {
		return err
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
		if pkiLifecycleTerminal(previous.State) {
			return nil
		}
		if previous.Phase != "relay_disable_pending" && previous.Phase != "relay_disabled" {
			return ErrPKILifecycleConflict
		}
		settings, found, err := tx.GetPKISettingsForUpdate(ctx)
		if err != nil {
			return err
		}
		if !found || !settings.RelayFailClosed {
			return fmt.Errorf("%w: emergency relay disable lost the fail-closed latch", ErrPKILifecycleInvalid)
		}
		next := previous
		next.RuntimeJSON = string(encoded)
		next.LastError = ""
		next.UpdatedAt = now
		if payload.RelayDisableBarrier.Converged {
			next.Phase = "relay_disabled"
			next.State = storage.PKILifecycleJobStateRunning
			next.NextAttemptAt = nil
		} else {
			next.Phase = "relay_disable_pending"
			next.State = storage.PKILifecycleJobStatePending
			retryAt := now.Add(r.heartbeatInterval)
			next.NextAttemptAt = &retryAt
		}
		if err := tx.UpdatePKILifecycleJob(ctx, previous, next); err != nil {
			return err
		}
		return requirePKIAuthorityLeaseFence(ctx, tx, grant)
	})
}

func (r *PKIAuthorityRuntime) commitEmergencyReplacement(
	ctx context.Context,
	state storage.PKICanonicalState,
	row storage.PKILifecycleJobRow,
	payload *pkiEmergencyRuntimePayload,
	material PKIAuthorityMaterial,
) (bool, error) {
	if payload == nil {
		return false, fmt.Errorf("%w: emergency replacement runtime is missing", ErrPKILifecycleInvalid)
	}
	if state.Settings == nil || !state.Settings.RelayFailClosed || state.Settings.SecurityRevision == int64(^uint64(0)>>1) {
		return false, fmt.Errorf("%w: emergency replacement requires the durable fail-closed latch", ErrPKILifecycleInvalid)
	}
	now := r.clock().UTC()
	keyReference := material.KeyReference
	newAuthority := storage.PKIAuthorityRow{
		ID:          pkiRuntimeAuthorityID(state.Settings.PKIDomainID, material.Generation),
		PKIDomainID: state.Settings.PKIDomainID, Generation: material.Generation, Status: "active",
		CertificatePEM: material.CertificatePEM, EncryptedKeyRef: &keyReference,
		FingerprintSHA256: material.CertificateFingerprint, NotBefore: material.NotBefore, NotAfter: material.NotAfter,
		CreatedReason: payload.Reason, CreatedAt: now, UpdatedAt: now,
	}
	projected := state
	projected.Authorities = []storage.PKIAuthorityRow{newAuthority}
	for index := range projected.Identities {
		projected.Identities[index].State = storage.PKIIdentityStateRevoked
		projected.Identities[index].CurrentCertificateID = nil
	}
	for index := range projected.Certificates {
		switch projected.Certificates[index].Status {
		case storage.PKICertificateStatusActive, storage.PKICertificateStatusPending, storage.PKICertificateStatusSuperseded:
			projected.Certificates[index].Status = storage.PKICertificateStatusRevoked
		}
	}
	signed, err := r.signProjectedSnapshot(ctx, projected, projected.Authorities, newAuthority,
		state.Settings.SecurityRevision+1)
	if err != nil {
		return false, err
	}
	grant, err := r.lease.RequirePKILease(ctx)
	if err != nil {
		return false, err
	}
	replaced := false
	err = r.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		if err := requirePKIAuthorityLeaseFence(ctx, tx, grant); err != nil {
			return err
		}
		previous, found, err := tx.GetPKILifecycleJobForUpdate(ctx, row.ID)
		if err != nil {
			return err
		}
		if !found || previous.UpdatedAt != row.UpdatedAt || previous.Phase != "relay_disabled" {
			return ErrPKILifecycleConflict
		}
		converged, err := r.relayGate.ConfirmPKIRelayBarrier(ctx, tx, payload.RelayDisableBarrier)
		if err != nil {
			return err
		}
		if !converged {
			next := previous
			next.Phase = "relay_disable_pending"
			next.State = storage.PKILifecycleJobStatePending
			retryAt := now.Add(r.heartbeatInterval)
			next.NextAttemptAt = &retryAt
			next.UpdatedAt = now
			return tx.UpdatePKILifecycleJob(ctx, previous, next)
		}
		agents, err := tx.ListPKIRelayBarrierAgents(ctx)
		if err != nil {
			return err
		}
		requiredReenrollment := make([]string, 0, len(agents))
		for _, agent := range agents {
			if agent.IsLocal || strings.TrimSpace(agent.AgentToken) == "" || payload.RelayDisableBarrier.Revisions[agent.ID] <= 0 {
				continue
			}
			requiredReenrollment = append(requiredReenrollment, agent.ID)
		}
		payload.RequiredReenrollmentAgentIDs = requiredReenrollment
		for _, authority := range state.Authorities {
			switch authority.Status {
			case "active", "prepared", "retiring", "staged":
				if err := tx.TransitionPKIAuthority(ctx, authority.ID, authority.Status, "revoked", payload.Reason, nil, now); err != nil {
					return err
				}
			}
		}
		if err := tx.CreatePKIAuthority(ctx, newAuthority); err != nil {
			return err
		}
		for _, identity := range state.Identities {
			if identity.State == storage.PKIIdentityStateRevoked {
				continue
			}
			if _, _, err := tx.RevokePKIIdentityCertificates(ctx, identity.ID, "emergency CA replacement: "+payload.Reason, now); err != nil {
				return err
			}
		}
		for _, agent := range agents {
			if _, err := tx.DisablePKIStableAgentToken(ctx, agent.ID); err != nil {
				return err
			}
		}
		if err := tx.SetPKISecurityState(ctx, state.Settings.SecurityRevision, state.Settings.SecurityRevision+1, true, true, now); err != nil {
			return err
		}
		canonical, err := tx.LoadPKICanonicalState(ctx)
		if err != nil {
			return err
		}
		persisted, err := storagePKISecuritySnapshot(canonical, signed)
		if err != nil {
			return err
		}
		encodedSnapshot, err := json.Marshal(persisted)
		if err != nil {
			return err
		}
		if err := tx.SavePKISecuritySnapshot(ctx, storage.PKISecuritySnapshotRow{
			PKIDomainID: state.Settings.PKIDomainID, PKIEpoch: state.Settings.PKIEpoch,
			SecurityRevision: state.Settings.SecurityRevision + 1, SnapshotJSON: string(encodedSnapshot), UpdatedAt: now,
		}); err != nil {
			return err
		}
		next := previous
		next.Phase = "relay_enable_pending"
		next.State = storage.PKILifecycleJobStateRunning
		next.LastError = ""
		next.NextAttemptAt = nil
		encodedPayload, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		next.RuntimeJSON = string(encodedPayload)
		next.UpdatedAt = now
		if err := tx.UpdatePKILifecycleJob(ctx, previous, next); err != nil {
			return err
		}
		if err := appendPKIAuthorityRuntimeEvent(ctx, tx, state.Settings.PKIDomainID,
			"pki.ca.emergency.rotated", row.ID, payload.OperatorID, payload.Reason, material.Generation,
			state.Settings.SecurityRevision+1, now, map[string]any{
				"previous_generation": payload.PreviousGeneration, "new_generation": material.Generation,
				"affected_identities": len(state.Identities), "control_tokens_disabled": true,
			}); err != nil {
			return err
		}
		replaced = true
		return requirePKIAuthorityLeaseFence(ctx, tx, grant)
	})
	if err == nil && !replaced && strings.TrimSpace(material.KeyReference) != "" {
		if cleanupGrant, leaseErr := r.lease.RequirePKILease(ctx); leaseErr == nil && samePKILeaseAuthority(grant, cleanupGrant) {
			_ = r.keyDestroyer.DestroyCAKey(material.KeyReference, state.Settings.PKIDomainID,
				material.Generation, pkiBackupCAPurpose)
		}
	}
	return replaced, err
}

func (r *PKIAuthorityRuntime) completeEmergencyRelayEnable(
	ctx context.Context,
	operationID string,
	payload pkiEmergencyRuntimePayload,
) error {
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
	if row.Phase != "relay_enable_pending" || state.Settings == nil || !state.Settings.RelayFailClosed {
		return fmt.Errorf("%w: emergency relay enable state is inconsistent", ErrPKILifecycleInvalid)
	}
	agents, err := r.store.ListAgents(ctx)
	if err != nil {
		return err
	}
	if !emergencyPKIReenrollmentReady(state, agents, payload, r.clock().UTC()) {
		return r.waitEmergencyRelayEnable(ctx, row, payload)
	}
	enableBarrier, err := r.relayGate.EnablePKIRelay(ctx, payload.RelayEnableBarrier)
	payload.RelayEnableBarrier = enableBarrier
	if err != nil {
		return r.retryEmergencyRelayEnable(ctx, row, payload, err)
	}
	if !enableBarrier.Converged {
		return r.waitEmergencyRelayEnable(ctx, row, payload)
	}
	grant, err := r.lease.RequirePKILease(ctx)
	if err != nil {
		return err
	}
	now := r.clock().UTC()
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	err = r.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
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
			return nil
		}
		if previous.Phase != "relay_enable_pending" {
			return ErrPKILifecycleConflict
		}
		settings, found, err := tx.GetPKISettingsForUpdate(ctx)
		if err != nil {
			return err
		}
		if !found || !settings.RelayFailClosed || settings.SecurityRevision != state.Settings.SecurityRevision {
			return fmt.Errorf("%w: emergency relay enable lost the fail-closed latch", ErrPKILifecycleInvalid)
		}
		agents, err := tx.ListPKIRelayBarrierAgents(ctx)
		if err != nil {
			return err
		}
		canonical, err := tx.LoadPKICanonicalState(ctx)
		if err != nil {
			return err
		}
		if !emergencyPKIReenrollmentReady(canonical, agents, payload, now) {
			next := previous
			next.State = storage.PKILifecycleJobStatePending
			retryAt := now.Add(r.heartbeatInterval)
			next.NextAttemptAt = &retryAt
			next.UpdatedAt = now
			return tx.UpdatePKILifecycleJob(ctx, previous, next)
		}
		converged, err := r.relayGate.ConfirmPKIRelayBarrier(ctx, tx, payload.RelayEnableBarrier)
		if err != nil {
			return err
		}
		if !converged {
			next := previous
			next.State = storage.PKILifecycleJobStatePending
			retryAt := now.Add(r.heartbeatInterval)
			next.NextAttemptAt = &retryAt
			next.UpdatedAt = now
			return tx.UpdatePKILifecycleJob(ctx, previous, next)
		}
		if err := tx.ClearPKIRelayFailClosed(ctx, settings.SecurityRevision, now); err != nil {
			return err
		}
		next := previous
		next.Phase = "completed"
		next.State = storage.PKILifecycleJobStateSucceeded
		next.LastError = ""
		next.NextAttemptAt = nil
		next.RuntimeJSON = string(encoded)
		next.UpdatedAt = now
		if err := tx.UpdatePKILifecycleJob(ctx, previous, next); err != nil {
			return err
		}
		if err := appendPKIAuthorityRuntimeEvent(ctx, tx, state.Settings.PKIDomainID,
			"pki.ca.emergency.relay_enabled", row.ID, payload.OperatorID, payload.Reason,
			payload.ReplacementGeneration, state.Settings.SecurityRevision, now,
			map[string]any{"relay_fail_closed": false}); err != nil {
			return err
		}
		return requirePKIAuthorityLeaseFence(ctx, tx, grant)
	})
	return err
}

func emergencyPKIReenrollmentReady(
	state storage.PKICanonicalState,
	agentRows []storage.AgentRow,
	payload pkiEmergencyRuntimePayload,
	now time.Time,
) bool {
	if now.IsZero() {
		return false
	}
	agents := make(map[string]storage.AgentRow, len(agentRows))
	for _, agent := range agentRows {
		agents[agent.ID] = agent
	}
	identities := make(map[string]storage.PKIIdentityRow)
	for _, identity := range state.Identities {
		if identity.Kind == storage.PKIIdentityKindAgent {
			identities[identity.AgentID] = identity
		}
	}
	certificates := make(map[string]storage.PKICertificateRow, len(state.Certificates))
	for _, certificate := range state.Certificates {
		certificates[certificate.ID] = certificate
	}
	for _, agentID := range payload.RequiredReenrollmentAgentIDs {
		agent, found := agents[agentID]
		if !found || agent.IsLocal {
			return false
		}
		identity, found := identities[agentID]
		if !found {
			return false
		}
		if strings.TrimSpace(agent.AgentToken) != "" && identity.State == storage.PKIIdentityStateActive && identity.CurrentCertificateID != nil {
			certificate, found := certificates[*identity.CurrentCertificateID]
			if found && certificate.IdentityID == identity.ID && certificate.Status == storage.PKICertificateStatusActive &&
				certificate.CAGeneration == payload.ReplacementGeneration &&
				certificate.Purpose == storage.PKICertificatePurposeClient && !certificate.NotBefore.After(now) && certificate.NotAfter.After(now) {
				continue
			}
		}
		// A replacement-generation certificate proves the agent completed
		// re-enrollment. If that credential was subsequently revoked and its
		// convergence job completed, explicit isolation may remove it from the
		// enable barrier without weakening the replacement-driven requirement.
		if strings.TrimSpace(agent.AgentToken) == "" && identity.State == storage.PKIIdentityStateRevoked &&
			emergencyPKIRevocationConverged(state, agentID) {
			for _, certificate := range state.Certificates {
				if certificate.IdentityID == identity.ID && certificate.CAGeneration == payload.ReplacementGeneration &&
					certificate.Status == storage.PKICertificateStatusRevoked {
					goto nextAgent
				}
			}
		}
		return false
	nextAgent:
	}
	return true
}

func (r *PKIAuthorityRuntime) waitEmergencyRelayEnable(
	ctx context.Context,
	row storage.PKILifecycleJobRow,
	payload pkiEmergencyRuntimePayload,
) error {
	grant, err := r.lease.RequirePKILease(ctx)
	if err != nil {
		return err
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
		previous, found, err := tx.GetPKILifecycleJobForUpdate(ctx, row.ID)
		if err != nil {
			return err
		}
		if !found || pkiLifecycleTerminal(previous.State) {
			return nil
		}
		if previous.Phase != "relay_enable_pending" {
			return ErrPKILifecycleConflict
		}
		next := previous
		next.Phase = "relay_enable_pending"
		next.State = storage.PKILifecycleJobStatePending
		retryAt := now.Add(r.heartbeatInterval)
		next.NextAttemptAt = &retryAt
		next.LastError = ""
		next.RuntimeJSON = string(encoded)
		next.UpdatedAt = now
		if err := tx.UpdatePKILifecycleJob(ctx, previous, next); err != nil {
			return err
		}
		settings, found, err := tx.GetPKISettingsForUpdate(ctx)
		if err != nil {
			return err
		}
		if !found || !settings.RelayFailClosed {
			return fmt.Errorf("%w: emergency relay enable lost the replacement state", ErrPKILifecycleInvalid)
		}
		return requirePKIAuthorityLeaseFence(ctx, tx, grant)
	})
}

func (r *PKIAuthorityRuntime) retryEmergencyRelayEnable(
	ctx context.Context,
	row storage.PKILifecycleJobRow,
	payload pkiEmergencyRuntimePayload,
	cause error,
) error {
	grant, err := r.lease.RequirePKILease(ctx)
	if err != nil {
		return err
	}
	now := r.clock().UTC()
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
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
		next.Phase = "relay_enable_pending"
		next.State = storage.PKILifecycleJobStatePending
		retryAt := now.Add(time.Minute)
		next.NextAttemptAt = &retryAt
		next.LastError = truncatePKIRuntimeError(cause)
		next.RuntimeJSON = string(encoded)
		next.Attempt++
		next.UpdatedAt = now
		if err := tx.UpdatePKILifecycleJob(ctx, previous, next); err != nil {
			return err
		}
		settings, found, err := tx.GetPKISettingsForUpdate(ctx)
		if err != nil {
			return err
		}
		if !found || !settings.RelayFailClosed {
			return fmt.Errorf("%w: emergency relay enable lost the replacement state", ErrPKILifecycleInvalid)
		}
		if err := appendPKIAuthorityRuntimeEvent(ctx, tx, settings.PKIDomainID,
			"pki.ca.emergency.relay_enable_failed", row.ID, payload.OperatorID, next.LastError,
			payload.ReplacementGeneration, settings.SecurityRevision, now,
			map[string]any{"relay_fail_closed": true}); err != nil {
			return err
		}
		return requirePKIAuthorityLeaseFence(ctx, tx, grant)
	})
}

func (r *PKIAuthorityRuntime) failEmergency(
	ctx context.Context,
	row storage.PKILifecycleJobRow,
	payload pkiEmergencyRuntimePayload,
	cause error,
) error {
	grant, err := r.lease.RequirePKILease(ctx)
	if err != nil {
		return err
	}
	now := r.clock().UTC()
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
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
		next.Phase = "relay_disabled"
		next.State = storage.PKILifecycleJobStatePending
		retryAt := now.Add(time.Minute)
		next.NextAttemptAt = &retryAt
		next.LastError = truncatePKIRuntimeError(cause)
		next.RuntimeJSON = string(encoded)
		next.Attempt++
		next.UpdatedAt = now
		if err := tx.UpdatePKILifecycleJob(ctx, previous, next); err != nil {
			return err
		}
		settings, found, err := tx.GetPKISettingsForUpdate(ctx)
		if err != nil {
			return err
		}
		if !found || !settings.RelayFailClosed {
			return fmt.Errorf("%w: emergency failure lost the fail-closed latch", ErrPKILifecycleInvalid)
		}
		if err := appendPKIAuthorityRuntimeEvent(ctx, tx, settings.PKIDomainID,
			"pki.ca.emergency.failed_closed", row.ID, payload.OperatorID, next.LastError,
			payload.PreviousGeneration, settings.SecurityRevision, now,
			map[string]any{"relay_fail_closed": true}); err != nil {
			return err
		}
		return requirePKIAuthorityLeaseFence(ctx, tx, grant)
	})
}

func (r *PKIAuthorityRuntime) destroyEmergencyKeysBestEffort(
	ctx context.Context,
	authorities []storage.PKIAuthorityRow,
	replacementGeneration int64,
) {
	for _, authority := range authorities {
		if authority.Generation == replacementGeneration || authority.PrivateKeyDestroyedAt != nil {
			continue
		}
		_ = r.destroyAuthorityKeyCoordinated(ctx, authority)
	}
}

func decodePKIEmergencyRuntime(row storage.PKILifecycleJobRow) (pkiEmergencyRuntimePayload, error) {
	var payload pkiEmergencyRuntimePayload
	if err := json.Unmarshal([]byte(row.RuntimeJSON), &payload); err != nil || payload.PreviousGeneration <= 0 ||
		payload.ReplacementGeneration <= payload.PreviousGeneration || strings.TrimSpace(payload.Reason) == "" ||
		strings.TrimSpace(payload.OperatorID) == "" {
		return pkiEmergencyRuntimePayload{}, fmt.Errorf("%w: emergency CA runtime is invalid", ErrPKILifecycleInvalid)
	}
	return payload, nil
}
