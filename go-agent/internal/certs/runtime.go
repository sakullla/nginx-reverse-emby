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
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

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

func NewManager(dataDir string) (*Manager, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("data directory is required")
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "certs", "managed"), 0755); err != nil {
		return nil, err
	}
	return &Manager{
		dataDir: dataDir,
		active:  &activeState{byID: map[int]*managedCertificate{}},
	}, nil
}

func (m *Manager) Apply(_ context.Context, bundles []model.ManagedCertificateBundle, policies []model.ManagedCertificatePolicy) error {
	next := &activeState{byID: map[int]*managedCertificate{}}
	bundleByID := make(map[int]model.ManagedCertificateBundle, len(bundles))
	for _, bundle := range bundles {
		bundleByID[bundle.ID] = bundle
	}

	for _, policy := range policies {
		if !policy.Enabled {
			continue
		}
		managed, err := m.buildManagedCertificate(policy, bundleByID[policy.ID])
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

func (m *Manager) buildManagedCertificate(policy model.ManagedCertificatePolicy, bundle model.ManagedCertificateBundle) (*managedCertificate, error) {
	certPEM, keyPEM, err := m.resolveMaterial(policy, bundle)
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
			Usage:           firstNonEmpty(policy.Usage, "https"),
			CertificateType: firstNonEmpty(policy.CertificateType, "acme"),
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

func (m *Manager) resolveMaterial(policy model.ManagedCertificatePolicy, bundle model.ManagedCertificateBundle) ([]byte, []byte, error) {
	if policy.CertificateType == "internal_ca" {
		return m.loadOrIssueInternalCA(policy)
	}
	if strings.TrimSpace(bundle.CertPEM) == "" || strings.TrimSpace(bundle.KeyPEM) == "" {
		return nil, nil, fmt.Errorf("missing PEM material from control plane")
	}
	return []byte(bundle.CertPEM), []byte(bundle.KeyPEM), nil
}

func (m *Manager) loadOrIssueInternalCA(policy model.ManagedCertificatePolicy) ([]byte, []byte, error) {
	certPath := filepath.Join(m.materialDir(policy.ID), "cert.pem")
	keyPath := filepath.Join(m.materialDir(policy.ID), "key.pem")

	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	if certErr == nil && keyErr == nil {
		if _, _, _, err := parseTLSMaterial(certPEM, keyPEM); err == nil {
			return certPEM, keyPEM, nil
		}
		return nil, nil, fmt.Errorf("invalid persisted internal_ca material")
	}
	if !os.IsNotExist(certErr) && certErr != nil {
		return nil, nil, certErr
	}
	if !os.IsNotExist(keyErr) && keyErr != nil {
		return nil, nil, keyErr
	}

	certPEM, keyPEM, err := issueInternalCA(policy)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(m.materialDir(policy.ID), 0755); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return nil, nil, err
	}
	return certPEM, keyPEM, nil
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
