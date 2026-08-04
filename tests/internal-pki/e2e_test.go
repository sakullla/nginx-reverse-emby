//go:build integration

package internalpki

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	localAgentID   = "embedded-e2e"
	processTimeout = 45 * time.Second
)

type testHarness struct {
	t             *testing.T
	ctx           context.Context
	repoRoot      string
	tempRoot      string
	controlBin    string
	agentBin      string
	panelToken    string
	registerToken string
	client        *http.Client
	childrenMu    sync.Mutex
	children      []*childProcess
}

type controlInstance struct {
	process *childProcess
	baseURL string
	dataDir string
}

type childProcess struct {
	cmd     *exec.Cmd
	log     *lockedBuffer
	done    chan error
	secrets []string
	once    sync.Once
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(value)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type apiResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

type overviewEnvelope struct {
	OK       bool `json:"ok"`
	Overview struct {
		PKIDomainID      string `json:"pki_domain_id"`
		PKIEpoch         int64  `json:"pki_epoch"`
		SecurityRevision int64  `json:"security_revision"`
		UpgradeState     string `json:"upgrade_state"`
		RuntimeStatus    string `json:"runtime_status"`
	} `json:"overview"`
}

// These fixture types deliberately mirror the public JSON enrollment
// contract. The standalone module must not import either product module's
// internal packages, otherwise the E2E could silently share implementation
// details with the code it is meant to verify.
type enrollmentRequest struct {
	RequestID   string   `json:"request_id"`
	Kind        string   `json:"kind"`
	ListenerID  string   `json:"listener_id,omitempty"`
	Purpose     string   `json:"purpose"`
	CSRPEM      string   `json:"csr_pem"`
	DNSNames    []string `json:"dns_names,omitempty"`
	IPAddresses []string `json:"ip_addresses,omitempty"`
}

type pendingEnrollment struct {
	Version              int               `json:"version"`
	StorageIdentity      string            `json:"storage_identity"`
	Request              enrollmentRequest `json:"request"`
	PKIDomainID          string            `json:"pki_domain_id,omitempty"`
	AgentID              string            `json:"agent_id,omitempty"`
	RequestFingerprint   string            `json:"request_fingerprint_sha256"`
	PublicKeyFingerprint string            `json:"public_key_fingerprint_sha256"`
	CreatedAt            time.Time         `json:"created_at"`
}

func TestInternalPKIMultiProcessLifecycle(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 7*time.Minute)
	defer cancel()

	h := newTestHarness(t, ctx)
	dataDir := filepath.Join(h.tempRoot, "primary-control")
	primary := h.startControl(dataDir)
	initial := h.waitForPKI(primary)
	if initial.Overview.UpgradeState != "tunnel_mtls_only" {
		t.Fatalf("fresh PKI upgrade state = %q, want tunnel_mtls_only", initial.Overview.UpgradeState)
	}
	if !strings.HasPrefix(primary.baseURL, "http://127.0.0.1:") {
		t.Fatalf("control URL = %q, want the existing loopback HTTP listener", primary.baseURL)
	}

	h.assertPanelAuthentication(primary)
	remoteData := filepath.Join(h.tempRoot, "remote-agent")
	remoteID, agentToken := h.joinAndReplayRemoteAgent(primary, remoteData)
	remote := h.startRemoteAgent(primary, remoteID, agentToken, remoteData)
	h.waitForRemoteHeartbeat(primary, remoteID, remote)
	h.assertTokenControlBoundary(primary, remoteID, agentToken)
	h.assertIndependentTunnelIdentities(primary, dataDir, remoteData, remote)

	backendURL, backendStop := startLoopbackHTTPServer(t, "internal-pki-relay-ok")
	defer backendStop()
	relayPort := reserveLoopbackPort(t)
	frontendPort := reserveLoopbackPort(t)
	listenerID := h.createPKIRelayListener(primary, relayPort)
	frontendURL := fmt.Sprintf("http://127.0.0.1:%d", frontendPort)
	h.createRelayedHTTPRule(primary, frontendURL, backendURL, listenerID)
	h.waitForHTTPBody(primary, frontendURL, "internal-pki-relay-ok")

	peerRelayPort := reserveLoopbackPort(t)
	peerFrontendPort := reserveLoopbackPort(t)
	peerListenerID, peerMutation := h.createPKIRelayListenerRequest(primary, remoteID, "pki-e2e-peer-identity", peerRelayPort)
	h.waitForMutation(primary, peerMutation, "create PKI relay listener for "+remoteID)
	peerFrontendURL := fmt.Sprintf("http://127.0.0.1:%d", peerFrontendPort)
	h.createRelayedHTTPRule(primary, peerFrontendURL, backendURL, peerListenerID)
	h.waitForHTTPBody(primary, peerFrontendURL, "internal-pki-relay-ok")

	revokedData := filepath.Join(h.tempRoot, "revoked-agent")
	revokedAgentID, revokedAgentToken := h.joinAndReplayRemoteAgent(primary, revokedData)
	revokedAgent := h.startRemoteAgent(primary, revokedAgentID, revokedAgentToken, revokedData)
	h.waitForRemoteHeartbeat(primary, revokedAgentID, revokedAgent)

	archive := h.exportProtectedBackup(primary)
	authority := h.trustedAuthorityFromBackup(primary, archive)
	h.assertRelayCertificateAttackMatrix(relayPort, authority)

	remote.stop()
	h.assertRelayClientIdentityAttackMatrix(peerFrontendURL, peerRelayPort, authority, remoteID, peerListenerID)
	h.assertRevokedAgentCertificateIsFenced(primary, relayPort, revokedAgentID, revokedData, revokedAgent)

	beforeRestore := h.waitForPKI(primary)
	h.assertRejectedRestoreDoesNotMutate(primary, archive, beforeRestore)
	h.assertPersistedStateDoesNotLeakControlSecrets(dataDir, agentToken)

	remote.stop()
	primary.process.stop()
	restarted := h.startControl(dataDir)
	afterRestart := h.waitForPKI(restarted)
	if afterRestart.Overview.PKIDomainID != initial.Overview.PKIDomainID ||
		afterRestart.Overview.PKIEpoch != initial.Overview.PKIEpoch ||
		afterRestart.Overview.SecurityRevision < initial.Overview.SecurityRevision {
		t.Fatalf("PKI version changed across clean process restart: before=%+v after=%+v", initial.Overview, afterRestart.Overview)
	}
	remote = h.startRemoteAgent(restarted, remoteID, agentToken, remoteData)
	h.waitForRemoteHeartbeat(restarted, remoteID, remote)
	h.waitForHTTPBody(restarted, frontendURL, "internal-pki-relay-ok")

	h.revokeListenerAndWaitForFence(restarted, listenerID, frontendURL)
	h.assertTokenControlBoundary(restarted, remoteID, agentToken)
}

func TestInternalPKIQUICRelayRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	h := newTestHarness(t, ctx)
	control := h.startControl(filepath.Join(h.tempRoot, "quic-control"))
	h.waitForPKI(control)
	backendURL, backendStop := startLoopbackHTTPServer(t, "internal-pki-quic-ok")
	defer backendStop()
	listenerID := h.createPKIRelayListenerForAgentWithTransport(
		control, localAgentID, "pki-e2e-quic", reserveLoopbackUDPPort(t), "quic",
	)
	frontendURL := fmt.Sprintf("http://127.0.0.1:%d", reserveLoopbackPort(t))
	h.createRelayedHTTPRule(control, frontendURL, backendURL, listenerID)
	h.waitForHTTPBody(control, frontendURL, "internal-pki-quic-ok")
}

func newTestHarness(t *testing.T, ctx context.Context) *testHarness {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate integration harness source")
	}
	repoRoot, err := filepath.Abs(filepath.Join(filepath.Dir(source), "..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	tempRoot := t.TempDir()
	binDir := filepath.Join(tempRoot, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatalf("create binary directory: %v", err)
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	h := &testHarness{
		t:             t,
		ctx:           ctx,
		repoRoot:      repoRoot,
		tempRoot:      tempRoot,
		controlBin:    filepath.Join(binDir, "nre-control-plane"+suffix),
		agentBin:      filepath.Join(binDir, "nre-agent"+suffix),
		panelToken:    randomSecret(t, "panel"),
		registerToken: randomSecret(t, "register"),
		client: &http.Client{Transport: &http.Transport{
			DisableKeepAlives: true,
		}, Timeout: 8 * time.Second},
	}
	h.buildProduct("panel/backend-go", "./cmd/nre-control-plane", h.controlBin)
	h.buildProduct("go-agent", "./cmd/nre-agent", h.agentBin)
	t.Cleanup(func() {
		h.childrenMu.Lock()
		children := append([]*childProcess(nil), h.children...)
		h.childrenMu.Unlock()
		for index := len(children) - 1; index >= 0; index-- {
			children[index].stop()
		}
	})
	return h
}

func (h *testHarness) buildProduct(module, pkg, output string) {
	h.t.Helper()
	goBinary, err := exec.LookPath("go")
	if err != nil {
		h.t.Fatalf("find Go executable: %v", err)
	}
	goBinary, err = filepath.Abs(goBinary)
	if err != nil {
		h.t.Fatalf("resolve Go executable: %v", err)
	}
	buildCtx, cancel := context.WithTimeout(h.ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(buildCtx, goBinary, "build", "-tags=integration", "-trimpath", "-o", output, pkg)
	cmd.Dir = filepath.Join(h.repoRoot, filepath.FromSlash(module))
	outputBytes, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("build %s: %v\n%s", module, err, sanitizeLog(string(outputBytes), h.panelToken, h.registerToken))
	}
}

func (h *testHarness) startControl(dataDir string) controlInstance {
	return h.startControlWithOverrides(dataDir, nil)
}

func (h *testHarness) startControlWithOverrides(dataDir string, extraEnvironment map[string]string) controlInstance {
	h.t.Helper()
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		h.t.Fatalf("create control data directory: %v", err)
	}
	frontendDir := filepath.Join(h.tempRoot, "empty-frontend")
	assetsDir := filepath.Join(h.tempRoot, "empty-assets")
	for _, directory := range []string{frontendDir, assetsDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			h.t.Fatalf("create fixture directory: %v", err)
		}
	}
	port := reserveLoopbackPort(h.t)
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	environment := map[string]string{
		"NRE_CONTROL_PLANE_ADDR":      fmt.Sprintf("127.0.0.1:%d", port),
		"NRE_CONTROL_PLANE_DATA_DIR":  dataDir,
		"NRE_PANEL_TOKEN":             h.panelToken,
		"NRE_REGISTER_TOKEN":          h.registerToken,
		"NRE_PUBLIC_URL":              baseURL,
		"NRE_FRONTEND_DIST_DIR":       frontendDir,
		"NRE_PUBLIC_AGENT_ASSETS_DIR": assetsDir,
		"NRE_ENABLE_LOCAL_AGENT":      "true",
		"NRE_LOCAL_AGENT_ID":          localAgentID,
		"NRE_LOCAL_AGENT_NAME":        "embedded-e2e",
		"NRE_HEARTBEAT_INTERVAL":      "250ms",
		"NRE_REVISION_APPLY_TIMEOUT":  "15s",
		"NRE_REVISION_DRAIN_TIMEOUT":  "5s",
		"NRE_TRAFFIC_STATS_ENABLED":   "false",
		"NRE_HTTP3_ENABLED":           "false",
	}
	for name, value := range extraEnvironment {
		environment[name] = value
	}
	process := h.startProcess(h.controlBin, nil, environment)
	return controlInstance{process: process, baseURL: baseURL, dataDir: dataDir}
}

func (h *testHarness) startRemoteAgent(control controlInstance, agentID, agentToken, dataDir string) *childProcess {
	return h.startRemoteAgentWithOverrides(control, agentID, agentToken, dataDir, nil)
}

func (h *testHarness) startRemoteAgentWithOverrides(control controlInstance, agentID, agentToken, dataDir string, extraEnvironment map[string]string) *childProcess {
	h.t.Helper()
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		h.t.Fatalf("create remote agent data directory: %v", err)
	}
	environment := map[string]string{
		"NRE_MASTER_URL":            control.baseURL,
		"NRE_AGENT_ID":              agentID,
		"NRE_AGENT_NAME":            "remote-e2e",
		"NRE_AGENT_TOKEN":           agentToken,
		"NRE_AGENT_VERSION":         "e2e",
		"NRE_DATA_DIR":              dataDir,
		"NRE_HEARTBEAT_INTERVAL":    "250ms",
		"NRE_TRAFFIC_STATS_ENABLED": "false",
		"NRE_HTTP3_ENABLED":         "false",
	}
	for name, value := range extraEnvironment {
		environment[name] = value
	}
	return h.startProcess(h.agentBin, nil, environment)
}

func (h *testHarness) startProcess(executable string, args []string, overrides map[string]string) *childProcess {
	h.t.Helper()
	cmd := exec.Command(executable, args...)
	cmd.Dir = h.repoRoot
	cmd.Env = mergedEnvironment(overrides)
	configureProcessTree(cmd)
	logBuffer := &lockedBuffer{}
	cmd.Stdout = logBuffer
	cmd.Stderr = logBuffer
	if err := cmd.Start(); err != nil {
		h.t.Fatalf("start %s: %v", filepath.Base(executable), err)
	}
	child := &childProcess{
		cmd: cmd, log: logBuffer, done: make(chan error, 1),
		secrets: []string{h.panelToken, h.registerToken, overrides["NRE_AGENT_TOKEN"]},
	}
	go func() { child.done <- cmd.Wait() }()
	h.childrenMu.Lock()
	h.children = append(h.children, child)
	h.childrenMu.Unlock()
	return child
}

func (p *childProcess) stop() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		terminateProcessTree(p.cmd)
		select {
		case <-p.done:
		case <-time.After(10 * time.Second):
		}
	})
}

func (p *childProcess) failureLog() string {
	if p == nil {
		return ""
	}
	const headLimit = 12 * 1024
	const tailLimit = 2 * 1024
	value := sanitizeLog(p.log.String(), p.secrets...)
	if len(value) <= headLimit+tailLimit {
		return value
	}
	return value[:headLimit] + "\n...[middle of process log omitted]...\n" + value[len(value)-tailLimit:]
}

func (h *testHarness) waitForPKI(control controlInstance) overviewEnvelope {
	h.t.Helper()
	var overview overviewEnvelope
	err := eventually(h.ctx, processTimeout, func(ctx context.Context) (bool, error) {
		select {
		case processErr := <-control.process.done:
			return false, fmt.Errorf("control process exited early: %v\n%s", processErr, control.process.failureLog())
		default:
		}
		response, err := h.request(ctx, http.MethodGet, control.baseURL+"/panel-api/pki/overview", nil, map[string]string{
			"X-Panel-Token": h.panelToken,
		})
		if err != nil {
			return false, err
		}
		if response.Status != http.StatusOK {
			return false, fmt.Errorf("PKI overview status %d: %s", response.Status, response.Body)
		}
		if err := json.Unmarshal(response.Body, &overview); err != nil {
			return false, err
		}
		return overview.OK && overview.Overview.PKIDomainID != "" && overview.Overview.RuntimeStatus == "ready", nil
	})
	if err != nil {
		h.t.Fatalf("wait for control PKI readiness: %v\n%s", err, control.process.failureLog())
	}
	return overview
}

func (h *testHarness) assertPanelAuthentication(control controlInstance) {
	h.t.Helper()
	response, err := h.request(h.ctx, http.MethodGet, control.baseURL+"/panel-api/pki/overview", nil, nil)
	if err != nil {
		h.t.Fatalf("unauthenticated overview request: %v", err)
	}
	if response.Status != http.StatusUnauthorized {
		h.t.Fatalf("unauthenticated PKI overview status = %d, want 401", response.Status)
	}
}

func (h *testHarness) joinAndReplayRemoteAgent(control controlInstance, dataDir string) (string, string) {
	h.t.Helper()
	tokenResponse := h.mustJSON(http.MethodPost, control.baseURL+"/panel-api/pki/enrollment-tokens", map[string]string{
		"scope": "new_agent",
	}, map[string]string{"X-Panel-Token": h.panelToken})
	if tokenResponse.Status != http.StatusCreated {
		h.t.Fatalf("create one-time PKI enrollment token status = %d: %s", tokenResponse.Status, tokenResponse.Body)
	}
	var tokenEnvelope struct {
		EnrollmentToken struct {
			Token string `json:"token"`
		} `json:"enrollment_token"`
	}
	if err := json.Unmarshal(tokenResponse.Body, &tokenEnvelope); err != nil || tokenEnvelope.EnrollmentToken.Token == "" {
		h.t.Fatalf("decode one-time PKI enrollment token: error=%v body=%s", err, tokenResponse.Body)
	}
	pending := prepareAnonymousAgentEnrollment(h.t, dataDir)
	payload := map[string]any{
		"name": "remote-e2e", "register_token": tokenEnvelope.EnrollmentToken.Token,
		"version": "e2e", "platform": runtime.GOOS + "/" + runtime.GOARCH,
		"capabilities": []string{"http_rules", "l4_rules", "relay"}, "mode": "pull",
		"pki_enrollment_request_id": pending.Request.RequestID,
		"tunnel_csr_pem":            pending.Request.CSRPEM,
	}
	type registrationResult struct {
		AgentID          string
		AgentToken       string
		TunnelCredential json.RawMessage
		SecuritySnapshot json.RawMessage
	}
	register := func() registrationResult {
		response := h.mustJSON(http.MethodPost, control.baseURL+"/panel-api/agents/register", payload, nil)
		if response.Status != http.StatusOK {
			h.t.Fatalf("register remote agent through one-time PKI enrollment status = %d: %s", response.Status, response.Body)
		}
		var envelope struct {
			OK  bool `json:"ok"`
			PKI struct {
				AgentID          string          `json:"agent_id"`
				AgentToken       string          `json:"agent_token"`
				TunnelCredential json.RawMessage `json:"tunnel_credential"`
				SecuritySnapshot json.RawMessage `json:"security_snapshot"`
			} `json:"pki"`
		}
		if err := json.Unmarshal(response.Body, &envelope); err != nil || !envelope.OK ||
			envelope.PKI.AgentID == "" || envelope.PKI.AgentToken == "" ||
			len(envelope.PKI.TunnelCredential) == 0 || len(envelope.PKI.SecuritySnapshot) == 0 {
			h.t.Fatalf("decode registration response: error=%v body=%s", err, response.Body)
		}
		return registrationResult{
			AgentID: envelope.PKI.AgentID, AgentToken: envelope.PKI.AgentToken,
			TunnelCredential: envelope.PKI.TunnelCredential, SecuritySnapshot: envelope.PKI.SecuritySnapshot,
		}
	}
	first := register()
	replayed := register()
	if replayed.AgentID != first.AgentID || replayed.AgentToken != first.AgentToken ||
		!bytes.Equal(replayed.TunnelCredential, first.TunnelCredential) {
		h.t.Fatal("one-time registration replay did not return the original stable identity and credential")
	}
	stageRegistrationResponse(h.t, dataDir, first.AgentID, first.TunnelCredential, first.SecuritySnapshot)
	return first.AgentID, first.AgentToken
}

func (h *testHarness) boundReenrollRemoteAgent(control controlInstance, dataDir, domainID, agentID string) string {
	h.t.Helper()
	tokenResponse := h.mustJSON(http.MethodPost, control.baseURL+"/panel-api/pki/enrollment-tokens", map[string]string{
		"scope": "bound_reenrollment", "bound_agent_id": agentID,
	}, map[string]string{"X-Panel-Token": h.panelToken})
	if tokenResponse.Status != http.StatusCreated {
		h.t.Fatalf("create bound re-enrollment token status = %d: %s", tokenResponse.Status, tokenResponse.Body)
	}
	var tokenEnvelope struct {
		EnrollmentToken struct {
			Token string `json:"token"`
		} `json:"enrollment_token"`
	}
	if err := json.Unmarshal(tokenResponse.Body, &tokenEnvelope); err != nil || tokenEnvelope.EnrollmentToken.Token == "" {
		h.t.Fatalf("decode bound re-enrollment token: error=%v body=%s", err, tokenResponse.Body)
	}
	pending := prepareBoundAgentEnrollment(h.t, dataDir, domainID, agentID)
	response := h.mustJSON(http.MethodPost, control.baseURL+"/panel-api/agents/register", map[string]any{
		"agent_id": agentID, "name": "remote-e2e", "register_token": tokenEnvelope.EnrollmentToken.Token,
		"version": "e2e", "platform": runtime.GOOS + "/" + runtime.GOARCH,
		"capabilities": []string{"http_rules", "l4_rules", "relay"}, "mode": "pull",
		"pki_enrollment_request_id": pending.Request.RequestID, "tunnel_csr_pem": pending.Request.CSRPEM,
	}, nil)
	if response.Status != http.StatusOK {
		h.t.Fatalf("bound re-enrollment status = %d: %s", response.Status, response.Body)
	}
	var envelope struct {
		OK  bool `json:"ok"`
		PKI struct {
			AgentID          string          `json:"agent_id"`
			AgentToken       string          `json:"agent_token"`
			TunnelCredential json.RawMessage `json:"tunnel_credential"`
			SecuritySnapshot json.RawMessage `json:"security_snapshot"`
		} `json:"pki"`
	}
	if err := json.Unmarshal(response.Body, &envelope); err != nil || !envelope.OK || envelope.PKI.AgentID != agentID ||
		envelope.PKI.AgentToken == "" || len(envelope.PKI.TunnelCredential) == 0 || len(envelope.PKI.SecuritySnapshot) == 0 {
		h.t.Fatalf("decode bound re-enrollment response: error=%v body=%s", err, response.Body)
	}
	stageRegistrationResponse(h.t, dataDir, agentID, envelope.PKI.TunnelCredential, envelope.PKI.SecuritySnapshot)
	return envelope.PKI.AgentToken
}

func prepareAnonymousAgentEnrollment(t *testing.T, dataDir string) pendingEnrollment {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate anonymous enrollment key: %v", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}, privateKey)
	if err != nil {
		t.Fatalf("create anonymous enrollment CSR: %v", err)
	}
	parsedCSR, err := x509.ParseCertificateRequest(csrDER)
	if err != nil || parsedCSR.CheckSignature() != nil {
		t.Fatalf("validate anonymous enrollment CSR: %v", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	privateDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal anonymous enrollment key: %v", err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privateDER})

	requestIDBytes := make([]byte, 16)
	if _, err := rand.Read(requestIDBytes); err != nil {
		t.Fatalf("generate anonymous enrollment request ID: %v", err)
	}
	request := enrollmentRequest{
		RequestID: hex.EncodeToString(requestIDBytes), Kind: "agent",
		Purpose: "client_auth", CSRPEM: string(csrPEM),
	}
	canonical := struct {
		StorageIdentity string            `json:"storage_identity"`
		PKIDomainID     string            `json:"pki_domain_id"`
		AgentID         string            `json:"agent_id"`
		Request         enrollmentRequest `json:"request"`
	}{StorageIdentity: "agent", Request: request}
	encodedCanonical, err := json.Marshal(canonical)
	if err != nil {
		t.Fatalf("encode enrollment fingerprint input: %v", err)
	}
	requestDigest := sha256.Sum256(encodedCanonical)
	publicDigest := sha256.Sum256(parsedCSR.RawSubjectPublicKeyInfo)
	pending := pendingEnrollment{
		Version: 1, StorageIdentity: "agent", Request: request,
		RequestFingerprint:   hex.EncodeToString(requestDigest[:]),
		PublicKeyFingerprint: hex.EncodeToString(publicDigest[:]),
		CreatedAt:            time.Now().UTC(),
	}
	journal, err := json.Marshal(pending)
	if err != nil {
		t.Fatalf("encode pending enrollment journal: %v", err)
	}

	pendingRoot := filepath.Join(dataDir, "pki", "identities", "agent", "pending")
	if err := os.MkdirAll(pendingRoot, 0o700); err != nil {
		t.Fatalf("create pending enrollment directory: %v", err)
	}
	for _, directory := range []string{
		dataDir,
		filepath.Join(dataDir, "pki"),
		filepath.Join(dataDir, "pki", "identities"),
		filepath.Join(dataDir, "pki", "identities", "agent"),
		pendingRoot,
	} {
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Fatalf("secure pending enrollment directory %s: %v", directory, err)
		}
	}
	writePrivateFixture(t, filepath.Join(pendingRoot, "private-key.pem"), privatePEM)
	writePrivateFixture(t, filepath.Join(pendingRoot, "request.csr.pem"), csrPEM)
	writePrivateFixture(t, filepath.Join(pendingRoot, "request.json"), journal)
	return pending
}

func prepareBoundAgentEnrollment(t *testing.T, dataDir, domainID, agentID string) pendingEnrollment {
	t.Helper()
	identityURI := &url.URL{Scheme: "spiffe", Host: domainID, Path: "/agent/" + agentID}
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate bound enrollment key: %v", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		SignatureAlgorithm: x509.ECDSAWithSHA256,
		Subject:            pkix.Name{CommonName: identityURI.String()},
		URIs:               []*url.URL{identityURI},
	}, privateKey)
	if err != nil {
		t.Fatalf("create bound enrollment CSR: %v", err)
	}
	parsedCSR, err := x509.ParseCertificateRequest(csrDER)
	if err != nil || parsedCSR.CheckSignature() != nil {
		t.Fatalf("validate bound enrollment CSR: %v", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	privateDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal bound enrollment key: %v", err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privateDER})
	requestIDBytes := make([]byte, 16)
	if _, err := rand.Read(requestIDBytes); err != nil {
		t.Fatalf("generate bound enrollment request ID: %v", err)
	}
	request := enrollmentRequest{
		RequestID: hex.EncodeToString(requestIDBytes), Kind: "agent", Purpose: "client_auth", CSRPEM: string(csrPEM),
	}
	canonical := struct {
		StorageIdentity string            `json:"storage_identity"`
		PKIDomainID     string            `json:"pki_domain_id"`
		AgentID         string            `json:"agent_id"`
		Request         enrollmentRequest `json:"request"`
	}{StorageIdentity: "agent", PKIDomainID: domainID, AgentID: agentID, Request: request}
	encodedCanonical, err := json.Marshal(canonical)
	if err != nil {
		t.Fatalf("encode bound enrollment fingerprint input: %v", err)
	}
	requestDigest := sha256.Sum256(encodedCanonical)
	publicDigest := sha256.Sum256(parsedCSR.RawSubjectPublicKeyInfo)
	pending := pendingEnrollment{
		Version: 1, StorageIdentity: "agent", Request: request, PKIDomainID: domainID, AgentID: agentID,
		RequestFingerprint: hex.EncodeToString(requestDigest[:]), PublicKeyFingerprint: hex.EncodeToString(publicDigest[:]),
		CreatedAt: time.Now().UTC(),
	}
	journal, err := json.Marshal(pending)
	if err != nil {
		t.Fatalf("encode bound pending enrollment journal: %v", err)
	}
	identityRoot := filepath.Join(dataDir, "pki", "identities", "agent")
	pendingRoot := filepath.Join(identityRoot, "pending")
	if err := os.RemoveAll(pendingRoot); err != nil {
		t.Fatalf("clear superseded bound pending enrollment: %v", err)
	}
	for _, directory := range []string{identityRoot, filepath.Join(identityRoot, "generations"), pendingRoot} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create bound enrollment directory %s: %v", directory, err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Fatalf("secure bound enrollment directory %s: %v", directory, err)
		}
	}
	writePrivateFixture(t, filepath.Join(pendingRoot, "private-key.pem"), privatePEM)
	writePrivateFixture(t, filepath.Join(pendingRoot, "request.csr.pem"), csrPEM)
	writePrivateFixture(t, filepath.Join(pendingRoot, "request.json"), journal)
	return pending
}

func stageRegistrationResponse(t *testing.T, dataDir, agentID string, credential, security json.RawMessage) {
	t.Helper()
	if strings.TrimSpace(agentID) == "" || !json.Valid(credential) || !json.Valid(security) ||
		len(credential) == 0 || credential[0] != '{' || len(security) == 0 || security[0] != '{' {
		t.Fatal("refuse to stage an incomplete public registration response")
	}
	staged := struct {
		AgentID          string          `json:"agent_id"`
		TunnelCredential json.RawMessage `json:"tunnel_credential"`
		SecuritySnapshot json.RawMessage `json:"security_snapshot"`
	}{AgentID: agentID, TunnelCredential: credential, SecuritySnapshot: security}
	encoded, err := json.Marshal(staged)
	if err != nil {
		t.Fatalf("encode staged registration response: %v", err)
	}
	writePrivateFixture(t, filepath.Join(dataDir, "pki", "identities", "agent", "pending", "response.json"), encoded)
}

func writePrivateFixture(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write private fixture %s: %v", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("secure private fixture %s: %v", path, err)
	}
}

func (h *testHarness) waitForRemoteHeartbeat(control controlInstance, agentID string, process *childProcess) {
	h.t.Helper()
	err := eventually(h.ctx, processTimeout, func(ctx context.Context) (bool, error) {
		select {
		case processErr := <-process.done:
			return false, fmt.Errorf("remote agent exited early: %v\n%s", processErr, process.failureLog())
		default:
		}
		response, err := h.request(ctx, http.MethodGet, control.baseURL+"/panel-api/agents/"+url.PathEscape(agentID), nil, map[string]string{
			"X-Panel-Token": h.panelToken,
		})
		if err != nil || response.Status != http.StatusOK {
			return false, err
		}
		var envelope struct {
			Agent struct {
				LastSeenAt string `json:"last_seen_at"`
				Status     string `json:"status"`
			} `json:"agent"`
		}
		if err := json.Unmarshal(response.Body, &envelope); err != nil {
			return false, err
		}
		return envelope.Agent.LastSeenAt != "", nil
	})
	if err != nil {
		h.t.Fatalf("wait for remote heartbeat: %v\ncontrol:\n%s\nagent:\n%s", err, control.process.failureLog(), process.failureLog())
	}
}

func (h *testHarness) assertTokenControlBoundary(control controlInstance, agentID, agentToken string) {
	h.t.Helper()
	payload := map[string]any{
		"agent_id": agentID, "name": "remote-e2e", "version": "e2e", "platform": runtime.GOOS,
		"current_revision": 0, "last_apply_revision": 0, "last_apply_status": "success",
	}
	unauthorized := h.mustJSON(http.MethodPost, control.baseURL+"/panel-api/agents/heartbeat", payload, nil)
	if unauthorized.Status != http.StatusUnauthorized {
		h.t.Fatalf("heartbeat without X-Agent-Token status = %d, want 401", unauthorized.Status)
	}
	authorized := h.mustJSON(http.MethodPost, control.baseURL+"/panel-api/agents/heartbeat", payload, map[string]string{
		"X-Agent-Token": agentToken,
	})
	if authorized.Status != http.StatusOK {
		h.t.Fatalf("token-authenticated HTTP heartbeat status = %d: %s", authorized.Status, authorized.Body)
	}
}

func (h *testHarness) assertIndependentTunnelIdentities(control controlInstance, controlData, remoteData string, remote *childProcess) {
	h.t.Helper()
	var embeddedCert, remoteCert []byte
	err := eventually(h.ctx, 15*time.Second, func(context.Context) (bool, error) {
		embeddedCert, _ = firstNamedFile(filepath.Join(controlData, "embedded-agent-state", "pki"), "certificate.pem")
		remoteCert, _ = firstNamedFile(filepath.Join(remoteData, "pki"), "certificate.pem")
		return len(embeddedCert) > 0 && len(remoteCert) > 0, nil
	})
	if err != nil {
		h.t.Fatalf("wait for independent embedded/remote PKI credentials: %v (embedded=%d remote=%d)\ncontrol files:\n%s\nremote files:\n%s\ncontrol:\n%s\nremote:\n%s",
			err, len(embeddedCert), len(remoteCert), relativeFileList(controlData), relativeFileList(remoteData), control.process.failureLog(), remote.failureLog())
	}
	if bytes.Equal(embeddedCert, remoteCert) {
		h.t.Fatal("embedded and remote agents persisted the same tunnel certificate")
	}
}

func (h *testHarness) createPKIRelayListener(control controlInstance, port int) int {
	return h.createPKIRelayListenerForAgent(control, localAgentID, "pki-e2e-relay", port)
}

func (h *testHarness) createPKIRelayListenerForAgent(control controlInstance, agentID, name string, port int) int {
	return h.createPKIRelayListenerForAgentWithTransport(control, agentID, name, port, "tls_tcp")
}

func (h *testHarness) createPKIRelayListenerForAgentWithTransport(control controlInstance, agentID, name string, port int, transportMode string) int {
	listenerID, response := h.createPKIRelayListenerRequestWithTransport(control, agentID, name, port, transportMode)
	h.waitForMutation(control, response, "create PKI relay listener for "+agentID)
	return listenerID
}

func (h *testHarness) createPKIRelayListenerRequest(control controlInstance, agentID, name string, port int) (int, apiResponse) {
	return h.createPKIRelayListenerRequestWithTransport(control, agentID, name, port, "tls_tcp")
}

func (h *testHarness) createPKIRelayListenerRequestWithTransport(control controlInstance, agentID, name string, port int, transportMode string) (int, apiResponse) {
	h.t.Helper()
	payload := map[string]any{
		"name": name, "bind_hosts": []string{"127.0.0.1"},
		"listen_host": "127.0.0.1", "listen_port": port,
		"public_host": "127.0.0.1", "public_port": port,
		"enabled": true, "transport_mode": transportMode, "allow_transport_fallback": false,
	}
	response := h.mustJSON(http.MethodPost, control.baseURL+"/panel-api/agents/"+url.PathEscape(agentID)+"/relay-listeners", payload, map[string]string{
		"X-Panel-Token":   h.panelToken,
		"Idempotency-Key": randomSecret(h.t, "listener"),
	})
	if response.Status != http.StatusAccepted && response.Status != http.StatusCreated {
		h.t.Fatalf("create PKI relay listener status = %d: %s", response.Status, response.Body)
	}
	var envelope struct {
		Listener struct {
			ID      int    `json:"id"`
			TLSMode string `json:"tls_mode"`
		} `json:"listener"`
	}
	if err := json.Unmarshal(response.Body, &envelope); err != nil || envelope.Listener.ID <= 0 {
		h.t.Fatalf("decode relay listener response: error=%v body=%s", err, response.Body)
	}
	if envelope.Listener.TLSMode != "pki_mtls" {
		h.t.Fatalf("relay TLS mode = %q, want pki_mtls", envelope.Listener.TLSMode)
	}
	return envelope.Listener.ID, response
}

func (h *testHarness) createRelayedHTTPRule(control controlInstance, frontendURL, backendURL string, listenerID int) {
	h.t.Helper()
	payload := map[string]any{
		"frontend_url": frontendURL, "backends": []map[string]string{{"url": backendURL}},
		"relay_layers": [][]int{{listenerID}}, "enabled": true,
	}
	response := h.mustJSON(http.MethodPost, control.baseURL+"/panel-api/agents/"+localAgentID+"/rules", payload, map[string]string{
		"X-Panel-Token":   h.panelToken,
		"Idempotency-Key": randomSecret(h.t, "rule"),
	})
	if response.Status != http.StatusAccepted && response.Status != http.StatusCreated {
		h.t.Fatalf("create relayed HTTP rule status = %d: %s", response.Status, response.Body)
	}
	h.waitForMutation(control, response, "create relayed HTTP rule")
}

func (h *testHarness) waitForMutation(control controlInstance, accepted apiResponse, label string) {
	h.t.Helper()
	if accepted.Status != http.StatusAccepted {
		return
	}
	var envelope struct {
		OperationID string `json:"operation_id"`
		StatusURL   string `json:"status_url"`
		ApplyStatus string `json:"apply_status"`
	}
	if err := json.Unmarshal(accepted.Body, &envelope); err != nil || envelope.OperationID == "" || envelope.StatusURL == "" {
		h.t.Fatalf("decode %s mutation reference: error=%v body=%s", label, err, accepted.Body)
	}
	statusEndpoint := envelope.StatusURL
	if strings.HasPrefix(statusEndpoint, "/") {
		statusEndpoint = control.baseURL + statusEndpoint
	}
	ctx, cancel := context.WithTimeout(h.ctx, processTimeout)
	defer cancel()
	ticker := time.NewTicker(75 * time.Millisecond)
	defer ticker.Stop()
	lastBody := append([]byte(nil), accepted.Body...)
	for {
		response, err := h.request(ctx, http.MethodGet, statusEndpoint, nil, map[string]string{
			"X-Panel-Token": h.panelToken,
		})
		if err == nil && response.Status == http.StatusOK {
			lastBody = append(lastBody[:0], response.Body...)
			var status struct {
				Operation struct {
					ApplyStatus  string `json:"apply_status"`
					ErrorCode    string `json:"error_code"`
					ErrorMessage string `json:"error_message"`
				} `json:"operation"`
			}
			if decodeErr := json.Unmarshal(response.Body, &status); decodeErr == nil {
				switch status.Operation.ApplyStatus {
				case "applied":
					return
				case "failed", "superseded":
					h.t.Fatalf("%s mutation %s ended as %s (%s: %s): %s\nlisteners=%s\nidentities=%s", label, envelope.OperationID,
						status.Operation.ApplyStatus, status.Operation.ErrorCode, status.Operation.ErrorMessage, response.Body,
						h.diagnosticGET(control, "/panel-api/agents/"+localAgentID+"/relay-listeners"),
						h.diagnosticGET(control, "/panel-api/pki/identities"))
				}
			}
		}
		select {
		case <-ctx.Done():
			h.t.Fatalf("wait for %s mutation %s: %v; last status=%s\n%s", label, envelope.OperationID, ctx.Err(), lastBody, control.process.failureLog())
		case <-ticker.C:
		}
	}
}

func (h *testHarness) waitForHTTPBody(control controlInstance, endpoint, marker string) {
	h.t.Helper()
	err := eventually(h.ctx, processTimeout, func(ctx context.Context) (bool, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return false, err
		}
		response, err := h.client.Do(request)
		if err != nil {
			return false, err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		if err != nil {
			return false, err
		}
		return response.StatusCode == http.StatusOK && strings.Contains(string(body), marker), nil
	})
	if err != nil {
		h.t.Fatalf("wait for relay round trip through %s: %v\nrules=%s\nlisteners=%s\nidentities=%s\n%s", endpoint, err,
			h.diagnosticGET(control, "/panel-api/agents/"+localAgentID+"/rules"),
			h.diagnosticGET(control, "/panel-api/agents/"+localAgentID+"/relay-listeners"),
			h.diagnosticGET(control, "/panel-api/pki/identities"), control.process.failureLog())
	}
}

func (h *testHarness) diagnosticGET(control controlInstance, path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := h.request(ctx, http.MethodGet, control.baseURL+path, nil, map[string]string{
		"X-Panel-Token": h.panelToken,
	})
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("status=%d body=%s", response.Status, response.Body)
}

func (h *testHarness) assertRelayRejectsUntrustedClients(port int) {
	h.t.Helper()
	address := fmt.Sprintf("127.0.0.1:%d", port)
	tests := []struct {
		name      string
		notBefore time.Time
		notAfter  time.Time
		usage     []x509.ExtKeyUsage
	}{
		{name: "untrusted", notBefore: time.Now().Add(-time.Minute), notAfter: time.Now().Add(time.Hour), usage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
		{name: "expired", notBefore: time.Now().Add(-2 * time.Hour), notAfter: time.Now().Add(-time.Hour), usage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
		{name: "wrong-eku", notBefore: time.Now().Add(-time.Minute), notAfter: time.Now().Add(time.Hour), usage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}},
	}
	if err := tlsHandshake(address, nil); err == nil {
		h.t.Fatal("PKI relay accepted a TLS client without a certificate")
	}
	for _, test := range tests {
		h.t.Run("reject-"+test.name, func(t *testing.T) {
			certificate := selfSignedCertificate(t, test.notBefore, test.notAfter, test.usage)
			if err := tlsHandshake(address, &certificate); err == nil {
				t.Fatalf("PKI relay accepted %s client certificate", test.name)
			}
		})
	}
}

func (h *testHarness) exportProtectedBackup(control controlInstance) []byte {
	h.t.Helper()
	response := h.mustJSON(http.MethodPost, control.baseURL+"/panel-api/pki/backups/export", map[string]any{
		"passphrase": "e2e-backup-passphrase-strong",
	}, map[string]string{"X-Panel-Token": h.panelToken})
	if response.Status != http.StatusAccepted {
		h.t.Fatalf("protected export status = %d: %s", response.Status, response.Body)
	}
	var envelope struct {
		Operation struct {
			State     string `json:"state"`
			LastError string `json:"last_error"`
			Result    struct {
				Archive string `json:"archive"`
			} `json:"result"`
		} `json:"operation"`
	}
	if err := json.Unmarshal(response.Body, &envelope); err != nil {
		h.t.Fatalf("decode protected export: %v", err)
	}
	archive, err := base64.StdEncoding.DecodeString(envelope.Operation.Result.Archive)
	if err != nil || len(archive) == 0 || envelope.Operation.State != "succeeded" {
		h.t.Fatalf("protected export result is incomplete: state=%q archive=%d decode_error=%v operation_error=%q body=%s",
			envelope.Operation.State, len(archive), err, envelope.Operation.LastError, response.Body)
	}
	return archive
}

func (h *testHarness) assertRejectedRestoreDoesNotMutate(control controlInstance, archive []byte, before overviewEnvelope) {
	h.t.Helper()
	wrongPassphrase := h.importProtectedBackup(control, archive, "wrong-backup-passphrase", false)
	if wrongPassphrase != "failed" {
		h.t.Fatalf("wrong-passphrase restore state = %q, want failed", wrongPassphrase)
	}
	tampered := append([]byte(nil), archive...)
	tampered[len(tampered)/2] ^= 0x40
	if state := h.importProtectedBackup(control, tampered, "e2e-backup-passphrase-strong", false); state != "failed" {
		h.t.Fatalf("tampered restore state = %q, want failed", state)
	}
	after := h.waitForPKI(control)
	if after.Overview.PKIDomainID != before.Overview.PKIDomainID ||
		after.Overview.PKIEpoch != before.Overview.PKIEpoch ||
		after.Overview.SecurityRevision != before.Overview.SecurityRevision {
		h.t.Fatalf("rejected restore mutated active PKI version: before=%+v after=%+v", before.Overview, after.Overview)
	}
}

func (h *testHarness) importProtectedBackup(control controlInstance, archive []byte, passphrase string, force bool) string {
	h.t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("archive", "internal-pki.backup")
	if err != nil {
		h.t.Fatalf("create protected import part: %v", err)
	}
	if _, err := part.Write(archive); err != nil {
		h.t.Fatalf("write protected import archive: %v", err)
	}
	for name, value := range map[string]string{
		"passphrase": passphrase, "force": fmt.Sprintf("%t", force), "reason": "e2e restore validation",
	} {
		if err := writer.WriteField(name, value); err != nil {
			h.t.Fatalf("write protected import field: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		h.t.Fatalf("close protected import form: %v", err)
	}
	response, err := h.request(h.ctx, http.MethodPost, control.baseURL+"/panel-api/pki/backups/import", body.Bytes(), map[string]string{
		"X-Panel-Token": h.panelToken, "Content-Type": writer.FormDataContentType(),
	})
	if err != nil {
		h.t.Fatalf("protected import request: %v", err)
	}
	if response.Status != http.StatusAccepted {
		h.t.Fatalf("protected import status = %d: %s", response.Status, response.Body)
	}
	var envelope struct {
		Operation struct {
			State string `json:"state"`
		} `json:"operation"`
	}
	if err := json.Unmarshal(response.Body, &envelope); err != nil {
		h.t.Fatalf("decode protected import response: %v", err)
	}
	return envelope.Operation.State
}

func (h *testHarness) assertPersistedStateDoesNotLeakControlSecrets(dataDir, agentToken string) {
	h.t.Helper()
	for _, secret := range []string{h.panelToken, h.registerToken, agentToken} {
		if secret == "" {
			continue
		}
		secretFound := errors.New("control secret found")
		leakedPath := ""
		err := eventually(h.ctx, processTimeout, func(context.Context) (bool, error) {
			walkErr := filepath.WalkDir(dataDir, func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil || entry.IsDir() {
					return walkErr
				}
				if entry.Name() == "panel.db" || strings.HasSuffix(entry.Name(), "-wal") || strings.HasSuffix(entry.Name(), "-shm") {
					return nil
				}
				contents, readErr := os.ReadFile(path)
				if readErr != nil {
					return readErr
				}
				if bytes.Contains(contents, []byte(secret)) {
					leakedPath = path
					return secretFound
				}
				return nil
			})
			if errors.Is(walkErr, secretFound) {
				return true, nil
			}
			return walkErr == nil, walkErr
		})
		if leakedPath != "" {
			h.t.Fatalf("control secret leaked into %s", leakedPath)
		}
		if err != nil {
			h.t.Fatal(err)
		}
	}
}

func (h *testHarness) revokeListenerAndWaitForFence(control controlInstance, listenerID int, frontendURL string) {
	h.t.Helper()
	response := h.mustJSON(http.MethodGet, control.baseURL+"/panel-api/pki/identities", nil, map[string]string{
		"X-Panel-Token": h.panelToken,
	})
	if response.Status != http.StatusOK {
		h.t.Fatalf("list PKI identities status = %d: %s", response.Status, response.Body)
	}
	var identities struct {
		Identities []struct {
			ID         string `json:"id"`
			Kind       string `json:"kind"`
			ListenerID string `json:"listener_id"`
			State      string `json:"state"`
		} `json:"identities"`
	}
	if err := json.Unmarshal(response.Body, &identities); err != nil {
		h.t.Fatalf("decode PKI identities: %v", err)
	}
	identityID := ""
	for _, identity := range identities.Identities {
		if identity.Kind == "listener" && identity.ListenerID == fmtInt(listenerID) {
			identityID = identity.ID
			break
		}
	}
	if identityID == "" {
		h.t.Fatalf("active PKI identity for relay listener %d not found: %s", listenerID, response.Body)
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
	if revoke.Status != http.StatusAccepted {
		h.t.Fatalf("revoke listener identity status = %d: %s", revoke.Status, revoke.Body)
	}

	fenceCtx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
	defer cancel()
	err := eventually(fenceCtx, 5*time.Second, func(ctx context.Context) (bool, error) {
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, frontendURL, nil)
		if requestErr != nil {
			return false, requestErr
		}
		response, requestErr := h.client.Do(request)
		if requestErr != nil {
			return true, nil
		}
		response.Body.Close()
		return response.StatusCode >= 500, nil
	})
	if err != nil {
		h.t.Fatalf("revoked listener was not fenced within five seconds: %v\n%s", err, control.process.failureLog())
	}
}

func (h *testHarness) mustJSON(method, endpoint string, payload any, headers map[string]string) apiResponse {
	h.t.Helper()
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			h.t.Fatalf("encode JSON request: %v", err)
		}
		if headers == nil {
			headers = map[string]string{}
		}
		headers["Content-Type"] = "application/json"
	}
	response, err := h.request(h.ctx, method, endpoint, body, headers)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, endpoint, err)
	}
	return response
}

func (h *testHarness) request(ctx context.Context, method, endpoint string, body []byte, headers map[string]string) (apiResponse, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return apiResponse{}, err
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := h.client.Do(request)
	if err != nil {
		return apiResponse{}, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 40<<20))
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Status: response.StatusCode, Header: response.Header.Clone(), Body: responseBody}, nil
}

func eventually(parent context.Context, timeout time.Duration, condition func(context.Context) (bool, error)) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		ok, err := condition(ctx)
		if ok {
			return nil
		}
		if err != nil {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("%w (last error: %v)", ctx.Err(), lastErr)
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func startLoopbackHTTPServer(t *testing.T, responseBody string) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start backend listener: %v", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(response, responseBody)
	})}
	done := make(chan struct{})
	go func() {
		_ = server.Serve(listener)
		close(done)
	}()
	stop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		select {
		case <-done:
		case <-ctx.Done():
		}
	}
	return "http://" + listener.Addr().String(), stop
}

func reserveLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func reserveLoopbackUDPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback UDP port: %v", err)
	}
	defer listener.Close()
	return listener.LocalAddr().(*net.UDPAddr).Port
}

func tlsHandshake(address string, certificate *tls.Certificate) error {
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	config := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12} // Test the server-side client verifier.
	if certificate != nil {
		config.Certificates = []tls.Certificate{*certificate}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	connection, err := tls.DialWithDialer(dialer, "tcp", address, config)
	if err != nil {
		return err
	}
	defer connection.Close()
	if err := connection.HandshakeContext(ctx); err != nil {
		return err
	}
	// In TLS 1.3 the client may finish its local handshake before receiving
	// the server's fatal alert for a missing or rejected client certificate.
	// The relay sends no application bytes before the client protocol header,
	// so an immediate close proves server-side rejection while a read timeout
	// means the server kept the authenticated session open.
	if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	var probe [1]byte
	_, err = connection.Read(probe[:])
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return nil
	}
	return err
}

func selfSignedCertificate(t *testing.T, notBefore, notAfter time.Time, usage []x509.ExtKeyUsage) tls.Certificate {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate attack key: %v", err)
	}
	serialBytes := make([]byte, 16)
	if _, err := rand.Read(serialBytes); err != nil {
		t.Fatalf("generate attack serial: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: newBigInt(serialBytes), Subject: pkix.Name{CommonName: "untrusted-e2e"},
		NotBefore: notBefore, NotAfter: notAfter, ExtKeyUsage: usage,
		KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create attack certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal attack key: %v", err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	)
	if err != nil {
		t.Fatalf("load attack certificate: %v", err)
	}
	return certificate
}

func newBigInt(value []byte) *big.Int {
	result := new(big.Int).SetBytes(value)
	if result.Sign() == 0 {
		result.SetInt64(1)
	}
	return result
}

func firstNamedFile(root, name string) ([]byte, string) {
	var contents []byte
	var selected string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return filepath.SkipDir
			}
			return walkErr
		}
		if entry.IsDir() || entry.Name() != name {
			return nil
		}
		value, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		contents, selected = value, path
		return io.EOF
	})
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrNotExist) {
		return nil, ""
	}
	return contents, selected
}

func relativeFileList(root string) string {
	paths := make([]string, 0)
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr == nil {
			paths = append(paths, relative)
		}
		return nil
	})
	sort.Strings(paths)
	return strings.Join(paths, "\n")
}

func mergedEnvironment(overrides map[string]string) []string {
	values := make(map[string]string)
	for _, entry := range os.Environ() {
		name, value, found := strings.Cut(entry, "=")
		if found {
			values[strings.ToUpper(name)] = name + "=" + value
		}
	}
	for name, value := range overrides {
		values[strings.ToUpper(name)] = name + "=" + value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, values[key])
	}
	return result
}

func randomSecret(t *testing.T, prefix string) string {
	t.Helper()
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		t.Fatalf("generate %s fixture secret: %v", prefix, err)
	}
	return prefix + "-" + hex.EncodeToString(random)
}

func sanitizeLog(value string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	const max = 32 << 10
	if len(value) > max {
		value = value[len(value)-max:]
	}
	return value
}
