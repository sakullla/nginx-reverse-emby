package relay

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
	"errors"
	"math/big"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestRelayTLSAndQUICMTLSUseSingleStrictVerifier(t *testing.T) {
	fixture := newRelayMTLSFixture(t)
	listener := fixture.listener()

	serverTLS, err := serverTLSConfig(t.Context(), fixture.provider, listener)
	if err != nil {
		t.Fatalf("serverTLSConfig() error = %v", err)
	}
	clientTLS, err := clientTLSConfig(t.Context(), fixture.provider, listener, "127.0.0.1:443", "")
	if err != nil {
		t.Fatalf("clientTLSConfig() error = %v", err)
	}
	serverQUIC, err := serverQUICTLSConfig(t.Context(), fixture.provider, listener)
	if err != nil {
		t.Fatalf("serverQUICTLSConfig() error = %v", err)
	}
	clientQUIC, err := clientQUICTLSConfig(t.Context(), fixture.provider, listener, "127.0.0.1:443", "")
	if err != nil {
		t.Fatalf("clientQUICTLSConfig() error = %v", err)
	}

	for name, config := range map[string]*tls.Config{
		"server tls_tcp": serverTLS,
		"client tls_tcp": clientTLS,
		"server quic":    serverQUIC,
		"client quic":    clientQUIC,
	} {
		if config.MinVersion != tls.VersionTLS13 || config.VerifyConnection == nil || !config.SessionTicketsDisabled {
			t.Fatalf("%s does not preserve strict shared mTLS policy: %+v", name, config)
		}
	}
	if serverTLS.ClientAuth != tls.RequireAnyClientCert || serverTLS.GetCertificate == nil {
		t.Fatalf("TLS/TCP server config is not strict mutual TLS: %+v", serverTLS)
	}
	if serverQUIC.ClientAuth != serverTLS.ClientAuth || serverQUIC.GetCertificate == nil {
		t.Fatalf("QUIC server changed mutual TLS policy: %+v", serverQUIC)
	}
	if clientTLS.GetClientCertificate == nil || clientQUIC.GetClientCertificate == nil {
		t.Fatal("relay clients did not install the tunnel client credential")
	}
	if strings.Join(serverQUIC.NextProtos, ",") != relayQUICALPN || strings.Join(clientQUIC.NextProtos, ",") != relayQUICALPN {
		t.Fatalf("QUIC ALPN = server %v client %v", serverQUIC.NextProtos, clientQUIC.NextProtos)
	}

	serverExpectation := tunnelPeerExpectation{
		purpose: model.PKICertificatePurposeServer, domain: fixture.domain,
		agentID: fixture.agentID, listenerID: fixture.listenerID,
		identityID: fixture.listenerIdentityID, verificationName: "127.0.0.1",
	}
	clientExpectation := tunnelPeerExpectation{purpose: model.PKICertificatePurposeClient, domain: fixture.domain}
	if _, err := verifyTunnelPeer([]*x509.Certificate{fixture.server.leaf}, fixture.security, serverExpectation); err != nil {
		t.Fatalf("valid server peer rejected: %v", err)
	}
	if _, err := verifyTunnelPeer([]*x509.Certificate{fixture.client.leaf}, fixture.security, clientExpectation); err != nil {
		t.Fatalf("valid client peer rejected: %v", err)
	}

	untrusted := newRelayMTLSAuthority(t, "untrusted")
	wrongAuthorityServer := untrusted.issue(t, relayMTLSCertificateSpec{
		serial: 30, domain: fixture.domain, agentID: fixture.agentID, listenerID: fixture.listenerID,
		purpose: model.PKICertificatePurposeServer, ipAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	})
	expired := fixture.authority.issue(t, relayMTLSCertificateSpec{
		serial: 31, domain: fixture.domain, agentID: fixture.agentID, listenerID: fixture.listenerID,
		purpose: model.PKICertificatePurposeServer, ipAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		notBefore: time.Now().Add(-2 * time.Hour), notAfter: time.Now().Add(-time.Hour),
	})
	wrongEKU := fixture.authority.issue(t, relayMTLSCertificateSpec{
		serial: 32, domain: fixture.domain, agentID: fixture.agentID, listenerID: fixture.listenerID,
		purpose: model.PKICertificatePurposeClient,
	})
	wrongAgent := fixture.authority.issue(t, relayMTLSCertificateSpec{
		serial: 33, domain: fixture.domain, agentID: "another-agent", listenerID: fixture.listenerID,
		purpose: model.PKICertificatePurposeServer, ipAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	})
	wrongListener := fixture.authority.issue(t, relayMTLSCertificateSpec{
		serial: 34, domain: fixture.domain, agentID: fixture.agentID, listenerID: "999",
		purpose: model.PKICertificatePurposeServer, ipAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	})
	wrongDomain := fixture.authority.issue(t, relayMTLSCertificateSpec{
		serial: 35, domain: "another-domain", agentID: fixture.agentID, listenerID: fixture.listenerID,
		purpose: model.PKICertificatePurposeServer, ipAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	})
	wrongSAN := fixture.authority.issue(t, relayMTLSCertificateSpec{
		serial: 36, domain: fixture.domain, agentID: fixture.agentID, listenerID: fixture.listenerID,
		purpose: model.PKICertificatePurposeServer, dnsNames: []string{"relay.example"},
	})
	revokedSerial := cloneTunnelSecurityState(fixture.security)
	revokedSerial.Snapshot.RevokedSerials = []string{strings.ToLower(fixture.server.leaf.SerialNumber.Text(16))}
	revokedIdentity := cloneTunnelSecurityState(fixture.security)
	revokedIdentity.Snapshot.RevokedIdentityIDs = []string{fixture.listenerIdentityID}

	attacks := []struct {
		name         string
		certificates []*x509.Certificate
		security     TunnelSecurityState
		expectation  tunnelPeerExpectation
	}{
		{name: "missing certificate", security: fixture.security, expectation: serverExpectation},
		{name: "untrusted authority", certificates: []*x509.Certificate{wrongAuthorityServer.leaf}, security: fixture.security, expectation: serverExpectation},
		{name: "expired", certificates: []*x509.Certificate{expired.leaf}, security: fixture.security, expectation: serverExpectation},
		{name: "wrong EKU", certificates: []*x509.Certificate{wrongEKU.leaf}, security: fixture.security, expectation: serverExpectation},
		{name: "wrong agent", certificates: []*x509.Certificate{wrongAgent.leaf}, security: fixture.security, expectation: serverExpectation},
		{name: "wrong listener", certificates: []*x509.Certificate{wrongListener.leaf}, security: fixture.security, expectation: serverExpectation},
		{name: "wrong PKI domain", certificates: []*x509.Certificate{wrongDomain.leaf}, security: fixture.security, expectation: serverExpectation},
		{name: "wrong DNS or IP", certificates: []*x509.Certificate{wrongSAN.leaf}, security: fixture.security, expectation: serverExpectation},
		{name: "revoked serial", certificates: []*x509.Certificate{fixture.server.leaf}, security: revokedSerial, expectation: serverExpectation},
		{name: "revoked identity", certificates: []*x509.Certificate{fixture.server.leaf}, security: revokedIdentity, expectation: serverExpectation},
	}
	for _, attack := range attacks {
		t.Run(attack.name, func(t *testing.T) {
			if _, err := verifyTunnelPeer(attack.certificates, attack.security, attack.expectation); err == nil {
				t.Fatal("attack certificate was accepted")
			}
		})
	}
}

func TestTunnelIdentityURIRequiresCanonicalAgentAndListenerShape(t *testing.T) {
	fixture := newRelayMTLSFixture(t)
	agentID, listenerID, err := parseTunnelIdentityURI(fixture.server.leaf, fixture.domain)
	if err != nil {
		t.Fatalf("parseTunnelIdentityURI() error = %v", err)
	}
	if agentID != fixture.agentID || listenerID != fixture.listenerID {
		t.Fatalf("identity = %q/%q", agentID, listenerID)
	}

	for _, raw := range []string{
		"https://" + fixture.domain + "/agent/" + fixture.agentID + "/listener/" + fixture.listenerID,
		"spiffe://another-domain/agent/" + fixture.agentID + "/listener/" + fixture.listenerID,
		"spiffe://" + fixture.domain + "/agent/" + fixture.agentID + "/listener",
		"spiffe://" + fixture.domain + "/listener/" + fixture.listenerID,
		"spiffe://" + fixture.domain + "/agent/" + fixture.agentID + "/listener/" + fixture.listenerID + "?role=relay",
	} {
		t.Run(raw, func(t *testing.T) {
			parsed, err := url.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			certificate := *fixture.server.leaf
			certificate.URIs = []*url.URL{parsed}
			if _, _, err := parseTunnelIdentityURI(&certificate, fixture.domain); err == nil {
				t.Fatal("invalid URI identity was accepted")
			}
		})
	}
}

func TestSecuritySnapshotBindsPoolsFallbackAndEmergencyFence(t *testing.T) {
	fixture := newRelayMTLSFixture(t)
	hop := Hop{Address: "127.0.0.1:443", Listener: fixture.listener()}
	previousProcessProvider := processTunnelCredentialProvider()
	SetProcessTunnelCredentialProvider(fixture.provider)
	t.Cleanup(func() { SetProcessTunnelCredentialProvider(previousProcessProvider) })
	if _, err := bindTunnelSecurityToHop(t.Context(), relayMTLSLegacyProvider{}, hop); err != nil {
		t.Fatalf("shared managed-certificate provider did not reach the process tunnel facade: %v", err)
	}
	first, err := bindTunnelSecurityToHop(t.Context(), fixture.provider, hop)
	if err != nil {
		t.Fatalf("bindTunnelSecurityToHop() error = %v", err)
	}
	firstTLSKey, err := tlsTCPSessionPoolKey(first, "")
	if err != nil {
		t.Fatal(err)
	}
	firstQUICKey, err := quicSessionPoolKey(first)
	if err != nil {
		t.Fatal(err)
	}
	if first.securityBinding == "" || !strings.Contains(firstTLSKey, first.securityBinding) || !strings.Contains(firstQUICKey, first.securityBinding) {
		t.Fatalf("security binding missing from pool keys: binding=%q tls=%q quic=%q", first.securityBinding, firstTLSKey, firstQUICKey)
	}

	nextSecurity := cloneTunnelSecurityState(fixture.security)
	nextSecurity.Hash = "security-revision-9"
	nextSecurity.Snapshot.SecurityRevision++
	fixture.provider.setSecurity(nextSecurity)
	second, err := bindTunnelSecurityToHop(t.Context(), fixture.provider, hop)
	if err != nil {
		t.Fatal(err)
	}
	secondTLSKey, _ := tlsTCPSessionPoolKey(second, "")
	secondQUICKey, _ := quicSessionPoolKey(second)
	if firstTLSKey == secondTLSKey || firstQUICKey == secondQUICKey {
		t.Fatal("security revision reused an old TLS/TCP or QUIC pool key")
	}
	fallbacks := newRelayVerifiedFallbackStore()
	fallbacks.Mark(first)
	if !fallbacks.Has(first) || fallbacks.Has(second) {
		t.Fatal("fallback verification crossed a PKI security generation")
	}

	module := NewModule(Config{})
	module.SetTunnelCredentialProvider(fixture.provider)
	server := newRelayServer(t.Context(), fixture.provider, StartOptions{})
	server.trackTunnelListener(fixture.listener())
	tracked, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	server.trackConn(tracked)
	poolConn, poolPeer := net.Pipe()
	t.Cleanup(func() { _ = poolPeer.Close() })
	pooled := newTestGenerationTunnel(poolConn)
	server.poolScope.tls.mu.Lock()
	server.poolScope.tls.sessions["security-bound"] = []*tlsTCPTunnel{pooled}
	server.poolScope.tls.mu.Unlock()
	module.trackRuntime(server)
	if err := module.ReconcileTunnelSecurity(t.Context()); err != nil {
		t.Fatalf("baseline ReconcileTunnelSecurity() error = %v", err)
	}

	emergency := cloneTunnelSecurityState(nextSecurity)
	emergency.Hash = "security-revision-10-revoked"
	emergency.Snapshot.SecurityRevision++
	emergency.Snapshot.RevokedIdentityIDs = []string{fixture.listenerIdentityID}
	fixture.provider.setSecurity(emergency)
	started := time.Now()
	if err := module.ReconcileTunnelSecurity(t.Context()); err != nil {
		t.Fatalf("emergency ReconcileTunnelSecurity() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("emergency fence took %s", elapsed)
	}
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := peer.Read(make([]byte, 1)); err == nil {
		t.Fatal("tracked relay session remained open after revocation")
	}
	select {
	case <-pooled.closed:
	case <-time.After(time.Second):
		t.Fatal("security-bound pooled tunnel remained open after revocation")
	}
	module.mu.Lock()
	remaining := len(module.runtimes)
	module.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("tracked fenced runtimes = %d, want 0", remaining)
	}
}

func TestRelayMTLSGlobalOutboundPoolsFenceWithoutLocalListener(t *testing.T) {
	for _, transportMode := range []string{ListenerTransportModeTLSTCP, ListenerTransportModeQUIC} {
		t.Run(transportMode, func(t *testing.T) {
			resetTLSTCPSessionPoolForTest()
			t.Cleanup(resetTLSTCPSessionPoolForTest)

			fixture := newRelayMTLSFixture(t)
			clientProvider := fixture.provider.clone()
			listener := fixture.listener()
			listener.TransportMode = transportMode
			listener.AllowTransportFallback = false
			if transportMode == ListenerTransportModeQUIC {
				listener.ListenPort = pickFreeUDPPort(t)
			} else {
				listener.ListenPort = pickFreeTCPPort(t)
			}
			listener.PublicPort = listener.ListenPort
			address := net.JoinHostPort(listener.PublicHost, strconv.Itoa(listener.PublicPort))

			backendAddress, stopBackend := startTCPEchoServer(t)
			defer stopBackend()
			peer, err := Start(t.Context(), []Listener{listener}, fixture.provider)
			if err != nil {
				t.Fatalf("Start(peer) error = %v", err)
			}
			defer peer.Close()

			module := NewModule(Config{})
			module.SetTunnelCredentialProvider(clientProvider)
			if err := module.ReconcileTunnelSecurity(t.Context()); err != nil {
				t.Fatalf("baseline ReconcileTunnelSecurity() error = %v", err)
			}
			module.mu.Lock()
			trackedBeforeFence := len(module.runtimes)
			module.mu.Unlock()
			if trackedBeforeFence != 0 {
				t.Fatalf("client module tracked runtimes = %d, want 0", trackedBeforeFence)
			}

			fencedScope := globalRelayPoolScope()
			conn, result, err := DialWithResult(t.Context(), "tcp", backendAddress, []Hop{{Address: address, Listener: listener}}, clientProvider)
			if err != nil {
				t.Fatalf("DialWithResult(%s) error = %v", transportMode, err)
			}
			defer conn.Close()
			if result.TransportMode != transportMode {
				t.Fatalf("DialWithResult() transport = %q, want %q", result.TransportMode, transportMode)
			}
			assertRoundTrip(t, conn, []byte("before-security-fence"))

			emergency := cloneTunnelSecurityState(fixture.security)
			emergency.Hash = "security-revision-9-revoked-listener"
			emergency.Snapshot.SecurityRevision++
			emergency.Snapshot.RevokedIdentityIDs = []string{fixture.listenerIdentityID}
			clientProvider.setSecurity(emergency)
			started := time.Now()
			if err := module.ReconcileTunnelSecurity(t.Context()); err != nil {
				t.Fatalf("emergency ReconcileTunnelSecurity() error = %v", err)
			}
			if elapsed := time.Since(started); elapsed > 5*time.Second {
				t.Fatalf("global outbound fence took %s", elapsed)
			}
			if current := globalRelayPoolScope(); current == fencedScope {
				t.Fatal("emergency fence did not atomically replace the global outbound pools")
			}
			if !fencedScope.quic.isClosed() || !fencedScope.tls.isClosed() {
				t.Fatal("emergency fence left an old outbound transport pool open")
			}
			assertRelayConnectionFenced(t, conn)

			module.mu.Lock()
			trackedAfterFence := len(module.runtimes)
			module.mu.Unlock()
			if trackedAfterFence != 0 {
				t.Fatalf("client module tracked runtimes after fence = %d, want 0", trackedAfterFence)
			}
			peer.mu.Lock()
			peerClosing := peer.closing
			peer.mu.Unlock()
			if peerClosing {
				t.Fatal("untracked relay peer was closed and masked the outbound-pool fence")
			}

			healthyConn, healthyResult, err := DialWithResult(t.Context(), "tcp", backendAddress, []Hop{{Address: address, Listener: listener}}, fixture.provider)
			if err != nil {
				t.Fatalf("peer did not remain online after client fence: %v", err)
			}
			defer healthyConn.Close()
			if healthyResult.TransportMode != transportMode {
				t.Fatalf("healthy peer transport = %q, want %q", healthyResult.TransportMode, transportMode)
			}
			assertRoundTrip(t, healthyConn, []byte("peer-still-online"))
		})
	}
}

func assertRelayConnectionFenced(t *testing.T, conn net.Conn) {
	t.Helper()
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		return
	}
	payload := []byte("after-security-fence")
	if _, err := conn.Write(payload); err != nil {
		return
	}
	buffer := make([]byte, len(payload))
	if count, err := conn.Read(buffer); err == nil || count > 0 {
		t.Fatalf("fenced relay stream remained usable: read=%d error=%v", count, err)
	}
}

type relayMTLSFixture struct {
	domain             string
	agentID            string
	listenerID         string
	listenerIdentityID string
	authority          relayMTLSAuthority
	client             relayMTLSCertificate
	server             relayMTLSCertificate
	security           TunnelSecurityState
	provider           *relayMTLSProvider
}

func newRelayMTLSFixture(t *testing.T) relayMTLSFixture {
	t.Helper()
	authority := newRelayMTLSAuthority(t, "authority-1")
	domain := "pki.test"
	agentID := "edge-1"
	listenerID := "41"
	client := authority.issue(t, relayMTLSCertificateSpec{
		serial: 11, domain: domain, agentID: agentID, purpose: model.PKICertificatePurposeClient,
	})
	server := authority.issue(t, relayMTLSCertificateSpec{
		serial: 12, domain: domain, agentID: agentID, listenerID: listenerID,
		purpose: model.PKICertificatePurposeServer, ipAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	})
	security := TunnelSecurityState{
		Hash: "security-revision-8",
		Snapshot: model.PKISecuritySnapshot{
			PKIDomainID: domain, PKIEpoch: 2, SecurityRevision: 8, Full: true,
			TrustRoots: []model.PKITrustRoot{authority.trustRoot(3, "active")},
		},
	}
	provider := &relayMTLSProvider{
		security: security,
		credentials: map[string]relayMTLSProviderCredential{
			AgentTunnelCredentialIdentity: {
				certificate: client.tls,
				metadata:    relayMTLSMetadata(client, "agent-generation", "agent-identity", "agent-certificate", domain, agentID, "", model.PKICertificatePurposeClient),
			},
			relayListenerStorageIdentity(41): {
				certificate: server.tls,
				metadata:    relayMTLSMetadata(server, "listener-generation", "listener-identity", "listener-certificate", domain, agentID, listenerID, model.PKICertificatePurposeServer),
			},
		},
	}
	return relayMTLSFixture{
		domain: domain, agentID: agentID, listenerID: listenerID, listenerIdentityID: "listener-identity",
		authority: authority, client: client, server: server, security: security, provider: provider,
	}
}

func (f relayMTLSFixture) listener() Listener {
	return Listener{
		ID: 41, AgentID: f.agentID, Name: "relay-mtls", ListenHost: "127.0.0.1", BindHosts: []string{"127.0.0.1"},
		ListenPort: 443, PublicHost: "127.0.0.1", PublicPort: 443, Enabled: true,
		TLSMode: TLSModePKIMTLS, TransportMode: ListenerTransportModeQUIC, AllowTransportFallback: true,
		PKIIdentityID: f.listenerIdentityID, PKIIdentityState: PKIIdentityStateActive, PKICertificateID: "listener-certificate", Revision: 7,
	}
}

type relayMTLSAuthority struct {
	id          string
	certificate *x509.Certificate
	privateKey  *ecdsa.PrivateKey
	encoded     string
	fingerprint string
}

func newRelayMTLSAuthority(t *testing.T, id string) relayMTLSAuthority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: id},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true, IsCA: true, MaxPathLen: 0,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(der)
	return relayMTLSAuthority{
		id: id, certificate: certificate, privateKey: key,
		encoded:     string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		fingerprint: hex.EncodeToString(digest[:]),
	}
}

func (a relayMTLSAuthority) trustRoot(generation int64, status string) model.PKITrustRoot {
	return model.PKITrustRoot{
		AuthorityID: a.id, Generation: generation, Status: status,
		CertificatePEM: a.encoded, FingerprintSHA256: a.fingerprint,
		NotBefore: a.certificate.NotBefore, NotAfter: a.certificate.NotAfter,
	}
}

type relayMTLSCertificateSpec struct {
	serial      int64
	domain      string
	agentID     string
	listenerID  string
	purpose     string
	dnsNames    []string
	ipAddresses []net.IP
	notBefore   time.Time
	notAfter    time.Time
}

type relayMTLSCertificate struct {
	tls  tls.Certificate
	leaf *x509.Certificate
}

func (a relayMTLSAuthority) issue(t *testing.T, spec relayMTLSCertificateSpec) relayMTLSCertificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if spec.notBefore.IsZero() {
		spec.notBefore = now.Add(-time.Hour)
	}
	if spec.notAfter.IsZero() {
		spec.notAfter = now.Add(12 * time.Hour)
	}
	identity := &url.URL{Scheme: "spiffe", Host: spec.domain, Path: "/agent/" + spec.agentID}
	if spec.listenerID != "" {
		identity.Path += "/listener/" + spec.listenerID
	}
	usage := x509.ExtKeyUsageClientAuth
	if spec.purpose == model.PKICertificatePurposeServer {
		usage = x509.ExtKeyUsageServerAuth
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(spec.serial), Subject: pkix.Name{CommonName: identity.String()},
		NotBefore: spec.notBefore, NotAfter: spec.notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
		BasicConstraintsValid: true, URIs: []*url.URL{identity},
		DNSNames: append([]string(nil), spec.dnsNames...), IPAddresses: append([]net.IP(nil), spec.ipAddresses...),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.certificate, &key.PublicKey, a.privateKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return relayMTLSCertificate{
		tls:  tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf},
		leaf: leaf,
	}
}

func relayMTLSMetadata(certificate relayMTLSCertificate, generation, identityID, certificateID, domain, agentID, listenerID, purpose string) TunnelCredentialMetadata {
	digest := sha256.Sum256(certificate.leaf.Raw)
	return TunnelCredentialMetadata{
		Generation: generation, CredentialFingerprintSHA256: hex.EncodeToString(digest[:]),
		IdentityID: identityID, CertificateID: certificateID, Purpose: purpose,
		AuthorityID: "authority-1", CAGeneration: 3, PKIDomainID: domain,
		PKIEpoch: 2, SecurityRevision: 8, AgentID: agentID, ListenerID: listenerID,
	}
}

type relayMTLSProviderCredential struct {
	metadata    TunnelCredentialMetadata
	certificate tls.Certificate
}

type relayMTLSLegacyProvider struct{}

func (relayMTLSLegacyProvider) ServerCertificate(context.Context, int) (*tls.Certificate, error) {
	return nil, errors.New("managed certificate not found")
}

func (relayMTLSLegacyProvider) TrustedCAPool(context.Context, []int) (*x509.CertPool, error) {
	return nil, errors.New("managed CA pool not found")
}

type relayMTLSProvider struct {
	mu          sync.Mutex
	security    TunnelSecurityState
	credentials map[string]relayMTLSProviderCredential
}

func (p *relayMTLSProvider) ServerCertificate(context.Context, int) (*tls.Certificate, error) {
	return nil, errors.New("managed certificates are unavailable for pki_mtls")
}

func (p *relayMTLSProvider) TrustedCAPool(context.Context, []int) (*x509.CertPool, error) {
	return nil, errors.New("public CA pools are unavailable for pki_mtls")
}

func (p *relayMTLSProvider) InstallTunnelCertificate(_ context.Context, storageIdentity string, config *tls.Config) (TunnelCredentialMetadata, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	credential, ok := p.credentials[storageIdentity]
	if !ok {
		return TunnelCredentialMetadata{}, errors.New("credential not found")
	}
	certificate := credential.certificate
	if credential.metadata.Purpose == model.PKICertificatePurposeClient {
		config.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			copyValue := certificate
			return &copyValue, nil
		}
	} else {
		config.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			copyValue := certificate
			return &copyValue, nil
		}
	}
	return credential.metadata, nil
}

func (p *relayMTLSProvider) LoadTunnelCredential(_ context.Context, storageIdentity string) (TunnelCredentialMetadata, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	credential, ok := p.credentials[storageIdentity]
	if !ok {
		return TunnelCredentialMetadata{}, errors.New("credential not found")
	}
	return credential.metadata, nil
}

func (p *relayMTLSProvider) LoadTunnelSecurity(context.Context) (TunnelSecurityState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return cloneTunnelSecurityState(p.security), nil
}

func (p *relayMTLSProvider) setSecurity(security TunnelSecurityState) {
	p.mu.Lock()
	p.security = cloneTunnelSecurityState(security)
	p.mu.Unlock()
}

func (p *relayMTLSProvider) clone() *relayMTLSProvider {
	p.mu.Lock()
	defer p.mu.Unlock()
	credentials := make(map[string]relayMTLSProviderCredential, len(p.credentials))
	for identity, credential := range p.credentials {
		credentials[identity] = credential
	}
	return &relayMTLSProvider{
		security:    cloneTunnelSecurityState(p.security),
		credentials: credentials,
	}
}
