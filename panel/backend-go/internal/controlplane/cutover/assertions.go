package cutover

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	goagentembedded "github.com/sakullla/nginx-reverse-emby/go-agent/embedded"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type panelResponse struct {
	StatusCode int
	Body       string
}

type managedCertificateACMEView struct {
	MainDomain string `json:"Main_Domain"`
	CA         string `json:"CA"`
	Profile    string `json:"Profile"`
	Renew      string `json:"Renew"`
}

type managedCertificateView struct {
	ID              int                        `json:"id"`
	Domain          string                     `json:"domain"`
	Enabled         bool                       `json:"enabled"`
	Scope           string                     `json:"scope"`
	IssuerMode      string                     `json:"issuer_mode"`
	Status          string                     `json:"status"`
	Usage           string                     `json:"usage"`
	CertificateType string                     `json:"certificate_type"`
	SelfSigned      bool                       `json:"self_signed"`
	ACMEInfo        managedCertificateACMEView `json:"acme_info"`
}

type relayDialRoundTripResult struct {
	Payload      string
	PeerSPKIPin  string
	TrustedCAIDs []int
}

type stableApplyMetadata struct {
	DesiredRevision             int
	CurrentRevision             int
	LastApplyRevision           int
	LastApplyStatus             string
	LastApplyMessage            string
	LastSyncError               string
	PersistedDesiredRevision    int
	PersistedCurrentRevision    int
	PersistedCurrentRevisionRaw string
	PersistedLastApplyRevision  int
	PersistedLastApplyStatus    string
	PersistedLastApplyMessage   string
}

type embeddedRuntimeStateFile struct {
	CurrentRevision int64             `json:"current_revision"`
	Metadata        map[string]string `json:"metadata"`
}

type embeddedDesiredSnapshotFile struct {
	DesiredRevision int64 `json:"desired_revision"`
}

func (h *cutoverHarness) GetPanel(path string) panelResponse {
	h.t.Helper()

	req, err := http.NewRequest(http.MethodGet, h.panelServer.URL+path, nil)
	if err != nil {
		h.t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("X-Panel-Token", h.cfg.PanelToken)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		h.t.Fatalf("panel request error = %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatalf("io.ReadAll() error = %v", err)
	}
	return panelResponse{
		StatusCode: resp.StatusCode,
		Body:       string(body),
	}
}

func (h *cutoverHarness) GetHTTPFrontend(host string) string {
	h.t.Helper()
	return waitForHTTPFrontendBody(h.t, h.httpClient, h.fixture.httpFrontendPort, host, 4*time.Second)
}

func (h *cutoverHarness) RoundTripTCPOverL4(payload string) string {
	h.t.Helper()
	address := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", h.fixture.l4FrontendPort))
	return waitForTCPRoundTrip(h.t, address, []byte(payload), 4*time.Second)
}

func (h *cutoverHarness) RoundTripRelayDial(payload string) string {
	h.t.Helper()
	return h.RoundTripRelayDialWithTrust(payload).Payload
}

func (h *cutoverHarness) RoundTripRelayDialWithTrust(payload string) relayDialRoundTripResult {
	h.t.Helper()
	if !h.fixture.l4UsesRelay {
		h.t.Fatal("RoundTripRelayDialWithTrust() requires relay-enabled fixture")
	}

	certPath := fmt.Sprintf("%s/cert", h.fixture.managedCertMaterialDir)
	keyPath := fmt.Sprintf("%s/key", h.fixture.managedCertMaterialDir)
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		h.t.Fatalf("ReadFile(%s) error = %v", certPath, err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		h.t.Fatalf("ReadFile(%s) error = %v", keyPath, err)
	}
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		h.t.Fatalf("tls.X509KeyPair() error = %v", err)
	}
	leafDER := certificate.Certificate[0]
	leafCert, err := x509.ParseCertificate(leafDER)
	if err != nil {
		h.t.Fatalf("x509.ParseCertificate() error = %v", err)
	}
	certificate.Leaf = leafCert

	if strings.TrimSpace(h.fixture.relayInternalCAPEM) == "" {
		h.t.Fatal("relay internal CA PEM must be seeded")
	}

	provider := staticRelayMaterialProvider{
		certificatesByID: map[int]tls.Certificate{
			h.fixture.relayCertificateID: certificate,
		},
		trustedCAPEMByID: map[int][]byte{
			h.fixture.relayInternalCAID: []byte(h.fixture.relayInternalCAPEM),
		},
	}
	hop := goagentembedded.RelayHop{
		Address: net.JoinHostPort("127.0.0.1", strconv.Itoa(h.fixture.relayListenerPort)),
		Listener: goagentembedded.RelayListener{
			ID:            h.fixture.relayListenerID,
			AgentID:       h.fixture.localAgentID,
			Name:          "fixture-relay",
			ListenHost:    "127.0.0.1",
			BindHosts:     []string{"127.0.0.1"},
			ListenPort:    h.fixture.relayListenerPort,
			PublicHost:    "127.0.0.1",
			PublicPort:    h.fixture.relayListenerPort,
			Enabled:       true,
			CertificateID: intPtr(h.fixture.relayCertificateID),
			TLSMode:       "pin_and_ca",
			PinSet: []goagentembedded.RelayPin{
				{
					Type:  "spki_sha256",
					Value: h.fixture.relayPinSPKISHA256,
				},
			},
			TrustedCACertificateIDs: []int{h.fixture.relayInternalCAID},
			AllowSelfSigned:         false,
		},
	}
	reply := waitForRelayDialRoundTrip(h.t, h.fixture.tcpBackendAddr, []goagentembedded.RelayHop{hop}, &provider, []byte(payload), 4*time.Second)
	return relayDialRoundTripResult{
		Payload:      string(reply.payload),
		PeerSPKIPin:  peerSPKIPin(reply.connState),
		TrustedCAIDs: provider.requestedTrustedCAIDs(),
	}
}

func (h *cutoverHarness) IsRelayListenerReachable() bool {
	h.t.Helper()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(h.fixture.relayListenerPort))
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

func (h *cutoverHarness) ListGlobalManagedCertificates() []managedCertificateView {
	h.t.Helper()
	resp := h.GetPanel("/panel-api/certificates")
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("GET /panel-api/certificates = %d body=%s", resp.StatusCode, resp.Body)
	}

	var payload struct {
		Certificates []managedCertificateView `json:"certificates"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &payload); err != nil {
		h.t.Fatalf("json.Unmarshal(certificates) error = %v body=%s", err, resp.Body)
	}
	return payload.Certificates
}

func (h *cutoverHarness) WaitForStableApplyMetadata(targetRevision int) stableApplyMetadata {
	h.t.Helper()
	localState := waitForStableLocalApply(h.t, h.apiStore, targetRevision, 4*time.Second)
	desiredSnapshot := waitForEmbeddedDesiredSnapshot(h.t, h.fixture.dataDir, targetRevision, 4*time.Second)
	runtimeState := waitForEmbeddedRuntimeState(h.t, h.fixture.dataDir, targetRevision, 4*time.Second)

	return stableApplyMetadata{
		DesiredRevision:             localState.DesiredRevision,
		CurrentRevision:             localState.CurrentRevision,
		LastApplyRevision:           localState.LastApplyRevision,
		LastApplyStatus:             strings.TrimSpace(localState.LastApplyStatus),
		LastApplyMessage:            localState.LastApplyMessage,
		LastSyncError:               strings.TrimSpace(runtimeState.Metadata["last_sync_error"]),
		PersistedDesiredRevision:    int(desiredSnapshot.DesiredRevision),
		PersistedCurrentRevision:    persistedRuntimeCurrentRevision(runtimeState),
		PersistedCurrentRevisionRaw: strings.TrimSpace(runtimeState.Metadata["current_revision"]),
		PersistedLastApplyRevision:  parseRuntimeRevision(runtimeState.Metadata["last_apply_revision"]),
		PersistedLastApplyStatus:    strings.TrimSpace(runtimeState.Metadata["last_apply_status"]),
		PersistedLastApplyMessage:   runtimeState.Metadata["last_apply_message"],
	}
}

func waitForStableLocalApply(t *testing.T, store *storage.SQLiteStore, targetRevision int, timeout time.Duration) storage.LocalAgentStateRow {
	t.Helper()

	state, err := waitForStableLocalApplyState(store, targetRevision, timeout)
	if err != nil {
		t.Fatalf("waitForStableLocalApply() error: %v", err)
	}
	return state
}

func waitForStableLocalApplyState(store *storage.SQLiteStore, targetRevision int, timeout time.Duration) (storage.LocalAgentStateRow, error) {
	deadline := time.Now().Add(timeout)
	last := storage.LocalAgentStateRow{}
	for time.Now().Before(deadline) {
		state, err := store.LoadLocalAgentState(context.Background())
		if err == nil {
			last = state
			if state.CurrentRevision >= targetRevision &&
				state.LastApplyRevision >= targetRevision &&
				strings.EqualFold(strings.TrimSpace(state.LastApplyStatus), "success") &&
				strings.TrimSpace(state.LastApplyMessage) == "" {
				return state, nil
			}
		}
		time.Sleep(15 * time.Millisecond)
	}

	return storage.LocalAgentStateRow{}, fmt.Errorf("timed out waiting for local apply convergence: target_revision=%d last_state=%+v", targetRevision, last)
}

func waitForHTTPFrontendBody(t *testing.T, client *http.Client, port int, host string, timeout time.Duration) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	hostHeader := fmt.Sprintf("%s:%d", host, port)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, frontendAddress(port), nil)
		if err != nil {
			t.Fatalf("http.NewRequest() error = %v", err)
		}
		req.Host = hostHeader

		resp, err := client.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr == nil && resp.StatusCode == http.StatusOK {
				return string(body)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for frontend response host=%q port=%d", host, port)
	return ""
}

func waitForTCPRoundTrip(t *testing.T, address string, payload []byte, timeout time.Duration) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 350*time.Millisecond)
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}

		_ = conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
		if _, err := conn.Write(payload); err != nil {
			_ = conn.Close()
			time.Sleep(20 * time.Millisecond)
			continue
		}

		reply := make([]byte, len(payload))
		if _, err := io.ReadFull(conn, reply); err != nil {
			_ = conn.Close()
			time.Sleep(20 * time.Millisecond)
			continue
		}
		_ = conn.Close()
		if bytes.Equal(reply, payload) {
			return string(reply)
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for tcp round-trip at %s", address)
	return ""
}

func waitForRelayDialRoundTrip(
	t *testing.T,
	target string,
	chain []goagentembedded.RelayHop,
	provider goagentembedded.RelayTLSMaterialProvider,
	payload []byte,
	timeout time.Duration,
) relayRoundTripReply {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		// panel/backend-go cannot import go-agent/internal/relay directly because of Go internal
		// package boundaries. The exported embedded bridge delegates straight to relay.Dial.
		conn, err := goagentembedded.DialRelay(ctx, "tcp", target, chain, provider)
		cancel()
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}

		_ = conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
		if _, err := conn.Write(payload); err != nil {
			_ = conn.Close()
			time.Sleep(20 * time.Millisecond)
			continue
		}
		reply := make([]byte, len(payload))
		if _, err := io.ReadFull(conn, reply); err != nil {
			_ = conn.Close()
			time.Sleep(20 * time.Millisecond)
			continue
		}
		_ = conn.Close()
		if bytes.Equal(reply, payload) {
			return relayRoundTripReply{
				payload:   reply,
				connState: relayConnectionState(conn),
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for relay dial round-trip target=%q", target)
	return relayRoundTripReply{}
}

type relayRoundTripReply struct {
	payload   []byte
	connState *tls.ConnectionState
}

func relayConnectionState(conn net.Conn) *tls.ConnectionState {
	type connectionStater interface {
		ConnectionState() tls.ConnectionState
	}
	stater, ok := conn.(connectionStater)
	if !ok {
		return nil
	}
	state := stater.ConnectionState()
	return &state
}

func peerSPKIPin(state *tls.ConnectionState) string {
	if state == nil || len(state.PeerCertificates) == 0 {
		return ""
	}
	sum := sha256.Sum256(state.PeerCertificates[0].RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}

type staticRelayMaterialProvider struct {
	certificatesByID map[int]tls.Certificate
	trustedCAPEMByID map[int][]byte

	mu                      sync.Mutex
	trustedCAUsages         [][]int
	serverCertificateUsages []int
}

func (p *staticRelayMaterialProvider) ServerCertificate(_ context.Context, certificateID int) (*tls.Certificate, error) {
	p.mu.Lock()
	p.serverCertificateUsages = append(p.serverCertificateUsages, certificateID)
	p.mu.Unlock()

	certificate, ok := p.certificatesByID[certificateID]
	if !ok {
		return nil, fmt.Errorf("certificate %d not found", certificateID)
	}
	copyCert := certificate
	return &copyCert, nil
}

func (p *staticRelayMaterialProvider) TrustedCAPool(_ context.Context, certificateIDs []int) (*x509.CertPool, error) {
	p.mu.Lock()
	copyIDs := append([]int(nil), certificateIDs...)
	p.trustedCAUsages = append(p.trustedCAUsages, copyIDs)
	p.mu.Unlock()

	if len(certificateIDs) == 0 {
		return nil, nil
	}

	pool := x509.NewCertPool()
	for _, certificateID := range certificateIDs {
		pemBytes, ok := p.trustedCAPEMByID[certificateID]
		if !ok {
			return nil, fmt.Errorf("trusted CA %d not found", certificateID)
		}
		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, fmt.Errorf("trusted CA %d has invalid PEM", certificateID)
		}
	}
	return pool, nil
}

func (p *staticRelayMaterialProvider) requestedTrustedCAIDs() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.trustedCAUsages) == 0 {
		return nil
	}
	return append([]int(nil), p.trustedCAUsages[len(p.trustedCAUsages)-1]...)
}

func waitForEmbeddedRuntimeState(t *testing.T, dataDir string, targetRevision int, timeout time.Duration) embeddedRuntimeStateFile {
	t.Helper()

	statePath := filepath.Join(dataDir, "embedded-agent-state", "runtime-state.json")
	deadline := time.Now().Add(timeout)
	last := embeddedRuntimeStateFile{}
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(statePath)
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}

		var state embeddedRuntimeStateFile
		if err := json.Unmarshal(raw, &state); err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		last = state
		currentRevision := persistedRuntimeCurrentRevision(state)
		lastApplyRevision := parseRuntimeRevision(state.Metadata["last_apply_revision"])
		lastApplyStatus := strings.ToLower(strings.TrimSpace(state.Metadata["last_apply_status"]))
		lastApplyMessage := strings.TrimSpace(state.Metadata["last_apply_message"])
		lastSyncError := strings.TrimSpace(state.Metadata["last_sync_error"])

		if currentRevision >= targetRevision &&
			parseRuntimeRevision(state.Metadata["current_revision"]) >= targetRevision &&
			lastApplyRevision >= targetRevision &&
			lastApplyStatus == "success" &&
			lastApplyMessage == "" &&
			lastSyncError == "" {
			return state
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for embedded runtime-state metadata convergence: target_revision=%d last=%+v", targetRevision, last)
	return embeddedRuntimeStateFile{}
}

func waitForEmbeddedDesiredSnapshot(t *testing.T, dataDir string, targetRevision int, timeout time.Duration) embeddedDesiredSnapshotFile {
	t.Helper()

	statePath := filepath.Join(dataDir, "embedded-agent-state", "desired-snapshot.json")
	deadline := time.Now().Add(timeout)
	last := embeddedDesiredSnapshotFile{}
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(statePath)
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}

		var state embeddedDesiredSnapshotFile
		if err := json.Unmarshal(raw, &state); err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		last = state
		if int(state.DesiredRevision) >= targetRevision {
			return state
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for embedded desired-snapshot convergence: target_revision=%d last=%+v", targetRevision, last)
	return embeddedDesiredSnapshotFile{}
}

func persistedRuntimeCurrentRevision(state embeddedRuntimeStateFile) int {
	if current := parseRuntimeRevision(state.Metadata["current_revision"]); current > 0 {
		return current
	}
	return int(state.CurrentRevision)
}

func parseRuntimeRevision(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return value
}
