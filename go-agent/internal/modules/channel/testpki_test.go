package channel

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

// testCA is one in-memory tunnel authority issuing credentials with the
// SPIFFE URI profile the host tunnel PKI enforces.
type testCA struct {
	t      *testing.T
	domain string
	cert   *x509.Certificate
	key    *ecdsa.PrivateKey
	root   model.PKITrustRoot

	mu     sync.Mutex
	serial int64
}

func newTestCA(t *testing.T, domain string) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate test CA key: %v", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test tunnel authority"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create test CA certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse test CA certificate: %v", err)
	}
	fingerprint := sha256.Sum256(der)
	return &testCA{
		t:      t,
		domain: domain,
		cert:   cert,
		key:    key,
		root: model.PKITrustRoot{
			AuthorityID:       "authority-1",
			Generation:        1,
			Status:            "active",
			CertificatePEM:    string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
			FingerprintSHA256: hex.EncodeToString(fingerprint[:]),
			NotBefore:         template.NotBefore,
			NotAfter:          template.NotAfter,
		},
	}
}

func (ca *testCA) securityState() relay.TunnelSecurityState {
	return relay.TunnelSecurityState{
		Hash: "test-security-hash",
		Snapshot: model.PKISecuritySnapshot{
			PKIDomainID:      ca.domain,
			PKIEpoch:         1,
			SecurityRevision: 1,
			Full:             true,
			IssuedAt:         time.Now(),
			TrustRoots:       []model.PKITrustRoot{ca.root},
		},
	}
}

func (ca *testCA) issueLeaf(uris []*url.URL, dnsNames []string, usage x509.ExtKeyUsage) *tls.Certificate {
	ca.t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		ca.t.Fatalf("generate leaf key: %v", err)
	}
	ca.mu.Lock()
	ca.serial++
	serial := ca.serial
	ca.mu.Unlock()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(serial + 1),
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{usage},
		URIs:                  uris,
		DNSNames:              dnsNames,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		ca.t.Fatalf("issue leaf certificate: %v", err)
	}
	return &tls.Certificate{Certificate: [][]byte{der, ca.cert.Raw}, PrivateKey: key}
}

// testTunnelPKI is a self-contained per-agent tunnel credential provider
// backed by a shared test CA.
type testTunnelPKI struct {
	t        *testing.T
	ca       *testCA
	security relay.TunnelSecurityState

	mu     sync.Mutex
	issued map[string]testIssuedCredential
}

type testIssuedCredential struct {
	certificate *tls.Certificate
	metadata    relay.TunnelCredentialMetadata
}

func newTestTunnelPKI(t *testing.T, ca *testCA) *testTunnelPKI {
	t.Helper()
	return &testTunnelPKI{
		t:        t,
		ca:       ca,
		security: ca.securityState(),
		issued:   make(map[string]testIssuedCredential),
	}
}

func (p *testTunnelPKI) baseMetadata(storageIdentity string, certificate *tls.Certificate) relay.TunnelCredentialMetadata {
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		p.t.Fatalf("parse issued certificate: %v", err)
	}
	fingerprint := sha256.Sum256(leaf.Raw)
	return relay.TunnelCredentialMetadata{
		Generation:                  "gen-1",
		CredentialFingerprintSHA256: hex.EncodeToString(fingerprint[:]),
		CertificateID:               "certificate-" + storageIdentity,
		AuthorityID:                 p.ca.root.AuthorityID,
		CAGeneration:                p.ca.root.Generation,
		PKIDomainID:                 p.ca.domain,
		PKIEpoch:                    p.security.Snapshot.PKIEpoch,
		SecurityRevision:            p.security.Snapshot.SecurityRevision,
	}
}

// issueAgent stores an agent-identity credential under the shared agent
// storage identity.
func (p *testTunnelPKI) issueAgent(agentID string) {
	p.t.Helper()
	identityURI := &url.URL{Scheme: "spiffe", Host: p.ca.domain, Path: "/agent/" + url.PathEscape(agentID)}
	certificate := p.ca.issueLeaf([]*url.URL{identityURI}, nil, x509.ExtKeyUsageClientAuth)
	metadata := p.baseMetadata(relay.AgentTunnelCredentialIdentity, certificate)
	metadata.Purpose = model.PKICertificatePurposeClient
	metadata.IdentityID = "identity-agent-" + agentID
	metadata.AgentID = agentID
	p.mu.Lock()
	p.issued[relay.AgentTunnelCredentialIdentity] = testIssuedCredential{certificate: certificate, metadata: metadata}
	p.mu.Unlock()
}

// issueListener stores a relay listener server credential under the listener
// storage identity used by the relay module.
func (p *testTunnelPKI) issueListener(agentID string, listenerID int, publicHost string) {
	p.t.Helper()
	identityURI := &url.URL{
		Scheme: "spiffe",
		Host:   p.ca.domain,
		Path:   "/agent/" + url.PathEscape(agentID) + "/listener/" + strconv.Itoa(listenerID),
	}
	certificate := p.ca.issueLeaf([]*url.URL{identityURI}, []string{publicHost}, x509.ExtKeyUsageServerAuth)
	storageIdentity := fmt.Sprintf("listener-%d", listenerID)
	metadata := p.baseMetadata(storageIdentity, certificate)
	metadata.Purpose = model.PKICertificatePurposeServer
	metadata.IdentityID = fmt.Sprintf("identity-listener-%d", listenerID)
	metadata.AgentID = agentID
	metadata.ListenerID = strconv.Itoa(listenerID)
	metadata.DNSNames = []string{publicHost}
	p.mu.Lock()
	p.issued[storageIdentity] = testIssuedCredential{certificate: certificate, metadata: metadata}
	p.mu.Unlock()
}

// listenerIdentityID returns the PKI identity a panel would attach to the
// relay listener projection for the given listener id.
func (p *testTunnelPKI) listenerIdentityID(listenerID int) string {
	return fmt.Sprintf("identity-listener-%d", listenerID)
}

func (p *testTunnelPKI) InstallTunnelCertificate(_ context.Context, storageIdentity string, config *tls.Config) (relay.TunnelCredentialMetadata, error) {
	p.mu.Lock()
	issued, ok := p.issued[storageIdentity]
	p.mu.Unlock()
	if !ok {
		return relay.TunnelCredentialMetadata{}, fmt.Errorf("test credential %q is not issued", storageIdentity)
	}
	config.Certificates = nil
	config.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
		copyValue := *issued.certificate
		return &copyValue, nil
	}
	config.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		copyValue := *issued.certificate
		return &copyValue, nil
	}
	return issued.metadata, nil
}

func (p *testTunnelPKI) LoadTunnelCredential(_ context.Context, storageIdentity string) (relay.TunnelCredentialMetadata, error) {
	p.mu.Lock()
	issued, ok := p.issued[storageIdentity]
	p.mu.Unlock()
	if !ok {
		return relay.TunnelCredentialMetadata{}, fmt.Errorf("test credential %q is not issued", storageIdentity)
	}
	return issued.metadata, nil
}

func (p *testTunnelPKI) LoadTunnelSecurity(context.Context) (relay.TunnelSecurityState, error) {
	return p.security, nil
}

var _ relay.TunnelCredentialProvider = (*testTunnelPKI)(nil)
