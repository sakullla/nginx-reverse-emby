package certs

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
)

const (
	acmeGenerationProjectionFileName = "acme_generation.json"
	acmeGenerationProjectionVersion  = 1
)

type CertificateInfo struct {
	ID              int
	Domain          string
	Revision        int64
	Usage           string
	CertificateType string
	SelfSigned      bool
	Scope           string
	IssuerMode      string
	Status          string
	Fingerprint     string
	ACMEInfo        model.ManagedCertificateACMEInfo
}

type Manager struct {
	dataDir string
	cfg     managerConfig

	mu                 sync.RWMutex
	active             *activeState
	activeBase         *activeState
	activePublications []activeStatePublication
	renewalLoopStarted sync.Once
	renewalCancel      context.CancelFunc
	renewalWG          sync.WaitGroup
	closeOnce          sync.Once
	issuanceMu         sync.Mutex
	issuanceByID       map[int]*issuanceLockEntry
	pendingMu          sync.Mutex
	pendingByID        map[int]resolvedCertificateMaterial
	publicationMu      sync.Mutex
	nextPublicationID  uint64
	publications       map[string]*acmeGenerationPublication
}

// issuanceLockEntry is a per-certificate-ID lock carrying a refcount of the
// goroutines currently holding or waiting on it. An entry is removed from
// Manager.issuanceByID once its refcount drops to zero, so the map is bounded
// by the number of in-flight issuances instead of growing without bound as new
// certificate IDs are issued over the process lifetime.
type issuanceLockEntry struct {
	mu      sync.Mutex
	waiters int
}

type activeState struct {
	byID map[int]*managedCertificate

	publishMu  sync.Mutex
	published  bool
	publishErr error
}

type activeStatePublication struct {
	ownerID  uint64
	state    *activeState
	accepted bool
}

type managedCertificate struct {
	info         CertificateInfo
	certificate  tls.Certificate
	parsedChain  []*x509.Certificate
	materialHash string
	pending      *pendingACMEGeneration
}

type resolvedCertificateMaterial struct {
	certPEM []byte
	keyPEM  []byte
	pending *pendingACMEGeneration
}

type pendingACMEGeneration struct {
	certificateID        int
	stateRoot            string
	generationID         string
	previousGenerationID string
	accountKeyPEM        []byte
	metadata             localMaterialMetadata
	scope                string
	recordRenewal        bool
}

type acmeGenerationPublication struct {
	generationID         string
	previousGenerationID string
	owners               map[uint64]struct{}
	accepted             bool
	legacySnapshots      []legacyFileSnapshot
}

type persistedACMEMaterial struct {
	certPEM                []byte
	keyPEM                 []byte
	accountKeyPEM          []byte
	account                acmeflow.AccountMetadata
	store                  *acmeflow.StateStore
	pending                *acmeflow.PendingGeneration
	currentGenerationID    string
	projectionCurrentSplit bool
	metadata               localMaterialMetadata
}

type acmeGenerationProjection struct {
	Version      int    `json:"version"`
	GenerationID string `json:"generation_id"`
}

type localMaterialMetadata struct {
	Domain          string `json:"domain"`
	Scope           string `json:"scope"`
	IssuerMode      string `json:"issuer_mode"`
	CertificateType string `json:"certificate_type"`
}

func NewManager(dataDir string, opts ...Option) (*Manager, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("data directory is required")
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "certs", "managed"), 0755); err != nil {
		return nil, err
	}

	cfg := defaultManagerConfig()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.acme.http01Port == "" {
		cfg.acme.http01Port = "80"
	}
	if cfg.acme.directoryURL == "" {
		cfg.acme.directoryURL = defaultManagerConfig().acme.directoryURL
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	if cfg.issuerFactory == nil {
		cfg.issuerFactory = defaultACMEIssuerFactory
	}

	renewalCtx, renewalCancel := context.WithCancel(context.Background())
	manager := &Manager{
		dataDir:       dataDir,
		cfg:           cfg,
		active:        &activeState{byID: map[int]*managedCertificate{}, published: true},
		renewalCancel: renewalCancel,
		issuanceByID:  map[int]*issuanceLockEntry{},
		pendingByID:   map[int]resolvedCertificateMaterial{},
		publications:  map[string]*acmeGenerationPublication{},
	}
	manager.activeBase = manager.active
	manager.startRenewalLoop(renewalCtx)
	return manager, nil
}

func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		if m.renewalCancel != nil {
			m.renewalCancel()
		}
		m.renewalWG.Wait()
	})
	return nil
}

func (m *Manager) Apply(ctx context.Context, bundles []model.ManagedCertificateBundle, policies []model.ManagedCertificatePolicy) error {
	next := &activeState{byID: map[int]*managedCertificate{}}
	bundleByID := make(map[int]model.ManagedCertificateBundle, len(bundles))
	for _, bundle := range bundles {
		bundleByID[bundle.ID] = bundle
	}

	for _, policy := range policies {
		if !policy.Enabled {
			continue
		}
		managed, err := m.buildManagedCertificate(ctx, policy, bundleByID[policy.ID])
		if err != nil {
			return fmt.Errorf("certificate %d: %w", policy.ID, err)
		}
		next.byID[policy.ID] = managed
	}
	if err := m.publishActiveState(ctx, next); err != nil {
		return err
	}

	m.installActiveState(next)
	return nil
}

func (m *Manager) ServerCertificate(_ context.Context, certificateID int) (*tls.Certificate, error) {
	entry, err := m.lookup(certificateID)
	if err != nil {
		return nil, err
	}
	if !allowsServerUsage(entry.info.Usage) {
		return nil, fmt.Errorf("certificate %d usage %q is not valid for server certificates", certificateID, entry.info.Usage)
	}
	cert := entry.certificate
	return &cert, nil
}

func (m *Manager) ServerCertificateForHost(_ context.Context, host string) (*tls.Certificate, error) {
	entry, err := m.lookupServerCertificateByHost(host)
	if err != nil {
		return nil, err
	}
	cert := entry.certificate
	return &cert, nil
}

func (m *Manager) TrustedCAPool(_ context.Context, certificateIDs []int) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	for _, certificateID := range certificateIDs {
		entry, err := m.lookup(certificateID)
		if err != nil {
			return nil, err
		}
		if !allowsTrustUsage(entry.info.Usage) {
			return nil, fmt.Errorf("certificate %d usage %q is not valid for trust pools", certificateID, entry.info.Usage)
		}
		for _, cert := range entry.parsedChain {
			pool.AddCert(cert)
		}
	}
	return pool, nil
}

func (m *Manager) CertificateInfo(certificateID int) (CertificateInfo, error) {
	entry, err := m.lookup(certificateID)
	if err != nil {
		return CertificateInfo{}, err
	}
	return entry.info, nil
}

func (m *Manager) lookup(certificateID int) (*managedCertificate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.active.byID[certificateID]
	if !ok {
		return nil, fmt.Errorf("certificate %d not found", certificateID)
	}
	return entry, nil
}

func (m *Manager) lookupServerCertificateByHost(host string) (*managedCertificate, error) {
	normalizedHost := normalizeCertificateHost(host)
	if normalizedHost == "" {
		return nil, fmt.Errorf("host is required")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var best *managedCertificate
	bestScore := -1
	bestDomainLen := -1
	var bestRevision int64 = -1

	for _, entry := range m.active.byID {
		if !allowsServerUsage(entry.info.Usage) {
			continue
		}
		score := certificateHostMatchScore(entry.info, normalizedHost)
		if score < 0 {
			continue
		}
		domainLen := len(normalizeCertificateHost(entry.info.Domain))
		if best == nil || score > bestScore || (score == bestScore && domainLen > bestDomainLen) || (score == bestScore && domainLen == bestDomainLen && entry.info.Revision > bestRevision) {
			best = entry
			bestScore = score
			bestDomainLen = domainLen
			bestRevision = entry.info.Revision
		}
	}

	if best == nil {
		return nil, fmt.Errorf("no server certificate available for host %q", normalizedHost)
	}
	return best, nil
}

func (m *Manager) buildManagedCertificate(ctx context.Context, policy model.ManagedCertificatePolicy, bundle model.ManagedCertificateBundle) (*managedCertificate, error) {
	material, err := m.resolveMaterial(ctx, policy, bundle)
	if err != nil {
		return nil, err
	}

	tlsCert, parsedChain, fingerprint, err := parseTLSMaterial(material.certPEM, material.keyPEM)
	if err != nil {
		return nil, err
	}

	return &managedCertificate{
		info: CertificateInfo{
			ID:              policy.ID,
			Domain:          firstNonEmpty(policy.Domain, bundle.Domain),
			Revision:        maxRevision(policy.Revision, bundle.Revision),
			Usage:           normalizeUsage(policy.Usage),
			CertificateType: normalizeCertificateType(policy.CertificateType),
			SelfSigned:      policy.SelfSigned,
			Scope:           policy.Scope,
			IssuerMode:      policy.IssuerMode,
			Status:          policy.Status,
			Fingerprint:     fingerprint,
			ACMEInfo:        policy.ACMEInfo,
		},
		certificate:  tlsCert,
		parsedChain:  parsedChain,
		materialHash: hashManagedCertificateMaterial(material.certPEM, material.keyPEM),
		pending:      material.pending,
	}, nil
}

func (m *Manager) resolveMaterial(ctx context.Context, policy model.ManagedCertificatePolicy, bundle model.ManagedCertificateBundle) (resolvedCertificateMaterial, error) {
	switch normalizeCertificateType(policy.CertificateType) {
	case "uploaded":
		if strings.TrimSpace(bundle.CertPEM) == "" || strings.TrimSpace(bundle.KeyPEM) == "" {
			return resolvedCertificateMaterial{}, fmt.Errorf("uploaded certificates require control-plane PEM material")
		}
		return resolvedCertificateMaterial{certPEM: []byte(bundle.CertPEM), keyPEM: []byte(bundle.KeyPEM)}, nil
	case "internal_ca":
		if strings.TrimSpace(bundle.CertPEM) != "" && strings.TrimSpace(bundle.KeyPEM) != "" {
			return resolvedCertificateMaterial{certPEM: []byte(bundle.CertPEM), keyPEM: []byte(bundle.KeyPEM)}, nil
		}
		certPEM, keyPEM, err := m.loadOrIssueInternalCA(policy)
		return resolvedCertificateMaterial{certPEM: certPEM, keyPEM: keyPEM}, err
	case "acme":
		if policy.IssuerMode == "master_cf_dns" && strings.TrimSpace(bundle.CertPEM) != "" && strings.TrimSpace(bundle.KeyPEM) != "" {
			return resolvedCertificateMaterial{certPEM: []byte(bundle.CertPEM), keyPEM: []byte(bundle.KeyPEM)}, nil
		}
		return m.loadOrIssueACME(ctx, policy)
	default:
		return resolvedCertificateMaterial{}, fmt.Errorf("unsupported certificate type %q", policy.CertificateType)
	}
}

func (m *Manager) loadOrIssueInternalCA(policy model.ManagedCertificatePolicy) ([]byte, []byte, error) {
	certPath := filepath.Join(m.materialDir(policy.ID), "cert.pem")
	keyPath := filepath.Join(m.materialDir(policy.ID), "key.pem")
	metadata, metadataUsable, err := m.loadLocalMaterialMetadataIfUsable(policy.ID)
	if err != nil {
		return nil, nil, err
	}

	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	if certErr == nil && keyErr == nil && metadataUsable && metadata.matchesPolicy(policy) {
		if _, _, _, err := parseTLSMaterial(certPEM, keyPEM); err == nil {
			return certPEM, keyPEM, nil
		}
	}
	if !os.IsNotExist(certErr) && certErr != nil {
		return nil, nil, certErr
	}
	if !os.IsNotExist(keyErr) && keyErr != nil {
		return nil, nil, keyErr
	}

	certPEM, keyPEM, err = issueInternalCA(policy)
	if err != nil {
		return nil, nil, err
	}
	if err := m.writeLocalMaterialFiles(policy.ID, certPEM, keyPEM, policyMetadata(policy)); err != nil {
		return nil, nil, err
	}
	return certPEM, keyPEM, nil
}

func (m *Manager) loadOrIssueACME(ctx context.Context, policy model.ManagedCertificatePolicy) (resolvedCertificateMaterial, error) {
	defer m.issuanceLock(policy.ID)()
	return m.loadOrIssueACMEUnlocked(ctx, policy)
}

func (m *Manager) loadOrIssueACMEUnlocked(ctx context.Context, policy model.ManagedCertificatePolicy) (material resolvedCertificateMaterial, err error) {
	var failureRecorded bool
	defer func() {
		if err != nil && !failureRecorded {
			m.recordRenewalFailureLocked(policy.ID, err)
		}
	}()

	persisted, err := m.loadPersistedACMEMaterial(ctx, policy.ID)
	if err != nil {
		return resolvedCertificateMaterial{}, err
	}
	if persisted.store != nil {
		defer persisted.store.Close()
	}

	if !persisted.projectionCurrentSplit && len(persisted.certPEM) > 0 && len(persisted.keyPEM) > 0 {
		tlsCert, _, _, err := parseTLSMaterial(persisted.certPEM, persisted.keyPEM)
		if err == nil && tlsCert.Leaf != nil && persisted.metadata.matchesPolicy(policy) && !m.needsRenewalForScope(tlsCert.Leaf, policy.Scope) {
			material = resolvedCertificateMaterial{certPEM: persisted.certPEM, keyPEM: persisted.keyPEM}
			if persisted.currentGenerationID == "" && strings.TrimSpace(persisted.account.URI) != "" {
				material.pending, err = m.stageACMEGeneration(ctx, persisted.store, policy, acmeIssueResult{
					CertPEM:       persisted.certPEM,
					KeyPEM:        persisted.keyPEM,
					AccountKeyPEM: persisted.accountKeyPEM,
					Account:       persisted.account,
				}, "", false)
				if err != nil {
					return resolvedCertificateMaterial{}, err
				}
				m.cachePendingACMEMaterial(policy.ID, material)
			}
			return material, nil
		}
	}
	if cached, ok := m.cachedPendingACMEMaterial(policy); ok {
		return cached, nil
	}
	if recovered, ok, err := m.recoverPersistedPendingACMEMaterial(policy, persisted); err != nil {
		return resolvedCertificateMaterial{}, err
	} else if ok {
		m.cachePendingACMEMaterial(policy.ID, recovered)
		return recovered, nil
	}

	// Failure backoff (R5② / R4): if a prior attempt for this certificate failed
	// and we are still inside the backoff window, do not re-attempt issuance. This
	// gates BOTH the heartbeat-driven first-issuance path (Apply) and the renewal
	// loop, so neither burns Let's Encrypt's 5-failed-validations/hour/hostname
	// quota. The next attempt is allowed once the recorded next-retry-at elapses;
	// zero/legacy state (no backoff class) falls through to normal issuance.
	// Returning an error preserves the existing Apply contract; the renewal loop
	// separately skips in-backoff candidates via isInRenewalBackoffLocked.
	if m.isInRenewalBackoffLocked(policy.ID, m.cfg.now()) {
		failureRecorded = true
		return resolvedCertificateMaterial{}, fmt.Errorf("certificate %d: issuance deferred by failure backoff", policy.ID)
	}

	request, err := m.newACMEIssueRequest(policy, persisted)
	if err != nil {
		return resolvedCertificateMaterial{}, err
	}

	issuer, err := m.cfg.issuerFactory(request)
	if err != nil {
		return resolvedCertificateMaterial{}, err
	}

	result, err := issuer.Issue(ctx, request)
	if err != nil {
		if persistErr := m.persistACMEAccount(ctx, persisted.store, result); persistErr != nil {
			return resolvedCertificateMaterial{}, fmt.Errorf("%w (persist acme account: %v)", err, persistErr)
		}
		if saveErr := m.savePersistedACMEAccountState(policy.ID, result); saveErr != nil {
			return resolvedCertificateMaterial{}, fmt.Errorf("%w (persist acme account state: %v)", err, saveErr)
		}
		// Record the failure into the renewal state so the backoff curve and
		// last-error metadata apply uniformly to first-issuance failures and
		// renewal failures (R5② / requirement #4). The Apply caller still
		// receives the original issuance error; this only persists backoff
		// metadata used by later retries.
		m.recordRenewalFailureLocked(policy.ID, err)
		failureRecorded = true
		return resolvedCertificateMaterial{}, err
	}

	if len(result.AccountKeyPEM) == 0 {
		result.AccountKeyPEM = persisted.accountKeyPEM
	}
	if strings.TrimSpace(result.Account.URI) == "" {
		result.Account = persisted.account
	}

	if err := m.persistACMEAccount(ctx, persisted.store, result); err != nil {
		return resolvedCertificateMaterial{}, err
	}
	if err := m.savePersistedACMEAccountState(policy.ID, result); err != nil {
		return resolvedCertificateMaterial{}, err
	}
	if _, _, _, err := parseTLSMaterial(result.CertPEM, result.KeyPEM); err != nil {
		return resolvedCertificateMaterial{}, err
	}
	pending, err := m.stageACMEGeneration(ctx, persisted.store, policy, result, persisted.currentGenerationID, true)
	if err != nil {
		return resolvedCertificateMaterial{}, err
	}
	material = resolvedCertificateMaterial{certPEM: result.CertPEM, keyPEM: result.KeyPEM, pending: pending}
	m.cachePendingACMEMaterial(policy.ID, material)
	return material, nil
}

func (m *Manager) cachePendingACMEMaterial(certificateID int, material resolvedCertificateMaterial) {
	if material.pending == nil {
		return
	}
	material = cloneResolvedCertificateMaterial(material)
	m.pendingMu.Lock()
	m.pendingByID[certificateID] = material
	m.pendingMu.Unlock()
}

func (m *Manager) cachedPendingACMEMaterial(policy model.ManagedCertificatePolicy) (resolvedCertificateMaterial, bool) {
	m.pendingMu.Lock()
	material, ok := m.pendingByID[policy.ID]
	m.pendingMu.Unlock()
	if !ok || material.pending == nil || !material.pending.metadata.matchesPolicy(policy) {
		return resolvedCertificateMaterial{}, false
	}
	tlsCert, _, _, err := parseTLSMaterial(material.certPEM, material.keyPEM)
	if err != nil || tlsCert.Leaf == nil || m.needsRenewalForScope(tlsCert.Leaf, policy.Scope) {
		m.discardCachedPending(policy.ID, material.pending.generationID)
		return resolvedCertificateMaterial{}, false
	}
	return cloneResolvedCertificateMaterial(material), true
}

func (m *Manager) recoverPersistedPendingACMEMaterial(policy model.ManagedCertificatePolicy, persisted persistedACMEMaterial) (resolvedCertificateMaterial, bool, error) {
	if persisted.pending == nil {
		return resolvedCertificateMaterial{}, false, nil
	}
	pending := persisted.pending
	if pending.Reference.PolicySHA256 != pendingACMEPolicySHA256(policy) ||
		pending.Reference.PreviousGenerationID != persisted.currentGenerationID ||
		pending.Reference.GenerationID == persisted.currentGenerationID {
		return resolvedCertificateMaterial{}, false, nil
	}
	certPEM := pending.Generation.Material.CertificatePEM
	keyPEM := pending.Generation.Material.PrivateKeyPEM
	tlsCert, _, _, err := parseTLSMaterial(certPEM, keyPEM)
	if err != nil {
		return resolvedCertificateMaterial{}, false, err
	}
	if tlsCert.Leaf == nil || m.needsRenewalForScope(tlsCert.Leaf, policy.Scope) {
		return resolvedCertificateMaterial{}, false, nil
	}
	return resolvedCertificateMaterial{
		certPEM: append([]byte(nil), certPEM...),
		keyPEM:  append([]byte(nil), keyPEM...),
		pending: &pendingACMEGeneration{
			certificateID:        policy.ID,
			stateRoot:            m.acmeStateRoot(policy.ID),
			generationID:         pending.Reference.GenerationID,
			previousGenerationID: pending.Reference.PreviousGenerationID,
			accountKeyPEM:        append([]byte(nil), persisted.accountKeyPEM...),
			metadata:             policyMetadata(policy),
			scope:                policy.Scope,
			recordRenewal:        pending.Reference.RecordRenewal,
		},
	}, true, nil
}

func cloneResolvedCertificateMaterial(material resolvedCertificateMaterial) resolvedCertificateMaterial {
	material.certPEM = append([]byte(nil), material.certPEM...)
	material.keyPEM = append([]byte(nil), material.keyPEM...)
	material.pending = clonePendingACMEGeneration(material.pending)
	return material
}

func clonePendingACMEGeneration(pending *pendingACMEGeneration) *pendingACMEGeneration {
	if pending == nil {
		return nil
	}
	clone := *pending
	clone.accountKeyPEM = append([]byte(nil), pending.accountKeyPEM...)
	return &clone
}

func (m *Manager) discardCachedPending(certificateID int, generationID string) {
	m.pendingMu.Lock()
	defer m.pendingMu.Unlock()
	material, ok := m.pendingByID[certificateID]
	if ok && material.pending != nil && material.pending.generationID == generationID {
		delete(m.pendingByID, certificateID)
	}
}

func (m *Manager) issuanceLock(certificateID int) func() {
	m.issuanceMu.Lock()
	entry, ok := m.issuanceByID[certificateID]
	if !ok {
		entry = &issuanceLockEntry{}
		m.issuanceByID[certificateID] = entry
	}
	entry.waiters++
	m.issuanceMu.Unlock()

	entry.mu.Lock()

	return func() {
		entry.mu.Unlock()
		m.issuanceMu.Lock()
		entry.waiters--
		if entry.waiters == 0 {
			delete(m.issuanceByID, certificateID)
		}
		m.issuanceMu.Unlock()
	}
}

func (m *Manager) newACMEIssueRequest(policy model.ManagedCertificatePolicy, persisted persistedACMEMaterial) (acmeIssueRequest, error) {
	request := acmeIssueRequest{
		CertificateID:   policy.ID,
		Domain:          policy.Domain,
		Scope:           policy.Scope,
		IssuerMode:      policy.IssuerMode,
		DirectoryURL:    m.cfg.acme.directoryURL,
		Email:           m.cfg.acme.email,
		HTTP01Interface: m.cfg.acme.http01Interface,
		HTTP01Port:      m.cfg.acme.http01Port,
		ExistingKeyPEM:  persisted.keyPEM,
		AccountKeyPEM:   persisted.accountKeyPEM,
		Account:         persisted.account,
		AccountStore: &agentACMEStateStore{
			StateStore:    persisted.store,
			manager:       m,
			certificateID: policy.ID,
		},
	}

	switch policy.IssuerMode {
	case "local_http01":
		request.ChallengeType = challengeTypeHTTP01
		if policy.Scope == "ip" {
			request.Profile = "shortlived"
		}
	case "master_cf_dns":
		if !m.cfg.localAgent || m.cfg.nodeRole != "master" {
			return acmeIssueRequest{}, fmt.Errorf("master_cf_dns issuance is only allowed on the local master agent")
		}
		if strings.TrimSpace(m.cfg.acme.cloudflareDNSAPIToken) == "" {
			return acmeIssueRequest{}, fmt.Errorf("cloudflare credentials are required for master_cf_dns issuance")
		}
		request.ChallengeType = challengeTypeDNS01Cloudflare
		request.CloudflareDNSAPIToken = m.cfg.acme.cloudflareDNSAPIToken
		request.CloudflareZoneAPIToken = firstNonEmpty(m.cfg.acme.cloudflareZoneAPIToken, m.cfg.acme.cloudflareDNSAPIToken)
	default:
		return acmeIssueRequest{}, fmt.Errorf("unsupported ACME issuer mode %q", policy.IssuerMode)
	}

	return request, nil
}

func (m *Manager) needsRenewal(leaf *x509.Certificate) bool {
	return m.needsRenewalForScope(leaf, "")
}

func (m *Manager) needsRenewalForScope(leaf *x509.Certificate, scope string) bool {
	if leaf == nil {
		return true
	}
	return !leaf.NotAfter.After(m.cfg.now().Add(m.renewBeforeForScope(leaf, scope)))
}

func (m *Manager) renewBeforeForScope(leaf *x509.Certificate, scope string) time.Duration {
	renewBefore := m.cfg.acme.renewBefore
	if leaf == nil {
		return renewBefore
	}
	if strings.EqualFold(strings.TrimSpace(scope), "ip") {
		lifetime := leaf.NotAfter.Sub(leaf.NotBefore)
		if lifetime > 0 && lifetime < renewBefore {
			scaled := lifetime / 3
			if scaled > 0 {
				return scaled
			}
		}
	}
	return renewBefore
}

func (m *Manager) loadPersistedACMEMaterial(ctx context.Context, certificateID int) (persistedACMEMaterial, error) {
	result := persistedACMEMaterial{}

	certPEM, err := os.ReadFile(filepath.Join(m.materialDir(certificateID), "cert.pem"))
	if err == nil {
		result.certPEM = certPEM
	} else if !os.IsNotExist(err) {
		return persistedACMEMaterial{}, err
	}

	keyPEM, err := os.ReadFile(filepath.Join(m.materialDir(certificateID), "key.pem"))
	if err == nil {
		result.keyPEM = keyPEM
	} else if !os.IsNotExist(err) {
		return persistedACMEMaterial{}, err
	}
	projectedCertPEM := append([]byte(nil), result.certPEM...)
	projectedKeyPEM := append([]byte(nil), result.keyPEM...)
	projection, projectionExists, err := m.loadACMEGenerationProjection(certificateID)
	if err != nil {
		return persistedACMEMaterial{}, err
	}

	state, stateUsable, err := m.loadManagedCertificateState(certificateID)
	if err != nil {
		return persistedACMEMaterial{}, err
	}
	if stateUsable {
		if state.ACME != nil {
			result.accountKeyPEM = append([]byte(nil), state.ACME.Account.KeyPEM...)
			if state.ACME.Account.Metadata != nil {
				result.account = accountMetadataFromModel(*state.ACME.Account.Metadata)
			} else if len(state.ACME.Account.Registration) > 0 {
				result.account, _ = metadataFromLegacyRegistration(state.ACME.Account.Registration, m.acmeAccountLookup())
			}
		}
		if isUsableLocalMaterialMetadata(state.LocalMetadata) {
			result.metadata = state.LocalMetadata
		}
	}

	if len(result.accountKeyPEM) == 0 {
		accountKeyPEM, err := os.ReadFile(filepath.Join(m.materialDir(certificateID), "acme_account_key.pem"))
		if err == nil {
			result.accountKeyPEM = accountKeyPEM
		} else if !os.IsNotExist(err) {
			return persistedACMEMaterial{}, err
		}
	}

	if strings.TrimSpace(result.account.URI) == "" {
		accountPayload, err := os.ReadFile(filepath.Join(m.materialDir(certificateID), "acme_account.json"))
		if err == nil {
			var account acmeflow.AccountMetadata
			if json.Unmarshal(accountPayload, &account) == nil {
				result.account = account
			}
		} else if !os.IsNotExist(err) {
			return persistedACMEMaterial{}, err
		}
	}

	if strings.TrimSpace(result.account.URI) == "" {
		registrationPayload, err := os.ReadFile(filepath.Join(m.materialDir(certificateID), "acme_registration.json"))
		if err == nil {
			result.account, _ = metadataFromLegacyRegistration(registrationPayload, m.acmeAccountLookup())
		} else if !os.IsNotExist(err) {
			return persistedACMEMaterial{}, err
		}
	}

	if !isUsableLocalMaterialMetadata(result.metadata) {
		metadata, metadataUsable, err := m.loadLocalMaterialMetadataIfUsable(certificateID)
		if err != nil {
			return persistedACMEMaterial{}, err
		}
		if metadataUsable {
			result.metadata = metadata
		}
	}

	store, err := acmeflow.OpenStateStore(m.acmeStateRoot(certificateID), acmeflow.WithStateClock(m.cfg.now))
	if err != nil {
		return persistedACMEMaterial{}, err
	}
	result.store = store
	fail := func(err error) (persistedACMEMaterial, error) {
		_ = store.Close()
		return persistedACMEMaterial{}, err
	}
	if _, err := store.Reconcile(ctx); err != nil {
		return fail(err)
	}
	lookup := m.acmeAccountLookup()
	legacyAccountKeyPEM := append([]byte(nil), result.accountKeyPEM...)
	legacyAccount := result.account
	account, accountErr := store.LoadAccount(ctx, lookup)
	if accountErr != nil && !errors.Is(accountErr, acmeflow.ErrAccountNotFound) {
		return fail(accountErr)
	}
	migratedLegacyAccountKey := false
	if errors.Is(accountErr, acmeflow.ErrAccountNotFound) && len(legacyAccountKeyPEM) > 0 {
		if err := store.SaveAccountKey(ctx, lookup, legacyAccountKeyPEM); err != nil {
			return fail(err)
		}
		account.KeyPEM = append([]byte(nil), legacyAccountKeyPEM...)
		migratedLegacyAccountKey = true
	}
	if migratedLegacyAccountKey && strings.TrimSpace(account.Metadata.URI) == "" && accountMetadataMatchesLookup(legacyAccount, lookup) {
		if err := store.SaveAccountMetadata(ctx, legacyAccount); err != nil {
			return fail(err)
		}
	}
	if account, err := store.LoadAccount(ctx, lookup); err == nil {
		result.accountKeyPEM = append([]byte(nil), account.KeyPEM...)
		result.account = account.Metadata
	} else if !errors.Is(err, acmeflow.ErrAccountNotFound) {
		return fail(err)
	}
	if current, err := store.LoadCurrent(ctx); err == nil {
		result.currentGenerationID = current.Manifest.ID
		result.certPEM = append([]byte(nil), current.Material.CertificatePEM...)
		result.keyPEM = append([]byte(nil), current.Material.PrivateKeyPEM...)
		result.account = current.Account
	} else if !errors.Is(err, acmeflow.ErrNoCurrentGeneration) {
		return fail(err)
	}
	if pending, err := store.LoadPendingGeneration(ctx); err == nil {
		result.pending = &pending
	} else if !errors.Is(err, acmeflow.ErrNoPendingGeneration) {
		return fail(err)
	}
	if projectionExists {
		result.projectionCurrentSplit = projection.GenerationID != result.currentGenerationID
	} else if result.pending != nil && result.pending.Reference.GenerationID != result.currentGenerationID {
		result.projectionCurrentSplit = bytes.Equal(projectedCertPEM, result.pending.Generation.Material.CertificatePEM) &&
			bytes.Equal(projectedKeyPEM, result.pending.Generation.Material.PrivateKeyPEM)
	}

	return result, nil
}

func accountMetadataMatchesLookup(metadata acmeflow.AccountMetadata, lookup acmeflow.AccountLookup) bool {
	return strings.TrimSpace(metadata.URI) != "" &&
		strings.TrimSpace(metadata.DirectoryURL) == strings.TrimSpace(lookup.DirectoryURL) &&
		strings.TrimSpace(metadata.Email) == strings.TrimSpace(lookup.Email)
}

func (m *Manager) persistACMEAccount(ctx context.Context, store *acmeflow.StateStore, result acmeIssueResult) error {
	if store == nil {
		return acmeflow.WrapError(acmeflow.CategoryAccount, "agent_account_store", errors.New("account store is unavailable"))
	}
	lookup := m.acmeAccountLookup()
	if len(result.AccountKeyPEM) > 0 {
		if err := store.SaveAccountKey(ctx, lookup, result.AccountKeyPEM); err != nil {
			return err
		}
	}
	if strings.TrimSpace(result.Account.URI) != "" {
		if err := store.SaveAccountMetadata(ctx, result.Account); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) stageACMEGeneration(
	ctx context.Context,
	store *acmeflow.StateStore,
	policy model.ManagedCertificatePolicy,
	result acmeIssueResult,
	previousGenerationID string,
	recordRenewal bool,
) (*pendingACMEGeneration, error) {
	if store == nil {
		return nil, acmeflow.WrapError(acmeflow.CategoryMaterial, "agent_generation_stage", errors.New("state store is unavailable"))
	}
	identifiers := acmeIdentifiersForPolicy(policy)
	manifest, err := store.StageGeneration(ctx, acmeflow.GenerationInput{
		Material: acmeflow.CertificateMaterial{
			CertificatePEM: append([]byte(nil), result.CertPEM...),
			PrivateKeyPEM:  append([]byte(nil), result.KeyPEM...),
			Profile:        strings.TrimSpace(resultProfile(policy)),
		},
		Policy: acmeflow.MaterialPolicy{
			Identifiers: identifiers,
			Profile:     strings.TrimSpace(resultProfile(policy)),
			Now:         m.cfg.now(),
		},
		Account: result.Account,
		Pending: &acmeflow.PendingGenerationInput{
			PreviousGenerationID: previousGenerationID,
			PolicySHA256:         pendingACMEPolicySHA256(policy),
			RecordRenewal:        recordRenewal,
		},
	})
	if err != nil {
		return nil, err
	}
	return &pendingACMEGeneration{
		certificateID:        policy.ID,
		stateRoot:            m.acmeStateRoot(policy.ID),
		generationID:         manifest.ID,
		previousGenerationID: previousGenerationID,
		accountKeyPEM:        append([]byte(nil), result.AccountKeyPEM...),
		metadata:             policyMetadata(policy),
		scope:                policy.Scope,
		recordRenewal:        recordRenewal,
	}, nil
}

func acmeIdentifiersForPolicy(policy model.ManagedCertificatePolicy) []acmeflow.Identifier {
	identifierType := acmeflow.IdentifierDNS
	if strings.EqualFold(strings.TrimSpace(policy.Scope), "ip") || net.ParseIP(strings.TrimSpace(policy.Domain)) != nil {
		identifierType = acmeflow.IdentifierIP
	}
	return []acmeflow.Identifier{{Type: identifierType, Value: strings.TrimSpace(policy.Domain)}}
}

func resultProfile(policy model.ManagedCertificatePolicy) string {
	if strings.EqualFold(strings.TrimSpace(policy.Scope), "ip") {
		return "shortlived"
	}
	return ""
}

func (m *Manager) nextACMEPublicationOwner() uint64 {
	m.publicationMu.Lock()
	defer m.publicationMu.Unlock()
	m.nextPublicationID++
	if m.nextPublicationID == 0 {
		m.nextPublicationID++
	}
	return m.nextPublicationID
}

func (m *Manager) publishActiveState(ctx context.Context, state *activeState) error {
	ownerID := m.nextACMEPublicationOwner()
	pending := m.pendingACMEGenerations(state)
	if err := m.publishActiveStateOwned(ctx, state, ownerID); err != nil {
		return err
	}
	m.finalizeACMEGenerationsOwned(pending, ownerID)
	return nil
}

func (m *Manager) publishActiveStateOwned(ctx context.Context, state *activeState, ownerID uint64) error {
	if state == nil {
		return nil
	}
	state.publishMu.Lock()
	defer state.publishMu.Unlock()
	if state.published {
		return state.publishErr
	}
	if ownerID == 0 {
		return acmeflow.WrapError(acmeflow.CategoryMaterial, "agent_generation_publish", errors.New("publication owner is required"))
	}

	pending := m.pendingACMEGenerations(state)
	m.publicationMu.Lock()
	defer m.publicationMu.Unlock()
	promoted := make([]*pendingACMEGeneration, 0, len(pending))
	for _, generation := range pending {
		err := m.promoteACMEGenerationLocked(ctx, generation, ownerID)
		if err != nil {
			_, rollbackErr := m.rollbackACMEGenerationsLocked(ctx, append(promoted, generation), ownerID)
			state.publishErr = errors.Join(err, rollbackErr)
			return state.publishErr
		}
		promoted = append(promoted, generation)
	}
	state.published = true
	state.publishErr = nil
	return nil
}

func (m *Manager) promoteACMEGeneration(ctx context.Context, pending *pendingACMEGeneration) error {
	if pending == nil {
		return nil
	}
	ownerID := m.nextACMEPublicationOwner()
	m.publicationMu.Lock()
	defer m.publicationMu.Unlock()
	if err := m.promoteACMEGenerationLocked(ctx, pending, ownerID); err != nil {
		_, rollbackErr := m.rollbackACMEGenerationsLocked(ctx, []*pendingACMEGeneration{pending}, ownerID)
		return errors.Join(err, rollbackErr)
	}
	m.finalizeACMEGenerationLocked(pending, ownerID)
	return nil
}

func (m *Manager) promoteACMEGenerationLocked(ctx context.Context, pending *pendingACMEGeneration, ownerID uint64) (err error) {
	if pending == nil {
		return nil
	}
	if ownerID == 0 {
		return acmeflow.WrapError(acmeflow.CategoryMaterial, "agent_generation_promote", errors.New("publication owner is required"))
	}
	publicationKey := acmeGenerationPublicationKey(pending)
	store, err := acmeflow.OpenStateStore(pending.stateRoot, acmeflow.WithStateClock(m.cfg.now))
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()

	current, currentErr := store.LoadCurrent(ctx)
	switch {
	case currentErr == nil && current.Manifest.ID == pending.generationID:
		publication := m.publications[publicationKey]
		if publication == nil {
			publication = &acmeGenerationPublication{
				generationID:         pending.generationID,
				previousGenerationID: pending.previousGenerationID,
				owners:               map[uint64]struct{}{},
				accepted:             true,
			}
			m.publications[publicationKey] = publication
		}
		publication.owners[ownerID] = struct{}{}
		return nil
	case currentErr == nil && current.Manifest.ID != pending.previousGenerationID:
		return acmeflow.WrapError(acmeflow.CategoryMaterial, "agent_generation_promote", errors.New("current generation changed after staging"))
	case errors.Is(currentErr, acmeflow.ErrNoCurrentGeneration) && pending.previousGenerationID != "":
		return acmeflow.WrapError(acmeflow.CategoryMaterial, "agent_generation_promote", errors.New("previous generation is no longer current"))
	case currentErr != nil && !errors.Is(currentErr, acmeflow.ErrNoCurrentGeneration):
		return currentErr
	}

	snapshots, err := snapshotLegacyFiles(m.acmeProjectionTargets(pending.certificateID))
	if err != nil {
		return err
	}
	err = store.PromoteGeneration(ctx, pending.generationID, acmeflow.LegacyProjectionFunc(func(ctx context.Context, generation acmeflow.Generation) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		result := acmeIssueResult{
			CertPEM:       append([]byte(nil), generation.Material.CertificatePEM...),
			KeyPEM:        append([]byte(nil), generation.Material.PrivateKeyPEM...),
			AccountKeyPEM: append([]byte(nil), pending.accountKeyPEM...),
			Account:       generation.Account,
		}
		return m.projectACMEGeneration(pending, result)
	}))
	if err != nil {
		if restoreErr := restoreLegacyFiles(snapshots); restoreErr != nil {
			return errors.Join(err, restoreErr)
		}
		return err
	}
	m.publications[publicationKey] = &acmeGenerationPublication{
		generationID:         pending.generationID,
		previousGenerationID: pending.previousGenerationID,
		owners:               map[uint64]struct{}{ownerID: {}},
		legacySnapshots:      snapshots,
	}
	return nil
}

func acmeGenerationPublicationKey(pending *pendingACMEGeneration) string {
	if pending == nil {
		return ""
	}
	return filepath.Clean(pending.stateRoot) + "\x00" + pending.generationID
}

type legacyFileSnapshot struct {
	path   string
	data   []byte
	perm   os.FileMode
	exists bool
}

func (m *Manager) projectACMEGeneration(pending *pendingACMEGeneration, result acmeIssueResult) error {
	if err := m.savePersistedACMEMaterial(pending.certificateID, pending.scope, result, pending.recordRenewal); err != nil {
		return err
	}
	if err := m.saveLocalMaterialMetadata(pending.certificateID, pending.metadata); err != nil {
		return err
	}
	return m.saveACMEGenerationProjection(pending.certificateID, pending.generationID)
}

func (m *Manager) acmeProjectionTargets(certificateID int) []string {
	directory := m.materialDir(certificateID)
	return []string{
		filepath.Join(directory, "cert.pem"),
		filepath.Join(directory, "key.pem"),
		filepath.Join(directory, "acme_account_key.pem"),
		filepath.Join(directory, "acme_account.json"),
		filepath.Join(directory, managedCertificateStateFileName),
		filepath.Join(directory, "local_metadata.json"),
		filepath.Join(directory, acmeGenerationProjectionFileName),
	}
}

func (m *Manager) saveACMEGenerationProjection(certificateID int, generationID string) error {
	marker := acmeGenerationProjection{
		Version:      acmeGenerationProjectionVersion,
		GenerationID: strings.TrimSpace(generationID),
	}
	if marker.GenerationID == "" {
		return errors.New("ACME generation projection identifier is required")
	}
	payload, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	directory := m.materialDir(certificateID)
	if err := writeFileAtomically(filepath.Join(directory, acmeGenerationProjectionFileName), payload, 0600); err != nil {
		return err
	}
	return syncACMEProjectionDirectory(directory)
}

func (m *Manager) loadACMEGenerationProjection(certificateID int) (acmeGenerationProjection, bool, error) {
	payload, err := os.ReadFile(filepath.Join(m.materialDir(certificateID), acmeGenerationProjectionFileName))
	if os.IsNotExist(err) {
		return acmeGenerationProjection{}, false, nil
	}
	if err != nil {
		return acmeGenerationProjection{}, false, err
	}
	var marker acmeGenerationProjection
	if err := json.Unmarshal(payload, &marker); err != nil {
		return acmeGenerationProjection{}, false, err
	}
	marker.GenerationID = strings.TrimSpace(marker.GenerationID)
	if marker.Version != acmeGenerationProjectionVersion || marker.GenerationID == "" {
		return acmeGenerationProjection{}, false, errors.New("ACME generation projection marker is invalid")
	}
	return marker, true, nil
}

func syncACMEProjectionDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	return errors.Join(syncErr, directory.Close())
}

func (m *Manager) rollbackACMEGenerationsOwned(ctx context.Context, promoted []*pendingACMEGeneration, ownerID uint64) (bool, error) {
	m.publicationMu.Lock()
	defer m.publicationMu.Unlock()
	return m.rollbackACMEGenerationsLocked(ctx, promoted, ownerID)
}

func (m *Manager) rollbackACMEGenerationsLocked(ctx context.Context, promoted []*pendingACMEGeneration, ownerID uint64) (bool, error) {
	restorePreviousState := true
	var rollbackErrors []error
	for index := len(promoted) - 1; index >= 0; index-- {
		pending := promoted[index]
		if pending == nil {
			continue
		}
		publicationKey := acmeGenerationPublicationKey(pending)
		publication := m.publications[publicationKey]
		if publication == nil {
			continue
		}
		if _, owns := publication.owners[ownerID]; !owns {
			continue
		}
		if len(publication.owners) > 1 || publication.accepted {
			restorePreviousState = false
			delete(publication.owners, ownerID)
			if len(publication.owners) == 0 {
				delete(m.publications, publicationKey)
			}
			continue
		}
		if err := m.rollbackACMEGenerationLocked(ctx, pending, publication); err != nil {
			restorePreviousState = false
			rollbackErrors = append(rollbackErrors, fmt.Errorf("certificate %d generation rollback: %w", pending.certificateID, err))
			continue
		}
		delete(publication.owners, ownerID)
		delete(m.publications, publicationKey)
	}
	return restorePreviousState, errors.Join(rollbackErrors...)
}

func (m *Manager) rollbackACMEGenerationLocked(ctx context.Context, pending *pendingACMEGeneration, publication *acmeGenerationPublication) error {
	store, err := acmeflow.OpenStateStore(pending.stateRoot, acmeflow.WithStateClock(m.cfg.now))
	if err != nil {
		return err
	}
	current, loadErr := store.LoadCurrent(ctx)
	clearCurrent := false
	switch {
	case loadErr == nil && current.Manifest.ID == publication.generationID:
		if publication.previousGenerationID == "" {
			clearCurrent = true
		} else {
			err = store.PromoteGeneration(ctx, publication.previousGenerationID, nil)
		}
	case loadErr == nil && current.Manifest.ID == publication.previousGenerationID:
	case errors.Is(loadErr, acmeflow.ErrNoCurrentGeneration) && publication.previousGenerationID == "":
	case loadErr != nil:
		err = loadErr
	default:
		err = acmeflow.WrapError(acmeflow.CategoryMaterial, "agent_generation_rollback", errors.New("current generation changed during rollback"))
	}
	if closeErr := store.Close(); closeErr != nil {
		err = errors.Join(err, closeErr)
	}
	if err != nil {
		return err
	}
	if clearCurrent {
		if err := clearACMECurrentReferences(pending.stateRoot); err != nil {
			return err
		}
	}
	return restoreLegacyFiles(publication.legacySnapshots)
}

func clearACMECurrentReferences(stateRoot string) error {
	paths := []string{
		filepath.Join(stateRoot, "current", "slot-0.json"),
		filepath.Join(stateRoot, "current", "slot-1.json"),
	}
	snapshots, err := snapshotLegacyFiles(paths)
	if err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		if !snapshot.exists {
			continue
		}
		if err := os.Remove(snapshot.path); err != nil {
			return errors.Join(err, restoreLegacyFiles(snapshots))
		}
	}
	return nil
}

func (m *Manager) finalizeACMEGenerationsOwned(pending []*pendingACMEGeneration, ownerID uint64) {
	if len(pending) == 0 {
		return
	}
	m.publicationMu.Lock()
	defer m.publicationMu.Unlock()
	for _, generation := range pending {
		m.finalizeACMEGenerationLocked(generation, ownerID)
		m.discardCachedPending(generation.certificateID, generation.generationID)
	}
}

func (m *Manager) finalizeACMEGenerationLocked(pending *pendingACMEGeneration, ownerID uint64) {
	if pending == nil {
		return
	}
	publicationKey := acmeGenerationPublicationKey(pending)
	publication := m.publications[publicationKey]
	if publication != nil {
		if _, owns := publication.owners[ownerID]; owns {
			publication.accepted = true
			publication.legacySnapshots = nil
			delete(publication.owners, ownerID)
			if len(publication.owners) == 0 {
				delete(m.publications, publicationKey)
			}
		}
	}
}

func snapshotLegacyFiles(paths []string) ([]legacyFileSnapshot, error) {
	snapshots := make([]legacyFileSnapshot, 0, len(paths))
	for _, path := range paths {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			snapshots = append(snapshots, legacyFileSnapshot{path: path})
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("legacy projection target is not a regular file")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, legacyFileSnapshot{path: path, data: data, perm: info.Mode().Perm(), exists: true})
	}
	return snapshots, nil
}

func restoreLegacyFiles(snapshots []legacyFileSnapshot) error {
	var restoreErrors []error
	for index := len(snapshots) - 1; index >= 0; index-- {
		snapshot := snapshots[index]
		if snapshot.exists {
			if err := writeFileAtomically(snapshot.path, snapshot.data, snapshot.perm); err != nil {
				restoreErrors = append(restoreErrors, err)
			}
			continue
		}
		if err := os.Remove(snapshot.path); err != nil && !os.IsNotExist(err) {
			restoreErrors = append(restoreErrors, err)
		}
	}
	return errors.Join(restoreErrors...)
}

func (m *Manager) savePersistedACMEAccountState(certificateID int, result acmeIssueResult) error {
	if len(result.AccountKeyPEM) == 0 && strings.TrimSpace(result.Account.URI) == "" {
		return nil
	}

	materialDir := m.materialDir(certificateID)
	if err := os.MkdirAll(materialDir, 0755); err != nil {
		return err
	}
	if len(result.AccountKeyPEM) > 0 {
		if err := writeFileAtomically(filepath.Join(materialDir, "acme_account_key.pem"), result.AccountKeyPEM, 0600); err != nil {
			return err
		}
	}
	if strings.TrimSpace(result.Account.URI) != "" {
		payload, err := json.Marshal(result.Account)
		if err != nil {
			return err
		}
		if err := writeFileAtomically(filepath.Join(materialDir, "acme_account.json"), payload, 0600); err != nil {
			return err
		}
	}

	state, _, err := m.loadManagedCertificateState(certificateID)
	if err != nil {
		return err
	}
	if state.ACME == nil {
		state.ACME = &model.ManagedCertificateACMEState{}
	}
	if len(result.AccountKeyPEM) > 0 {
		state.ACME.Account.KeyPEM = append([]byte(nil), result.AccountKeyPEM...)
	}
	if strings.TrimSpace(result.Account.URI) != "" {
		metadata := accountMetadataToModel(result.Account)
		state.ACME.Account.Metadata = &metadata
	}
	return m.saveManagedCertificateState(certificateID, state)
}

func (m *Manager) savePersistedACMEMaterial(certificateID int, scope string, result acmeIssueResult, recordRenewal bool) error {
	materialDir := m.materialDir(certificateID)
	if err := os.MkdirAll(materialDir, 0755); err != nil {
		return err
	}
	if err := writeFileAtomically(filepath.Join(materialDir, "cert.pem"), result.CertPEM, 0600); err != nil {
		return err
	}
	if err := writeFileAtomically(filepath.Join(materialDir, "key.pem"), result.KeyPEM, 0600); err != nil {
		return err
	}
	if err := m.savePersistedACMEAccountState(certificateID, result); err != nil {
		return err
	}

	state, _, err := m.loadManagedCertificateState(certificateID)
	if err != nil {
		return err
	}
	if state.ACME == nil {
		state.ACME = &model.ManagedCertificateACMEState{}
	}
	if tlsCert, _, _, err := parseTLSMaterial(result.CertPEM, result.KeyPEM); recordRenewal && err == nil && tlsCert.Leaf != nil {
		renewBefore := m.renewBeforeForScope(tlsCert.Leaf, scope)
		state.ACME.Renewal.NotAfterUnix = tlsCert.Leaf.NotAfter.Unix()
		state.ACME.Renewal.RenewAtUnix = tlsCert.Leaf.NotAfter.Add(-renewBefore).Unix()
		nowUnix := m.cfg.now().Unix()
		state.ACME.Renewal.LastRenewedAtUnix = nowUnix
		state.ACME.Renewal.LastAttemptAtUnix = nowUnix
		state.ACME.Renewal.LastAttemptError = ""
		state.ACME.Renewal.LastAttemptStatus = "success"
		state.ACME.Renewal.LastAttemptNotAfter = tlsCert.Leaf.NotAfter.Unix()
		// Successful issuance clears the failure backoff curve so a future
		// failure sequence restarts from retryCount=1.
		state.ACME.Renewal.BackoffClass = ""
		state.ACME.Renewal.BackoffRetryNext = 0
		state.ACME.Renewal.BackoffRetryNum = 0
	}
	if err := m.saveManagedCertificateState(certificateID, state); err != nil {
		return err
	}
	return nil
}

func (m *Manager) loadLocalMaterialMetadata(certificateID int) (localMaterialMetadata, error) {
	payload, err := os.ReadFile(filepath.Join(m.materialDir(certificateID), "local_metadata.json"))
	if err != nil {
		return localMaterialMetadata{}, err
	}
	var metadata localMaterialMetadata
	if err := json.Unmarshal(payload, &metadata); err != nil {
		return localMaterialMetadata{}, err
	}
	return metadata, nil
}

func (m *Manager) saveLocalMaterialMetadata(certificateID int, metadata localMaterialMetadata) error {
	payload, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if err := writeFileAtomically(filepath.Join(m.materialDir(certificateID), "local_metadata.json"), payload, 0600); err != nil {
		return err
	}
	state, _, err := m.loadManagedCertificateState(certificateID)
	if err != nil {
		return err
	}
	state.LocalMetadata = metadata
	return m.saveManagedCertificateState(certificateID, state)
}

func (m *Manager) materialDir(certificateID int) string {
	return filepath.Join(m.dataDir, "certs", "managed", strconv.Itoa(certificateID))
}

func issueInternalCA(policy model.ManagedCertificatePolicy) ([]byte, []byte, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, err
	}

	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               pkix.Name{CommonName: firstNonEmpty(policy.Domain, fmt.Sprintf("internal-ca-%d", policy.ID))},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(3650 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	return certPEM, keyPEM, nil
}

func parseTLSMaterial(certPEM, keyPEM []byte) (tls.Certificate, []*x509.Certificate, string, error) {
	certs, err := parseCertificateChain(certPEM)
	if err != nil {
		return tls.Certificate{}, nil, "", err
	}
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, nil, "", err
	}
	tlsCert.Leaf = certs[0]
	fingerprint := fingerprintFromCertificate(certs[0])
	return tlsCert, certs, fingerprint, nil
}

func parseCertificateChain(certPEM []byte) ([]*x509.Certificate, error) {
	rest := certPEM
	var certificates []*x509.Certificate
	for {
		rest = bytes.TrimSpace(rest)
		if len(rest) == 0 {
			break
		}
		block, next := pem.Decode(rest)
		if block == nil {
			return nil, fmt.Errorf("invalid certificate PEM")
		}
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("expected CERTIFICATE PEM block")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		certificates = append(certificates, cert)
		rest = next
	}
	if len(certificates) == 0 {
		return nil, fmt.Errorf("invalid certificate PEM")
	}
	return certificates, nil
}

func fingerprintFromCertificate(cert *x509.Certificate) string {
	sum := sha256Sum(cert.Raw)
	return fmt.Sprintf("%x", sum)
}

func hashManagedCertificateMaterial(certPEM, keyPEM []byte) string {
	if len(bytes.TrimSpace(certPEM)) == 0 || len(bytes.TrimSpace(keyPEM)) == 0 {
		return ""
	}
	hash := sha256.New()
	_, _ = hash.Write(certPEM)
	_, _ = hash.Write([]byte("\n---\n"))
	_, _ = hash.Write(keyPEM)
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func (m *Manager) ManagedCertificateReports(context.Context) ([]model.ManagedCertificateReport, error) {
	m.mu.RLock()
	entries := make([]*managedCertificate, 0, len(m.active.byID))
	for _, entry := range m.active.byID {
		if entry == nil || !managedCertificateReportIssuerMode(entry.info.IssuerMode) {
			continue
		}
		entries = append(entries, entry)
	}
	m.mu.RUnlock()

	reports := make([]model.ManagedCertificateReport, 0, len(entries))
	for _, entry := range entries {
		report := model.ManagedCertificateReport{
			ID:           entry.info.ID,
			Domain:       entry.info.Domain,
			Status:       managedCertificateReportStatus(entry),
			MaterialHash: entry.materialHash,
			ACMEInfo:     entry.info.ACMEInfo,
		}
		if entry.info.IssuerMode == "local_http01" {
			state, ok, err := m.loadManagedCertificateState(entry.info.ID)
			if err != nil {
				return nil, err
			}
			if ok && state.ACME != nil {
				if renewedAt := state.ACME.Renewal.LastRenewedAtUnix; renewedAt > 0 {
					report.LastIssueAt = time.Unix(renewedAt, 0).UTC().Format(time.RFC3339)
				}
				if lastAttempt := state.ACME.Renewal.LastAttemptAtUnix; lastAttempt > 0 {
					report.UpdatedAt = time.Unix(lastAttempt, 0).UTC().Format(time.RFC3339)
				}
				report.LastError = state.ACME.Renewal.LastAttemptError
				if normalizeManagedCertificateReportStatus(state.ACME.Renewal.LastAttemptStatus) != "" {
					report.Status = normalizeManagedCertificateReportStatus(state.ACME.Renewal.LastAttemptStatus)
				}
			}
		}
		reports = append(reports, report)
	}
	return reports, nil
}

func managedCertificateReportIssuerMode(value string) bool {
	switch value {
	case "local_http01", "master_cf_dns":
		return true
	default:
		return false
	}
}

func managedCertificateReportStatus(entry *managedCertificate) string {
	if entry == nil {
		return ""
	}
	if entry.materialHash != "" {
		return "active"
	}
	return normalizeManagedCertificateReportStatus(entry.info.Status)
}

func normalizeManagedCertificateReportStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "pending", "active", "error":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func sha256Sum(raw []byte) [32]byte {
	return sha256.Sum256(raw)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func maxRevision(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func policyMetadata(policy model.ManagedCertificatePolicy) localMaterialMetadata {
	return localMaterialMetadata{
		Domain:          policy.Domain,
		Scope:           policy.Scope,
		IssuerMode:      policy.IssuerMode,
		CertificateType: normalizeCertificateType(policy.CertificateType),
	}
}

func pendingACMEPolicySHA256(policy model.ManagedCertificatePolicy) string {
	payload, _ := json.Marshal(policyMetadata(policy))
	return fmt.Sprintf("%x", sha256.Sum256(payload))
}

func (m localMaterialMetadata) matchesPolicy(policy model.ManagedCertificatePolicy) bool {
	return m.Domain == policy.Domain &&
		m.Scope == policy.Scope &&
		m.IssuerMode == policy.IssuerMode &&
		m.CertificateType == normalizeCertificateType(policy.CertificateType)
}

func normalizeCertificateType(value string) string {
	return firstNonEmpty(value, "acme")
}

func normalizeUsage(value string) string {
	return firstNonEmpty(value, "https")
}

func allowsServerUsage(usage string) bool {
	switch normalizeUsage(usage) {
	case "https", "relay_tunnel", "mixed":
		return true
	default:
		return false
	}
}

func allowsTrustUsage(usage string) bool {
	switch normalizeUsage(usage) {
	case "relay_ca", "mixed":
		return true
	default:
		return false
	}
}

func certificateHostMatchScore(info CertificateInfo, host string) int {
	domain := normalizeCertificateHost(info.Domain)
	if domain == "" || host == "" {
		return -1
	}
	if strings.EqualFold(info.Scope, "ip") {
		if domain == host {
			return 3
		}
		return -1
	}
	if domain == host {
		return 3
	}
	if wildcardCertificateMatchesHost(domain, host) {
		return 2
	}
	return -1
}

func wildcardCertificateMatchesHost(domain, host string) bool {
	if !strings.HasPrefix(domain, "*.") {
		return false
	}
	suffix := strings.TrimPrefix(domain, "*.")
	if suffix == "" || !strings.HasSuffix(host, "."+suffix) {
		return false
	}
	hostParts := strings.Split(host, ".")
	suffixParts := strings.Split(suffix, ".")
	return len(hostParts) == len(suffixParts)+1
}

func normalizeCertificateHost(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func (m *Manager) loadLocalMaterialMetadataIfUsable(certificateID int) (localMaterialMetadata, bool, error) {
	state, stateUsable, err := m.loadManagedCertificateState(certificateID)
	if err != nil {
		return localMaterialMetadata{}, false, err
	}
	if stateUsable && isUsableLocalMaterialMetadata(state.LocalMetadata) {
		return state.LocalMetadata, true, nil
	}

	payload, err := os.ReadFile(filepath.Join(m.materialDir(certificateID), "local_metadata.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return localMaterialMetadata{}, false, nil
		}
		return localMaterialMetadata{}, false, err
	}

	var metadata localMaterialMetadata
	if err := json.Unmarshal(payload, &metadata); err != nil {
		return localMaterialMetadata{}, false, nil
	}
	return metadata, true, nil
}

func isUsableLocalMaterialMetadata(metadata localMaterialMetadata) bool {
	return strings.TrimSpace(metadata.Domain) != "" &&
		strings.TrimSpace(metadata.Scope) != "" &&
		strings.TrimSpace(metadata.IssuerMode) != "" &&
		strings.TrimSpace(metadata.CertificateType) != ""
}

func (m *Manager) writeLocalMaterialFiles(certificateID int, certPEM, keyPEM []byte, metadata localMaterialMetadata) error {
	materialDir := m.materialDir(certificateID)
	if err := os.MkdirAll(materialDir, 0755); err != nil {
		return err
	}
	if err := writeFileAtomically(filepath.Join(materialDir, "cert.pem"), certPEM, 0600); err != nil {
		return err
	}
	if err := writeFileAtomically(filepath.Join(materialDir, "key.pem"), keyPEM, 0600); err != nil {
		return err
	}
	return m.saveLocalMaterialMetadata(certificateID, metadata)
}

func writeFileAtomically(targetPath string, payload []byte, perm os.FileMode) (retErr error) {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return err
	}

	tempFile, err := os.CreateTemp(filepath.Dir(targetPath), filepath.Base(targetPath)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	defer func() {
		if retErr != nil {
			_ = os.Remove(tempPath)
		}
	}()

	if err := tempFile.Chmod(perm); err != nil {
		_ = tempFile.Close()
		return err
	}
	if _, err := tempFile.Write(payload); err != nil {
		_ = tempFile.Close()
		return err
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}

	if err := renameReplace(tempPath, targetPath); err != nil {
		return err
	}
	return nil
}

func renameReplace(sourcePath, targetPath string) error {
	return replaceFile(sourcePath, targetPath)
}
