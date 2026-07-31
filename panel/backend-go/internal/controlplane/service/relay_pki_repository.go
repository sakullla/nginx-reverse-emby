package service

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/sha256"
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
		identity, certificates, err := tx.RevokePKIIdentityCertificates(ctx, mutation.Request.IdentityID, mutation.Request.Reason, now)
		if err != nil {
			return err
		}
		if identity.Kind != storage.PKIIdentityKindAgent {
			return fmt.Errorf("%w: only an agent identity can disable a control token", ErrPKILifecycleInvalid)
		}
		tokenDisabled, err := tx.DisablePKIStableAgentToken(ctx, identity.AgentID)
		if err != nil {
			return err
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
		facts := PKIRevocationFacts{
			PKIDomainID: settings.PKIDomainID, PKIEpoch: settings.PKIEpoch,
			PreviousRevision: settings.SecurityRevision, SecurityRevision: settings.SecurityRevision + 1,
			IdentityID: identity.ID, RevokedSerials: revokedSerials, ActiveTrustGenerations: trustGenerations,
		}
		snapshot, err := buildSnapshot(ctx, facts)
		if err != nil {
			return err
		}
		if err := tx.SetPKISecurityRevision(ctx, facts.PreviousRevision, facts.SecurityRevision, now); err != nil {
			return err
		}
		generation := int64(0)
		if len(trustGenerations) != 0 {
			generation = trustGenerations[len(trustGenerations)-1]
		}
		event := PKIAuditEvent{
			Type: "pki.identity.revoked", OccurredAt: now, Source: mutation.Request.Source,
			OperatorID: mutation.Request.OperatorID, ObjectType: "pki_identity", ObjectID: identity.ID,
			CAGeneration: generation, Result: "success", Reason: mutation.Request.Reason,
			SecurityRevision: facts.SecurityRevision,
			Details:          map[string]string{"agent_id": identity.AgentID, "certificate_count": fmt.Sprintf("%d", len(certificates))},
		}
		event.ID = stablePKIAuditEventID(event)
		if err := ValidatePKIAuditEvent(event); err != nil {
			return err
		}
		details, err := json.Marshal(map[string]any{
			"agent_id": identity.AgentID, "security_snapshot": snapshot,
			"control_session_targets": []string{identity.AgentID}, "relay_session_targets": []string{identity.AgentID},
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
			Kind: "revoke", Phase: "completed", State: storage.PKILifecycleJobStateSucceeded,
			OperationID: jobID, IdempotencyKey: jobID, LeaseOwner: mutation.Lease.InstanceID,
			LeaseDeadline: &deadline, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		commit = PKIRevocationCommit{
			Facts: facts, Snapshot: snapshot, IdentityRevoked: true, CertificatesRevoked: len(certificates),
			ControlTokenDisabled: tokenDisabled, ControlSessionTargets: []string{identity.AgentID},
			RelaySessionTargets: []string{identity.AgentID}, Lease: mutation.Lease, Event: event,
		}
		return nil
	})
	return commit, err
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

func (p *PKISecurityTaskPublisher) PublishPKISecuritySnapshot(ctx context.Context, signed PKISignedSecuritySnapshot) error {
	state, err := p.stateSource.LoadPKICanonicalState(ctx)
	if err != nil {
		return err
	}
	snapshot, err := storagePKISecuritySnapshot(state, signed)
	if err != nil {
		return err
	}
	return p.tasks.PublishPKISecuritySnapshot(ctx, snapshot, "")
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
	for _, agentID := range append(slices.Clone(commit.ControlSessionTargets), commit.RelaySessionTargets...) {
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
		closeErr = errors.Join(closeErr, c.tasks.CloseAgentSessions(agentID))
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
