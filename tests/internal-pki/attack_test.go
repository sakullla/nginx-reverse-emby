//go:build integration

package internalpki

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"
)

const protectedBackupPassphrase = "e2e-backup-passphrase-strong"

type protectedBackupKDF struct {
	Algorithm   string `json:"algorithm"`
	MemoryKiB   uint32 `json:"memory_kib"`
	Iterations  uint32 `json:"iterations"`
	Parallelism uint8  `json:"parallelism"`
	KeyBytes    uint32 `json:"key_bytes"`
	Salt        []byte `json:"salt"`
}

type protectedBackupCipher struct {
	Algorithm string `json:"algorithm"`
	Nonce     []byte `json:"nonce"`
}

type protectedBackupEnvelope struct {
	Format     string                `json:"format"`
	KDF        protectedBackupKDF    `json:"kdf"`
	Cipher     protectedBackupCipher `json:"cipher"`
	Manifest   json.RawMessage       `json:"manifest"`
	Ciphertext []byte                `json:"ciphertext"`
}

type protectedBackupAAD struct {
	Format   string                `json:"format"`
	KDF      protectedBackupKDF    `json:"kdf"`
	Cipher   protectedBackupCipher `json:"cipher"`
	Manifest json.RawMessage       `json:"manifest"`
}

type protectedBackupAuthorityKey struct {
	AuthorityID string `json:"authority_id"`
	Generation  int64  `json:"generation"`
	PKCS8       []byte `json:"pkcs8"`
}

type protectedBackupPayload struct {
	SQLiteSnapshot []byte                        `json:"sqlite_snapshot"`
	AuthorityKeys  []protectedBackupAuthorityKey `json:"authority_keys"`
}

type trustedAuthorityFixture struct {
	DomainID    string
	AuthorityID string
	Generation  int64
	Certificate *x509.Certificate
	PrivateKey  *ecdsa.PrivateKey
}

type leafCertificateSpec struct {
	CommonName       string
	DomainID         string
	AgentID          string
	ListenerID       string
	IdentityURI      *url.URL
	NotBefore        time.Time
	NotAfter         time.Time
	Usage            x509.ExtKeyUsage
	DNSNames         []string
	IPAddresses      []net.IP
	CorruptSignature bool
}

func (h *testHarness) trustedAuthorityFromBackup(control controlInstance, archive []byte) trustedAuthorityFixture {
	h.t.Helper()
	var envelope protectedBackupEnvelope
	if err := json.Unmarshal(archive, &envelope); err != nil {
		h.t.Fatalf("decode protected backup envelope: %v", err)
	}
	if envelope.Format != "nre-pki-protected-backup-v1" || envelope.KDF.Algorithm != "argon2id" ||
		envelope.Cipher.Algorithm != "aes-256-gcm" || envelope.KDF.KeyBytes != 32 {
		h.t.Fatalf("unexpected protected backup metadata: format=%q kdf=%+v cipher=%+v", envelope.Format, envelope.KDF, envelope.Cipher)
	}
	aad, err := json.Marshal(protectedBackupAAD{
		Format: envelope.Format, KDF: envelope.KDF, Cipher: envelope.Cipher, Manifest: envelope.Manifest,
	})
	if err != nil {
		h.t.Fatalf("encode protected backup AAD: %v", err)
	}
	key := argon2.IDKey([]byte(protectedBackupPassphrase), envelope.KDF.Salt, envelope.KDF.Iterations,
		envelope.KDF.MemoryKiB, envelope.KDF.Parallelism, envelope.KDF.KeyBytes)
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		h.t.Fatalf("initialize protected backup cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		h.t.Fatalf("initialize protected backup AEAD: %v", err)
	}
	plaintext, err := gcm.Open(nil, envelope.Cipher.Nonce, envelope.Ciphertext, aad)
	if err != nil {
		h.t.Fatalf("authenticate protected backup fixture: %v", err)
	}
	defer clear(plaintext)
	var payload protectedBackupPayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		h.t.Fatalf("decode protected backup payload: %v", err)
	}
	var manifest struct {
		PKIDomainID string `json:"pki_domain_id"`
	}
	if err := json.Unmarshal(envelope.Manifest, &manifest); err != nil || strings.TrimSpace(manifest.PKIDomainID) == "" {
		h.t.Fatalf("decode protected backup manifest domain: %v", err)
	}

	response := h.mustJSON(http.MethodGet, control.baseURL+"/panel-api/pki/authorities", nil, map[string]string{
		"X-Panel-Token": h.panelToken,
	})
	if response.Status != http.StatusOK {
		h.t.Fatalf("list PKI authorities status = %d: %s", response.Status, response.Body)
	}
	var authorities struct {
		Authorities []struct {
			ID             string `json:"id"`
			Generation     int64  `json:"generation"`
			Status         string `json:"status"`
			CertificatePEM string `json:"certificate_pem"`
		} `json:"authorities"`
	}
	if err := json.Unmarshal(response.Body, &authorities); err != nil {
		h.t.Fatalf("decode PKI authorities: %v", err)
	}
	for _, authority := range authorities.Authorities {
		if authority.Status != "active" {
			continue
		}
		for _, candidate := range payload.AuthorityKeys {
			if candidate.AuthorityID != authority.ID || candidate.Generation != authority.Generation {
				continue
			}
			certificate := parseSingleCertificate(h.t, []byte(authority.CertificatePEM))
			parsedKey, err := x509.ParsePKCS8PrivateKey(candidate.PKCS8)
			if err != nil {
				h.t.Fatalf("parse exported CA private key: %v", err)
			}
			privateKey, ok := parsedKey.(*ecdsa.PrivateKey)
			if !ok || !privateKey.PublicKey.Equal(certificate.PublicKey) {
				h.t.Fatal("exported active CA key does not match its public certificate")
			}
			return trustedAuthorityFixture{
				DomainID: manifest.PKIDomainID, AuthorityID: authority.ID, Generation: authority.Generation,
				Certificate: certificate, PrivateKey: privateKey,
			}
		}
	}
	h.t.Fatalf("active authority key was absent from authenticated protected backup: authorities=%s", response.Body)
	return trustedAuthorityFixture{}
}

func (h *testHarness) assertRelayCertificateAttackMatrix(port int, authority trustedAuthorityFixture) {
	h.t.Helper()
	address := fmt.Sprintf("127.0.0.1:%d", port)
	now := time.Now().UTC()
	valid := issueLeafCertificate(h.t, authority, leafCertificateSpec{
		CommonName: "trusted-valid-agent", DomainID: authority.DomainID, AgentID: "trusted-valid-agent",
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), Usage: x509.ExtKeyUsageClientAuth,
	})
	if err := tlsHandshake(address, &valid); err != nil {
		h.t.Fatalf("PKI relay rejected the trusted valid attack-control certificate: %v", err)
	}
	if err := tlsHandshake(address, nil); err == nil {
		h.t.Fatal("PKI relay accepted a TLS client without a certificate")
	}

	untrustedCA := newUntrustedAuthority(h.t, now)
	tests := []struct {
		name        string
		authority   trustedAuthorityFixture
		spec        leafCertificateSpec
		failureOnly string
	}{
		{
			name: "untrusted-chain", authority: untrustedCA,
			spec:        leafCertificateSpec{CommonName: "untrusted-chain", DomainID: authority.DomainID, AgentID: "untrusted-chain", NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), Usage: x509.ExtKeyUsageClientAuth},
			failureOnly: "trust root",
		},
		{
			name: "expired", authority: authority,
			spec:        leafCertificateSpec{CommonName: "expired", DomainID: authority.DomainID, AgentID: "expired", NotBefore: now.Add(-2 * time.Hour), NotAfter: now.Add(-time.Hour), Usage: x509.ExtKeyUsageClientAuth},
			failureOnly: "validity interval",
		},
		{
			name: "wrong-eku", authority: authority,
			spec:        leafCertificateSpec{CommonName: "wrong-eku", DomainID: authority.DomainID, AgentID: "wrong-eku", NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), Usage: x509.ExtKeyUsageServerAuth},
			failureOnly: "extended key usage",
		},
		{
			name: "wrong-domain", authority: authority,
			spec:        leafCertificateSpec{CommonName: "wrong-domain", DomainID: "wrong-pki-domain", AgentID: "wrong-domain", NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), Usage: x509.ExtKeyUsageClientAuth},
			failureOnly: "SPIFFE trust domain",
		},
		{
			name: "wrong-agent-shape", authority: authority,
			spec:        leafCertificateSpec{CommonName: "wrong-agent-shape", IdentityURI: &url.URL{Scheme: "spiffe", Host: authority.DomainID, Path: "/agent/"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), Usage: x509.ExtKeyUsageClientAuth},
			failureOnly: "agent identity shape",
		},
		{
			name: "listener-in-client-certificate", authority: authority,
			spec:        leafCertificateSpec{CommonName: "wrong-listener", DomainID: authority.DomainID, AgentID: "wrong-listener", ListenerID: "42", NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), Usage: x509.ExtKeyUsageClientAuth},
			failureOnly: "listener identity on a client certificate",
		},
		{
			name: "invalid-signature", authority: authority,
			spec:        leafCertificateSpec{CommonName: "invalid-signature", DomainID: authority.DomainID, AgentID: "invalid-signature", NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), Usage: x509.ExtKeyUsageClientAuth, CorruptSignature: true},
			failureOnly: "certificate signature",
		},
	}
	for _, test := range tests {
		h.t.Run("reject-"+test.name, func(t *testing.T) {
			certificate := issueLeafCertificate(t, test.authority, test.spec)
			if err := tlsHandshake(address, &certificate); err == nil {
				t.Fatalf("PKI relay accepted certificate with only an invalid %s", test.failureOnly)
			}
		})
	}
}

func (h *testHarness) assertRelayClientIdentityAttackMatrix(frontendURL string, relayPort int, authority trustedAuthorityFixture, expectedAgentID string, expectedListenerID int) {
	h.t.Helper()
	now := time.Now().UTC()
	base := leafCertificateSpec{
		CommonName: "relay-server", DomainID: authority.DomainID, AgentID: expectedAgentID,
		ListenerID: fmtInt(expectedListenerID), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		Usage: x509.ExtKeyUsageServerAuth, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	tests := []struct {
		name   string
		spec   leafCertificateSpec
		accept bool
	}{
		{name: "wrong-agent", spec: withLeafIdentity(base, authority.DomainID, "another-agent", fmtInt(expectedListenerID))},
		{name: "wrong-listener", spec: withLeafIdentity(base, authority.DomainID, expectedAgentID, fmtInt(expectedListenerID+1))},
		{name: "wrong-domain", spec: withLeafIdentity(base, "another-pki-domain", expectedAgentID, fmtInt(expectedListenerID))},
		{name: "trusted-control", spec: base, accept: true},
	}
	for _, test := range tests {
		h.t.Run("relay-client-"+test.name, func(t *testing.T) {
			certificate := issueLeafCertificate(t, authority, test.spec)
			err := probeRelayClientTLS(t, frontendURL, relayPort, certificate)
			if test.accept && err != nil {
				t.Fatalf("relay client rejected trusted server identity control: %v", err)
			}
			if !test.accept && err == nil {
				t.Fatalf("relay client accepted server certificate with %s", test.name)
			}
		})
	}
}

func (h *testHarness) assertRevokedAgentCertificateIsFenced(control controlInstance, relayPort int, agentID, dataDir string, process *childProcess) {
	h.t.Helper()
	certificate := loadActiveAgentCertificate(h.t, dataDir)
	address := fmt.Sprintf("127.0.0.1:%d", relayPort)
	if err := tlsHandshake(address, &certificate); err != nil {
		h.t.Fatalf("relay rejected enrolled agent certificate before revocation: %v", err)
	}
	h.revokePKIIdentity(control, "agent", agentID, "")
	fenceCtx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
	defer cancel()
	if err := eventually(fenceCtx, 5*time.Second, func(context.Context) (bool, error) {
		return tlsHandshake(address, &certificate) != nil, nil
	}); err != nil {
		h.t.Fatalf("revoked agent certificate remained usable after five seconds: %v\n%s", err, control.process.failureLog())
	}
	process.stop()
}

func (h *testHarness) revokePKIIdentity(control controlInstance, kind, agentID, listenerID string) {
	h.t.Helper()
	response := h.mustJSON(http.MethodGet, control.baseURL+"/panel-api/pki/identities", nil, map[string]string{
		"X-Panel-Token": h.panelToken,
	})
	if response.Status != http.StatusOK {
		h.t.Fatalf("list PKI identities status = %d: %s", response.Status, response.Body)
	}
	var envelope struct {
		Identities []struct {
			ID         string `json:"id"`
			Kind       string `json:"kind"`
			AgentID    string `json:"agent_id"`
			ListenerID string `json:"listener_id"`
			State      string `json:"state"`
		} `json:"identities"`
	}
	if err := json.Unmarshal(response.Body, &envelope); err != nil {
		h.t.Fatalf("decode PKI identities: %v", err)
	}
	identityID := ""
	for _, identity := range envelope.Identities {
		if identity.Kind == kind && identity.AgentID == agentID && identity.ListenerID == listenerID && identity.State == "active" {
			identityID = identity.ID
			break
		}
	}
	if identityID == "" {
		h.t.Fatalf("active %s PKI identity not found for agent=%q listener=%q: %s", kind, agentID, listenerID, response.Body)
	}
	confirmation := h.mustJSON(http.MethodPost, control.baseURL+"/panel-api/pki/confirmations", map[string]string{
		"action": "revoke", "target_id": identityID,
	}, map[string]string{"X-Panel-Token": h.panelToken})
	if confirmation.Status != http.StatusCreated {
		h.t.Fatalf("issue revoke confirmation status = %d: %s", confirmation.Status, confirmation.Body)
	}
	var confirmationEnvelope struct {
		Confirmation struct {
			Nonce string `json:"nonce"`
		} `json:"confirmation"`
	}
	if err := json.Unmarshal(confirmation.Body, &confirmationEnvelope); err != nil || confirmationEnvelope.Confirmation.Nonce == "" {
		h.t.Fatalf("decode revoke confirmation: error=%v body=%s", err, confirmation.Body)
	}
	revoke := h.mustJSON(http.MethodPost, control.baseURL+"/panel-api/pki/identities/"+url.PathEscape(identityID)+"/revoke", map[string]string{
		"reason": "integration compromise simulation", "confirmation_nonce": confirmationEnvelope.Confirmation.Nonce,
	}, map[string]string{"X-Panel-Token": h.panelToken})
	if revoke.Status != http.StatusAccepted && !h.identityHasState(control, identityID, "revoked") {
		h.t.Fatalf("revoke PKI identity status = %d: %s", revoke.Status, revoke.Body)
	}
}

func (h *testHarness) identityHasState(control controlInstance, identityID, expected string) bool {
	response := h.mustJSON(http.MethodGet, control.baseURL+"/panel-api/pki/identities", nil, map[string]string{
		"X-Panel-Token": h.panelToken,
	})
	if response.Status != http.StatusOK {
		return false
	}
	var envelope struct {
		Identities []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"identities"`
	}
	if json.Unmarshal(response.Body, &envelope) != nil {
		return false
	}
	for _, identity := range envelope.Identities {
		if identity.ID == identityID {
			return identity.State == expected
		}
	}
	return false
}

func loadActiveAgentCertificate(t *testing.T, dataDir string) tls.Certificate {
	t.Helper()
	identityRoot := filepath.Join(dataDir, "pki", "identities", "agent")
	pointerBytes, err := os.ReadFile(filepath.Join(identityRoot, "active.json"))
	if err != nil {
		t.Fatalf("read active agent credential pointer: %v", err)
	}
	var pointer struct {
		Generation string `json:"generation"`
	}
	if err := json.Unmarshal(pointerBytes, &pointer); err != nil || pointer.Generation == "" || filepath.Base(pointer.Generation) != pointer.Generation {
		t.Fatalf("decode active agent credential pointer: generation=%q error=%v", pointer.Generation, err)
	}
	generationRoot := filepath.Join(identityRoot, "generations", pointer.Generation)
	certificatePEM, err := os.ReadFile(filepath.Join(generationRoot, "certificate.pem"))
	if err != nil {
		t.Fatalf("read active agent certificate: %v", err)
	}
	privateKeyPEM, err := os.ReadFile(filepath.Join(generationRoot, "private-key.pem"))
	if err != nil {
		t.Fatalf("read active agent private key: %v", err)
	}
	certificate, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		t.Fatalf("load active agent certificate: %v", err)
	}
	return certificate
}

func withLeafIdentity(base leafCertificateSpec, domainID, agentID, listenerID string) leafCertificateSpec {
	base.DomainID = domainID
	base.AgentID = agentID
	base.ListenerID = listenerID
	return base
}

func probeRelayClientTLS(t *testing.T, frontendURL string, relayPort int, certificate tls.Certificate) error {
	t.Helper()
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", relayPort))
	if err != nil {
		t.Fatalf("start fake relay listener: %v", err)
	}
	defer listener.Close()
	result := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			result <- acceptErr
			return
		}
		defer connection.Close()
		server := tls.Server(connection, &tls.Config{
			Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS13,
			ClientAuth: tls.RequestClientCert,
		})
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		result <- server.HandshakeContext(ctx)
	}()

	probeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, frontendURL, nil)
	if err != nil {
		t.Fatalf("create fake relay trigger request: %v", err)
	}
	go func() {
		client := &http.Client{Timeout: 2500 * time.Millisecond, Transport: &http.Transport{DisableKeepAlives: true}}
		response, requestErr := client.Do(request)
		if requestErr == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
	}()
	select {
	case handshakeErr := <-result:
		return handshakeErr
	case <-probeCtx.Done():
		return fmt.Errorf("relay client did not attempt a TLS handshake: %w", probeCtx.Err())
	}
}

func issueLeafCertificate(t *testing.T, authority trustedAuthorityFixture, spec leafCertificateSpec) tls.Certificate {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf fixture key: %v", err)
	}
	serialBytes := make([]byte, 16)
	if _, err := rand.Read(serialBytes); err != nil {
		t.Fatalf("generate leaf fixture serial: %v", err)
	}
	identityURI := spec.IdentityURI
	if identityURI == nil {
		path := "/agent/" + url.PathEscape(spec.AgentID)
		if spec.ListenerID != "" {
			path += "/listener/" + url.PathEscape(spec.ListenerID)
		}
		identityURI = &url.URL{Scheme: "spiffe", Host: spec.DomainID, Path: path}
	}
	template := &x509.Certificate{
		SerialNumber: newBigInt(serialBytes), Subject: pkix.Name{CommonName: spec.CommonName},
		NotBefore: spec.NotBefore, NotAfter: spec.NotAfter, SignatureAlgorithm: x509.ECDSAWithSHA256,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{spec.Usage},
		BasicConstraintsValid: true, URIs: []*url.URL{identityURI},
		DNSNames: append([]string(nil), spec.DNSNames...), IPAddresses: cloneIPs(spec.IPAddresses),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, authority.Certificate, &privateKey.PublicKey, authority.PrivateKey)
	if err != nil {
		t.Fatalf("issue leaf fixture certificate: %v", err)
	}
	if spec.CorruptSignature {
		der = append([]byte(nil), der...)
		der[len(der)-1] ^= 0x01
		if _, err := x509.ParseCertificate(der); err != nil {
			t.Fatalf("signature-only corruption made certificate unparsable: %v", err)
		}
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal leaf fixture key: %v", err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	)
	if err != nil {
		t.Fatalf("load leaf fixture key pair: %v", err)
	}
	certificate.Certificate = append(certificate.Certificate, append([]byte(nil), authority.Certificate.Raw...))
	return certificate
}

func newUntrustedAuthority(t *testing.T, now time.Time) trustedAuthorityFixture {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate untrusted CA key: %v", err)
	}
	serial := make([]byte, 16)
	if _, err := rand.Read(serial); err != nil {
		t.Fatalf("generate untrusted CA serial: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: newBigInt(serial), Subject: pkix.Name{CommonName: "untrusted-e2e-ca"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage:           x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create untrusted CA certificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse untrusted CA certificate: %v", err)
	}
	return trustedAuthorityFixture{DomainID: "untrusted-domain", Certificate: certificate, PrivateKey: privateKey}
}

func parseSingleCertificate(t *testing.T, encoded []byte) *x509.Certificate {
	t.Helper()
	block, rest := pem.Decode(bytes.TrimSpace(encoded))
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		t.Fatal("expected exactly one PEM certificate")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse PEM certificate: %v", err)
	}
	return certificate
}

func cloneIPs(values []net.IP) []net.IP {
	result := make([]net.IP, len(values))
	for index := range values {
		result[index] = append(net.IP(nil), values[index]...)
	}
	return result
}

func certificateSHA256(certificate *x509.Certificate) string {
	if certificate == nil {
		return ""
	}
	digest := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(digest[:])
}
