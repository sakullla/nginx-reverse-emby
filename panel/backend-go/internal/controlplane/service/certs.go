package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var ErrCertificateNotFound = errors.New("certificate not found")

const systemRelayCATag = "system:relay-ca"
const systemTag = "system"
const autoRelayListenerTag = "auto:relay-listener"
const relayCADomainIdentity = "__relay-ca.internal"

type ManagedCertificateACMEInfo struct {
	MainDomain string `json:"Main_Domain"`
	KeyLength  string `json:"KeyLength"`
	SANDomains string `json:"SAN_Domains"`
	Profile    string `json:"Profile"`
	CA         string `json:"CA"`
	Created    string `json:"Created"`
	Renew      string `json:"Renew"`
}

type ManagedCertificateAgentReport struct {
	Status       string                     `json:"status"`
	LastIssueAt  string                     `json:"last_issue_at"`
	LastError    string                     `json:"last_error"`
	MaterialHash string                     `json:"material_hash"`
	NotAfter     string                     `json:"not_after,omitempty"`
	ACMEInfo     ManagedCertificateACMEInfo `json:"acme_info"`
	UpdatedAt    string                     `json:"updated_at"`
}

type ManagedCertificateHeartbeatReport struct {
	ID           int                        `json:"id"`
	Domain       string                     `json:"domain"`
	Status       string                     `json:"status"`
	LastIssueAt  string                     `json:"last_issue_at"`
	LastError    string                     `json:"last_error"`
	MaterialHash string                     `json:"material_hash"`
	NotAfter     string                     `json:"not_after,omitempty"`
	ACMEInfo     ManagedCertificateACMEInfo `json:"acme_info"`
	UpdatedAt    string                     `json:"updated_at"`
}

type ManagedCertificate struct {
	ID              int                                      `json:"id"`
	Domain          string                                   `json:"domain"`
	Enabled         bool                                     `json:"enabled"`
	Scope           string                                   `json:"scope"`
	IssuerMode      string                                   `json:"issuer_mode"`
	TargetAgentIDs  []string                                 `json:"target_agent_ids"`
	AgentID         string                                   `json:"agent_id,omitempty"`
	AgentName       string                                   `json:"agent_name,omitempty"`
	Status          string                                   `json:"status"`
	LastIssueAt     string                                   `json:"last_issue_at"`
	LastError       string                                   `json:"last_error"`
	MaterialHash    string                                   `json:"material_hash"`
	AgentReports    map[string]ManagedCertificateAgentReport `json:"agent_reports"`
	ACMEInfo        ManagedCertificateACMEInfo               `json:"acme_info"`
	Tags            []string                                 `json:"tags"`
	Usage           string                                   `json:"usage"`
	CertificateType string                                   `json:"certificate_type"`
	SelfSigned      bool                                     `json:"self_signed"`
	Revision        int                                      `json:"revision"`
	NextRetryAtUnix int64                                    `json:"next_retry_at_unix"`
	RetryCount      int                                      `json:"retry_count"`
	BackoffClass    string                                   `json:"backoff_class"`
	NotAfter        string                                   `json:"not_after"`
}

type ManagedCertificateInput struct {
	ID              *int                                      `json:"id,omitempty"`
	Domain          *string                                   `json:"domain,omitempty"`
	Enabled         *bool                                     `json:"enabled,omitempty"`
	Scope           *string                                   `json:"scope,omitempty"`
	IssuerMode      *string                                   `json:"issuer_mode,omitempty"`
	TargetAgentIDs  *[]string                                 `json:"target_agent_ids,omitempty"`
	Status          *string                                   `json:"status,omitempty"`
	LastIssueAt     *string                                   `json:"last_issue_at,omitempty"`
	LastError       *string                                   `json:"last_error,omitempty"`
	MaterialHash    *string                                   `json:"material_hash,omitempty"`
	AgentReports    *map[string]ManagedCertificateAgentReport `json:"agent_reports,omitempty"`
	ACMEInfo        *ManagedCertificateACMEInfo               `json:"acme_info,omitempty"`
	Tags            *[]string                                 `json:"tags,omitempty"`
	Usage           *string                                   `json:"usage,omitempty"`
	CertificateType *string                                   `json:"certificate_type,omitempty"`
	CertificatePEM  *string                                   `json:"certificate_pem,omitempty"`
	PrivateKeyPEM   *string                                   `json:"private_key_pem,omitempty"`
	CAPEM           *string                                   `json:"ca_pem,omitempty"`
	SelfSigned      *bool                                     `json:"self_signed,omitempty"`
	NotAfter        *string                                   `json:"not_after,omitempty"`
}

type managedCertificateIntent struct {
	Input                ManagedCertificateInput `json:"input"`
	CertificatePEMSHA256 *string                 `json:"certificate_pem_sha256,omitempty"`
	PrivateKeyPEMSHA256  *string                 `json:"private_key_pem_sha256,omitempty"`
	CAPEMSHA256          *string                 `json:"ca_pem_sha256,omitempty"`
}

func managedCertificateMutationIntent(input ManagedCertificateInput) managedCertificateIntent {
	intent := managedCertificateIntent{
		Input:                input,
		CertificatePEMSHA256: mutationSecretDigestPointer(input.CertificatePEM),
		PrivateKeyPEMSHA256:  mutationSecretDigestPointer(input.PrivateKeyPEM),
		CAPEMSHA256:          mutationSecretDigestPointer(input.CAPEM),
	}
	intent.Input.CertificatePEM = nil
	intent.Input.PrivateKeyPEM = nil
	intent.Input.CAPEM = nil
	return intent
}

func mutationSecretDigestPointer(value *string) *string {
	if value == nil {
		return nil
	}
	digest := mutationSecretDigest(*value)
	return &digest
}

func mutationSecretDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", digest[:])
}

type managedCertificateGeneration struct {
	revision    int
	fingerprint string
}

func managedCertificateGenerationFor(cert ManagedCertificate) managedCertificateGeneration {
	canonical := cert
	canonical.TargetAgentIDs = append([]string(nil), cert.TargetAgentIDs...)
	sort.Strings(canonical.TargetAgentIDs)
	payload, _ := json.Marshal(canonical)
	digest := sha256.Sum256(payload)
	return managedCertificateGeneration{revision: cert.Revision, fingerprint: fmt.Sprintf("%x", digest[:])}
}

func (generation managedCertificateGeneration) Matches(cert ManagedCertificate) bool {
	current := managedCertificateGenerationFor(cert)
	return generation.revision == current.revision && generation.fingerprint == current.fingerprint
}

func (s *certificateService) loadManagedCertificateGeneration(
	ctx context.Context,
	certificateID int,
	generation managedCertificateGeneration,
) ([]storage.ManagedCertificateRow, ManagedCertificate, int, bool, error) {
	rows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return nil, ManagedCertificate{}, -1, false, err
	}
	current, index, found := findManagedCertificateByID(rows, certificateID)
	if !found || !generation.Matches(current) {
		return rows, current, index, false, nil
	}
	return rows, current, index, true, nil
}

type certificateService struct {
	cfg                     config.Config
	store                   storage.Store
	now                     func() time.Time
	renewalIssuer           managedCertificateRenewalIssuer
	localApplyTrigger       func(context.Context) error
	mutationExecutor        *revision.Executor
	generationStore         storage.ManagedCertificateGenerationStore
	generationRecoveryStore storage.ManagedCertificateGenerationStore
	revisionMutation        bool
	revisionNumbers         map[string]int64
	postCommitActions       *[]func()
	rollbackActions         *[]func() error
}

type localManagedCertificateSyncStore interface {
	LoadLocalSnapshot(context.Context, string) (storage.Snapshot, error)
	SaveLocalRuntimeState(context.Context, string, storage.RuntimeState) error
}

func NewCertificateService(cfg config.Config, store storage.Store) *certificateService {
	return newCertificateServiceWithRenewal(cfg, store, nil)
}

func newCertificateServiceWithRenewal(cfg config.Config, store storage.Store, issuer managedCertificateRenewalIssuer) *certificateService {
	generationStore, _ := store.(storage.ManagedCertificateGenerationStore)
	return &certificateService{
		cfg:                     cfg,
		store:                   store,
		now:                     time.Now,
		renewalIssuer:           issuer,
		mutationExecutor:        newManagedCertificateMutationExecutor(cfg, store),
		generationStore:         generationStore,
		generationRecoveryStore: generationStore,
	}
}

func (s *certificateService) certificateRevisionTransactionService(
	tx *storage.GormStore,
	revisions map[string]int64,
	postCommitActions *[]func(),
	rollbackActions *[]func() error,
) *certificateService {
	generationStore, _ := any(tx).(storage.ManagedCertificateGenerationStore)
	return &certificateService{
		cfg: s.cfg, store: tx, now: s.now, renewalIssuer: s.renewalIssuer,
		generationStore: generationStore, generationRecoveryStore: s.generationRecoveryStore,
		revisionMutation: true, revisionNumbers: revisions,
		postCommitActions: postCommitActions, rollbackActions: rollbackActions,
	}
}

func (s *certificateService) withManagedCertificateDomainLock(ctx context.Context, domain string, mutate func(context.Context) error) error {
	locker, ok := s.store.(storage.ManagedCertificateDomainLocker)
	if !ok {
		return mutate(ctx)
	}
	return locker.WithManagedCertificateDomainLock(ctx, domain, mutate)
}

func certificateMutationRollbackError(mutationErr error, actions []func() error) error {
	var firstRollbackErr error
	for index := len(actions) - 1; index >= 0; index-- {
		if actions[index] == nil {
			continue
		}
		if rollbackErr := actions[index](); rollbackErr != nil && firstRollbackErr == nil {
			firstRollbackErr = rollbackErr
		}
	}
	if firstRollbackErr != nil {
		return errors.Join(mutationErr, fmt.Errorf("certificate material restore failed: %w", firstRollbackErr))
	}
	return mutationErr
}

func (s *certificateService) runAfterRevisionCommit(action func()) {
	if action == nil {
		return
	}
	if s.revisionMutation && s.postCommitActions != nil {
		*s.postCommitActions = append(*s.postCommitActions, action)
		return
	}
	action()
}

func (s *certificateService) cleanupManagedCertificateMaterialAfterMutation(ctx context.Context, previous []storage.ManagedCertificateRow, next []storage.ManagedCertificateRow) {
	s.runAfterRevisionCommit(func() {
		cleanupManagedCertificateMaterialBestEffort(ctx, s.store, previous, next)
	})
}

type managedCertificateMaterialRestoreError struct {
	writeErr   error
	restoreErr error
}

func (e *managedCertificateMaterialRestoreError) Error() string {
	if e == nil {
		return ""
	}
	if e.writeErr == nil {
		return e.restoreErr.Error()
	}
	if e.restoreErr == nil {
		return e.writeErr.Error()
	}
	return fmt.Sprintf("%v (restore failed: %v)", e.writeErr, e.restoreErr)
}

func (e *managedCertificateMaterialRestoreError) Unwrap() []error {
	if e == nil {
		return nil
	}
	unwrapped := make([]error, 0, 2)
	if e.writeErr != nil {
		unwrapped = append(unwrapped, e.writeErr)
	}
	if e.restoreErr != nil {
		unwrapped = append(unwrapped, e.restoreErr)
	}
	return unwrapped
}

func managedCertificateMaterialRestoreFailed(err error) bool {
	var restoreErr *managedCertificateMaterialRestoreError
	return errors.As(err, &restoreErr)
}

func typedManagedCertificateMaterialRestoreError(err error) error {
	if err == nil || managedCertificateMaterialRestoreFailed(err) {
		return err
	}
	return &managedCertificateMaterialRestoreError{restoreErr: err}
}

const managedCertificateMaterialCleanupTimeout = 5 * time.Second

var managedCertificateMaterialLocksMu sync.Mutex
var managedCertificateMaterialLocks = make(map[string]*issuanceLockEntry)

func managedCertificateMaterialLock(domain string) func() {
	key := strings.ToLower(strings.TrimSpace(domain))
	managedCertificateMaterialLocksMu.Lock()
	entry, ok := managedCertificateMaterialLocks[key]
	if !ok {
		entry = &issuanceLockEntry{}
		managedCertificateMaterialLocks[key] = entry
	}
	entry.waiters++
	managedCertificateMaterialLocksMu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		managedCertificateMaterialLocksMu.Lock()
		entry.waiters--
		if entry.waiters == 0 {
			delete(managedCertificateMaterialLocks, key)
		}
		managedCertificateMaterialLocksMu.Unlock()
	}
}

func managedCertificateMaterialCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), managedCertificateMaterialCleanupTimeout)
}

func managedCertificateMaterialEqual(left, right storage.ManagedCertificateBundle) bool {
	return strings.TrimSpace(left.Domain) == strings.TrimSpace(right.Domain) &&
		left.CertPEM == right.CertPEM && left.KeyPEM == right.KeyPEM
}

func restoreManagedCertificateMaterialCAS(
	ctx context.Context,
	store storage.Store,
	domain string,
	previous storage.ManagedCertificateBundle,
	previousFound bool,
	written storage.ManagedCertificateBundle,
) error {
	cleanupCtx, cancel := managedCertificateMaterialCleanupContext(ctx)
	defer cancel()
	current, currentFound, err := store.LoadManagedCertificateMaterial(cleanupCtx, domain)
	if err != nil {
		return typedManagedCertificateMaterialRestoreError(err)
	}
	if previousFound && currentFound && managedCertificateMaterialEqual(current, previous) {
		return nil
	}
	if !previousFound && !currentFound {
		return nil
	}
	if !currentFound || !managedCertificateMaterialEqual(current, written) {
		return typedManagedCertificateMaterialRestoreError(
			fmt.Errorf("certificate material for %q changed before rollback", domain),
		)
	}
	if previousFound {
		return typedManagedCertificateMaterialRestoreError(
			store.SaveManagedCertificateMaterial(cleanupCtx, domain, previous),
		)
	}
	return typedManagedCertificateMaterialRestoreError(
		store.CleanupManagedCertificateMaterial(
			cleanupCtx,
			[]storage.ManagedCertificateRow{{Domain: domain}},
			nil,
		),
	)
}

func saveManagedCertificateMaterialWithRollback(
	ctx context.Context,
	store storage.Store,
	domain string,
	bundle storage.ManagedCertificateBundle,
) (func() error, error) {
	writeRestore, _, err := saveManagedCertificateMaterialWithRollbackStores(ctx, store, store, domain, bundle)
	return writeRestore, err
}

func saveManagedCertificateMaterialWithRollbackStores(
	ctx context.Context,
	writeStore storage.Store,
	recoveryStore storage.Store,
	domain string,
	bundle storage.ManagedCertificateBundle,
) (func() error, func() error, error) {
	domain = strings.TrimSpace(domain)
	bundle.Domain = domain
	unlock := managedCertificateMaterialLock(domain)
	previous, previousFound, err := writeStore.LoadManagedCertificateMaterial(ctx, domain)
	if err != nil {
		unlock()
		return nil, nil, err
	}
	restoreWithStore := func(store storage.Store) func() error {
		var once sync.Once
		var restoreErr error
		return func() error {
			once.Do(func() {
				unlock := managedCertificateMaterialLock(domain)
				defer unlock()
				restoreErr = restoreManagedCertificateMaterialCAS(ctx, store, domain, previous, previousFound, bundle)
			})
			return restoreErr
		}
	}
	writeRestore := restoreWithStore(writeStore)
	recoveryRestore := restoreWithStore(recoveryStore)
	err = writeStore.SaveManagedCertificateMaterial(ctx, domain, bundle)
	unlock()
	if err != nil {
		if rollbackErr := writeRestore(); rollbackErr != nil {
			return nil, nil, &managedCertificateMaterialRestoreError{writeErr: err, restoreErr: rollbackErr}
		}
		return nil, nil, err
	}
	return writeRestore, recoveryRestore, nil
}

func stageManagedCertificateMaterialWithRollback(
	ctx context.Context,
	store storage.Store,
	domain string,
	bundle storage.ManagedCertificateBundle,
) (func(), func() error, error) {
	domain = strings.TrimSpace(domain)
	bundle.Domain = domain
	unlock := managedCertificateMaterialLock(domain)
	previous, previousFound, err := store.LoadManagedCertificateMaterial(ctx, domain)
	if err != nil {
		unlock()
		return nil, nil, err
	}
	var once sync.Once
	var restoreErr error
	commit := func() {
		once.Do(unlock)
	}
	rollback := func() error {
		once.Do(func() {
			defer unlock()
			restoreErr = restoreManagedCertificateMaterialCAS(ctx, store, domain, previous, previousFound, bundle)
		})
		return restoreErr
	}
	if err := store.SaveManagedCertificateMaterial(ctx, domain, bundle); err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			return nil, nil, &managedCertificateMaterialRestoreError{writeErr: err, restoreErr: rollbackErr}
		}
		return nil, nil, err
	}
	return commit, rollback, nil
}

func (s *certificateService) saveManagedCertificateMaterial(ctx context.Context, domain string, bundle storage.ManagedCertificateBundle) error {
	commit, restore, err := stageManagedCertificateMaterialWithRollback(ctx, s.store, domain, bundle)
	if err != nil {
		return err
	}
	if s.revisionMutation && s.postCommitActions != nil && s.rollbackActions != nil {
		*s.postCommitActions = append(*s.postCommitActions, commit)
		*s.rollbackActions = append(*s.rollbackActions, restore)
		return nil
	}
	commit()
	return nil
}

func (s *certificateService) SetLocalApplyTrigger(trigger func(context.Context) error) {
	s.localApplyTrigger = wrapLocalApplyTrigger(trigger)
}

func (s *certificateService) List(ctx context.Context, agentID string) ([]ManagedCertificate, error) {
	if strings.TrimSpace(agentID) == "" {
		rows, err := s.store.ListManagedCertificates(ctx)
		if err != nil {
			return nil, err
		}
		certs := make([]ManagedCertificate, 0, len(rows))
		for _, row := range rows {
			certs = append(certs, managedCertificateFromRow(row))
		}
		return certs, nil
	}

	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return nil, err
	}

	rows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return nil, err
	}

	certs := make([]ManagedCertificate, 0, len(rows))
	for _, row := range rows {
		cert := managedCertificateFromRow(row)
		if containsString(cert.TargetAgentIDs, resolvedID) {
			cert = overlayManagedCertificateForAgent(cert, resolvedID)
			certs = append(certs, cert)
		}
	}
	return certs, nil
}

func (s *certificateService) ListPage(ctx context.Context, query ListQuery) ([]ManagedCertificate, PageMeta, error) {
	query = NormalizeListQuery(query)
	names, err := agentDisplayNameMap(ctx, s.cfg, s.store)
	if err != nil {
		return nil, PageMeta{}, err
	}

	rows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return nil, PageMeta{}, err
	}

	var refHostnames map[string]struct{}
	var refCertIDs map[int]struct{}
	if query.Referenced != nil {
		hostnames, certIDs, refErr := certificateReferenceSets(ctx, s.cfg, s.store)
		if refErr != nil {
			return nil, PageMeta{}, refErr
		}
		refHostnames, refCertIDs = hostnames, certIDs
	}
	matchesExtended := func(cert ManagedCertificate) bool {
		if !matchesTagsFilter(query.Tags, cert.Tags) {
			return false
		}
		return matchesReferencedFilter(query.Referenced, certificateReferenced(cert.Domain, cert.ID, refHostnames, refCertIDs))
	}

	filtered := make([]ManagedCertificate, 0, len(rows))
	if query.AgentID != "" {
		resolvedID, err := s.ensureAgentExists(ctx, query.AgentID)
		if err != nil {
			return nil, PageMeta{}, err
		}
		for _, row := range rows {
			cert := managedCertificateFromRow(row)
			if !containsString(cert.TargetAgentIDs, resolvedID) {
				continue
			}
			cert = overlayManagedCertificateForAgent(cert, resolvedID)
			cert.AgentID = resolvedID
			cert.AgentName = resolveAgentDisplayName(names, resolvedID)
			if !matchesListQuery(query.Q, cert.Domain, cert.Status, cert.Usage, cert.AgentID, cert.AgentName, strings.Join(cert.Tags, " "), strings.Join(cert.TargetAgentIDs, " ")) {
				continue
			}
			if !matchesEnabledFilter(query.Enabled, cert.Enabled) {
				continue
			}
			if !matchesStatusFilter(query.Status, cert.Status) {
				continue
			}
			if !matchesExtended(cert) {
				continue
			}
			filtered = append(filtered, cert)
		}
	} else {
		for _, row := range rows {
			cert := managedCertificateFromRow(row)
			if len(cert.TargetAgentIDs) > 0 {
				cert.AgentID = cert.TargetAgentIDs[0]
				cert.AgentName = resolveAgentDisplayName(names, cert.AgentID)
			}
			if !matchesListQuery(query.Q, cert.Domain, cert.Status, cert.Usage, cert.AgentID, cert.AgentName, strings.Join(cert.Tags, " "), strings.Join(cert.TargetAgentIDs, " ")) {
				continue
			}
			if !matchesEnabledFilter(query.Enabled, cert.Enabled) {
				continue
			}
			if !matchesStatusFilter(query.Status, cert.Status) {
				continue
			}
			if !matchesExtended(cert) {
				continue
			}
			filtered = append(filtered, cert)
		}
	}

	page, meta := ApplyPage(filtered, query)
	return page, meta, nil
}

func (s *certificateService) Create(ctx context.Context, agentID string, input ManagedCertificateInput) (ManagedCertificate, error) {
	if err := requireConfigMutationStore(s.store, s.mutationExecutor, s.revisionMutation); err != nil {
		return ManagedCertificate{}, err
	}
	if s.mutationExecutor == nil || s.revisionMutation {
		return s.createLegacy(ctx, agentID, input)
	}
	resolvedID := strings.TrimSpace(agentID)
	var err error
	if resolvedID != "" {
		resolvedID, err = s.ensureAgentExists(ctx, resolvedID)
		if err != nil {
			return ManagedCertificate{}, err
		}
	}
	previewID := preferredInt(input.ID)
	if previewID <= 0 {
		previewID = 1
	}
	preview, err := normalizeManagedCertificateInput(input, ManagedCertificate{}, previewID, resolvedID, resolvedID == "")
	if err != nil {
		return ManagedCertificate{}, err
	}
	targetAgentIDs, err := s.certificateMutationTargetAgentIDs(ctx, preview)
	if err != nil {
		return ManagedCertificate{}, err
	}
	postCommitActions := make([]func(), 0)
	rollbackActions := make([]func() error, 0)
	var created ManagedCertificate
	_, err = s.mutationExecutor.Execute(ctx, revision.MutationRequest{
		Kind:                "certificate.create",
		DependencyAction:    revision.DependencyActionApply,
		Request:             managedCertificateMutationIntent(input),
		Targets:             configMutationTargets(s.cfg, targetAgentIDs, nil),
		ResourceState:       managedCertificateMutationResourceState,
		ReplayResourceField: "certificate",
		ReplayResource:      func() any { return created },
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			txService := s.certificateRevisionTransactionService(tx, revisions, &postCommitActions, &rollbackActions)
			var mutateErr error
			created, mutateErr = txService.createLegacy(ctx, resolvedID, input)
			return mutateErr
		},
	})
	if err != nil {
		return ManagedCertificate{}, certificateMutationRollbackError(err, rollbackActions)
	}
	runConfigPostCommitActions(postCommitActions)
	return created, nil
}

func (s *certificateService) createLegacy(ctx context.Context, agentID string, input ManagedCertificateInput) (ManagedCertificate, error) {
	resolvedID := strings.TrimSpace(agentID)
	var err error
	if resolvedID != "" {
		resolvedID, err = s.ensureAgentExists(ctx, resolvedID)
		if err != nil {
			return ManagedCertificate{}, err
		}
	}

	current, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return ManagedCertificate{}, err
	}
	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return ManagedCertificate{}, err
	}

	maxRevision := 0
	for _, row := range current {
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
	}

	allowEmptyTargets := resolvedID == ""
	allocatedID := allocator.AllocateCertificateID(preferredInt(input.ID))
	normalizedInput := input
	// Keep the caller's preferred ID only for allocator conflict resolution.
	// Normalization should see the assigned ID, not re-read the raw preference.
	normalizedInput.ID = nil
	cert, err := normalizeManagedCertificateInput(normalizedInput, ManagedCertificate{}, allocatedID, resolvedID, allowEmptyTargets)
	if err != nil {
		return ManagedCertificate{}, err
	}
	if err := s.rejectCanonicalPKICertificateMutation(ctx, cert); err != nil {
		return ManagedCertificate{}, err
	}
	if err := assertManagedCertificateMutationAllowed(nil, cert); err != nil {
		return ManagedCertificate{}, err
	}
	if err := assertManagedCertificateTargetingAllowed(s.cfg, cert); err != nil {
		return ManagedCertificate{}, err
	}
	if err := s.assertCertificateDistributionTargetsAllowed(ctx, cert); err != nil {
		return ManagedCertificate{}, err
	}
	uploadMaterial, hasUploadMaterial, err := s.resolveUploadedMaterialForMutation(ctx, input, cert, nil)
	if err != nil {
		return ManagedCertificate{}, err
	}
	if hasUploadMaterial {
		cert.MaterialHash = hashManagedCertificateMaterial(uploadMaterial.CertPEM, uploadMaterial.KeyPEM)
		cert.NotAfter = managedCertificateNotAfterFromPEM(uploadMaterial.CertPEM, cert.NotAfter)
		if cert.Enabled && cert.IssuerMode == "local_http01" {
			cert.Status = "pending"
			cert.LastError = ""
		}
	}
	cert.Revision = s.certificateMutationRevision(allocator.AllocateRevisionForTargets(cert.TargetAgentIDs, maxRevision))

	originalRows := make([]storage.ManagedCertificateRow, 0, len(current))
	rows := make([]storage.ManagedCertificateRow, 0, len(current)+1)
	for _, row := range current {
		originalRows = append(originalRows, row)
		rows = append(rows, row)
	}
	rows = append(rows, managedCertificateToRow(cert))
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		return ManagedCertificate{}, err
	}
	if hasUploadMaterial {
		if err := s.saveManagedCertificateMaterial(ctx, cert.Domain, uploadMaterial); err != nil {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, originalRows); rollbackErr != nil {
				return ManagedCertificate{}, errors.Join(err, fmt.Errorf("restore certificate metadata: %w", rollbackErr))
			}
			s.cleanupManagedCertificateMaterialAfterMutation(ctx, rows, originalRows)
			return ManagedCertificate{}, err
		}
	}
	s.cleanupManagedCertificateMaterialAfterMutation(ctx, current, rows)
	return s.finishManagedCertificateMutation(ctx, rows, len(rows)-1, nil, cert, maxRevision)
}

func (s *certificateService) Update(ctx context.Context, agentID string, id int, input ManagedCertificateInput) (ManagedCertificate, error) {
	if err := requireConfigMutationStore(s.store, s.mutationExecutor, s.revisionMutation); err != nil {
		return ManagedCertificate{}, err
	}
	if s.mutationExecutor == nil || s.revisionMutation {
		return s.updateLegacy(ctx, agentID, id, input)
	}
	resolvedID := strings.TrimSpace(agentID)
	var err error
	if resolvedID != "" {
		resolvedID, err = s.ensureAgentExists(ctx, resolvedID)
		if err != nil {
			return ManagedCertificate{}, err
		}
	}
	current, err := s.certificateByID(ctx, resolvedID, id)
	if err != nil {
		return ManagedCertificate{}, err
	}
	defaultAgentID := resolvedID
	if defaultAgentID == "" {
		defaultAgentID = s.cfg.LocalAgentID
	}
	preview, err := normalizeManagedCertificateInput(input, current, id, defaultAgentID, resolvedID == "")
	if err != nil {
		return ManagedCertificate{}, err
	}
	targetAgentIDs, err := s.certificateMutationTargetAgentIDs(ctx, current, preview)
	if err != nil {
		return ManagedCertificate{}, err
	}
	postCommitActions := make([]func(), 0)
	rollbackActions := make([]func() error, 0)
	var updated ManagedCertificate
	result, err := s.mutationExecutor.Execute(ctx, revision.MutationRequest{
		Kind:             "certificate.update",
		DependencyAction: revision.DependencyActionApply,
		Request: struct {
			ID     int                      `json:"id"`
			Intent managedCertificateIntent `json:"intent"`
		}{ID: id, Intent: managedCertificateMutationIntent(input)},
		Targets:             configMutationTargets(s.cfg, targetAgentIDs, nil),
		ResourceState:       managedCertificateMutationResourceState,
		ReplayResourceField: "certificate",
		ReplayResource:      func() any { return updated },
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			txService := s.certificateRevisionTransactionService(tx, revisions, &postCommitActions, &rollbackActions)
			var mutateErr error
			updated, mutateErr = txService.updateLegacy(ctx, resolvedID, id, input)
			return mutateErr
		},
	})
	if err != nil {
		return ManagedCertificate{}, certificateMutationRollbackError(err, rollbackActions)
	}
	runConfigPostCommitActions(postCommitActions)
	if result.NoOp {
		return s.certificateByID(ctx, resolvedID, id)
	}
	return updated, nil
}

func (s *certificateService) updateLegacy(ctx context.Context, agentID string, id int, input ManagedCertificateInput) (ManagedCertificate, error) {
	resolvedID := strings.TrimSpace(agentID)
	var err error
	if resolvedID != "" {
		resolvedID, err = s.ensureAgentExists(ctx, resolvedID)
		if err != nil {
			return ManagedCertificate{}, err
		}
	}

	rows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return ManagedCertificate{}, err
	}
	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return ManagedCertificate{}, err
	}

	maxRevision := 0
	targetIndex := -1
	var current ManagedCertificate
	for i, row := range rows {
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
		cert := managedCertificateFromRow(row)
		if cert.ID == id && (resolvedID == "" || containsString(cert.TargetAgentIDs, resolvedID)) {
			targetIndex = i
			current = cert
		}
	}
	if targetIndex < 0 {
		return ManagedCertificate{}, ErrCertificateNotFound
	}

	defaultAgentID := resolvedID
	if defaultAgentID == "" {
		defaultAgentID = s.cfg.LocalAgentID
	}
	allowEmptyTargets := resolvedID == ""
	next, err := normalizeManagedCertificateInput(input, current, id, defaultAgentID, allowEmptyTargets)
	if err != nil {
		return ManagedCertificate{}, err
	}
	if err := s.rejectCanonicalPKICertificateMutation(ctx, next); err != nil {
		return ManagedCertificate{}, err
	}
	if err := assertManagedCertificateMutationAllowed(&current, next); err != nil {
		return ManagedCertificate{}, err
	}
	if current.CertificateType == "uploaded" && next.CertificateType != "uploaded" {
		return ManagedCertificate{}, fmt.Errorf("%w: cannot change certificate_type from uploaded to %s", ErrInvalidArgument, next.CertificateType)
	}
	if err := assertManagedCertificateTargetingAllowed(s.cfg, next); err != nil {
		return ManagedCertificate{}, err
	}
	if err := s.assertCertificateDistributionTargetsAllowed(ctx, next); err != nil {
		return ManagedCertificate{}, err
	}
	uploadMaterial, hasUploadMaterial, err := s.resolveUploadedMaterialForMutation(ctx, input, next, &current)
	if err != nil {
		return ManagedCertificate{}, err
	}
	if hasUploadMaterial {
		next.MaterialHash = hashManagedCertificateMaterial(uploadMaterial.CertPEM, uploadMaterial.KeyPEM)
		next.NotAfter = managedCertificateNotAfterFromPEM(uploadMaterial.CertPEM, next.NotAfter)
		if next.Enabled && next.IssuerMode == "local_http01" {
			next.Status = "pending"
			next.LastError = ""
		}
	}
	next.Revision = s.certificateMutationRevision(allocator.AllocateRevisionForTargets(
		unionManagedCertificateAgentIDs(current.TargetAgentIDs, next.TargetAgentIDs),
		maxRevision,
	))
	rows[targetIndex] = managedCertificateToRow(next)
	originalRows := append([]storage.ManagedCertificateRow(nil), rows...)
	originalRows[targetIndex] = managedCertificateToRow(current)
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		return ManagedCertificate{}, err
	}
	if hasUploadMaterial {
		if err := s.saveManagedCertificateMaterial(ctx, next.Domain, uploadMaterial); err != nil {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, originalRows); rollbackErr != nil {
				return ManagedCertificate{}, errors.Join(err, fmt.Errorf("restore certificate metadata: %w", rollbackErr))
			}
			s.cleanupManagedCertificateMaterialAfterMutation(ctx, rows, originalRows)
			return ManagedCertificate{}, err
		}
	}
	s.cleanupManagedCertificateMaterialAfterMutation(ctx, originalRows, rows)
	return s.finishManagedCertificateMutation(ctx, rows, targetIndex, &current, next, maxRevision)
}

func (s *certificateService) Delete(ctx context.Context, agentID string, id int) (ManagedCertificate, error) {
	if err := requireConfigMutationStore(s.store, s.mutationExecutor, s.revisionMutation); err != nil {
		return ManagedCertificate{}, err
	}
	if s.mutationExecutor == nil || s.revisionMutation {
		return s.deleteLegacy(ctx, agentID, id)
	}
	resolvedID := strings.TrimSpace(agentID)
	var err error
	if resolvedID != "" {
		resolvedID, err = s.ensureAgentExists(ctx, resolvedID)
		if err != nil {
			return ManagedCertificate{}, err
		}
	}
	current, err := s.certificateByID(ctx, resolvedID, id)
	if err != nil {
		return ManagedCertificate{}, err
	}
	targetCertificates := []ManagedCertificate{current}
	if resolvedID != "" && len(current.TargetAgentIDs) > 1 {
		next := current
		next.TargetAgentIDs = removeString(current.TargetAgentIDs, resolvedID)
		targetCertificates = append(targetCertificates, next)
	}
	targetAgentIDs, err := s.certificateMutationTargetAgentIDs(ctx, targetCertificates...)
	if err != nil {
		return ManagedCertificate{}, err
	}
	postCommitActions := make([]func(), 0)
	rollbackActions := make([]func() error, 0)
	var deleted ManagedCertificate
	_, err = s.mutationExecutor.Execute(ctx, revision.MutationRequest{
		Kind:                "certificate.delete",
		DependencyAction:    revision.DependencyActionDelete,
		Request:             map[string]any{"id": id, "agent_id": resolvedID},
		Targets:             configMutationTargets(s.cfg, targetAgentIDs, nil),
		ResourceState:       managedCertificateMutationResourceState,
		ReplayResourceField: "certificate",
		ReplayResource:      func() any { return deleted },
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			txService := s.certificateRevisionTransactionService(tx, revisions, &postCommitActions, &rollbackActions)
			var mutateErr error
			deleted, mutateErr = txService.deleteLegacy(ctx, resolvedID, id)
			return mutateErr
		},
	})
	if err != nil {
		return ManagedCertificate{}, certificateMutationRollbackError(err, rollbackActions)
	}
	runConfigPostCommitActions(postCommitActions)
	return deleted, nil
}

func (s *certificateService) deleteLegacy(ctx context.Context, agentID string, id int) (ManagedCertificate, error) {
	resolvedID := strings.TrimSpace(agentID)
	var err error
	if resolvedID != "" {
		resolvedID, err = s.ensureAgentExists(ctx, resolvedID)
		if err != nil {
			return ManagedCertificate{}, err
		}
	}

	rows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return ManagedCertificate{}, err
	}
	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return ManagedCertificate{}, err
	}

	maxRevision := 0
	targetIndex := -1
	var current ManagedCertificate
	for i, row := range rows {
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
		cert := managedCertificateFromRow(row)
		if cert.ID == id && (resolvedID == "" || containsString(cert.TargetAgentIDs, resolvedID)) {
			targetIndex = i
			current = cert
		}
	}
	if targetIndex < 0 {
		return ManagedCertificate{}, ErrCertificateNotFound
	}
	if isSystemRelayCACertificate(current) {
		return ManagedCertificate{}, fmt.Errorf("%w: system relay ca cannot be deleted", ErrInvalidArgument)
	}
	if err := s.assertManagedCertificateNotReferencedByRelayListener(ctx, current); err != nil {
		return ManagedCertificate{}, err
	}

	if resolvedID != "" && len(current.TargetAgentIDs) > 1 {
		nextTargets := removeString(current.TargetAgentIDs, resolvedID)
		next := current
		next.TargetAgentIDs = nextTargets
		next.Revision = s.certificateMutationRevision(allocator.AllocateRevisionForTargets(current.TargetAgentIDs, maxRevision))
		originalRows := append([]storage.ManagedCertificateRow(nil), rows...)
		rows[targetIndex] = managedCertificateToRow(next)
		if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
			return ManagedCertificate{}, err
		}
		s.cleanupManagedCertificateMaterialAfterMutation(ctx, originalRows, rows)
		current.TargetAgentIDs = []string{resolvedID}
		return current, nil
	}

	nextRows := append([]storage.ManagedCertificateRow(nil), rows[:targetIndex]...)
	nextRows = append(nextRows, rows[targetIndex+1:]...)
	if err := s.store.SaveManagedCertificates(ctx, nextRows); err != nil {
		return ManagedCertificate{}, err
	}
	allocator, err = newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return ManagedCertificate{}, err
	}
	nextRevision := allocator.AllocateRevisionForTargets(current.TargetAgentIDs, current.Revision)
	s.cleanupManagedCertificateMaterialAfterMutation(ctx, rows, nextRows)
	if err := s.syncManagedCertificateAgentIDs(ctx, current.TargetAgentIDs, nextRevision); err != nil {
		return ManagedCertificate{}, err
	}
	return current, nil
}

func (s *certificateService) assertManagedCertificateNotReferencedByRelayListener(ctx context.Context, cert ManagedCertificate) error {
	if !isAutoRelayListenerCertificate(cert, 0) {
		return nil
	}

	rows, err := s.store.ListRelayListeners(ctx, "")
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.CertificateID == nil || *row.CertificateID != cert.ID {
			continue
		}
		return fmt.Errorf("%w: certificate %d is referenced by relay listener %d on agent %s", ErrInvalidArgument, cert.ID, row.ID, strings.TrimSpace(row.AgentID))
	}
	return nil
}

func (s *certificateService) Issue(ctx context.Context, agentID string, id int) (ManagedCertificate, error) {
	if err := requireConfigMutationStore(s.store, s.mutationExecutor, s.revisionMutation); err != nil {
		return ManagedCertificate{}, err
	}
	if s.mutationExecutor == nil || s.revisionMutation {
		return s.issueLegacy(ctx, agentID, id)
	}
	requestedAgentID := strings.TrimSpace(agentID)
	if requestedAgentID != "" {
		resolvedID, err := s.ensureAgentExists(ctx, requestedAgentID)
		if err != nil {
			return ManagedCertificate{}, err
		}
		requestedAgentID = resolvedID
	}
	current, err := s.certificateByID(ctx, "", id)
	if err != nil {
		return ManagedCertificate{}, err
	}
	targetCert := current
	if requestedAgentID != "" {
		targetCert.TargetAgentIDs = []string{requestedAgentID}
	}
	targetAgentIDs, err := s.certificateMutationTargetAgentIDs(ctx, targetCert)
	if err != nil {
		return ManagedCertificate{}, err
	}
	postCommitActions := make([]func(), 0)
	rollbackActions := make([]func() error, 0)
	var issued ManagedCertificate
	result, err := s.mutationExecutor.Execute(ctx, revision.MutationRequest{
		Kind:                "certificate.issue",
		DependencyAction:    revision.DependencyActionApply,
		Request:             map[string]any{"id": id, "agent_id": requestedAgentID},
		Targets:             configMutationTargets(s.cfg, targetAgentIDs, nil),
		ResourceState:       managedCertificateMutationResourceState,
		ReplayResourceField: "certificate",
		ReplayResource:      func() any { return issued },
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			txService := s.certificateRevisionTransactionService(tx, revisions, &postCommitActions, &rollbackActions)
			var mutateErr error
			issued, mutateErr = txService.issueLegacy(ctx, requestedAgentID, id)
			return mutateErr
		},
	})
	if err != nil {
		return ManagedCertificate{}, certificateMutationRollbackError(err, rollbackActions)
	}
	runConfigPostCommitActions(postCommitActions)
	if result.NoOp {
		return s.certificateByID(ctx, "", id)
	}
	return issued, nil
}

func (s *certificateService) issueLegacy(ctx context.Context, agentID string, id int) (ManagedCertificate, error) {
	rows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return ManagedCertificate{}, err
	}

	maxRevision := 0
	for _, row := range rows {
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
	}

	current, targetIndex, ok := findManagedCertificateByID(rows, id)
	if !ok {
		return ManagedCertificate{}, ErrCertificateNotFound
	}
	requestedAgentID := strings.TrimSpace(agentID)
	if current.IssuerMode == "local_http01" && current.CertificateType == "acme" {
		return s.issueLocalHTTP01ACME(ctx, rows, targetIndex, current, maxRevision, requestedAgentID)
	}
	if current.IssuerMode == "local_http01" && current.CertificateType == "internal_ca" {
		return s.issueLocalHTTP01InternalCA(ctx, rows, targetIndex, current, maxRevision, requestedAgentID)
	}

	resolvedID := ""
	if requestedAgentID != "" {
		resolvedID, err = s.ensureAgentExists(ctx, requestedAgentID)
		if err != nil {
			return ManagedCertificate{}, err
		}
		if !containsString(current.TargetAgentIDs, resolvedID) {
			return ManagedCertificate{}, ErrCertificateNotFound
		}
	}

	if err := s.assertCertificateDistributionTargetsAllowed(ctx, current); err != nil {
		return ManagedCertificate{}, err
	}
	if current.IssuerMode == "master_cf_dns" {
		// Async issuance: persist "issuing" + revision, dispatch a background signer, and return
		// immediately so the HTTP request no longer blocks on the ACME order. The dispatcher's
		// sign function owns issuanceLock and performs the actual issue (see
		// ManagedCertificateBackgroundSigner / issueManagedCertificateInBackground).
		return s.scheduleManagedCertificateIssue(ctx, rows, targetIndex, current, maxRevision, false, nil)
	}
	if current.CertificateType == "uploaded" && current.IssuerMode == "local_http01" {
		return s.syncStaticLocalCertificate(ctx, rows, targetIndex, current, maxRevision, resolvedID, true)
	}
	current.Status = "active"
	current.LastIssueAt = s.now().UTC().Format(time.RFC3339)
	current.LastError = ""
	current.Revision = s.certificateMutationRevision(maxRevision + 1)
	originalRows := append([]storage.ManagedCertificateRow(nil), rows...)
	rows[targetIndex] = managedCertificateToRow(current)
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		return ManagedCertificate{}, err
	}
	s.cleanupManagedCertificateMaterialAfterMutation(ctx, originalRows, rows)
	return current, nil
}

func (s *certificateService) issueLocalHTTP01InternalCA(ctx context.Context, rows []storage.ManagedCertificateRow, targetIndex int, current ManagedCertificate, maxRevision int, requestedAgentID string) (ManagedCertificate, error) {
	if !current.Enabled {
		return ManagedCertificate{}, fmt.Errorf("%w: certificate is disabled", ErrInvalidArgument)
	}
	if current.IssuerMode != "local_http01" {
		return ManagedCertificate{}, fmt.Errorf("%w: certificate is not configured for local_http01", ErrInvalidArgument)
	}

	requestedTargetIDs := append([]string(nil), current.TargetAgentIDs...)
	if requestedAgentID != "" {
		requestedAgentID = strings.TrimSpace(requestedAgentID)
		requestedTargetIDs = requestedTargetIDs[:0]
		for _, targetAgentID := range current.TargetAgentIDs {
			if strings.TrimSpace(targetAgentID) == requestedAgentID {
				requestedTargetIDs = append(requestedTargetIDs, requestedAgentID)
			}
		}
	}
	if requestedAgentID != "" && len(requestedTargetIDs) == 0 {
		return ManagedCertificate{}, fmt.Errorf("%w: certificate is not assigned to the requested agent", ErrInvalidArgument)
	}

	for _, targetAgentID := range requestedTargetIDs {
		_, displayName, capabilities, err := s.resolveCertificateTarget(ctx, targetAgentID)
		if errors.Is(err, ErrAgentNotFound) {
			return ManagedCertificate{}, fmt.Errorf("%w: target agent not found: %s", ErrInvalidArgument, strings.TrimSpace(targetAgentID))
		}
		if err != nil {
			return ManagedCertificate{}, err
		}
		if !agentHasCapability(capabilities, "cert_install") {
			return ManagedCertificate{}, fmt.Errorf("%w: target agent does not support certificate install: %s", ErrInvalidArgument, displayName)
		}
	}

	material, materialFound, err := s.store.LoadManagedCertificateMaterial(ctx, current.Domain)
	if err != nil {
		return ManagedCertificate{}, err
	}
	if !materialFound || validateUploadedManagedCertificateBundle(material) != nil {
		material, err = generateInternalCAMaterial(current.Domain)
		if err != nil {
			return ManagedCertificate{}, err
		}
		if err := s.saveManagedCertificateMaterial(ctx, current.Domain, material); err != nil {
			return ManagedCertificate{}, err
		}
	}

	now := s.now().UTC()
	issuedAt := now.Format(time.RFC3339)
	materialHash := hashManagedCertificateMaterial(strings.TrimSpace(material.CertPEM), strings.TrimSpace(material.KeyPEM))
	next := current
	next.Status = "active"
	next.LastIssueAt = issuedAt
	next.LastError = ""
	next.MaterialHash = materialHash
	next.Revision = s.certificateMutationRevision(maxRevision + 1)
	next.NotAfter = managedCertificateNotAfterFromPEM(material.CertPEM, next.NotAfter)
	for _, targetAgentID := range requestedTargetIDs {
		next = updateManagedCertificateAgentReport(next, targetAgentID, ManagedCertificateHeartbeatReport{
			Status:       "active",
			LastIssueAt:  issuedAt,
			LastError:    "",
			MaterialHash: materialHash,
			ACMEInfo:     current.ACMEInfo,
			UpdatedAt:    issuedAt,
		}, now)
	}

	originalRows := append([]storage.ManagedCertificateRow(nil), rows...)
	rows[targetIndex] = managedCertificateToRow(next)
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		return ManagedCertificate{}, err
	}
	s.cleanupManagedCertificateMaterialAfterMutation(ctx, originalRows, rows)
	if err := s.syncManagedCertificateAgentIDs(ctx, requestedTargetIDs, next.Revision); err != nil {
		return ManagedCertificate{}, err
	}
	return next, nil
}

func (s *certificateService) issueLocalHTTP01ACME(ctx context.Context, rows []storage.ManagedCertificateRow, targetIndex int, current ManagedCertificate, maxRevision int, requestedAgentID string) (ManagedCertificate, error) {
	if !current.Enabled {
		return ManagedCertificate{}, fmt.Errorf("%w: certificate is disabled", ErrInvalidArgument)
	}
	if current.IssuerMode != "local_http01" {
		return ManagedCertificate{}, fmt.Errorf("%w: certificate is not configured for local_http01", ErrInvalidArgument)
	}

	requestedTargetIDs := append([]string(nil), current.TargetAgentIDs...)
	if requestedAgentID != "" {
		requestedAgentID = strings.TrimSpace(requestedAgentID)
		requestedTargetIDs = requestedTargetIDs[:0]
		for _, targetAgentID := range current.TargetAgentIDs {
			if strings.TrimSpace(targetAgentID) == requestedAgentID {
				requestedTargetIDs = append(requestedTargetIDs, requestedAgentID)
			}
		}
	}
	if len(requestedTargetIDs) == 0 {
		return ManagedCertificate{}, fmt.Errorf("%w: certificate is not assigned to the requested agent", ErrInvalidArgument)
	}
	if requestedAgentID == "" && len(requestedTargetIDs) > 1 {
		return ManagedCertificate{}, fmt.Errorf("%w: local_http01 certificates must be issued from the per-agent endpoint", ErrInvalidArgument)
	}

	for _, targetAgentID := range requestedTargetIDs {
		resolvedID, displayName, capabilities, err := s.resolveCertificateTarget(ctx, targetAgentID)
		if errors.Is(err, ErrAgentNotFound) {
			return ManagedCertificate{}, fmt.Errorf("%w: target agent not found: %s", ErrInvalidArgument, strings.TrimSpace(targetAgentID))
		}
		if err != nil {
			return ManagedCertificate{}, err
		}
		if !agentHasCapability(capabilities, "cert_install") {
			return ManagedCertificate{}, fmt.Errorf("%w: target agent does not support certificate install: %s", ErrInvalidArgument, displayName)
		}
		if !agentHasCapability(capabilities, "local_acme") {
			return ManagedCertificate{}, fmt.Errorf("%w: target agent does not support local ACME issuance: %s", ErrInvalidArgument, displayName)
		}

		rules, err := s.store.ListHTTPRules(ctx, resolvedID)
		if err != nil {
			return ManagedCertificate{}, err
		}
		if !hasMatchingHTTPSRuleForCertificateInRows(rules, current) {
			return ManagedCertificate{}, fmt.Errorf("%w: no enabled HTTPS HTTP rule found for %s on agent %s", ErrInvalidArgument, current.Domain, displayName)
		}
	}

	now := s.now().UTC()
	next := current
	next.Status = "pending"
	next.LastError = ""
	next.Revision = s.certificateMutationRevision(maxRevision + 1)
	for _, targetAgentID := range requestedTargetIDs {
		previousReport := current.AgentReports[strings.TrimSpace(targetAgentID)]
		next = updateManagedCertificateAgentReport(next, targetAgentID, ManagedCertificateHeartbeatReport{
			Status:       "pending",
			LastIssueAt:  previousReport.LastIssueAt,
			LastError:    "",
			MaterialHash: "",
			ACMEInfo:     ManagedCertificateACMEInfo{},
			UpdatedAt:    now.Format(time.RFC3339),
		}, now)
	}

	originalRows := append([]storage.ManagedCertificateRow(nil), rows...)
	rows[targetIndex] = managedCertificateToRow(next)
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		return ManagedCertificate{}, err
	}
	s.cleanupManagedCertificateMaterialAfterMutation(ctx, originalRows, rows)
	if err := s.syncManagedCertificateAgentIDs(ctx, requestedTargetIDs, next.Revision); err != nil {
		return ManagedCertificate{}, err
	}
	return next, nil
}

func (s *certificateService) finishManagedCertificateMutation(ctx context.Context, rows []storage.ManagedCertificateRow, targetIndex int, previous *ManagedCertificate, current ManagedCertificate, maxRevision int) (ManagedCertificate, error) {
	affectedAgentIDs := append([]string(nil), current.TargetAgentIDs...)
	removedAgentIDs := []string(nil)
	if previous != nil {
		affectedAgentIDs = unionManagedCertificateAgentIDs(previous.TargetAgentIDs, current.TargetAgentIDs)
		removedAgentIDs = differenceManagedCertificateAgentIDs(previous.TargetAgentIDs, current.TargetAgentIDs)
	}

	if current.Enabled && current.Scope == "domain" && current.IssuerMode == "master_cf_dns" && managedCertificateMutationNeedsManagedDNSIssue(previous, current) {
		// Async issuance on Create/Update: persist "issuing" + current revision (no extra bump),
		// notify removed agents, and dispatch the background signer. The revision bump for the
		// issued material happens inside the background signer on success.
		return s.scheduleManagedCertificateIssue(ctx, rows, targetIndex, current, maxRevision, false, removedAgentIDs)
	}
	if current.Enabled && current.IssuerMode == "local_http01" && current.CertificateType == "uploaded" {
		synced, err := s.syncStaticLocalCertificate(ctx, rows, targetIndex, current, maxRevision, "", false)
		if err != nil {
			return ManagedCertificate{}, err
		}
		if err := s.syncManagedCertificateAgentIDs(ctx, removedAgentIDs, synced.Revision); err != nil {
			return ManagedCertificate{}, err
		}
		return synced, nil
	}
	if err := s.syncManagedCertificateAgentIDs(ctx, affectedAgentIDs, current.Revision); err != nil {
		return ManagedCertificate{}, err
	}
	return current, nil
}

func managedCertificateMutationNeedsManagedDNSIssue(previous *ManagedCertificate, current ManagedCertificate) bool {
	if !current.Enabled || current.Scope != "domain" || current.IssuerMode != "master_cf_dns" {
		return false
	}
	if previous == nil {
		return true
	}
	if !previous.Enabled {
		return true
	}
	return previous.Domain != current.Domain ||
		previous.Scope != current.Scope ||
		previous.IssuerMode != current.IssuerMode ||
		previous.CertificateType != current.CertificateType ||
		!reflect.DeepEqual(previous.TargetAgentIDs, current.TargetAgentIDs)
}

func (s *certificateService) issueManagedCertificateInBackground(ctx context.Context, rows []storage.ManagedCertificateRow, targetIndex int, current ManagedCertificate, maxRevision int) (ManagedCertificate, error) {
	if err := requireConfigMutationStore(s.store, s.mutationExecutor, s.revisionMutation); err != nil {
		return ManagedCertificate{}, err
	}
	if err := s.assertCertificateDistributionTargetsAllowed(ctx, current); err != nil {
		return ManagedCertificate{}, err
	}
	if err := s.assertManagedCertificateManualIssueAllowed(current); err != nil {
		return ManagedCertificate{}, err
	}

	issuer := s.renewalIssuer
	if issuer == nil && s.cfg.ManagedDNSCertificatesEnabled {
		issuer = newMasterCFDNSManagedCertificateIssuer()
	}
	if issuer == nil {
		return ManagedCertificate{}, fmt.Errorf("%w: managed certificates require ACME_DNS_PROVIDER=cf and CF_Token", ErrInvalidArgument)
	}

	unlock := issuanceLock(current.ID)
	defer unlock()

	// Retry once when a concurrent edit changes the domain/targets while the
	// ACME order is in flight. The current goroutine already holds the in-flight
	// slot, so Submit from scheduleManagedCertificateIssue was de-duplicated; we
	// re-read the updated row under the lock and restart issuance instead.
	for attempt := 0; attempt < 2; attempt++ {
		freshRows, err := s.store.ListManagedCertificates(ctx)
		if err != nil {
			return ManagedCertificate{}, err
		}
		fresh, freshIndex, found := findManagedCertificateByID(freshRows, current.ID)
		if !found {
			return ManagedCertificate{}, nil
		}
		if !managedCertificateEligibleForBackgroundIssue(fresh) {
			return fresh, nil
		}
		rows, targetIndex, current, maxRevision = freshRows, freshIndex, fresh, highestManagedCertificateRevisionForService(freshRows)
		if pending, pendingErr := s.hasPendingManagedCertificateGeneration(ctx, current.Domain); pendingErr != nil {
			return ManagedCertificate{}, pendingErr
		} else if pending {
			return current, nil
		}
		generation := managedCertificateGenerationFor(current)

		issueResult, err := issuer.Issue(ctx, current)
		if err != nil {
			// Re-read before recording failure — the ACME order may have taken long
			// enough that the certificate was concurrently deleted or edited. Using the
			// stale pre-order snapshot would risk resurrecting a deleted row as "error"
			// or overwriting a concurrent domain/target/status change.
			persistRows, persistErr := s.store.ListManagedCertificates(ctx)
			if persistErr != nil {
				return ManagedCertificate{}, persistErr
			}
			persistCert, persistIndex, persistFound := findManagedCertificateByID(persistRows, current.ID)
			if !persistFound {
				return ManagedCertificate{}, err
			}
			// Only record the failure if the row still matches the order we attempted.
			// If the domain was changed or the certificate is no longer eligible, the
			// failure belongs to a stale configuration and must not be applied.
			if !generation.Matches(persistCert) {
				if persistCert.Status == "issuing" && managedCertificateEligibleForBackgroundIssue(persistCert) && attempt == 0 {
					rows, targetIndex, current, maxRevision = persistRows, persistIndex, persistCert, highestManagedCertificateRevisionForService(persistRows)
					continue
				}
				return persistCert, nil
			}
			return s.failManagedCertificateIssue(ctx, persistRows, persistIndex, current, highestManagedCertificateRevisionForService(persistRows), err, false)
		}
		issuedMaterial, err := resolveManagedCertificateIssueMaterial(current, issueResult)
		if err != nil {
			// Re-read before recording a post-order validation failure for the same
			// reason as issuer errors above: the certificate may have been deleted or
			// edited while ACME was in flight.
			persistRows, persistErr := s.store.ListManagedCertificates(ctx)
			if persistErr != nil {
				return ManagedCertificate{}, persistErr
			}
			persistCert, persistIndex, persistFound := findManagedCertificateByID(persistRows, current.ID)
			if !persistFound {
				return ManagedCertificate{}, err
			}
			if !generation.Matches(persistCert) {
				if persistCert.Status == "issuing" && managedCertificateEligibleForBackgroundIssue(persistCert) && attempt == 0 {
					rows, targetIndex, current, maxRevision = persistRows, persistIndex, persistCert, highestManagedCertificateRevisionForService(persistRows)
					continue
				}
				return persistCert, nil
			}
			return s.failManagedCertificateIssue(ctx, persistRows, persistIndex, current, highestManagedCertificateRevisionForService(persistRows), err, false)
		}

		// Revalidate the certificate row after the (potentially long) ACME order before
		// writing any material to disk: if the certificate was deleted, disabled, or changed
		// to another domain while the order was in flight, returning here avoids leaving
		// orphaned PEM files behind.
		persistRows, err := s.store.ListManagedCertificates(ctx)
		if err != nil {
			return ManagedCertificate{}, err
		}
		persistCert, persistIndex, persistFound := findManagedCertificateByID(persistRows, current.ID)
		if !persistFound {
			return ManagedCertificate{}, nil
		}
		if !generation.Matches(persistCert) {
			// A concurrent edit changed the certificate while the ACME order was in
			// flight. If the row is still eligible, restart issuance with the updated
			// data instead of leaving the certificate stuck in "issuing".
			if persistCert.Status == "issuing" && managedCertificateEligibleForBackgroundIssue(persistCert) && attempt == 0 {
				rows, targetIndex, current, maxRevision = persistRows, persistIndex, persistCert, highestManagedCertificateRevisionForService(persistRows)
				continue
			}
			return persistCert, nil
		}

		next, persistErr := s.persistManagedCertificateIssueSuccess(
			ctx, persistRows, persistIndex, current, issueResult, issuedMaterial,
		)
		if persistErr != nil {
			if managedCertificateMaterialRestoreFailed(persistErr) {
				return ManagedCertificate{}, persistErr
			}
			return s.failManagedCertificateIssue(
				ctx, persistRows, persistIndex, current,
				highestManagedCertificateRevisionForService(persistRows), persistErr, false,
			)
		}
		return next, nil
	}
	return ManagedCertificate{}, nil
}

func (s *certificateService) persistManagedCertificateIssueSuccess(
	ctx context.Context,
	rows []storage.ManagedCertificateRow,
	targetIndex int,
	current ManagedCertificate,
	issueResult managedCertificateRenewalResult,
	issuedMaterial storage.ManagedCertificateBundle,
) (ManagedCertificate, error) {
	if err := requireConfigMutationStore(s.store, s.mutationExecutor, s.revisionMutation); err != nil {
		return ManagedCertificate{}, err
	}
	expectedGeneration := managedCertificateGenerationFor(current)
	freshRows, fresh, freshIndex, matched, err := s.loadManagedCertificateGeneration(ctx, current.ID, expectedGeneration)
	if err != nil {
		return ManagedCertificate{}, err
	}
	if !matched {
		return fresh, nil
	}
	rows, current, targetIndex = freshRows, fresh, freshIndex
	if s.mutationExecutor == nil || s.revisionMutation {
		return s.persistManagedCertificateIssueSuccessLegacy(ctx, rows, targetIndex, current, issueResult, issuedMaterial)
	}
	targetAgentIDs, err := s.certificateMutationTargetAgentIDs(ctx, current)
	if err != nil {
		return ManagedCertificate{}, err
	}
	postCommitActions := make([]func(), 0)
	rollbackActions := make([]func() error, 0)
	var persisted bool
	var next ManagedCertificate
	request := revision.MutationRequest{
		Kind:             "certificate.issue.complete",
		DependencyAction: revision.DependencyActionApply,
		Request:          map[string]any{"id": current.ID, "domain": current.Domain, "material_hash": issueResult.MaterialHash},
		Targets:          configMutationTargets(s.cfg, targetAgentIDs, nil),
		ResourceState:    managedCertificateMutationResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			freshRows, loadErr := tx.ListManagedCertificates(ctx)
			if loadErr != nil {
				return loadErr
			}
			fresh, freshIndex, found := findManagedCertificateByID(freshRows, current.ID)
			if !found || !expectedGeneration.Matches(fresh) || !managedCertificateEligibleForBackgroundIssue(fresh) {
				next = fresh
				return nil
			}
			txService := s.certificateRevisionTransactionService(tx, revisions, &postCommitActions, &rollbackActions)
			persisted = true
			next, loadErr = txService.persistManagedCertificateIssueSuccessLegacy(
				ctx, freshRows, freshIndex, fresh, issueResult, issuedMaterial,
			)
			return loadErr
		},
	}
	err = s.withManagedCertificateDomainLock(ctx, current.Domain, func(lockedCtx context.Context) error {
		_, mutationErr := s.mutationExecutor.Execute(lockedCtx, request)
		if mutationErr != nil {
			return certificateMutationRollbackError(mutationErr, rollbackActions)
		}
		return nil
	})
	if err != nil {
		return ManagedCertificate{}, err
	}
	runConfigPostCommitActions(postCommitActions)
	if !persisted {
		return ManagedCertificate{}, nil
	}
	return next, nil
}

func (s *certificateService) persistManagedCertificateIssueSuccessLegacy(
	ctx context.Context,
	rows []storage.ManagedCertificateRow,
	targetIndex int,
	current ManagedCertificate,
	issueResult managedCertificateRenewalResult,
	issuedMaterial storage.ManagedCertificateBundle,
) (ManagedCertificate, error) {
	if targetIndex < 0 || targetIndex >= len(rows) {
		return current, nil
	}
	fresh := managedCertificateFromRow(rows[targetIndex])
	if !managedCertificateGenerationFor(current).Matches(fresh) {
		return fresh, nil
	}
	current = fresh
	if s.generationStore != nil {
		return s.persistManagedCertificateIssueSuccessGeneration(ctx, rows, targetIndex, current, issueResult, issuedMaterial)
	}
	commitMaterial, restore, err := stageManagedCertificateMaterialWithRollback(ctx, s.store, current.Domain, issuedMaterial)
	if err != nil {
		return ManagedCertificate{}, err
	}
	materialRegistered := s.revisionMutation && s.postCommitActions != nil && s.rollbackActions != nil
	if materialRegistered {
		*s.postCommitActions = append(*s.postCommitActions, commitMaterial)
		*s.rollbackActions = append(*s.rollbackActions, restore)
	}

	next := current
	next.Status = "active"
	next.LastIssueAt = issueResult.LastIssueAt
	if strings.TrimSpace(next.LastIssueAt) == "" {
		next.LastIssueAt = s.now().UTC().Format(time.RFC3339)
	}
	next.LastError = ""
	next.BackoffClass = ""
	next.RetryCount = 0
	next.NextRetryAtUnix = 0
	next.MaterialHash = issueResult.MaterialHash
	if strings.TrimSpace(next.MaterialHash) == "" {
		next.MaterialHash = hashManagedCertificateMaterial(strings.TrimSpace(issuedMaterial.CertPEM), strings.TrimSpace(issuedMaterial.KeyPEM))
	}
	next.NotAfter = managedCertificateNotAfterFromPEM(issuedMaterial.CertPEM, next.NotAfter)
	next.ACMEInfo = issueResult.ACMEInfo
	next.Revision = s.certificateMutationRevision(highestManagedCertificateRevisionForService(rows) + 1)

	originalRows := append([]storage.ManagedCertificateRow(nil), rows...)
	rows[targetIndex] = managedCertificateToRow(next)
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		if !materialRegistered {
			if restoreErr := restore(); restoreErr != nil {
				return ManagedCertificate{}, &managedCertificateMaterialRestoreError{
					writeErr: fmt.Errorf("save issued certificate metadata: %w", err), restoreErr: restoreErr,
				}
			}
		}
		return ManagedCertificate{}, err
	}
	if !materialRegistered {
		commitMaterial()
	}
	s.cleanupManagedCertificateMaterialAfterMutation(ctx, originalRows, rows)
	if err := s.syncManagedCertificateAgentIDs(ctx, next.TargetAgentIDs, next.Revision); err != nil {
		return ManagedCertificate{}, err
	}
	return next, nil
}

func (s *certificateService) syncStaticLocalCertificate(ctx context.Context, rows []storage.ManagedCertificateRow, targetIndex int, current ManagedCertificate, maxRevision int, requestedAgentID string, bumpRevision bool) (ManagedCertificate, error) {
	if !current.Enabled {
		return ManagedCertificate{}, fmt.Errorf("%w: certificate is disabled", ErrInvalidArgument)
	}
	if current.IssuerMode != "local_http01" {
		return ManagedCertificate{}, fmt.Errorf("%w: certificate is not configured for local_http01", ErrInvalidArgument)
	}
	if current.CertificateType == "acme" {
		return ManagedCertificate{}, fmt.Errorf("%w: certificate requires local ACME issuance", ErrInvalidArgument)
	}

	requestedTargetIDs := append([]string(nil), current.TargetAgentIDs...)
	if requestedAgentID != "" {
		requestedTargetIDs = requestedTargetIDs[:0]
		for _, targetAgentID := range current.TargetAgentIDs {
			if strings.TrimSpace(targetAgentID) == requestedAgentID {
				requestedTargetIDs = append(requestedTargetIDs, requestedAgentID)
			}
		}
	}
	if len(requestedTargetIDs) == 0 {
		return ManagedCertificate{}, fmt.Errorf("%w: certificate is not assigned to the requested agent", ErrInvalidArgument)
	}
	for _, targetAgentID := range requestedTargetIDs {
		_, displayName, capabilities, err := s.resolveCertificateTarget(ctx, targetAgentID)
		if errors.Is(err, ErrAgentNotFound) {
			return ManagedCertificate{}, fmt.Errorf("%w: target agent not found: %s", ErrInvalidArgument, strings.TrimSpace(targetAgentID))
		}
		if err != nil {
			return ManagedCertificate{}, err
		}
		if !agentHasCapability(capabilities, "cert_install") {
			return ManagedCertificate{}, fmt.Errorf("%w: target agent does not support certificate install: %s", ErrInvalidArgument, displayName)
		}
	}

	material, ok, err := s.store.LoadManagedCertificateMaterial(ctx, current.Domain)
	if err != nil {
		return ManagedCertificate{}, err
	}
	if !ok {
		return ManagedCertificate{}, fmt.Errorf("%w: certificate material not found", ErrInvalidArgument)
	}
	if err := validateUploadedManagedCertificateBundle(material); err != nil {
		return ManagedCertificate{}, err
	}

	now := s.now().UTC()
	issuedAt := now.Format(time.RFC3339)
	materialHash := hashManagedCertificateMaterial(material.CertPEM, material.KeyPEM)
	next := current
	next.Status = "active"
	next.LastIssueAt = issuedAt
	next.LastError = ""
	next.MaterialHash = materialHash
	next.Revision = s.certificateMutationRevision(managedCertificateMutationRevision(current, maxRevision, bumpRevision))
	next.NotAfter = managedCertificateNotAfterFromPEM(material.CertPEM, next.NotAfter)
	for _, targetAgentID := range requestedTargetIDs {
		next = updateManagedCertificateAgentReport(next, targetAgentID, ManagedCertificateHeartbeatReport{
			Status:       "active",
			LastIssueAt:  issuedAt,
			LastError:    "",
			MaterialHash: materialHash,
			ACMEInfo:     current.ACMEInfo,
			UpdatedAt:    issuedAt,
		}, now)
	}

	originalRows := append([]storage.ManagedCertificateRow(nil), rows...)
	rows[targetIndex] = managedCertificateToRow(next)
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		return ManagedCertificate{}, err
	}
	s.cleanupManagedCertificateMaterialAfterMutation(ctx, originalRows, rows)
	if err := s.syncManagedCertificateAgentIDs(ctx, requestedTargetIDs, next.Revision); err != nil {
		return ManagedCertificate{}, err
	}
	return next, nil
}

func (s *certificateService) syncManagedCertificateAgentIDs(ctx context.Context, agentIDs []string, revision int) error {
	if s.revisionMutation {
		return nil
	}
	seen := make(map[string]struct{}, len(agentIDs))
	for _, agentID := range agentIDs {
		resolvedID := strings.TrimSpace(agentID)
		if resolvedID == "" {
			continue
		}
		if _, ok := seen[resolvedID]; ok {
			continue
		}
		seen[resolvedID] = struct{}{}

		resolvedID, _, capabilities, err := s.resolveCertificateTarget(ctx, resolvedID)
		if errors.Is(err, ErrAgentNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if !agentHasCapability(capabilities, "cert_install") {
			continue
		}
		if s.cfg.EnableLocalAgent && resolvedID == s.cfg.LocalAgentID {
			if s.localApplyTrigger != nil {
				if err := s.localApplyTrigger(ctx); err != nil {
					return err
				}
				continue
			}
			if err := s.applyLocalManagedCertificateSync(ctx); err != nil {
				return err
			}
			continue
		}
		if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, revision); err != nil {
			if errors.Is(err, ErrAgentNotFound) {
				continue
			}
			return err
		}
	}
	return nil
}

func (s *certificateService) applyLocalManagedCertificateSync(ctx context.Context) error {
	localStore, ok := s.store.(localManagedCertificateSyncStore)
	if !ok {
		return fmt.Errorf("local managed certificate sync requires local snapshot support")
	}
	snapshot, err := localStore.LoadLocalSnapshot(ctx, s.cfg.LocalAgentID)
	if err != nil {
		return err
	}
	return localStore.SaveLocalRuntimeState(ctx, s.cfg.LocalAgentID, storage.RuntimeState{
		CurrentRevision:   snapshot.Revision,
		Status:            "success",
		LastApplyRevision: snapshot.Revision,
		LastApplyStatus:   "success",
	})
}

func (s *certificateService) bumpRemoteDesiredRevision(ctx context.Context, agentID string, revision int) error {
	if s.cfg.EnableLocalAgent && agentID == s.cfg.LocalAgentID {
		return nil
	}

	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ID != agentID {
			continue
		}
		snapshot, err := s.store.LoadAgentSnapshot(ctx, row.ID, storage.AgentSnapshotInput{
			DesiredVersion:  row.DesiredVersion,
			DesiredRevision: row.DesiredRevision,
			CurrentRevision: row.CurrentRevision,
			Platform:        row.Platform,
		})
		if err != nil {
			return err
		}
		nextRevision := revision
		if int(snapshot.Revision) > nextRevision {
			nextRevision = int(snapshot.Revision)
		}
		if row.DesiredRevision < nextRevision {
			row.DesiredRevision = nextRevision
		}
		return s.store.SaveAgent(ctx, row)
	}
	return ErrAgentNotFound
}

func managedCertificateMutationRevision(current ManagedCertificate, maxRevision int, bumpRevision bool) int {
	if !bumpRevision {
		return current.Revision
	}
	return maxRevision + 1
}

func (s *certificateService) certificateMutationRevision(fallback int) int {
	return maxConfigMutationRevision(s.revisionNumbers, fallback)
}

func unionManagedCertificateAgentIDs(previous []string, next []string) []string {
	seen := make(map[string]struct{}, len(previous)+len(next))
	combined := make([]string, 0, len(previous)+len(next))
	for _, values := range [][]string{previous, next} {
		for _, value := range values {
			trimmed := strings.TrimSpace(value)
			if trimmed == "" {
				continue
			}
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			combined = append(combined, trimmed)
		}
	}
	return combined
}

func differenceManagedCertificateAgentIDs(previous []string, next []string) []string {
	nextSet := make(map[string]struct{}, len(next))
	for _, value := range next {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		nextSet[trimmed] = struct{}{}
	}

	removed := make([]string, 0, len(previous))
	seen := make(map[string]struct{}, len(previous))
	for _, value := range previous {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := nextSet[trimmed]; ok {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		removed = append(removed, trimmed)
	}
	return removed
}

func (s *certificateService) assertManagedCertificateManualIssueAllowed(cert ManagedCertificate) error {
	if cert.IssuerMode != "master_cf_dns" {
		return fmt.Errorf("%w: certificate is not configured for master_cf_dns", ErrInvalidArgument)
	}
	if !cert.Enabled {
		return fmt.Errorf("%w: certificate is disabled", ErrInvalidArgument)
	}
	if cert.Scope != "domain" {
		return fmt.Errorf("%w: only domain certificates can be managed by master", ErrInvalidArgument)
	}
	if cert.CertificateType != "acme" {
		return fmt.Errorf("%w: master_cf_dns only manages acme certificates", ErrInvalidArgument)
	}
	if err := assertManagedCertificateTargetingAllowed(s.cfg, cert); err != nil {
		return err
	}
	return nil
}

func resolveManagedCertificateIssueMaterial(cert ManagedCertificate, result managedCertificateRenewalResult) (storage.ManagedCertificateBundle, error) {
	bundle := result.Material
	bundle.Domain = cert.Domain
	bundle.CertPEM = strings.TrimSpace(bundle.CertPEM)
	bundle.KeyPEM = strings.TrimSpace(bundle.KeyPEM)
	if bundle.CertPEM == "" || bundle.KeyPEM == "" {
		return storage.ManagedCertificateBundle{}, fmt.Errorf("%w: issuer did not return certificate material", ErrInvalidArgument)
	}
	if err := validateUploadedManagedCertificateBundle(bundle); err != nil {
		return storage.ManagedCertificateBundle{}, err
	}
	return bundle, nil
}

func (s *certificateService) failManagedCertificateIssue(ctx context.Context, rows []storage.ManagedCertificateRow, targetIndex int, current ManagedCertificate, maxRevision int, issueErr error, bumpRevision bool) (ManagedCertificate, error) {
	if err := requireConfigMutationStore(s.store, s.mutationExecutor, s.revisionMutation); err != nil {
		return ManagedCertificate{}, err
	}
	expectedGeneration := managedCertificateGenerationFor(current)
	freshRows, fresh, freshIndex, matched, err := s.loadManagedCertificateGeneration(ctx, current.ID, expectedGeneration)
	if err != nil {
		return ManagedCertificate{}, err
	}
	if !matched {
		return fresh, nil
	}
	rows, current, targetIndex = freshRows, fresh, freshIndex
	if s.mutationExecutor == nil || s.revisionMutation {
		_, err := s.persistManagedCertificateIssueFailureLegacy(ctx, rows, targetIndex, current, maxRevision, issueErr, bumpRevision)
		if err != nil {
			return ManagedCertificate{}, err
		}
		return ManagedCertificate{}, issueErr
	}
	targetAgentIDs, err := s.certificateMutationTargetAgentIDs(ctx, current)
	if err != nil {
		return ManagedCertificate{}, err
	}
	postCommitActions := make([]func(), 0)
	rollbackActions := make([]func() error, 0)
	persisted := false
	stale := current
	_, err = s.mutationExecutor.Execute(ctx, revision.MutationRequest{
		Kind:             "certificate.issue.failure",
		DependencyAction: revision.DependencyActionApply,
		Request: map[string]any{
			"id": current.ID, "domain": current.Domain,
			"error_class": classifyManagedCertificateIssueError(issueErr),
		},
		Targets:       configMutationTargets(s.cfg, targetAgentIDs, nil),
		ResourceState: managedCertificateMutationResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			freshRows, loadErr := tx.ListManagedCertificates(ctx)
			if loadErr != nil {
				return loadErr
			}
			fresh, freshIndex, found := findManagedCertificateByID(freshRows, current.ID)
			if !found || !expectedGeneration.Matches(fresh) || !managedCertificateEligibleForBackgroundIssue(fresh) {
				stale = fresh
				return nil
			}
			txService := s.certificateRevisionTransactionService(tx, revisions, &postCommitActions, &rollbackActions)
			persisted = true
			_, loadErr = txService.persistManagedCertificateIssueFailureLegacy(
				ctx, freshRows, freshIndex, fresh,
				highestManagedCertificateRevisionForService(freshRows), issueErr, true,
			)
			return loadErr
		},
	})
	if err != nil {
		return ManagedCertificate{}, certificateMutationRollbackError(err, rollbackActions)
	}
	if !persisted {
		return stale, nil
	}
	runConfigPostCommitActions(postCommitActions)
	return ManagedCertificate{}, issueErr
}

func (s *certificateService) persistManagedCertificateIssueFailureLegacy(ctx context.Context, rows []storage.ManagedCertificateRow, targetIndex int, current ManagedCertificate, maxRevision int, issueErr error, bumpRevision bool) (ManagedCertificate, error) {
	failed := current
	failed.Status = "error"
	failed.LastError = issueErr.Error()
	failed.Revision = s.certificateMutationRevision(managedCertificateMutationRevision(current, maxRevision, bumpRevision))

	// Record failure backoff so retries space out and respect Let's Encrypt's
	// 5-failed-validations-per-hour-per-hostname limit. The class is derived from the error
	// text (lego's error types are not stable across releases), and the next attempt is
	// scheduled via exponential backoff capped per class. The renewal loop (cert_renewal.go)
	// reads NextRetryAtUnix to decide when this certificate is a retry candidate again.
	class := classifyManagedCertificateIssueError(issueErr)
	retryAfter := extractManagedCertificateRetryAfter(issueErr)
	failed.BackoffClass = class
	failed.RetryCount = current.RetryCount + 1
	failed.NextRetryAtUnix = s.now().Add(managedCertificateBackoffDelay(class, retryAfter, failed.RetryCount)).Unix()

	nextRows := append([]storage.ManagedCertificateRow(nil), rows...)
	originalRows := append([]storage.ManagedCertificateRow(nil), rows...)
	nextRows[targetIndex] = managedCertificateToRow(failed)
	if err := s.store.SaveManagedCertificates(ctx, nextRows); err != nil {
		return ManagedCertificate{}, err
	}
	s.cleanupManagedCertificateMaterialAfterMutation(ctx, originalRows, nextRows)
	return failed, nil
}

// scheduleManagedCertificateIssue persists the certificate as "issuing" with the appropriate
// revision semantics, notifies removed agents, dispatches a background issuance via the
// process-wide dispatcher, and returns the issuing certificate. It replaces the historical
// synchronous master_cf_dns issue path in Create/Update/Issue so those entry points no longer
// block the HTTP request on the ACME order. All eligibility checks still run synchronously
// (fail-fast) before anything is persisted.
func (s *certificateService) scheduleManagedCertificateIssue(ctx context.Context, rows []storage.ManagedCertificateRow, targetIndex int, current ManagedCertificate, maxRevision int, bumpRevision bool, removedAgentIDs []string) (ManagedCertificate, error) {
	if err := s.assertCertificateDistributionTargetsAllowed(ctx, current); err != nil {
		return ManagedCertificate{}, err
	}
	if err := s.assertManagedCertificateManualIssueAllowed(current); err != nil {
		return ManagedCertificate{}, err
	}
	if !s.managedCertificateIssuerAvailable() {
		return ManagedCertificate{}, fmt.Errorf("%w: managed certificates require ACME_DNS_PROVIDER=cf and CF_Token", ErrInvalidArgument)
	}

	scheduled := current
	scheduled.Status = "issuing"
	scheduled.LastError = ""
	// A fresh submit resets any prior failure backoff so the first attempt is immediate.
	scheduled.BackoffClass = ""
	scheduled.RetryCount = 0
	scheduled.NextRetryAtUnix = 0
	scheduled.Revision = s.certificateMutationRevision(managedCertificateMutationRevision(current, maxRevision, bumpRevision))

	originalRows := append([]storage.ManagedCertificateRow(nil), rows...)
	rows[targetIndex] = managedCertificateToRow(scheduled)
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		return ManagedCertificate{}, err
	}
	s.cleanupManagedCertificateMaterialAfterMutation(ctx, originalRows, rows)
	if len(removedAgentIDs) > 0 {
		if err := s.syncManagedCertificateAgentIDs(ctx, removedAgentIDs, scheduled.Revision); err != nil {
			return ManagedCertificate{}, err
		}
	}
	s.runAfterRevisionCommit(func() {
		ManagedCertificateDispatcher().Submit(scheduled.ID)
	})
	return scheduled, nil
}

// managedCertificateIssuerAvailable reports whether a master_cf_dns issuer can be constructed
// for this service: an injected renewal issuer, or the env-configured Cloudflare DNS issuer when
// ManagedDNSCertificatesEnabled. Used to fail-fast at submit time instead of after dispatch.
func (s *certificateService) managedCertificateIssuerAvailable() bool {
	if s.renewalIssuer != nil {
		return true
	}
	return s.cfg.ManagedDNSCertificatesEnabled && newMasterCFDNSManagedCertificateIssuer() != nil
}

// ManagedCertificateBackgroundSigner returns the background issuance function injected into the
// process-wide dispatcher. It opens a short-lived store (independent of any HTTP request's store
// or connection), reloads the certificate, and — if it is still eligible for issuance — runs
// issueManagedCertificateInBackground, which acquires issuanceLock, performs the ACME
// issue, and persists the outcome (active on success, error + backoff on failure). The production
// Cloudflare DNS issuer is built from cfg inside the issue body.
func ManagedCertificateBackgroundSigner(cfg config.Config, openStore func() (storage.Store, error), localApplyTrigger func(context.Context) error) managedCertificateSignFunc {
	return managedCertificateBackgroundSignerWithIssuer(cfg, openStore, nil, localApplyTrigger)
}

// managedCertificateBackgroundSignerWithIssuer is the testable core: tests inject a fake issuer
// while production passes nil so the Cloudflare DNS issuer is built from cfg on demand.
func managedCertificateBackgroundSignerWithIssuer(cfg config.Config, openStore func() (storage.Store, error), issuer managedCertificateRenewalIssuer, localApplyTrigger func(context.Context) error) managedCertificateSignFunc {
	return func(ctx context.Context, certID int) error {
		store, err := openStore()
		if err != nil {
			return err
		}
		// storage.Store has no Close(); close concrete stores (production *GormStore) when present
		// while staying compatible with in-memory test stores that omit Close.
		if closer, ok := store.(interface{ Close() error }); ok {
			defer func() { _ = closer.Close() }()
		}

		svc := newCertificateServiceWithRenewal(cfg, store, issuer)
		svc.SetLocalApplyTrigger(localApplyTrigger)
		rows, err := store.ListManagedCertificates(ctx)
		if err != nil {
			return err
		}
		current, targetIndex, ok := findManagedCertificateByID(rows, certID)
		if !ok {
			// Certificate was deleted while issuance was pending; nothing to do.
			return nil
		}
		if !managedCertificateEligibleForBackgroundIssue(current) {
			// Status changed (disabled, finalized by another path, etc.); leave it alone.
			return nil
		}
		maxRevision := 0
		for _, row := range rows {
			if row.Revision > maxRevision {
				maxRevision = row.Revision
			}
		}
		_, err = svc.issueManagedCertificateInBackground(ctx, rows, targetIndex, current, maxRevision)
		return err
	}
}

// managedCertificateEligibleForBackgroundIssue reports whether a certificate row should be
// (re)issued by the background signer. It guards against stale dispatches: only certificates
// persisted as "issuing" that are still enabled master_cf_dns ACME domain certs proceed.
func managedCertificateEligibleForBackgroundIssue(cert ManagedCertificate) bool {
	return cert.Status == "issuing" &&
		cert.Enabled &&
		cert.Scope == "domain" &&
		cert.IssuerMode == "master_cf_dns" &&
		cert.CertificateType == "acme"
}

// Failure backoff classes. Defaulting unknown errors to "persistent" keeps retries conservative
// so transient-looking but actually-durable failures do not burn Let's Encrypt validation quota.
const (
	managedCertificateBackoffClassTransient   = "transient"
	managedCertificateBackoffClassPersistent  = "persistent"
	managedCertificateBackoffClassRateLimited = "rate_limited"

	managedCertificateBackoffTransientBase  = 5 * time.Second
	managedCertificateBackoffTransientCap   = 5 * time.Minute
	managedCertificateBackoffPersistentBase = time.Hour
	managedCertificateBackoffPersistentCap  = 32 * time.Hour
	managedCertificateBackoffRateLimitedMin = time.Hour
	managedCertificateBackoffRateLimitedCap = 32 * time.Hour

	// managedCertificateBackoffMaxShift bounds exponential growth (base<<shift) so delays stay
	// within class caps and never overflow for large retry counts.
	managedCertificateBackoffMaxShift = 6
)

// classifyManagedCertificateIssueError maps an ACME/issuer failure to a backoff class. The
// heuristic is intentionally string-based because lego's error types are not stable across
// releases; durable misconfiguration (auth, validation, quota) is treated as persistent so
// retries do not burn LE's 5-failed-validations/hour/hostname limit.
func classifyManagedCertificateIssueError(err error) string {
	if err == nil {
		return managedCertificateBackoffClassTransient
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "rate-limit") ||
		strings.Contains(msg, "too many") ||
		strings.Contains(msg, "retry-after"):
		return managedCertificateBackoffClassRateLimited
	case managedCertificateTransientIssueMessage(msg):
		return managedCertificateBackoffClassTransient
	default:
		return managedCertificateBackoffClassPersistent
	}
}

func managedCertificateTransientIssueMessage(msg string) bool {
	markers := []string{
		"timeout", "timed out", "connection reset", "connection refused",
		"connection closed", "no such host", "temporary", "i/o timeout",
		"eof", "server misbehaving", "502", "503", "504", "service unavailable",
	}
	for _, marker := range markers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// extractManagedCertificateRetryAfter best-effort parses an ACME Retry-After value (seconds) from
// an error message. lego does not reliably surface Retry-After as a structured field across
// releases, so this scans the error text; callers fall back to the class curve when it returns 0.
func extractManagedCertificateRetryAfter(err error) time.Duration {
	if err == nil {
		return 0
	}
	msg := strings.ToLower(err.Error())
	idx := strings.Index(msg, "retry-after")
	if idx < 0 {
		return 0
	}
	rest := strings.TrimLeft(msg[idx+len("retry-after"):], " :=\t")
	digits := strings.Builder{}
	for _, r := range rest {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
			continue
		}
		break
	}
	if digits.Len() == 0 {
		return 0
	}
	seconds, err := strconv.Atoi(digits.String())
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// managedCertificateBackoffDelay computes the delay before the next attempt for a failed
// issuance. It is a pure function of (class, retryAfter, retryCount) so it is fully testable:
// exponential growth base<<shift capped per class, plus deterministic jitter (spread by
// retryCount) so simultaneously-failed certificates do not retry in lockstep without introducing
// randomness. The renewal loop also serializes retries, bounding herd risk further.
func managedCertificateBackoffDelay(class string, retryAfter time.Duration, retryCount int) time.Duration {
	base, capDelay := managedCertificateBackoffClassBaseAndCap(class, retryAfter)

	shift := retryCount - 1
	if shift < 0 {
		shift = 0
	}
	if shift > managedCertificateBackoffMaxShift {
		shift = managedCertificateBackoffMaxShift
	}
	delay := base << uint(shift)
	if delay <= 0 || delay > capDelay {
		delay = capDelay
	}

	jitter := delay / 4 * time.Duration(managedCertificateBackoffJitterFraction(retryCount))
	if maxJitter := capDelay / 4; jitter > maxJitter {
		jitter = maxJitter
	}
	return delay + jitter
}

// managedCertificateBackoffJitterFraction returns 0..3 (quarters of the delay) deterministically
// from the attempt count, spreading retries without randomness.
func managedCertificateBackoffJitterFraction(retryCount int) int64 {
	switch retryCount % 4 {
	case 1:
		return 1
	case 2:
		return 2
	case 3:
		return 3
	default:
		return 0
	}
}

func managedCertificateBackoffClassBaseAndCap(class string, retryAfter time.Duration) (time.Duration, time.Duration) {
	switch class {
	case managedCertificateBackoffClassTransient:
		return managedCertificateBackoffTransientBase, managedCertificateBackoffTransientCap
	case managedCertificateBackoffClassRateLimited:
		base := retryAfter
		if base <= 0 || base < managedCertificateBackoffRateLimitedMin {
			base = managedCertificateBackoffRateLimitedMin
		}
		return base, managedCertificateBackoffRateLimitedCap
	default:
		return managedCertificateBackoffPersistentBase, managedCertificateBackoffPersistentCap
	}
}

func (s *certificateService) certificateByID(ctx context.Context, agentID string, certificateID int) (ManagedCertificate, error) {
	rows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return ManagedCertificate{}, err
	}
	for _, row := range rows {
		certificate := managedCertificateFromRow(row)
		if certificate.ID != certificateID {
			continue
		}
		if strings.TrimSpace(agentID) != "" && !containsString(certificate.TargetAgentIDs, agentID) {
			continue
		}
		return certificate, nil
	}
	return ManagedCertificate{}, ErrCertificateNotFound
}

func (s *certificateService) certificateMutationTargetAgentIDs(ctx context.Context, certificates ...ManagedCertificate) ([]string, error) {
	knownAgentIDs, err := allKnownAgentIDs(ctx, s.cfg, s.store)
	if err != nil {
		return nil, err
	}
	targetAgentIDs := make([]string, 0)
	certificateIDs := make(map[int]struct{}, len(certificates))
	for _, certificate := range certificates {
		if certificate.ID > 0 {
			certificateIDs[certificate.ID] = struct{}{}
		}
		targetAgentIDs = append(targetAgentIDs, certificate.TargetAgentIDs...)
	}

	referencedListenerIDs := make(map[int]struct{})
	listenerRows, err := s.store.ListRelayListeners(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, listener := range listenerRows {
		referencesCertificate := listener.CertificateID != nil
		if referencesCertificate {
			_, referencesCertificate = certificateIDs[*listener.CertificateID]
		}
		if !referencesCertificate {
			for _, trustedCertificateID := range parseIntArray(listener.TrustedCACertificateIDs) {
				if _, ok := certificateIDs[trustedCertificateID]; ok {
					referencesCertificate = true
					break
				}
			}
		}
		if !referencesCertificate {
			continue
		}
		targetAgentIDs = append(targetAgentIDs, listener.AgentID)
		if listener.ID > 0 {
			referencedListenerIDs[listener.ID] = struct{}{}
		}
	}

	for _, candidate := range knownAgentIDs {
		httpRows, err := s.store.ListHTTPRules(ctx, candidate)
		if err != nil {
			return nil, err
		}
		candidateAffected := false
		for _, certificate := range certificates {
			if hasMatchingHTTPSRuleForCertificateInRows(httpRows, certificate) {
				candidateAffected = true
				break
			}
		}
		for _, row := range httpRows {
			for listenerID := range referencedListenerIDs {
				if row.Enabled && relayLayersReferenceListener(row.RelayLayersJSON, listenerID) {
					candidateAffected = true
					break
				}
			}
		}
		l4Rows, err := s.store.ListL4Rules(ctx, candidate)
		if err != nil {
			return nil, err
		}
		for _, row := range l4Rows {
			for listenerID := range referencedListenerIDs {
				if row.Enabled && relayLayersReferenceListener(row.RelayLayersJSON, listenerID) {
					candidateAffected = true
					break
				}
			}
		}
		if candidateAffected {
			targetAgentIDs = append(targetAgentIDs, candidate)
		}
	}

	targetAgentIDs = uniqueAgentIDs(targetAgentIDs)
	if len(targetAgentIDs) == 0 {
		if s.cfg.EnableLocalAgent && strings.TrimSpace(s.cfg.LocalAgentID) != "" {
			targetAgentIDs = append(targetAgentIDs, s.cfg.LocalAgentID)
		} else if len(knownAgentIDs) > 0 {
			targetAgentIDs = append(targetAgentIDs, knownAgentIDs[0])
		}
	}
	if len(targetAgentIDs) == 0 {
		return nil, ErrAgentNotFound
	}
	return expandConfigDependencyAgentIDs(ctx, s.store, targetAgentIDs)
}

func managedCertificateMutationResourceState(ctx context.Context, tx *storage.GormStore, _ revision.Target) (any, error) {
	rows, err := tx.ListManagedCertificates(ctx)
	if err != nil {
		return nil, err
	}
	for index := range rows {
		rows[index].Revision = 0
	}
	return rows, nil
}

func (s *certificateService) ensureAgentExists(ctx context.Context, agentID string) (string, error) {
	resolvedID := strings.TrimSpace(agentID)
	if resolvedID == "" {
		resolvedID = s.cfg.LocalAgentID
	}
	if s.cfg.EnableLocalAgent && resolvedID == s.cfg.LocalAgentID {
		return resolvedID, nil
	}

	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		if row.ID == resolvedID {
			return resolvedID, nil
		}
	}
	return "", ErrAgentNotFound
}

func (s *certificateService) assertCertificateDistributionTargetsAllowed(ctx context.Context, cert ManagedCertificate) error {
	if !cert.Enabled || cert.IssuerMode != "local_http01" || cert.CertificateType != "uploaded" {
		return nil
	}
	for _, targetAgentID := range cert.TargetAgentIDs {
		_, displayName, capabilities, err := s.resolveCertificateTarget(ctx, targetAgentID)
		if errors.Is(err, ErrAgentNotFound) {
			return fmt.Errorf("%w: target agent not found: %s", ErrInvalidArgument, strings.TrimSpace(targetAgentID))
		}
		if err != nil {
			return err
		}
		if !agentHasCapability(capabilities, "cert_install") {
			return fmt.Errorf("%w: target agent does not support certificate install: %s", ErrInvalidArgument, displayName)
		}
	}
	return nil
}

func (s *certificateService) resolveCertificateTarget(ctx context.Context, agentID string) (string, string, []string, error) {
	resolvedID := strings.TrimSpace(agentID)
	if resolvedID == "" {
		return "", "", nil, ErrAgentNotFound
	}
	if s.cfg.EnableLocalAgent && resolvedID == s.cfg.LocalAgentID {
		return resolvedID, resolvedID, append([]string(nil), defaultLocalCapabilities...), nil
	}

	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return "", "", nil, err
	}
	for _, row := range rows {
		if row.ID == resolvedID {
			displayName := resolvedID
			if strings.TrimSpace(row.Name) != "" {
				displayName = strings.TrimSpace(row.Name)
			}
			return resolvedID, displayName, parseStringArray(row.CapabilitiesJSON), nil
		}
	}
	return "", "", nil, ErrAgentNotFound
}

func (s *certificateService) resolveUploadedMaterialForMutation(ctx context.Context, input ManagedCertificateInput, next ManagedCertificate, previous *ManagedCertificate) (storage.ManagedCertificateBundle, bool, error) {
	if next.CertificateType != "uploaded" {
		return storage.ManagedCertificateBundle{}, false, nil
	}

	hasCertificate := input.CertificatePEM != nil
	hasKey := input.PrivateKeyPEM != nil
	hasCA := input.CAPEM != nil
	certificatePEM := normalizeUploadedPEMField(input.CertificatePEM)
	privateKeyPEM := normalizeUploadedPEMField(input.PrivateKeyPEM)
	caPEM := normalizeUploadedPEMField(input.CAPEM)
	certificateFromPreviousRaw := false
	caFromPreviousRaw := false

	if previous == nil {
		joinedCertificatePEM := joinUploadedCertificatePEM(certificatePEM, caPEM)
		bundle := storage.ManagedCertificateBundle{
			Domain:  next.Domain,
			CertPEM: joinedCertificatePEM,
			KeyPEM:  privateKeyPEM,
		}
		if err := validateUploadedManagedCertificateBundle(bundle); err != nil {
			return storage.ManagedCertificateBundle{}, false, err
		}
		return bundle, true, nil
	}

	if !hasCertificate || !hasKey || !hasCA {
		previousMaterial, ok, err := s.store.LoadManagedCertificateMaterial(ctx, previous.Domain)
		if err != nil {
			return storage.ManagedCertificateBundle{}, false, err
		}
		if !ok {
			return storage.ManagedCertificateBundle{}, false, fmt.Errorf("%w: certificate_pem is required for uploaded certificates", ErrInvalidArgument)
		}
		previousLeafPEM, previousCAPEM, splitErr := splitUploadedCertificatePEM(previousMaterial.CertPEM)
		if splitErr != nil {
			return storage.ManagedCertificateBundle{}, false, splitErr
		}
		if !hasCertificate {
			certificatePEM = previousLeafPEM
			certificateFromPreviousRaw = true
		}
		if !hasKey {
			privateKeyPEM = previousMaterial.KeyPEM
		}
		if !hasCA {
			caPEM = previousCAPEM
			caFromPreviousRaw = true
		}
	}

	joinedCertificatePEM := ""
	switch {
	case certificateFromPreviousRaw && caFromPreviousRaw:
		joinedCertificatePEM = certificatePEM + caPEM
	case certificateFromPreviousRaw:
		if strings.TrimSpace(caPEM) == "" {
			joinedCertificatePEM = certificatePEM
		} else {
			joinedCertificatePEM = certificatePEM + "\n" + strings.TrimSpace(caPEM)
		}
	case caFromPreviousRaw:
		if strings.TrimSpace(certificatePEM) == "" {
			joinedCertificatePEM = caPEM
		} else {
			joinedCertificatePEM = strings.TrimSpace(certificatePEM) + caPEM
		}
	default:
		joinedCertificatePEM = joinUploadedCertificatePEM(certificatePEM, caPEM)
	}
	bundle := storage.ManagedCertificateBundle{
		Domain:  next.Domain,
		CertPEM: joinedCertificatePEM,
		KeyPEM:  privateKeyPEM,
	}
	if err := validateUploadedManagedCertificateBundle(bundle); err != nil {
		return storage.ManagedCertificateBundle{}, false, err
	}
	return bundle, true, nil
}

func normalizeManagedCertificateInput(input ManagedCertificateInput, fallback ManagedCertificate, suggestedID int, defaultAgentID string, allowEmptyTargets bool) (ManagedCertificate, error) {
	id := fallback.ID
	if input.ID != nil && *input.ID > 0 {
		id = *input.ID
	}
	if id <= 0 {
		id = suggestedID
	}

	domain := strings.TrimSpace(pointerString(input.Domain))
	if domain == "" {
		domain = strings.TrimSpace(fallback.Domain)
	}
	if domain == "" {
		return ManagedCertificate{}, fmt.Errorf("%w: domain must be a valid domain or IP", ErrInvalidArgument)
	}

	enabled := true
	if fallback.ID > 0 {
		enabled = fallback.Enabled
	}
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	scope := strings.TrimSpace(pointerString(input.Scope))
	if scope == "" {
		scope = fallback.Scope
	}
	if scope == "" {
		scope = "domain"
	}
	if scope != "domain" && scope != "ip" {
		return ManagedCertificate{}, fmt.Errorf("%w: scope must be domain or ip", ErrInvalidArgument)
	}

	issuerMode := strings.TrimSpace(pointerString(input.IssuerMode))
	if issuerMode == "" {
		issuerMode = fallback.IssuerMode
	}
	if issuerMode == "" {
		issuerMode = "master_cf_dns"
	}
	if issuerMode != "master_cf_dns" && issuerMode != "local_http01" {
		return ManagedCertificate{}, fmt.Errorf("%w: issuer_mode must be master_cf_dns or local_http01", ErrInvalidArgument)
	}
	if scope == "ip" && issuerMode != "local_http01" {
		return ManagedCertificate{}, fmt.Errorf("%w: ip certificates must use local_http01", ErrInvalidArgument)
	}

	targetAgentIDs := append([]string(nil), fallback.TargetAgentIDs...)
	if input.TargetAgentIDs != nil {
		targetAgentIDs = normalizeTags(*input.TargetAgentIDs)
	}
	if len(targetAgentIDs) == 0 && !allowEmptyTargets {
		targetAgentIDs = []string{defaultAgentID}
	}
	if allowEmptyTargets && len(targetAgentIDs) == 0 {
		targetAgentIDs = []string{}
	}

	status := strings.TrimSpace(pointerString(input.Status))
	if status == "" {
		status = fallback.Status
	}
	if status == "" {
		status = "pending"
	}

	lastIssueAt := strings.TrimSpace(pointerString(input.LastIssueAt))
	if lastIssueAt == "" {
		lastIssueAt = fallback.LastIssueAt
	}

	lastError := strings.TrimSpace(pointerString(input.LastError))
	if lastError == "" {
		lastError = fallback.LastError
	}

	materialHash := strings.TrimSpace(pointerString(input.MaterialHash))
	if materialHash == "" {
		materialHash = fallback.MaterialHash
	}

	agentReports := fallback.AgentReports
	if agentReports == nil {
		agentReports = map[string]ManagedCertificateAgentReport{}
	}
	if input.AgentReports != nil {
		agentReports = *input.AgentReports
	}

	acmeInfo := fallback.ACMEInfo
	if input.ACMEInfo != nil {
		acmeInfo = *input.ACMEInfo
	}

	tags := append([]string(nil), fallback.Tags...)
	if input.Tags != nil {
		tags = normalizeTags(*input.Tags)
	}

	usage := strings.TrimSpace(pointerString(input.Usage))
	if usage == "" {
		usage = fallback.Usage
	}
	if usage == "" {
		usage = "https"
	}
	switch usage {
	case "https", "relay_tunnel", "relay_ca", "mixed":
	default:
		return ManagedCertificate{}, fmt.Errorf("%w: usage must be https, relay_tunnel, relay_ca, or mixed", ErrInvalidArgument)
	}

	certificateType := strings.TrimSpace(pointerString(input.CertificateType))
	if certificateType == "" {
		certificateType = fallback.CertificateType
	}
	if certificateType == "" {
		certificateType = "acme"
	}
	switch certificateType {
	case "acme", "uploaded", "internal_ca":
	default:
		return ManagedCertificate{}, fmt.Errorf("%w: certificate_type must be acme, uploaded, or internal_ca", ErrInvalidArgument)
	}

	selfSigned := fallback.SelfSigned
	if input.SelfSigned != nil {
		selfSigned = *input.SelfSigned
	}

	notAfter := strings.TrimSpace(pointerString(input.NotAfter))
	if notAfter == "" {
		notAfter = fallback.NotAfter
	}

	return ManagedCertificate{
		ID:              id,
		Domain:          domain,
		Enabled:         enabled,
		Scope:           scope,
		IssuerMode:      issuerMode,
		TargetAgentIDs:  targetAgentIDs,
		Status:          status,
		LastIssueAt:     lastIssueAt,
		LastError:       lastError,
		MaterialHash:    materialHash,
		AgentReports:    agentReports,
		ACMEInfo:        acmeInfo,
		Tags:            tags,
		Usage:           usage,
		CertificateType: certificateType,
		SelfSigned:      selfSigned,
		Revision:        fallback.Revision,
		NextRetryAtUnix: fallback.NextRetryAtUnix,
		RetryCount:      fallback.RetryCount,
		BackoffClass:    fallback.BackoffClass,
		NotAfter:        notAfter,
	}, nil
}

func managedCertificateFromRow(row storage.ManagedCertificateRow) ManagedCertificate {
	cert := ManagedCertificate{
		ID:              row.ID,
		Domain:          row.Domain,
		Enabled:         row.Enabled,
		Scope:           defaultString(row.Scope, "domain"),
		IssuerMode:      defaultString(row.IssuerMode, "master_cf_dns"),
		Status:          defaultString(row.Status, "pending"),
		LastIssueAt:     row.LastIssueAt,
		LastError:       row.LastError,
		MaterialHash:    row.MaterialHash,
		Tags:            parseStringArray(row.TagsJSON),
		Usage:           defaultString(row.Usage, "https"),
		CertificateType: defaultString(row.CertificateType, "acme"),
		SelfSigned:      row.SelfSigned,
		Revision:        row.Revision,
		NextRetryAtUnix: row.NextRetryAtUnix,
		RetryCount:      row.RetryCount,
		BackoffClass:    row.BackoffClass,
		NotAfter:        row.NotAfter,
		AgentReports:    map[string]ManagedCertificateAgentReport{},
	}
	cert.TargetAgentIDs = parseStringArray(row.TargetAgentIDs)
	_ = json.Unmarshal([]byte(defaultString(row.AgentReports, "{}")), &cert.AgentReports)
	_ = json.Unmarshal([]byte(defaultString(row.ACMEInfo, "{}")), &cert.ACMEInfo)
	return cert
}

func managedCertificateToRow(cert ManagedCertificate) storage.ManagedCertificateRow {
	return storage.ManagedCertificateRow{
		ID:              cert.ID,
		Domain:          cert.Domain,
		Enabled:         cert.Enabled,
		Scope:           cert.Scope,
		IssuerMode:      cert.IssuerMode,
		TargetAgentIDs:  marshalJSON(cert.TargetAgentIDs, "[]"),
		Status:          cert.Status,
		LastIssueAt:     cert.LastIssueAt,
		LastError:       cert.LastError,
		MaterialHash:    cert.MaterialHash,
		AgentReports:    marshalJSON(cert.AgentReports, "{}"),
		ACMEInfo:        marshalJSON(cert.ACMEInfo, "{}"),
		Usage:           cert.Usage,
		CertificateType: cert.CertificateType,
		SelfSigned:      cert.SelfSigned,
		TagsJSON:        marshalJSON(cert.Tags, "[]"),
		Revision:        cert.Revision,
		NextRetryAtUnix: cert.NextRetryAtUnix,
		RetryCount:      cert.RetryCount,
		BackoffClass:    cert.BackoffClass,
		NotAfter:        cert.NotAfter,
	}
}

func managedCertificateNotAfterFromPEM(certPEM string, fallback string) string {
	leaf, err := parseManagedCertificateLeaf([]byte(strings.TrimSpace(certPEM)))
	if err != nil {
		return fallback
	}
	return leaf.NotAfter.UTC().Format(time.RFC3339)
}

func overlayManagedCertificateForAgent(cert ManagedCertificate, agentID string) ManagedCertificate {
	report, ok := cert.AgentReports[agentID]
	if !ok {
		return cert
	}
	cert.Status = coalesceString(report.Status, cert.Status)
	// Agent reports only carry last_issue_at when they have observed an issuance;
	// an empty value must not erase the master-known timestamp.
	cert.LastIssueAt = coalesceString(report.LastIssueAt, cert.LastIssueAt)
	cert.LastError = report.LastError
	cert.MaterialHash = report.MaterialHash
	cert.NotAfter = coalesceString(report.NotAfter, cert.NotAfter)
	cert.ACMEInfo = report.ACMEInfo
	return cert
}

func normalizeManagedCertificateHeartbeatReports(reports []ManagedCertificateHeartbeatReport) []ManagedCertificateHeartbeatReport {
	normalized := make([]ManagedCertificateHeartbeatReport, 0, len(reports))
	for _, report := range reports {
		next := ManagedCertificateHeartbeatReport{
			Domain:       normalizeCertificateReportHost(report.Domain),
			Status:       normalizeManagedCertificateReportStatus(report.Status),
			LastIssueAt:  normalizeOptionalTimestamp(report.LastIssueAt),
			LastError:    report.LastError,
			MaterialHash: strings.TrimSpace(report.MaterialHash),
			NotAfter:     normalizeOptionalTimestamp(report.NotAfter),
			ACMEInfo:     report.ACMEInfo,
			UpdatedAt:    normalizeOptionalTimestamp(report.UpdatedAt),
		}
		if report.ID > 0 {
			next.ID = report.ID
		}
		if next.ID <= 0 && next.Domain == "" {
			continue
		}
		normalized = append(normalized, next)
	}
	return normalized
}

func applyManagedCertificateHeartbeatReports(rows []storage.ManagedCertificateRow, agentID string, reports []ManagedCertificateHeartbeatReport, now time.Time) ([]storage.ManagedCertificateRow, map[int]struct{}, bool) {
	if strings.TrimSpace(agentID) == "" || len(reports) == 0 {
		return rows, map[int]struct{}{}, false
	}

	reportsByID := make(map[int]ManagedCertificateHeartbeatReport, len(reports))
	reportsByDomain := make(map[string]ManagedCertificateHeartbeatReport, len(reports))
	for _, report := range normalizeManagedCertificateHeartbeatReports(reports) {
		if report.ID > 0 {
			reportsByID[report.ID] = report
		}
		if report.Domain != "" {
			reportsByDomain[report.Domain] = report
		}
	}

	reportedCertIDs := make(map[int]struct{}, len(reportsByID))
	changed := false
	nextRows := append([]storage.ManagedCertificateRow(nil), rows...)
	for index, row := range nextRows {
		cert := managedCertificateFromRow(row)
		isLocalReport := cert.IssuerMode == "local_http01" && containsString(cert.TargetAgentIDs, agentID)
		isMasterReport := cert.IssuerMode == "master_cf_dns"
		if !isLocalReport && !isMasterReport {
			continue
		}
		report, ok := findManagedCertificateHeartbeatReport(cert, reportsByID, reportsByDomain)
		if !ok {
			continue
		}
		reportedCertIDs[cert.ID] = struct{}{}
		next := updateManagedCertificateAgentReport(cert, agentID, report, now)
		if isLocalReport && len(cert.TargetAgentIDs) == 1 && cert.TargetAgentIDs[0] == agentID {
			next.Status = coalesceString(report.Status, cert.Status)
			next.LastIssueAt = coalesceString(report.LastIssueAt, cert.LastIssueAt)
			next.LastError = report.LastError
			next.MaterialHash = report.MaterialHash
			next.NotAfter = coalesceString(report.NotAfter, cert.NotAfter)
			next.ACMEInfo = report.ACMEInfo
		}
		if !managedCertificateEqual(cert, next) {
			nextRows[index] = managedCertificateToRow(next)
			changed = true
		}
	}
	return nextRows, reportedCertIDs, changed
}

func reconcileLocalHTTP01CertificatesForAgent(rows []storage.ManagedCertificateRow, agentID string, capabilities []string, rules []storage.HTTPRuleRow, applyRevision int, applyStatus string, applyMessage string, reportedCertIDs map[int]struct{}, now time.Time) ([]storage.ManagedCertificateRow, bool) {
	if strings.TrimSpace(agentID) == "" || applyRevision <= 0 {
		return rows, false
	}
	if !agentHasCapability(capabilities, "cert_install") || !agentHasCapability(capabilities, "local_acme") {
		return rows, false
	}
	status := strings.ToLower(strings.TrimSpace(applyStatus))
	if status != "success" && status != "error" {
		return rows, false
	}

	appliedAt := now.UTC().Format(time.RFC3339)
	changed := false
	nextRows := append([]storage.ManagedCertificateRow(nil), rows...)
	for index, row := range nextRows {
		cert := managedCertificateFromRow(row)
		if !cert.Enabled || cert.IssuerMode != "local_http01" || !containsString(cert.TargetAgentIDs, agentID) {
			continue
		}
		if _, ok := reportedCertIDs[cert.ID]; ok {
			continue
		}
		if cert.Revision > applyRevision || !hasMatchingHTTPSRuleForCertificateInRows(rules, cert) {
			continue
		}

		switch status {
		case "success":
			next := updateManagedCertificateAgentReport(cert, agentID, ManagedCertificateHeartbeatReport{
				Status:       "active",
				LastIssueAt:  appliedAt,
				LastError:    "",
				MaterialHash: cert.MaterialHash,
				ACMEInfo:     cert.ACMEInfo,
				UpdatedAt:    appliedAt,
			}, now)
			next.Status = "active"
			next.LastIssueAt = appliedAt
			next.LastError = ""
			if !managedCertificateEqual(cert, next) {
				nextRows[index] = managedCertificateToRow(next)
				changed = true
			}
		case "error":
			if cert.Status != "pending" {
				continue
			}
			message := coalesceString(strings.TrimSpace(applyMessage), "agent apply failed")
			next := cert
			next.Status = "error"
			next.LastError = message
			next = updateManagedCertificateAgentReport(next, agentID, ManagedCertificateHeartbeatReport{
				Status:       "error",
				LastIssueAt:  cert.LastIssueAt,
				LastError:    message,
				MaterialHash: cert.MaterialHash,
				ACMEInfo:     cert.ACMEInfo,
				UpdatedAt:    appliedAt,
			}, now)
			if !managedCertificateEqual(cert, next) {
				nextRows[index] = managedCertificateToRow(next)
				changed = true
			}
		}
	}
	return nextRows, changed
}

func findManagedCertificateHeartbeatReport(cert ManagedCertificate, reportsByID map[int]ManagedCertificateHeartbeatReport, reportsByDomain map[string]ManagedCertificateHeartbeatReport) (ManagedCertificateHeartbeatReport, bool) {
	if cert.ID > 0 {
		if report, ok := reportsByID[cert.ID]; ok {
			return report, true
		}
	}
	report, ok := reportsByDomain[normalizeCertificateReportHost(cert.Domain)]
	return report, ok
}

func updateManagedCertificateAgentReport(cert ManagedCertificate, agentID string, report ManagedCertificateHeartbeatReport, now time.Time) ManagedCertificate {
	reports := make(map[string]ManagedCertificateAgentReport, len(cert.AgentReports)+1)
	for existingAgentID, existingReport := range cert.AgentReports {
		reports[existingAgentID] = existingReport
	}
	cert.AgentReports = reports
	updatedAt := report.UpdatedAt
	if updatedAt == "" {
		updatedAt = now.UTC().Format(time.RFC3339)
	}
	existingReport := reports[strings.TrimSpace(agentID)]
	cert.AgentReports[strings.TrimSpace(agentID)] = ManagedCertificateAgentReport{
		Status:       report.Status,
		LastIssueAt:  coalesceString(report.LastIssueAt, existingReport.LastIssueAt),
		LastError:    report.LastError,
		MaterialHash: report.MaterialHash,
		NotAfter:     coalesceString(report.NotAfter, existingReport.NotAfter),
		ACMEInfo:     report.ACMEInfo,
		UpdatedAt:    updatedAt,
	}
	return cert
}

func hasMatchingHTTPSRuleForCertificateInRows(rows []storage.HTTPRuleRow, cert ManagedCertificate) bool {
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		target, ok := parseHTTPSRuleTarget(row.FrontendURL)
		if !ok {
			continue
		}
		if doesManagedCertificateMatchHost(cert, target) {
			return true
		}
	}
	return false
}

func parseHTTPSRuleTarget(frontendURL string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(frontendURL))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") {
		return "", false
	}
	return normalizeCertificateReportHost(parsed.Hostname()), true
}

func normalizeCertificateReportHost(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(trimmed); err == nil {
		trimmed = host
	}
	return strings.ToLower(normalizeCertificateHost(trimmed))
}

func normalizeOptionalTimestamp(value string) string {
	return strings.TrimSpace(value)
}

func normalizeManagedCertificateReportStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "pending", "active", "error", "issuing":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func managedCertificateEqual(left ManagedCertificate, right ManagedCertificate) bool {
	return reflect.DeepEqual(left, right)
}

func coalesceString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func assertManagedCertificateMutationAllowed(previous *ManagedCertificate, next ManagedCertificate) error {
	if previous != nil && isSystemRelayCACertificate(*previous) {
		return fmt.Errorf("%w: system relay ca is managed automatically", ErrInvalidArgument)
	}
	if isReservedSystemRelayCANext(next) {
		if usesReservedRelayCAIdentity(next) {
			return fmt.Errorf("%w: relay ca domain and system tag are reserved for the system relay ca", ErrInvalidArgument)
		}
		return fmt.Errorf("%w: relay ca certificates are managed automatically", ErrInvalidArgument)
	}
	return nil
}

func assertManagedCertificateTargetingAllowed(cfg config.Config, cert ManagedCertificate) error {
	if cert.IssuerMode != "master_cf_dns" {
		return nil
	}
	if cert.CertificateType != "acme" {
		return fmt.Errorf("%w: master_cf_dns certificates must use certificate_type=acme", ErrInvalidArgument)
	}
	localAgentID := strings.TrimSpace(cfg.LocalAgentID)
	if localAgentID == "" {
		return nil
	}
	if len(cert.TargetAgentIDs) != 1 || strings.TrimSpace(cert.TargetAgentIDs[0]) != localAgentID {
		return fmt.Errorf("%w: master_cf_dns certificates must target only the local master agent", ErrInvalidArgument)
	}
	return nil
}

func isReservedSystemRelayCANext(cert ManagedCertificate) bool {
	return cert.Usage == "relay_ca" || usesReservedRelayCAIdentity(cert)
}

func isSystemRelayCACertificate(cert ManagedCertificate) bool {
	return cert.Usage == "relay_ca" || usesReservedRelayCAIdentity(cert)
}

func canonicalizeSystemRelayCACertificate(cert ManagedCertificate) ManagedCertificate {
	cert.Domain = relayCADomainIdentity
	cert.Enabled = true
	cert.Scope = "domain"
	cert.IssuerMode = "local_http01"
	cert.Tags = normalizeTags([]string{systemRelayCATag, systemTag})
	cert.Usage = "relay_ca"
	cert.CertificateType = "internal_ca"
	cert.SelfSigned = true
	return cert
}

func usesReservedRelayCATags(tags []string) bool {
	if containsString(tags, systemRelayCATag) {
		return true
	}
	return containsString(tags, "relay-ca") && containsString(tags, systemTag)
}

func usesReservedRelayCAIdentity(cert ManagedCertificate) bool {
	return strings.EqualFold(strings.TrimSpace(cert.Domain), relayCADomainIdentity) || usesReservedRelayCATags(cert.Tags)
}

func isAutoRelayListenerCertificate(cert ManagedCertificate, listenerID int) bool {
	if cert.Usage != "relay_tunnel" || cert.CertificateType != "internal_ca" {
		return false
	}
	if !containsString(cert.Tags, "auto") {
		return false
	}
	if listenerID <= 0 {
		return true
	}
	return containsString(cert.Tags, relayListenerTag(listenerID))
}

func relayListenerTag(listenerID int) string {
	return fmt.Sprintf("listener:%d", listenerID)
}

func relayAgentTag(agentID string) string {
	return fmt.Sprintf("agent:%s", strings.TrimSpace(agentID))
}

func autoRelayListenerCertificateTags(listenerID int, agentID string) []string {
	return normalizeTags([]string{
		"relay",
		"auto",
		autoRelayListenerTag,
		relayListenerTag(listenerID),
		relayAgentTag(agentID),
	})
}

func findManagedCertificateByID(rows []storage.ManagedCertificateRow, certID int) (ManagedCertificate, int, bool) {
	for index, row := range rows {
		cert := managedCertificateFromRow(row)
		if cert.ID == certID {
			return cert, index, true
		}
	}
	return ManagedCertificate{}, -1, false
}

func findRelayCACertificate(rows []storage.ManagedCertificateRow) (ManagedCertificate, bool) {
	for _, row := range rows {
		cert := managedCertificateFromRow(row)
		if isSystemRelayCACertificate(cert) {
			return cert, true
		}
	}
	return ManagedCertificate{}, false
}

func deriveRelayTrustMaterial(ctx context.Context, store storage.Store, cert ManagedCertificate, rows []storage.ManagedCertificateRow, pending []storage.ManagedCertificateBundle) (string, []RelayPin, []int, bool, error) {
	material, ok, err := loadManagedCertificateMaterial(ctx, store, cert.Domain, pending)
	if err != nil {
		return "", nil, nil, false, err
	}
	if !ok || strings.TrimSpace(material.CertPEM) == "" {
		return "", nil, nil, false, fmt.Errorf("%w: unable to derive relay listener trust material for certificate %d", ErrInvalidArgument, cert.ID)
	}
	pins, err := deriveRelayPinSetFromCertificate(material.CertPEM)
	if err != nil {
		return "", nil, nil, false, err
	}

	trustedCAIDs := []int{}
	if relayCA, ok := findRelayCACertificate(rows); ok {
		relayCABundle, relayCAOk, err := loadManagedCertificateMaterial(ctx, store, relayCA.Domain, pending)
		if err != nil {
			return "", nil, nil, false, err
		}
		if relayCAOk && certificateChainUsesRelayCA(material, relayCABundle) {
			trustedCAIDs = []int{relayCA.ID}
		}
	}
	allowSelfSigned := cert.SelfSigned || len(trustedCAIDs) > 0
	if len(pins) > 0 && len(trustedCAIDs) > 0 {
		return "pin_and_ca", pins, trustedCAIDs, allowSelfSigned, nil
	}
	if len(pins) > 0 {
		return "pin_only", pins, trustedCAIDs, allowSelfSigned, nil
	}
	if len(trustedCAIDs) > 0 {
		return "ca_only", []RelayPin{}, trustedCAIDs, allowSelfSigned, nil
	}
	return "", nil, nil, false, fmt.Errorf("%w: unable to derive relay listener trust material for certificate %d", ErrInvalidArgument, cert.ID)
}

func stableManagedCertificateMaterialHash(cert ManagedCertificate) string {
	return hashManagedCertificateMaterial(
		fmt.Sprintf("%d|%s|%s|%v|%s", cert.ID, cert.Domain, cert.Usage, cert.SelfSigned, strings.Join(cert.Tags, ",")),
		cert.CertificateType,
	)
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func removeString(values []string, target string) []string {
	next := make([]string, 0, len(values))
	for _, value := range values {
		if value != target {
			next = append(next, value)
		}
	}
	return next
}
