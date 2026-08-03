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
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	agentmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	modulerelay "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

func TestSecuritySnapshotDegradedHeartbeatFencesActiveRelayWithoutRevisionUpdate(t *testing.T) {
	fixture := newLifecycleRelayMTLSFixture(t)
	listenerPort := lifecyclePickFreeTCPPort(t)
	listener := fixture.listener(listenerPort)

	registry := agentmodule.NewRegistry()
	if err := registry.Register(appProviderModule{
		name: "relay-mtls-material", provides: agentmodule.ProviderTLSMaterial, provider: fixture.provider,
	}); err != nil {
		t.Fatal(err)
	}
	relayModule := modulerelay.NewModule(modulerelay.Config{AgentID: fixture.agentID, AgentName: fixture.agentID})
	relayModule.SetTunnelCredentialProvider(fixture.provider)
	if err := registry.Register(relayModule); err != nil {
		t.Fatal(err)
	}
	runtime := core.NewRuntimeWithActivator(appSnapshotActivator(registry))
	active := Snapshot{
		DesiredVersion: "1.0.0", Revision: 7,
		Rules:               []model.HTTPRule{{ID: 901, AgentID: fixture.agentID}},
		L4Rules:             []model.L4Rule{},
		RelayListeners:      []model.RelayListener{listener},
		EgressProfiles:      []model.EgressProfile{},
		Certificates:        []model.ManagedCertificateBundle{},
		CertificatePolicies: []model.ManagedCertificatePolicy{},
	}
	if err := runtime.Apply(t.Context(), Snapshot{}, active); err != nil {
		t.Fatalf("seed active relay runtime: %v", err)
	}

	backendAddress, stopBackend := lifecycleStartTCPEchoServer(t)
	t.Cleanup(stopBackend)
	chain := []modulerelay.Hop{{
		Address:  net.JoinHostPort("127.0.0.1", lifecyclePortString(listenerPort)),
		Listener: listener,
	}}
	connection, err := modulerelay.Dial(t.Context(), "tcp", backendAddress, chain, fixture.provider)
	if err != nil {
		t.Fatalf("establish active mTLS relay session: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	payload := []byte("active-before-degraded-heartbeat")
	if _, err := connection.Write(payload); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(connection, reply); err != nil || !reflect.DeepEqual(reply, payload) {
		t.Fatalf("relay round trip = %q, error = %v", reply, err)
	}
	sessionClosed := make(chan error, 1)
	go func() {
		_, readErr := connection.Read(make([]byte, 1))
		sessionClosed <- readErr
	}()

	pkiHandler := &lifecyclePKIHeartbeatHandler{}
	controlFacts := &lifecycleControlRequestFacts{}
	controlServer := httptest.NewUnstartedServer(lifecycleDegradedControlHandler(t, fixture.security.Snapshot, controlFacts))
	controlServer.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.NoClientCert}
	controlServer.StartTLS()
	t.Cleanup(controlServer.Close)
	controlClient := control.NewSyncClient(control.SyncClientConfig{
		MasterURL: controlServer.URL, AgentID: fixture.agentID, AgentName: fixture.agentID,
		AgentToken: "control-token", PKIHeartbeatHandler: pkiHandler,
	}, controlServer.Client())

	store, err := core.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAppliedSnapshot(active); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDesiredSnapshot(active); err != nil {
		t.Fatal(err)
	}
	application := &App{
		cfg:        Config{AgentID: fixture.agentID, AgentName: fixture.agentID, CurrentVersion: "1.0.0"},
		syncClient: controlClient, store: store, runtime: runtime, moduleRegistry: registry,
	}
	if err := application.SyncNow(t.Context()); err != nil {
		t.Fatalf("SyncNow(degraded, no revision) error = %v", err)
	}

	select {
	case readErr := <-sessionClosed:
		if readErr == nil {
			t.Fatal("active relay session ended without a close error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("active relay session was not closed within five seconds")
	}
	if _, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", lifecyclePortString(listenerPort)), 250*time.Millisecond); err == nil {
		t.Fatal("degraded heartbeat left the old pki_mtls listener accepting connections")
	}
	retryCtx, cancelRetry := context.WithTimeout(t.Context(), time.Second)
	defer cancelRetry()
	if retry, err := modulerelay.Dial(retryCtx, "tcp", backendAddress, chain, fixture.provider); err == nil {
		_ = retry.Close()
		t.Fatal("degraded heartbeat reused the old relay pool/session")
	}

	if got := runtime.ActiveSnapshot(); !reflect.DeepEqual(got, active) {
		t.Fatalf("package-only heartbeat replaced active revision config: got %+v want %+v", got, active)
	}
	status, securityRevision := pkiHandler.applied()
	if status != "degraded" || securityRevision != fixture.security.Snapshot.SecurityRevision {
		t.Fatalf("PKI heartbeat consumed status=%q security_revision=%d", status, securityRevision)
	}
	heartbeats, pulls, tokenFailures, clientCertificates := controlFacts.snapshot()
	if heartbeats != 1 || pulls != 1 || tokenFailures != 0 || clientCertificates != 0 {
		t.Fatalf("control facts heartbeat=%d pull=%d token_failures=%d client_certificates=%d", heartbeats, pulls, tokenFailures, clientCertificates)
	}
}

func TestRunRestoresPKIMTLSRelayAfterRestartCredentialBinding(t *testing.T) {
	fixture := newLifecycleRelayMTLSFixture(t)
	listenerPort := lifecyclePickFreeTCPPort(t)
	listener := fixture.listener(listenerPort)

	registry := agentmodule.NewRegistry()
	if err := registry.Register(appProviderModule{
		name: "relay-mtls-material", provides: agentmodule.ProviderTLSMaterial, provider: fixture.provider,
	}); err != nil {
		t.Fatal(err)
	}
	relayModule := modulerelay.NewModule(modulerelay.Config{AgentID: fixture.agentID, AgentName: fixture.agentID})
	if err := registry.Register(relayModule); err != nil {
		t.Fatal(err)
	}
	modulerelay.SetProcessTunnelCredentialProvider(nil)
	t.Cleanup(func() { modulerelay.SetProcessTunnelCredentialProvider(nil) })

	applied := Snapshot{
		DesiredVersion: "1.0.0", Revision: 7,
		Rules:               []model.HTTPRule{},
		L4Rules:             []model.L4Rule{},
		RelayListeners:      []model.RelayListener{listener},
		EgressProfiles:      []model.EgressProfile{},
		Certificates:        []model.ManagedCertificateBundle{},
		CertificatePolicies: []model.ManagedCertificatePolicy{},
	}
	store := core.NewInMemory()
	if err := store.SaveAppliedSnapshot(applied); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDesiredSnapshot(applied); err != nil {
		t.Fatal(err)
	}
	application := &App{
		cfg: Config{
			AgentID: fixture.agentID, AgentName: fixture.agentID,
			CurrentVersion: "1.0.0", HeartbeatInterval: time.Hour,
		},
		syncClient: syncClientFunc(func(_ context.Context, request SyncRequest) (Snapshot, error) {
			return Snapshot{Revision: int64(request.CurrentRevision)}, nil
		}),
		store: store, runtime: core.NewRuntimeWithActivator(appSnapshotActivator(registry)),
		moduleRegistry: registry, relayTunnelCredentials: fixture.provider,
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- application.Run(runCtx) }()
	t.Cleanup(cancelRun)

	backendAddress, stopBackend := lifecycleStartTCPEchoServer(t)
	t.Cleanup(stopBackend)
	chain := []modulerelay.Hop{{
		Address: net.JoinHostPort("127.0.0.1", lifecyclePortString(listenerPort)), Listener: listener,
	}}
	deadline := time.Now().Add(5 * time.Second)
	var connection net.Conn
	var dialErr error
	for time.Now().Before(deadline) {
		dialCtx, cancelDial := context.WithTimeout(t.Context(), 250*time.Millisecond)
		connection, dialErr = modulerelay.Dial(dialCtx, "tcp", backendAddress, chain, fixture.provider)
		cancelDial()
		if dialErr == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if dialErr != nil {
		cancelRun()
		<-runDone
		t.Fatalf("restart hydration did not restore the pki_mtls relay: %v", dialErr)
	}
	payload := []byte("restored-after-restart")
	if _, err := connection.Write(payload); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(connection, reply); err != nil || !reflect.DeepEqual(reply, payload) {
		t.Fatalf("restored relay round trip = %q, error = %v", reply, err)
	}
	_ = connection.Close()

	cancelRun()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}
}

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

func lifecycleDegradedControlHandler(t *testing.T, security model.PKISecuritySnapshot, facts *lifecycleControlRequestFacts) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		facts.record(request)
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/agents/heartbeat":
			_ = json.NewEncoder(response).Encode(map[string]any{"sync": map[string]any{
				"desired_version": "ignored-heartbeat-config", "desired_revision": 99,
				"agent_config": map[string]any{}, "rules": []model.HTTPRule{}, "l4_rules": []model.L4Rule{},
				"relay_listeners": []model.RelayListener{}, "egress_profiles": []model.EgressProfile{},
				"certificates": []model.ManagedCertificateBundle{}, "certificate_policies": []model.ManagedCertificatePolicy{},
				"pki_security": security,
				"pki_status":   model.PKIControlStatus{Status: "degraded", Code: "runtime_unavailable", RecoveryHint: "retry ordinary control sync"},
			}})
		case "/api/agent-revisions/pull":
			_ = json.NewEncoder(response).Encode(map[string]any{"revision": map[string]any{
				"has_update": false, "desired_revision": 7,
			}})
		default:
			http.NotFound(response, request)
		}
	})
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

func TestHotRestartAbortResumesParentWhenChildWaitFails(t *testing.T) {
	activateErr := errors.New("child activation failed")
	waitErr := errors.New("child exited")
	process := &lifecycleHotRestartProcess{activateErr: activateErr, abortErr: waitErr}
	streams := &lifecycleStreamAuthority{}
	packets := &lifecyclePacketAuthority{}
	wrapper := &hotRestartResourceProcess{
		hotRestartProcess: process,
		streams:           streams,
		packets:           packets,
	}

	err := wrapper.Activate(t.Context())
	if !errors.Is(err, activateErr) || !errors.Is(err, waitErr) {
		t.Fatalf("Activate() error = %v, want activation and child wait errors", err)
	}
	if streams.resumeCalls != 1 || packets.resumeCalls != 1 {
		t.Fatalf("parent resume calls = streams:%d packets:%d, want 1/1", streams.resumeCalls, packets.resumeCalls)
	}
	_ = wrapper.Abort()
	if streams.resumeCalls != 1 || packets.resumeCalls != 1 {
		t.Fatalf("replayed abort resumed parent more than once: streams:%d packets:%d", streams.resumeCalls, packets.resumeCalls)
	}
}

func TestPackageOnlyHotRestartSynthesizesRevisionZeroIdentity(t *testing.T) {
	store, err := core.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := core.NewRuntimeWithGenerationManager(core.NewManagedGenerationManager(
		agentmodule.NewRegistry(), core.NewGenerationDrain(nil), time.Minute,
	))
	desired := Snapshot{}
	if err := runtime.Apply(t.Context(), Snapshot{}, desired); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDesiredSnapshot(desired); err != nil {
		t.Fatal(err)
	}
	active, managed := runtime.ActiveGenerationIdentity()
	if !managed || active.ID == "" || active.Revision != 0 {
		t.Fatalf("bootstrap runtime identity = %+v, managed=%t", active, managed)
	}

	app := &App{store: store, runtime: runtime}
	identity, _, err := app.hotRestartLaunchState()
	if err != nil {
		t.Fatalf("hotRestartLaunchState() error = %v", err)
	}
	if identity.Revision != 0 || identity.GenerationID != active.ID || identity.SnapshotDigest != active.SnapshotHash || identity.LeaseID == "" {
		t.Fatalf("bootstrap hot restart identity = %+v, active=%+v", identity, active)
	}
	identity.LaunchEpoch = "bootstrap-epoch"
	if err := app.validateHotRestartIdentity(identity, desired); err != nil {
		t.Fatalf("validateHotRestartIdentity(revision zero) error = %v", err)
	}
	if err := app.validateActiveHotRestartRuntime(identity); err != nil {
		t.Fatalf("validateActiveHotRestartRuntime(revision zero) error = %v", err)
	}
}

func TestPackageOnlyUpgradeFallsBackToColdExecForLegacyGeneration(t *testing.T) {
	store, err := core.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := Snapshot{Revision: 389}
	if err := store.SaveDesiredSnapshot(desired); err != nil {
		t.Fatal(err)
	}

	var coldRestartCalls int
	app := &App{store: store}
	app.hotRestartStart = func(context.Context, hotrestart.Launch) (hotRestartProcess, error) {
		t.Fatal("legacy package upgrade attempted a hot restart")
		return nil, nil
	}
	app.coldRestart = func(binary string, argv, env []string) error {
		coldRestartCalls++
		if binary != "/updates/new/nre-agent" || len(argv) != 1 || argv[0] != binary || len(env) != 1 || env[0] != "NRE_AGENT_VERSION=2" {
			t.Fatalf("cold restart inputs = %q %v %v", binary, argv, env)
		}
		return nil
	}

	err = app.hotRestartReplacement(
		t.Context(),
		"/updates/new/nre-agent",
		[]string{"/updates/new/nre-agent"},
		[]string{"NRE_AGENT_VERSION=2"},
	)
	if !errors.Is(err, core.ErrRestartRequested) {
		t.Fatalf("hotRestartReplacement() error = %v, want restart requested", err)
	}
	if coldRestartCalls != 1 {
		t.Fatalf("cold restart calls = %d, want 1", coldRestartCalls)
	}
}

func TestPackageOnlyUpgradeDoesNotColdExecNonemptyUnreadyJournal(t *testing.T) {
	store, err := core.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := Snapshot{Revision: 389}
	if err := store.SaveDesiredSnapshot(desired); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGenerationJournal(model.GenerationJournal{
		Version: 1,
		Candidate: &model.GenerationRecord{
			Revision: 389,
			Phase:    model.GenerationPhasePrepared,
		},
	}); err != nil {
		t.Fatal(err)
	}

	app := &App{store: store}
	app.hotRestartStart = func(context.Context, hotrestart.Launch) (hotRestartProcess, error) {
		t.Fatal("nonempty unready journal attempted a hot restart")
		return nil, nil
	}
	app.coldRestart = func(string, []string, []string) error {
		t.Fatal("nonempty unready journal triggered a cold restart")
		return nil
	}
	err = app.hotRestartReplacement(t.Context(), "/updates/new/nre-agent", nil, nil)
	if err == nil || errors.Is(err, core.ErrRestartRequested) || !strings.Contains(err.Error(), "durable generation is not ready") {
		t.Fatalf("hotRestartReplacement() error = %v, want readiness rejection", err)
	}
}

func TestPackageOnlyHotRestartUsesDurableActiveGeneration(t *testing.T) {
	store, err := core.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := Snapshot{Revision: 12, DesiredVersion: "1.0.0"}
	runtimeDigest, err := hotRestartSnapshotDigest(desired)
	if err != nil {
		t.Fatal(err)
	}
	canonicalDigest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := store.SaveDesiredSnapshot(desired); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGenerationJournal(model.GenerationJournal{Version: 1, Active: &model.GenerationRecord{
		GenerationID: "protocol-12", RuntimeGenerationID: "runtime-12", RuntimeSnapshotHash: runtimeDigest, Revision: 12,
		SnapshotDigest: canonicalDigest, Phase: model.GenerationPhaseActive, Acknowledged: true,
		Lease: model.RevisionLease{Revision: 12, LeaseID: "lease-12", DrainTimeoutSeconds: 23},
	}}); err != nil {
		t.Fatal(err)
	}

	process := &lifecycleHotRestartProcess{}
	app := &App{cfg: Config{DataDir: t.TempDir()}, store: store, runCtx: t.Context(), hotRestartChild: true}
	app.hotRestartStart = func(_ context.Context, launch hotrestart.Launch) (hotRestartProcess, error) {
		if launch.Identity.Revision != 12 || launch.Identity.GenerationID != "runtime-12" || launch.Identity.LeaseID != "lease-12" {
			t.Fatalf("package-only launch identity = %+v", launch.Identity)
		}
		if launch.Identity.SnapshotDigest != canonicalDigest {
			t.Fatalf("package-only canonical digest = %q", launch.Identity.SnapshotDigest)
		}
		if launch.Stdout != os.Stdout || launch.Stderr != os.Stderr {
			t.Fatal("hot restart child did not inherit stdout/stderr")
		}
		return process, nil
	}
	app.hotRestartDrain = func(context.Context, hotrestart.Identity) error {
		if process.transferCalls != 1 {
			t.Fatalf("authority transfer calls before drain = %d, want 1", process.transferCalls)
		}
		if app.hotRestartDrainTimeout != 23*time.Second {
			t.Fatalf("hot restart drain timeout = %s, want 23s", app.hotRestartDrainTimeout)
		}
		return nil
	}

	err = app.hotRestartReplacement(t.Context(), "/updates/new/nre-agent", nil, nil)
	if !errors.Is(err, core.ErrRestartRequested) {
		t.Fatalf("hotRestartReplacement() error = %v, want restart requested", err)
	}
	if process.waitCalls != 0 {
		t.Fatalf("retired parent waited for authoritative child %d time(s)", process.waitCalls)
	}
	if process.abortCalls != 0 {
		t.Fatalf("authoritative child abort calls = %d, want 0", process.abortCalls)
	}
	identity := hotrestart.Identity{
		Revision: 12, SnapshotDigest: canonicalDigest, GenerationID: "runtime-12", LeaseID: "lease-12", LaunchEpoch: "epoch-12",
	}
	if err := app.validateHotRestartIdentity(identity, desired); err != nil {
		t.Fatalf("validateHotRestartIdentity(active) error = %v", err)
	}
}

func TestHotRestartDrainFailureRetainsAuthoritativeChild(t *testing.T) {
	store, err := core.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := Snapshot{Revision: 7, DesiredVersion: "2.0.0"}
	runtimeDigest, err := hotRestartSnapshotDigest(desired)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDesiredSnapshot(desired); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGenerationJournal(model.GenerationJournal{Version: 1, Active: &model.GenerationRecord{
		GenerationID: "protocol-7", RuntimeGenerationID: "runtime-7", RuntimeSnapshotHash: runtimeDigest,
		Revision: 7, SnapshotDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Phase: model.GenerationPhaseActive, Acknowledged: true,
		Lease: model.RevisionLease{Revision: 7, LeaseID: "lease-7", DrainTimeoutSeconds: 10},
	}}); err != nil {
		t.Fatal(err)
	}

	process := &lifecycleHotRestartProcess{}
	app := &App{cfg: Config{DataDir: t.TempDir()}, store: store, runCtx: t.Context(), hotRestartChild: true}
	app.hotRestartStart = func(context.Context, hotrestart.Launch) (hotRestartProcess, error) { return process, nil }
	app.hotRestartDrain = func(context.Context, hotrestart.Identity) error {
		return errors.New("retired generation cleanup failed")
	}

	if err := app.hotRestartReplacement(t.Context(), "/updates/new/nre-agent", nil, nil); !errors.Is(err, core.ErrRestartRequested) {
		t.Fatalf("hotRestartReplacement() error = %v", err)
	}
	if process.transferCalls != 1 || process.abortCalls != 0 {
		t.Fatalf("authority transfer/abort calls = %d/%d, want 1/0", process.transferCalls, process.abortCalls)
	}
}

func TestHotRestartDrainTimeoutForcesRetiredParent(t *testing.T) {
	clock := newLifecycleGenerationClock(time.Unix(100, 0))
	controller := generation.NewDrainController(clock)
	resource := &lifecycleGenerationResource{}
	if err := controller.Activate(t.Context(), generation.Generation{
		ID: "generation-old", Revision: 1, Resource: resource,
	}, nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	session := &lifecycleGenerationSession{}
	if _, err := controller.RegisterSession(
		"generation-old", generation.EntityKey{Module: "http", ID: "1"}, "session-1", session,
	); err != nil {
		t.Fatal(err)
	}
	manager := core.NewManagedGenerationManager(nil, core.NewGenerationDrain(controller), time.Minute)
	app := &App{generations: manager, hotRestartDrainTimeout: time.Minute}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	drainDone := make(chan error, 1)
	go func() {
		drainDone <- app.drainHotRestartParent(ctx, hotrestart.Identity{})
	}()
	select {
	case <-clock.scheduled:
	case err := <-drainDone:
		t.Fatalf("drainHotRestartParent() returned before scheduling timeout: %v", err)
	case <-ctx.Done():
		t.Fatalf("hot restart drain timeout was not scheduled: %v", ctx.Err())
	}
	clock.Advance(time.Minute)
	if err := <-drainDone; err != nil {
		t.Fatalf("drainHotRestartParent() error = %v", err)
	}
	var status model.GenerationDrainStatus
	for _, candidate := range controller.Snapshot().Generations {
		if candidate.GenerationID == "generation-old" {
			status = candidate
		}
	}
	if status.State != model.GenerationDrainStateForced || status.ForceReason != model.GenerationForceReasonTimeout {
		t.Fatalf("retired parent status = %+v", status)
	}
	if session.forceCalls != 1 || resource.destroyCalls != 1 {
		t.Fatalf("retired parent force/destroy calls = %d/%d, want 1/1", session.forceCalls, resource.destroyCalls)
	}
}

func TestServiceMainProcessSupervisesAuthoritativeHotRestartChild(t *testing.T) {
	store, err := core.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := Snapshot{Revision: 4, DesiredVersion: "2.0.0"}
	runtimeDigest, err := hotRestartSnapshotDigest(desired)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDesiredSnapshot(desired); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGenerationJournal(model.GenerationJournal{Version: 1, Active: &model.GenerationRecord{
		GenerationID: "protocol-4", RuntimeGenerationID: "runtime-4", RuntimeSnapshotHash: runtimeDigest,
		Revision: 4, SnapshotDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Phase: model.GenerationPhaseActive, Acknowledged: true,
		Lease: model.RevisionLease{Revision: 4, LeaseID: "lease-4", DrainTimeoutSeconds: 10},
	}}); err != nil {
		t.Fatal(err)
	}

	process := &lifecycleHotRestartProcess{}
	app := &App{cfg: Config{DataDir: t.TempDir()}, store: store, runCtx: t.Context()}
	app.hotRestartStart = func(context.Context, hotrestart.Launch) (hotRestartProcess, error) { return process, nil }
	app.hotRestartSupervise = func(_ context.Context, got hotRestartProcess, journalPath string, identity hotrestart.Identity) error {
		if got != process || journalPath == "" || identity.GenerationID != "runtime-4" {
			t.Fatalf("supervisor inputs = %T %q %+v", got, journalPath, identity)
		}
		process.waitCalls++
		return context.Canceled
	}

	if err := app.hotRestartReplacement(t.Context(), "/updates/new/nre-agent", nil, nil); !errors.Is(err, core.ErrRestartRequested) {
		t.Fatalf("hotRestartReplacement() error = %v", err)
	}
	if process.waitCalls != 1 {
		t.Fatalf("service supervisor calls = %d, want 1", process.waitCalls)
	}
}

func TestHotRestartShutdownFollowsAuthorityTransfers(t *testing.T) {
	identity := hotrestart.Identity{
		Revision:       4,
		SnapshotDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		GenerationID:   "runtime-4",
		LeaseID:        "lease-4",
		LaunchEpoch:    "epoch-4",
	}
	journal := &lifecycleAuthorityJournal{
		identity: identity,
		owner:    hotrestart.AuthorityOwnerChild,
		pid:      101,
	}
	var stopped []int
	err := stopHotRestartAuthorityLineage(journal, 1, func(pid int) bool {
		return journal.owner != hotrestart.AuthorityOwnerNone && pid == journal.pid
	}, func(pid int) error {
		stopped = append(stopped, pid)
		switch pid {
		case 101:
			journal.pid = 202
		case 202:
			journal.owner = hotrestart.AuthorityOwnerNone
			journal.pid = 0
		}
		return nil
	})
	if err != nil {
		t.Fatalf("stopHotRestartAuthorityLineage() error = %v", err)
	}
	if len(stopped) != 2 || stopped[0] != 101 || stopped[1] != 202 {
		t.Fatalf("stopped authority pids = %v, want [101 202]", stopped)
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

type lifecycleGenerationResource struct{ destroyCalls int }

func (r *lifecycleGenerationResource) Destroy(context.Context) error {
	r.destroyCalls++
	return nil
}

type lifecycleGenerationSession struct{ forceCalls int }

func (s *lifecycleGenerationSession) ForceClose(context.Context, string) error {
	s.forceCalls++
	return nil
}

type lifecycleGenerationClock struct {
	mu        sync.Mutex
	now       time.Time
	scheduled chan struct{}
	timers    []*lifecycleGenerationTimer
}

func newLifecycleGenerationClock(now time.Time) *lifecycleGenerationClock {
	return &lifecycleGenerationClock{
		now:       now,
		scheduled: make(chan struct{}, 1),
	}
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
