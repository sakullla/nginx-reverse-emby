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
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/registration"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
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
}

type Manager struct {
	dataDir string
	cfg     managerConfig

	mu     sync.RWMutex
	active *activeState
}

type activeState struct {
	byID map[int]*managedCertificate
}

type managedCertificate struct {
	info        CertificateInfo
	certificate tls.Certificate
	parsedChain []*x509.Certificate
}

type persistedACMEMaterial struct {
	certPEM       []byte
	keyPEM        []byte
	accountKeyPEM []byte
	registration  *registration.Resource
	metadata      localMaterialMetadata
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

	return &Manager{
		dataDir: dataDir,
		cfg:     cfg,
		active:  &activeState{byID: map[int]*managedCertificate{}},
	}, nil
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

	m.mu.Lock()
	m.active = next
	m.mu.Unlock()
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
	certPEM, keyPEM, err := m.resolveMaterial(ctx, policy, bundle)
	if err != nil {
		return nil, err
	}

	tlsCert, parsedChain, fingerprint, err := parseTLSMaterial(certPEM, keyPEM)
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
		},
		certificate: tlsCert,
		parsedChain: parsedChain,
	}, nil
}

func (m *Manager) resolveMaterial(ctx context.Context, policy model.ManagedCertificatePolicy, bundle model.ManagedCertificateBundle) ([]byte, []byte, error) {
	switch normalizeCertificateType(policy.CertificateType) {
	case "uploaded":
		if strings.TrimSpace(bundle.CertPEM) == "" || strings.TrimSpace(bundle.KeyPEM) == "" {
			return nil, nil, fmt.Errorf("uploaded certificates require control-plane PEM material")
		}
		return []byte(bundle.CertPEM), []byte(bundle.KeyPEM), nil
	case "internal_ca":
		return m.loadOrIssueInternalCA(policy)
	case "acme":
		return m.loadOrIssueACME(ctx, policy)
	default:
		return nil, nil, fmt.Errorf("unsupported certificate type %q", policy.CertificateType)
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

func (m *Manager) loadOrIssueACME(ctx context.Context, policy model.ManagedCertificatePolicy) ([]byte, []byte, error) {
	persisted, err := m.loadPersistedACMEMaterial(policy.ID)
	if err != nil {
		return nil, nil, err
	}

	if len(persisted.certPEM) > 0 && len(persisted.keyPEM) > 0 {
		tlsCert, _, _, err := parseTLSMaterial(persisted.certPEM, persisted.keyPEM)
		if err == nil && tlsCert.Leaf != nil && persisted.metadata.matchesPolicy(policy) && !m.needsRenewal(tlsCert.Leaf) {
			return persisted.certPEM, persisted.keyPEM, nil
		}
	}

	request, err := m.newACMEIssueRequest(policy, persisted)
	if err != nil {
		return nil, nil, err
	}

	issuer, err := m.cfg.issuerFactory(request)
	if err != nil {
		return nil, nil, err
	}

	result, err := issuer.Issue(ctx, request)
	if err != nil {
		return nil, nil, err
	}

	if _, _, _, err := parseTLSMaterial(result.CertPEM, result.KeyPEM); err != nil {
		return nil, nil, err
	}

	if len(result.AccountKeyPEM) == 0 {
		result.AccountKeyPEM = persisted.accountKeyPEM
	}
	if result.Registration == nil {
		result.Registration = persisted.registration
	}

	if err := m.savePersistedACMEMaterial(policy.ID, result); err != nil {
		return nil, nil, err
	}
	if err := m.saveLocalMaterialMetadata(policy.ID, policyMetadata(policy)); err != nil {
		return nil, nil, err
	}
	return result.CertPEM, result.KeyPEM, nil
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
		Registration:    persisted.registration,
	}

	switch policy.IssuerMode {
	case "local_http01":
		request.ChallengeType = challengeTypeHTTP01
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
	if leaf == nil {
		return true
	}
	return !leaf.NotAfter.After(m.cfg.now().Add(m.cfg.acme.renewBefore))
}

func (m *Manager) loadPersistedACMEMaterial(certificateID int) (persistedACMEMaterial, error) {
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

	accountKeyPEM, err := os.ReadFile(filepath.Join(m.materialDir(certificateID), "acme_account_key.pem"))
	if err == nil {
		result.accountKeyPEM = accountKeyPEM
	} else if !os.IsNotExist(err) {
		return persistedACMEMaterial{}, err
	}

	registrationPayload, err := os.ReadFile(filepath.Join(m.materialDir(certificateID), "acme_registration.json"))
	if err == nil {
		var registrationResource registration.Resource
		if err := json.Unmarshal(registrationPayload, &registrationResource); err == nil {
			result.registration = &registrationResource
		}
	} else if !os.IsNotExist(err) {
		return persistedACMEMaterial{}, err
	}

	metadata, metadataUsable, err := m.loadLocalMaterialMetadataIfUsable(certificateID)
	if err != nil {
		return persistedACMEMaterial{}, err
	}
	if metadataUsable {
		result.metadata = metadata
	}

	return result, nil
}

func (m *Manager) savePersistedACMEMaterial(certificateID int, result acmeIssueResult) error {
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
	if len(result.AccountKeyPEM) > 0 {
		if err := writeFileAtomically(filepath.Join(materialDir, "acme_account_key.pem"), result.AccountKeyPEM, 0600); err != nil {
			return err
		}
	}
	if result.Registration != nil {
		payload, err := json.Marshal(result.Registration)
		if err != nil {
			return err
		}
		if err := writeFileAtomically(filepath.Join(materialDir, "acme_registration.json"), payload, 0600); err != nil {
			return err
		}
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
	return writeFileAtomically(filepath.Join(m.materialDir(certificateID), "local_metadata.json"), payload, 0600)
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
