package cutover

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	goagentembedded "github.com/sakullla/nginx-reverse-emby/go-agent/embedded"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type panelResponse struct {
	StatusCode int
	Body       string
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
	if !h.fixture.l4UsesRelay {
		h.t.Fatal("RoundTripRelayDial() requires relay-enabled fixture")
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

	provider := staticRelayMaterialProvider{
		certificatesByID: map[int]tls.Certificate{
			h.fixture.relayCertificateID: certificate,
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
			TLSMode:       "pin_only",
			PinSet: []goagentembedded.RelayPin{
				{
					Type:  "spki_sha256",
					Value: h.fixture.relayPinSPKISHA256,
				},
			},
			TrustedCACertificateIDs: nil,
			AllowSelfSigned:         false,
		},
	}
	reply := waitForRelayDialRoundTrip(h.t, h.fixture.tcpBackendAddr, []goagentembedded.RelayHop{hop}, provider, []byte(payload), 4*time.Second)
	return string(reply)
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
) []byte {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
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
			return reply
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for relay dial round-trip target=%q", target)
	return nil
}

type staticRelayMaterialProvider struct {
	certificatesByID map[int]tls.Certificate
}

func (p staticRelayMaterialProvider) ServerCertificate(_ context.Context, certificateID int) (*tls.Certificate, error) {
	certificate, ok := p.certificatesByID[certificateID]
	if !ok {
		return nil, fmt.Errorf("certificate %d not found", certificateID)
	}
	copyCert := certificate
	return &copyCert, nil
}

func (p staticRelayMaterialProvider) TrustedCAPool(_ context.Context, _ []int) (*x509.CertPool, error) {
	return nil, nil
}
