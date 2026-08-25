package service

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type pkiCanonicalStateSource interface {
	LoadPKICanonicalState(context.Context) (storage.PKICanonicalState, error)
}

type GormPKIRevocationRepositoryOptions struct {
	Store PKITransactionStore
	Clock func() time.Time
}

type GormPKIRevocationRepository struct {
	store PKITransactionStore
	clock func() time.Time
}

func NewGormPKIRevocationRepository(options GormPKIRevocationRepositoryOptions) (*GormPKIRevocationRepository, error) {
	if options.Store == nil {
		return nil, fmt.Errorf("%w: revocation transaction store is required", ErrPKILifecycleInvalid)
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	return &GormPKIRevocationRepository{store: options.Store, clock: options.Clock}, nil
}

func (r *GormPKIRevocationRepository) RevokePKIIdentityAtomically(
	ctx context.Context,
	mutation PKIRevocationMutation,
	buildSnapshot func(context.Context, PKIRevocationFacts) (PKISignedSecuritySnapshot, error),
) (PKIRevocationCommit, error) {
	if buildSnapshot == nil {
		return PKIRevocationCommit{}, fmt.Errorf("%w: security snapshot builder is required", ErrPKILifecycleInvalid)
	}
	var commit PKIRevocationCommit
	err := r.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		if err := tx.RequirePKILeaseFence(ctx, storage.PKILeaseFence{
			PKIDomainID: mutation.Lease.PKIDomainID, PKIEpoch: mutation.Lease.PKIEpoch,
			InstanceID: mutation.Lease.InstanceID, LeaseTerm: mutation.Lease.LeaseTerm,
			LeaseDeadline: mutation.Lease.LeaseDeadline,
		}); err != nil {
			if errors.Is(err, storage.ErrPKILeaseFence) {
				return ErrPKILeaseNotHeld
			}
			return err
		}
		settings, found, err := tx.GetPKISettingsForUpdate(ctx)
		if err != nil {
			return err
		}
		if !found || settings.PKIDomainID != mutation.Lease.PKIDomainID || settings.PKIEpoch != mutation.Lease.PKIEpoch {
			return ErrPKILeaseNotHeld
		}
		now := r.clock().UTC()
		if mutation.Request.ConfirmationDigest != "" {
			consumed, err := tx.ConsumePKIConfirmationNonce(
				ctx, settings.PKIDomainID, mutation.Request.ConfirmationDigest, mutation.Request.OperatorID,
				mutation.Request.ConfirmationAction, mutation.Request.ConfirmationTargetID, now,
			)
			if err != nil {
				return err
			}
			if !consumed {
				return fmt.Errorf("%w: confirmation nonce is expired, reused, or bound to another action", ErrInvalidArgument)
			}
		}
		identity, certificates, err := tx.RevokePKIIdentityCertificates(ctx, mutation.Request.IdentityID, mutation.Request.Reason, now)
		if err != nil {
			return err
		}
		tokenDisabled := false
		controlSessionTargets := []string(nil)
		if identity.Kind == storage.PKIIdentityKindAgent {
			tokenDisabled, err = tx.DisablePKIStableAgentToken(ctx, identity.AgentID)
			if err != nil {
				return err
			}
			controlSessionTargets = []string{identity.AgentID}
		} else if identity.Kind != storage.PKIIdentityKindListener {
			return fmt.Errorf("%w: revocation identity kind is unsupported", ErrPKILifecycleInvalid)
		}
		authorities, err := tx.ListTrustedPKIAuthoritiesForUpdate(ctx, settings.PKIDomainID)
		if err != nil {
			return err
		}
		trustGenerations := make([]int64, 0, len(authorities))
		for _, authority := range authorities {
			trustGenerations = append(trustGenerations, authority.Generation)
		}
		slices.Sort(trustGenerations)
		revokedSerials := make([]string, 0, len(certificates))
		for _, certificate := range certificates {
			revokedSerials = append(revokedSerials, certificate.SerialHex)
		}
		slices.Sort(revokedSerials)
		stateAfterRevocation, err := tx.LoadPKICanonicalState(ctx)
		if err != nil {
			return err
		}
		allRevokedIdentities := make([]string, 0)
		for _, currentIdentity := range stateAfterRevocation.Identities {
			if currentIdentity.State == storage.PKIIdentityStateRevoked {
				allRevokedIdentities = append(allRevokedIdentities, currentIdentity.ID)
			}
		}
		allRevokedSerials := make([]string, 0)
		for _, currentCertificate := range stateAfterRevocation.Certificates {
			if currentCertificate.Status == storage.PKICertificateStatusRevoked {
				allRevokedSerials = append(allRevokedSerials, currentCertificate.SerialHex)
			}
		}
		slices.Sort(allRevokedIdentities)
		slices.Sort(allRevokedSerials)
		facts := PKIRevocationFacts{
			PKIDomainID: settings.PKIDomainID, PKIEpoch: settings.PKIEpoch,
			PreviousRevision: settings.SecurityRevision, SecurityRevision: settings.SecurityRevision + 1,
			IdentityID: identity.ID, IdentityKind: identity.Kind, RevokedSerials: revokedSerials,
			RevokedIdentityIDs: allRevokedIdentities, AllRevokedSerials: allRevokedSerials,
			ActiveTrustGenerations: trustGenerations,
		}
		snapshot, err := buildSnapshot(ctx, facts)
		if err != nil {
			return err
		}
		if err := tx.SetPKISecurityRevision(ctx, facts.PreviousRevision, facts.SecurityRevision, now); err != nil {
			return err
		}
		canonicalState, err := tx.LoadPKICanonicalState(ctx)
		if err != nil {
			return err
		}
		persistedSnapshot, err := storagePKISecuritySnapshot(canonicalState, snapshot)
		if err != nil {
			return err
		}
		encodedSnapshot, err := json.Marshal(persistedSnapshot)
		if err != nil {
			return err
		}
		if err := tx.SavePKISecuritySnapshot(ctx, storage.PKISecuritySnapshotRow{
			PKIDomainID: settings.PKIDomainID, PKIEpoch: facts.PKIEpoch, SecurityRevision: facts.SecurityRevision,
			SnapshotJSON: string(encodedSnapshot), UpdatedAt: now,
		}); err != nil {
			return err
		}
		generation := snapshot.SignerGeneration
		event := PKIAuditEvent{
			Type: "pki.identity.revoked", OccurredAt: now, Source: mutation.Request.Source,
			OperatorID: mutation.Request.OperatorID, ObjectType: "pki_identity", ObjectID: identity.ID,
			CAGeneration: generation, Result: "success", Reason: mutation.Request.Reason,
			SecurityRevision: facts.SecurityRevision,
			Details:          map[string]any{"agent_id": identity.AgentID, "certificate_count": len(certificates)},
		}
		event.ID = stablePKIAuditEventID(event)
		if err := ValidatePKIAuditEvent(event); err != nil {
			return err
		}
		details, err := json.Marshal(map[string]any{
			"agent_id": identity.AgentID, "security_snapshot": snapshot,
			"control_session_targets": controlSessionTargets, "relay_session_targets": []string{identity.AgentID},
		})
		if err != nil {
			return err
		}
		if err := tx.AppendPKIEvent(ctx, storage.PKIEventRow{
			ID: event.ID, PKIDomainID: settings.PKIDomainID, Type: event.Type, OccurredAt: event.OccurredAt,
			Source: event.Source, OperatorID: event.OperatorID, ObjectType: event.ObjectType, ObjectID: event.ObjectID,
			CAGeneration: &generation, Result: event.Result, Reason: event.Reason,
			SecurityRevision: event.SecurityRevision, DetailsJSON: string(details),
		}); err != nil {
			return err
		}
		jobID := fmt.Sprintf("revoke-%s-r%d", identity.ID, facts.SecurityRevision)
		deadline := mutation.Lease.LeaseDeadline
		if err := tx.CreatePKILifecycleJob(ctx, storage.PKILifecycleJobRow{
			ID: jobID, PKIDomainID: settings.PKIDomainID, TargetType: "identity", TargetID: identity.ID,
			Kind: "revoke", Phase: "convergence", State: storage.PKILifecycleJobStateRunning,
			OperationID: jobID, IdempotencyKey: jobID, LeaseOwner: mutation.Lease.InstanceID,
			LeaseDeadline: &deadline, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		commit = PKIRevocationCommit{
			Facts: facts, Snapshot: snapshot, IdentityRevoked: true, CertificatesRevoked: len(certificates),
			ControlTokenDisabled: tokenDisabled, ControlSessionTargets: controlSessionTargets,
			RelaySessionTargets: []string{identity.AgentID}, Lease: mutation.Lease, Event: event,
			ConvergenceJobID: jobID,
		}
		if err := tx.RequirePKILeaseFence(ctx, storage.PKILeaseFence{
			PKIDomainID: mutation.Lease.PKIDomainID, PKIEpoch: mutation.Lease.PKIEpoch,
			InstanceID: mutation.Lease.InstanceID, LeaseTerm: mutation.Lease.LeaseTerm,
			LeaseDeadline: mutation.Lease.LeaseDeadline,
		}); err != nil {
			if errors.Is(err, storage.ErrPKILeaseFence) {
				return ErrPKILeaseNotHeld
			}
			return err
		}
		return nil
	})
	return commit, err
}

func (r *GormPKIRevocationRepository) RecordPKIRevocationConvergence(
	ctx context.Context,
	commit PKIRevocationCommit,
	convergenceErr error,
) error {
	jobID := strings.TrimSpace(commit.ConvergenceJobID)
	if jobID == "" {
		return fmt.Errorf("%w: revocation convergence job ID is required", ErrPKILifecycleInvalid)
	}
	return r.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		previous, found, err := tx.GetPKILifecycleJobForUpdate(ctx, jobID)
		if err != nil {
			return err
		}
		if !found || previous.Kind != "revoke" || previous.TargetID != commit.Facts.IdentityID {
			return fmt.Errorf("%w: revocation convergence job is missing", ErrPKILifecycleInvalid)
		}
		// A completed convergence is an irreversible terminal fact. A slower
		// attempt must never downgrade it after a newer retry has succeeded.
		if previous.State == storage.PKILifecycleJobStateSucceeded {
			return nil
		}
		now := r.clock().UTC()
		next := previous
		next.UpdatedAt = now
		next.Attempt++
		if convergenceErr == nil {
			next.Phase = "completed"
			next.State = storage.PKILifecycleJobStateSucceeded
			next.NextAttemptAt = nil
			next.LastError = ""
		} else {
			next.Phase = "retry_pending"
			next.State = storage.PKILifecycleJobStatePending
			retryAt := now.Add(10 * time.Second)
			next.NextAttemptAt = &retryAt
			next.LastError = strings.TrimSpace(convergenceErr.Error())
			if len(next.LastError) > 1024 {
				next.LastError = next.LastError[:1024]
			}
		}
		return tx.UpdatePKILifecycleJob(ctx, previous, next)
	})
}

type PKIVaultSecuritySnapshotSignerOptions struct {
	StateSource pkiCanonicalStateSource
	Signer      PKIEnrollmentAuthoritySigner
	Random      io.Reader
}

type PKIVaultSecuritySnapshotSigner struct {
	stateSource pkiCanonicalStateSource
	signer      PKIEnrollmentAuthoritySigner
	random      io.Reader
}

// PKIProjectedSecuritySnapshotSigner signs a trust projection that is about to
// become canonical in the same fenced transaction. It is used for CA cutover
// and emergency replacement, where the future active signer cannot yet be
// selected by reading the pre-commit canonical state.
type PKIProjectedSecuritySnapshotSigner interface {
	SignPKIProjectedSecuritySnapshot(context.Context, PKIUnsignedSecuritySnapshot, storage.PKIAuthorityRow) (PKISignedSecuritySnapshot, error)
}

func NewPKIVaultSecuritySnapshotSigner(options PKIVaultSecuritySnapshotSignerOptions) (*PKIVaultSecuritySnapshotSigner, error) {
	if options.StateSource == nil || options.Signer == nil {
		return nil, fmt.Errorf("%w: snapshot state source and signer are required", ErrPKILifecycleInvalid)
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	return &PKIVaultSecuritySnapshotSigner{stateSource: options.StateSource, signer: options.Signer, random: options.Random}, nil
}

func (s *PKIVaultSecuritySnapshotSigner) SignPKISecuritySnapshot(ctx context.Context, unsigned PKIUnsignedSecuritySnapshot) (PKISignedSecuritySnapshot, error) {
	state, err := s.stateSource.LoadPKICanonicalState(ctx)
	if err != nil {
		return PKISignedSecuritySnapshot{}, err
	}
	var authority *storage.PKIAuthorityRow
	for index := range state.Authorities {
		candidate := &state.Authorities[index]
		if candidate.Status == "active" && (authority == nil || candidate.Generation > authority.Generation) {
			authority = candidate
		}
	}
	if authority == nil || authority.PKIDomainID != unsigned.PKIDomainID {
		return PKISignedSecuritySnapshot{}, fmt.Errorf("%w: active snapshot signing authority is unavailable", ErrPKILifecycleInvalid)
	}
	trustRoots, err := canonicalPKISecurityTrustRoots(state, unsigned.TrustGenerations)
	if err != nil {
		return PKISignedSecuritySnapshot{}, err
	}
	unsigned.TrustRoots = trustRoots
	privateSigner, err := s.signer.LoadSigner(ctx, *authority)
	if err != nil {
		return PKISignedSecuritySnapshot{}, err
	}
	payload, err := marshalPKIUnsignedSecuritySnapshot(unsigned)
	if err != nil {
		return PKISignedSecuritySnapshot{}, err
	}
	digest := sha256.Sum256(payload)
	signature, err := privateSigner.Sign(s.random, digest[:], crypto.SHA256)
	if err != nil {
		return PKISignedSecuritySnapshot{}, err
	}
	return PKISignedSecuritySnapshot{
		PKIUnsignedSecuritySnapshot: unsigned, SignerGeneration: authority.Generation, Signature: signature,
	}, nil
}

func (s *PKIVaultSecuritySnapshotSigner) SignPKIProjectedSecuritySnapshot(
	ctx context.Context,
	unsigned PKIUnsignedSecuritySnapshot,
	authority storage.PKIAuthorityRow,
) (PKISignedSecuritySnapshot, error) {
	if strings.TrimSpace(unsigned.PKIDomainID) == "" || authority.PKIDomainID != unsigned.PKIDomainID ||
		authority.Generation <= 0 || authority.Status != "active" || unsigned.Version.Version.PKIEpoch < 0 ||
		unsigned.Version.Version.SecurityRevision < 0 || !unsigned.Version.Full || unsigned.IssuedAt.IsZero() ||
		!validPKISecurityTrustRootDescriptors(unsigned.TrustGenerations, unsigned.TrustRoots) {
		return PKISignedSecuritySnapshot{}, fmt.Errorf("%w: projected security snapshot is invalid", ErrPKILifecycleInvalid)
	}
	signerTrusted := false
	for _, root := range unsigned.TrustRoots {
		if root.Generation == authority.Generation && root.AuthorityID == authority.ID && root.Status == "active" &&
			strings.EqualFold(root.FingerprintSHA256, authority.FingerprintSHA256) && root.NotBefore.Equal(authority.NotBefore) &&
			root.NotAfter.Equal(authority.NotAfter) {
			signerTrusted = true
		}
	}
	if !signerTrusted {
		return PKISignedSecuritySnapshot{}, fmt.Errorf("%w: projected signer is not the active trust root", ErrPKILifecycleInvalid)
	}
	certificate, err := parsePKIAuthorityCertificate(authority.CertificatePEM)
	if err != nil {
		return PKISignedSecuritySnapshot{}, err
	}
	fingerprint := sha256.Sum256(certificate.Raw)
	if !strings.EqualFold(authority.FingerprintSHA256, fmt.Sprintf("%x", fingerprint)) {
		return PKISignedSecuritySnapshot{}, fmt.Errorf("%w: projected signer certificate is inconsistent", ErrPKILifecycleInvalid)
	}
	privateSigner, err := s.signer.LoadSigner(ctx, authority)
	if err != nil {
		return PKISignedSecuritySnapshot{}, err
	}
	publicDER, err := x509.MarshalPKIXPublicKey(privateSigner.Public())
	if err != nil {
		return PKISignedSecuritySnapshot{}, err
	}
	certificatePublicDER, err := x509.MarshalPKIXPublicKey(certificate.PublicKey)
	if err != nil || !slices.Equal(publicDER, certificatePublicDER) {
		return PKISignedSecuritySnapshot{}, fmt.Errorf("%w: projected signer key does not match its certificate", ErrPKILifecycleInvalid)
	}
	payload, err := marshalPKIUnsignedSecuritySnapshot(unsigned)
	if err != nil {
		return PKISignedSecuritySnapshot{}, err
	}
	digest := sha256.Sum256(payload)
	signature, err := privateSigner.Sign(s.random, digest[:], crypto.SHA256)
	if err != nil {
		return PKISignedSecuritySnapshot{}, err
	}
	return PKISignedSecuritySnapshot{
		PKIUnsignedSecuritySnapshot: unsigned,
		SignerGeneration:            authority.Generation,
		Signature:                   signature,
	}, nil
}

func marshalPKIUnsignedSecuritySnapshot(snapshot PKIUnsignedSecuritySnapshot) ([]byte, error) {
	canonical := struct {
		PKIDomainID        string                           `json:"pki_domain_id"`
		Version            PKISecuritySnapshotVersion       `json:"version"`
		IssuedAt           time.Time                        `json:"issued_at"`
		TrustGenerations   []int64                          `json:"trust_generations"`
		TrustRoots         []PKISecurityTrustRootDescriptor `json:"trust_roots"`
		RevokedIdentityIDs []string                         `json:"revoked_identity_ids"`
		RevokedSerials     []string                         `json:"revoked_serials"`
	}{
		PKIDomainID: snapshot.PKIDomainID, Version: snapshot.Version, IssuedAt: snapshot.IssuedAt.UTC(),
		TrustGenerations:   slices.Clone(snapshot.TrustGenerations),
		TrustRoots:         slices.Clone(snapshot.TrustRoots),
		RevokedIdentityIDs: slices.Clone(snapshot.RevokedIdentityIDs), RevokedSerials: slices.Clone(snapshot.RevokedSerials),
	}
	slices.Sort(canonical.TrustGenerations)
	sort.Slice(canonical.TrustRoots, func(i, j int) bool { return canonical.TrustRoots[i].Generation < canonical.TrustRoots[j].Generation })
	slices.Sort(canonical.RevokedIdentityIDs)
	slices.Sort(canonical.RevokedSerials)
	return json.Marshal(canonical)
}

func canonicalPKISecurityTrustRoots(state storage.PKICanonicalState, generations []int64) ([]PKISecurityTrustRootDescriptor, error) {
	wanted := make(map[int64]struct{}, len(generations))
	for _, generation := range generations {
		if generation <= 0 {
			return nil, fmt.Errorf("%w: trust generation is invalid", ErrPKILifecycleInvalid)
		}
		wanted[generation] = struct{}{}
	}
	if len(wanted) != len(generations) || len(wanted) == 0 {
		return nil, fmt.Errorf("%w: trust generations must be unique and non-empty", ErrPKILifecycleInvalid)
	}
	roots := make([]PKISecurityTrustRootDescriptor, 0, len(wanted))
	for _, authority := range state.Authorities {
		if _, ok := wanted[authority.Generation]; !ok {
			continue
		}
		certificate, err := parsePKIAuthorityCertificate(authority.CertificatePEM)
		if err != nil {
			return nil, err
		}
		fingerprint := sha256.Sum256(certificate.Raw)
		fingerprintHex := fmt.Sprintf("%x", fingerprint)
		if !strings.EqualFold(fingerprintHex, authority.FingerprintSHA256) {
			return nil, fmt.Errorf("%w: trust-root certificate fingerprint is inconsistent", ErrPKILifecycleInvalid)
		}
		roots = append(roots, PKISecurityTrustRootDescriptor{
			AuthorityID: authority.ID, Generation: authority.Generation, Status: authority.Status,
			FingerprintSHA256: fingerprintHex, NotBefore: authority.NotBefore.UTC(), NotAfter: authority.NotAfter.UTC(),
		})
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].Generation < roots[j].Generation })
	if len(roots) != len(generations) || !validPKISecurityTrustRootDescriptors(generations, roots) {
		return nil, fmt.Errorf("%w: canonical trust-root set is incomplete", ErrPKILifecycleInvalid)
	}
	return roots, nil
}

type PKISecurityTaskPublisher struct {
	stateSource pkiCanonicalStateSource
	tasks       *TaskService
}

func NewPKISecurityTaskPublisher(stateSource pkiCanonicalStateSource, tasks *TaskService) (*PKISecurityTaskPublisher, error) {
	if stateSource == nil || tasks == nil {
		return nil, fmt.Errorf("%w: security publisher dependencies are required", ErrPKILifecycleInvalid)
	}
	return &PKISecurityTaskPublisher{stateSource: stateSource, tasks: tasks}, nil
}

func (p *PKISecurityTaskPublisher) PublishPKISecuritySnapshot(ctx context.Context, signed PKISignedSecuritySnapshot, excludedAgentIDs []string) error {
	state, err := p.stateSource.LoadPKICanonicalState(ctx)
	if err != nil {
		return err
	}
	snapshot, err := storagePKISecuritySnapshot(state, signed)
	if err != nil {
		return err
	}
	return p.tasks.PublishPKISecuritySnapshotExcluding(ctx, snapshot, excludedAgentIDs)
}

type PKITaskSessionCloser struct {
	tasks *TaskService
}

func NewPKITaskSessionCloser(tasks *TaskService) (*PKITaskSessionCloser, error) {
	if tasks == nil {
		return nil, fmt.Errorf("%w: task service is required", ErrPKILifecycleInvalid)
	}
	return &PKITaskSessionCloser{tasks: tasks}, nil
}

func (c *PKITaskSessionCloser) CloseRevokedPKISessions(ctx context.Context, commit PKIRevocationCommit) error {
	var closeErr error
	seen := make(map[string]struct{})
	// TaskService owns only authenticated control streams. Relay session targets
	// converge through the relay data plane and must never fence a still-valid
	// Agent control token.
	for _, agentID := range commit.ControlSessionTargets {
		agentID = strings.TrimSpace(agentID)
		if agentID == "" {
			continue
		}
		if _, duplicate := seen[agentID]; duplicate {
			continue
		}
		seen[agentID] = struct{}{}
		if err := ctx.Err(); err != nil {
			return errors.Join(closeErr, err)
		}
		closeErr = errors.Join(closeErr, c.tasks.CloseAgentSessionsContext(ctx, agentID))
	}
	return closeErr
}

func storagePKISecuritySnapshot(state storage.PKICanonicalState, signed PKISignedSecuritySnapshot) (storage.PKISecuritySnapshot, error) {
	if state.Settings == nil || state.Settings.PKIDomainID != signed.PKIDomainID {
		return storage.PKISecuritySnapshot{}, fmt.Errorf("%w: snapshot PKI domain is not canonical", ErrPKILifecycleInvalid)
	}
	if !validPKISecurityTrustRootDescriptors(signed.TrustGenerations, signed.TrustRoots) {
		return storage.PKISecuritySnapshot{}, fmt.Errorf("%w: signed trust-root descriptors are invalid", ErrPKILifecycleInvalid)
	}
	wanted := make(map[int64]PKISecurityTrustRootDescriptor, len(signed.TrustRoots))
	for _, descriptor := range signed.TrustRoots {
		wanted[descriptor.Generation] = descriptor
	}
	roots := make([]storage.PKITrustRoot, 0, len(wanted))
	for _, authority := range state.Authorities {
		descriptor, ok := wanted[authority.Generation]
		if !ok {
			continue
		}
		if descriptor.AuthorityID != authority.ID || descriptor.Status != authority.Status ||
			!strings.EqualFold(descriptor.FingerprintSHA256, authority.FingerprintSHA256) ||
			!descriptor.NotBefore.Equal(authority.NotBefore) || !descriptor.NotAfter.Equal(authority.NotAfter) {
			return storage.PKISecuritySnapshot{}, fmt.Errorf("%w: signed trust root does not match canonical authority", ErrPKILifecycleInvalid)
		}
		certificate, err := parsePKIAuthorityCertificate(authority.CertificatePEM)
		if err != nil {
			return storage.PKISecuritySnapshot{}, err
		}
		fingerprint := sha256.Sum256(certificate.Raw)
		if !strings.EqualFold(descriptor.FingerprintSHA256, fmt.Sprintf("%x", fingerprint)) {
			return storage.PKISecuritySnapshot{}, fmt.Errorf("%w: signed trust-root certificate was substituted", ErrPKILifecycleInvalid)
		}
		roots = append(roots, storage.PKITrustRoot{
			AuthorityID: authority.ID, Generation: authority.Generation, Status: authority.Status,
			CertificatePEM: authority.CertificatePEM, FingerprintSHA256: authority.FingerprintSHA256,
			NotBefore: authority.NotBefore, NotAfter: authority.NotAfter,
		})
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].Generation < roots[j].Generation })
	if len(roots) != len(wanted) {
		return storage.PKISecuritySnapshot{}, fmt.Errorf("%w: signed trust generation is missing", ErrPKILifecycleInvalid)
	}
	return storage.PKISecuritySnapshot{
		PKIDomainID: signed.PKIDomainID, PKIEpoch: signed.Version.Version.PKIEpoch,
		SecurityRevision: signed.Version.Version.SecurityRevision, Full: signed.Version.Full,
		IssuedAt: signed.IssuedAt, TrustRoots: roots,
		RevokedIdentityIDs: slices.Clone(signed.RevokedIdentityIDs), RevokedSerials: slices.Clone(signed.RevokedSerials),
		SignerGeneration: signed.SignerGeneration, Signature: slices.Clone(signed.Signature),
	}, nil
}
