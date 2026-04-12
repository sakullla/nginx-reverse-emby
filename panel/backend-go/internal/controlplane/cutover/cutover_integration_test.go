package cutover

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestMasterEmbeddedCutoverAppliesHTTPRuleAndServesTraffic(t *testing.T) {
	harness := newCutoverHarness(t)
	defer harness.Close()

	resp := harness.GetPanel("/panel-api/info")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /panel-api/info = %d", resp.StatusCode)
	}

	body := harness.GetHTTPFrontend("fixture-http.example.test")
	if !strings.Contains(body, "backend:http") {
		t.Fatalf("unexpected frontend body %q", body)
	}
}

func TestCutoverAssertionHelpersRoundTripTCPOverL4(t *testing.T) {
	harness := newCutoverHarness(t)
	defer harness.Close()

	const payload = "fixture:l4-round-trip"
	reply := harness.RoundTripTCPOverL4(payload)
	if reply != payload {
		t.Fatalf("RoundTripTCPOverL4() = %q, want %q", reply, payload)
	}
}

func TestCutoverAssertionHelpersRoundTripRelayDialPath(t *testing.T) {
	harness := newCutoverHarnessWithOptions(t, cutoverHarnessOptions{
		enableRelayPath: true,
		disableL4Path:   true,
	})
	defer harness.Close()

	const payload = "fixture:relay-round-trip"
	reply := harness.RoundTripRelayDial(payload)
	if reply != payload {
		t.Fatalf("RoundTripRelayDial() = %q, want %q", reply, payload)
	}
}

func TestCutoverFixtureBuilderPersistsManagedCertificateMaterialAtNormalizedPath(t *testing.T) {
	httpBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("backend:http"))
	}))
	defer httpBackend.Close()
	tcpBackendAddr, stopTCP := startTCPEchoBackend(t)
	defer stopTCP()

	fixture := buildCutoverFixture(t, cutoverFixtureInput{
		httpBackendURL: httpBackend.URL,
		tcpBackendAddr: tcpBackendAddr,
	})
	if fixture.managedCertMaterialDir == "" {
		t.Fatal("managedCertMaterialDir must be set")
	}

	certPath := filepath.Join(fixture.managedCertMaterialDir, "cert")
	keyPath := filepath.Join(fixture.managedCertMaterialDir, "key")
	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("expected cert path %s: %v", certPath, err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("expected key path %s: %v", keyPath, err)
	}
}

func TestCutoverFixtureBuilderSeedsExplicitLocalAgentState(t *testing.T) {
	httpBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("backend:http"))
	}))
	defer httpBackend.Close()
	tcpBackendAddr, stopTCP := startTCPEchoBackend(t)
	defer stopTCP()

	fixture := buildCutoverFixture(t, cutoverFixtureInput{
		httpBackendURL: httpBackend.URL,
		tcpBackendAddr: tcpBackendAddr,
	})

	store, err := storage.NewSQLiteStore(fixture.dataDir, fixture.localAgentID)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer func() {
		_ = store.Close()
	}()

	state, err := store.LoadLocalAgentState(t.Context())
	if err != nil {
		t.Fatalf("LoadLocalAgentState() error = %v", err)
	}
	if state.CurrentRevision != fixture.seededLocalCurrentRevision {
		t.Fatalf("CurrentRevision = %d, want %d", state.CurrentRevision, fixture.seededLocalCurrentRevision)
	}
	if state.LastApplyStatus != fixture.seededLocalApplyStatus {
		t.Fatalf("LastApplyStatus = %q, want %q", state.LastApplyStatus, fixture.seededLocalApplyStatus)
	}
	if state.LastApplyMessage != fixture.seededLocalApplyMessage {
		t.Fatalf("LastApplyMessage = %q, want %q", state.LastApplyMessage, fixture.seededLocalApplyMessage)
	}
}

func TestCutoverHarnessRetriesWhenPreferredPortsAreOccupied(t *testing.T) {
	occupiedHTTP, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen(http) error = %v", err)
	}
	defer func() { _ = occupiedHTTP.Close() }()

	occupiedL4, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen(l4) error = %v", err)
	}
	defer func() { _ = occupiedL4.Close() }()

	occupiedRelay, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen(relay) error = %v", err)
	}
	defer func() { _ = occupiedRelay.Close() }()

	preferredHTTP := occupiedHTTP.Addr().(*net.TCPAddr).Port
	preferredL4 := occupiedL4.Addr().(*net.TCPAddr).Port
	preferredRelay := occupiedRelay.Addr().(*net.TCPAddr).Port

	harness := newCutoverHarnessWithOptions(t, cutoverHarnessOptions{
		preferredHTTPFrontendPort:  preferredHTTP,
		preferredL4FrontendPort:    preferredL4,
		preferredRelayListenerPort: preferredRelay,
	})
	defer harness.Close()

	if harness.fixture.httpFrontendPort == preferredHTTP {
		t.Fatalf("http frontend port stayed on occupied preferred port %d", preferredHTTP)
	}
	if harness.fixture.l4FrontendPort == preferredL4 {
		t.Fatalf("l4 frontend port stayed on occupied preferred port %d", preferredL4)
	}
	if harness.fixture.relayListenerPort == preferredRelay {
		t.Fatalf("relay listener port stayed on occupied preferred port %d", preferredRelay)
	}

	body := harness.GetHTTPFrontend("fixture-http.example.test")
	if !strings.Contains(body, "backend:http") {
		t.Fatalf("unexpected frontend body %q", body)
	}
}
