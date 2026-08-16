//go:build !integration

package app

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
	"io"
	"math/big"
	"net"
	"net/http"

	"net/url"
	"os"

	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"

	modulerelay "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

type lifecyclePKIHeartbeatHandler struct {
	mu               sync.Mutex
	status           string
	securityRevision int64
}

func (*lifecyclePKIHeartbeatHandler) PrepareHeartbeat(context.Context) (control.PKIHeartbeatState, error) {
	return control.PKIHeartbeatState{}, nil
}

func (h *lifecyclePKIHeartbeatHandler) ApplyHeartbeat(_ context.Context, reply control.PKIHeartbeatReply) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if reply.Status != nil {
		h.status = reply.Status.Status
	}
	if reply.Security != nil {
		h.securityRevision = reply.Security.SecurityRevision
	}
	return nil
}

func (h *lifecyclePKIHeartbeatHandler) applied() (string, int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status, h.securityRevision
}

type lifecycleControlRequestFacts struct {
	mu                 sync.Mutex
	heartbeats         int
	pulls              int
	tokenFailures      int
	clientCertificates int
}

func (f *lifecycleControlRequestFacts) record(request *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if request.Header.Get("X-Agent-Token") != "control-token" {
		f.tokenFailures++
	}
	if request.TLS != nil {
		f.clientCertificates += len(request.TLS.PeerCertificates)
	}
	switch request.URL.Path {
	case "/api/agents/heartbeat":
		f.heartbeats++
	case "/api/agent-revisions/pull":
		f.pulls++
	}
}

func (f *lifecycleControlRequestFacts) snapshot() (int, int, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.heartbeats, f.pulls, f.tokenFailures, f.clientCertificates
}

type lifecycleRelayMTLSFixture struct {
	domain   string
	agentID  string
	security modulerelay.TunnelSecurityState
	provider *lifecycleRelayMTLSProvider
}

func newLifecycleRelayMTLSFixture(t *testing.T) lifecycleRelayMTLSFixture {
	t.Helper()
	domain := "lifecycle-pki.test"
	agentID := "edge-lifecycle"
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "lifecycle-authority"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	caFingerprint := sha256.Sum256(caDER)
	security := modulerelay.TunnelSecurityState{
		Hash: "lifecycle-security-8",
		Snapshot: model.PKISecuritySnapshot{
			PKIDomainID: domain, PKIEpoch: 1, SecurityRevision: 8, Full: true,
			TrustRoots: []model.PKITrustRoot{{
				AuthorityID: "lifecycle-authority", Generation: 1, Status: "active",
				CertificatePEM: caPEM, FingerprintSHA256: hex.EncodeToString(caFingerprint[:]),
				NotBefore: ca.NotBefore, NotAfter: ca.NotAfter,
			}},
		},
	}
	client := lifecycleIssueRelayCertificate(t, ca, caKey, lifecycleRelayCertificateSpec{
		serial: 2, domain: domain, agentID: agentID, purpose: model.PKICertificatePurposeClient,
	})
	server := lifecycleIssueRelayCertificate(t, ca, caKey, lifecycleRelayCertificateSpec{
		serial: 3, domain: domain, agentID: agentID, listenerID: "71", purpose: model.PKICertificatePurposeServer,
		ipAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	})
	provider := &lifecycleRelayMTLSProvider{
		security: security,
		credentials: map[string]lifecycleRelayCredential{
			modulerelay.AgentTunnelCredentialIdentity: {
				certificate: client, metadata: lifecycleRelayMetadata(client, "agent-generation", "agent-identity", "agent-certificate", domain, agentID, "", model.PKICertificatePurposeClient),
			},
			"listener-71": {
				certificate: server, metadata: lifecycleRelayMetadata(server, "listener-generation", "listener-identity", "listener-certificate", domain, agentID, "71", model.PKICertificatePurposeServer),
			},
		},
	}
	return lifecycleRelayMTLSFixture{domain: domain, agentID: agentID, security: security, provider: provider}
}

func (f lifecycleRelayMTLSFixture) listener(port int) model.RelayListener {
	return model.RelayListener{
		ID: 71, AgentID: f.agentID, Name: "lifecycle-relay", ListenHost: "127.0.0.1", BindHosts: []string{"127.0.0.1"},
		ListenPort: port, PublicHost: "127.0.0.1", PublicPort: port, Enabled: true,
		TLSMode: modulerelay.TLSModePKIMTLS, TransportMode: modulerelay.ListenerTransportModeTLSTCP,
		PKIIdentityID: "listener-identity", PKIIdentityState: modulerelay.PKIIdentityStateActive,
		PKICertificateID: "listener-certificate", Revision: 7,
	}
}

type lifecycleRelayCertificateSpec struct {
	serial      int64
	domain      string
	agentID     string
	listenerID  string
	purpose     string
	ipAddresses []net.IP
}

func lifecycleIssueRelayCertificate(t *testing.T, authority *x509.Certificate, authorityKey *ecdsa.PrivateKey, spec lifecycleRelayCertificateSpec) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
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
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(12 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
		BasicConstraintsValid: true, URIs: []*url.URL{identity}, IPAddresses: append([]net.IP(nil), spec.ipAddresses...),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, authority, &key.PublicKey, authorityKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

func lifecycleRelayMetadata(certificate tls.Certificate, generation, identityID, certificateID, domain, agentID, listenerID, purpose string) modulerelay.TunnelCredentialMetadata {
	digest := sha256.Sum256(certificate.Leaf.Raw)
	return modulerelay.TunnelCredentialMetadata{
		Generation: generation, CredentialFingerprintSHA256: hex.EncodeToString(digest[:]),
		IdentityID: identityID, CertificateID: certificateID, Purpose: purpose,
		AuthorityID: "lifecycle-authority", CAGeneration: 1, PKIDomainID: domain,
		PKIEpoch: 1, SecurityRevision: 8, AgentID: agentID, ListenerID: listenerID,
	}
}

type lifecycleRelayCredential struct {
	metadata    modulerelay.TunnelCredentialMetadata
	certificate tls.Certificate
}

type lifecycleRelayMTLSProvider struct {
	security    modulerelay.TunnelSecurityState
	credentials map[string]lifecycleRelayCredential
}

func (*lifecycleRelayMTLSProvider) ServerCertificate(context.Context, int) (*tls.Certificate, error) {
	return nil, errors.New("managed certificate unavailable for pki_mtls")
}

func (*lifecycleRelayMTLSProvider) TrustedCAPool(context.Context, []int) (*x509.CertPool, error) {
	return nil, errors.New("public CA pool unavailable for pki_mtls")
}

func (p *lifecycleRelayMTLSProvider) InstallTunnelCertificate(_ context.Context, identity string, config *tls.Config) (modulerelay.TunnelCredentialMetadata, error) {
	credential, ok := p.credentials[identity]
	if !ok {
		return modulerelay.TunnelCredentialMetadata{}, errors.New("credential not found")
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

func (p *lifecycleRelayMTLSProvider) LoadTunnelCredential(_ context.Context, identity string) (modulerelay.TunnelCredentialMetadata, error) {
	credential, ok := p.credentials[identity]
	if !ok {
		return modulerelay.TunnelCredentialMetadata{}, errors.New("credential not found")
	}
	return credential.metadata, nil
}

func (p *lifecycleRelayMTLSProvider) LoadTunnelSecurity(context.Context) (modulerelay.TunnelSecurityState, error) {
	return p.security, nil
}

func lifecyclePickFreeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func lifecyclePortString(port int) string {
	return big.NewInt(int64(port)).String()
}

func lifecycleStartTCPEchoServer(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer connection.Close()
				_, _ = io.Copy(connection, connection)
			}()
		}
	}()
	return listener.Addr().String(), func() {
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("timed out stopping lifecycle echo server")
		}
	}
}

type lifecycleHotRestartProcess struct {
	waitCalls     int
	transferCalls int
	abortCalls    int
	activateErr   error
	abortErr      error
}

func (p *lifecycleHotRestartProcess) Activate(context.Context) error { return p.activateErr }
func (p *lifecycleHotRestartProcess) TransferAuthority(context.Context) error {
	p.transferCalls++
	return nil
}
func (p *lifecycleHotRestartProcess) Wait() error          { p.waitCalls++; return nil }
func (*lifecycleHotRestartProcess) Signal(os.Signal) error { return nil }
func (p *lifecycleHotRestartProcess) Abort() error         { p.abortCalls++; return p.abortErr }

type lifecycleStreamAuthority struct {
	pauseCalls  int
	resumeCalls int
}

func (a *lifecycleStreamAuthority) Pause() error  { a.pauseCalls++; return nil }
func (a *lifecycleStreamAuthority) Resume() error { a.resumeCalls++; return nil }

type lifecyclePacketAuthority struct {
	resumeCalls int
}

func (*lifecyclePacketAuthority) BeginForwarding() error    { return nil }
func (*lifecyclePacketAuthority) Pause() error              { return nil }
func (*lifecyclePacketAuthority) FlushForwarding() error    { return nil }
func (a *lifecyclePacketAuthority) Resume() error           { a.resumeCalls++; return nil }
func (*lifecyclePacketAuthority) FinalizeForwarding() error { return nil }

type lifecycleGenerationClock struct {
	mu        sync.Mutex
	now       time.Time
	scheduled chan struct{}
	timers    []*lifecycleGenerationTimer
}

func (c *lifecycleGenerationClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *lifecycleGenerationClock) AfterFunc(delay time.Duration, fn func()) generation.Timer {
	c.mu.Lock()
	timer := &lifecycleGenerationTimer{clock: c, at: c.now.Add(delay), fn: fn}
	c.timers = append(c.timers, timer)
	c.mu.Unlock()
	select {
	case c.scheduled <- struct{}{}:
	default:
	}
	return timer
}

func (c *lifecycleGenerationClock) Advance(elapsed time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(elapsed)
	var callbacks []func()
	for _, timer := range c.timers {
		if timer.stopped || timer.fired || timer.at.After(c.now) {
			continue
		}
		timer.fired = true
		callbacks = append(callbacks, timer.fn)
	}
	c.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
}

type lifecycleGenerationTimer struct {
	clock   *lifecycleGenerationClock
	at      time.Time
	fn      func()
	stopped bool
	fired   bool
}

func (t *lifecycleGenerationTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.stopped || t.fired {
		return false
	}
	t.stopped = true
	return true
}

type lifecycleAuthorityJournal struct {
	identity hotrestart.Identity
	owner    string
	pid      int
}

func (j *lifecycleAuthorityJournal) Load() (hotrestart.AuthorityRecord, error) {
	return j.record(), nil
}

func (j *lifecycleAuthorityJournal) Recover(hotrestart.Identity, func(int) bool) (string, hotrestart.AuthorityRecord, error) {
	return j.owner, j.record(), nil
}

func (j *lifecycleAuthorityJournal) record() hotrestart.AuthorityRecord {
	record := hotrestart.AuthorityRecord{Identity: j.identity}
	if j.owner == hotrestart.AuthorityOwnerParent {
		record.ParentPID = j.pid
	} else if j.owner == hotrestart.AuthorityOwnerChild {
		record.ChildPID = j.pid
	}
	return record
}
