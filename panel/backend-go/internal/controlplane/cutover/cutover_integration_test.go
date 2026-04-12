package cutover

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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

func TestMasterEmbeddedCutoverAppliesL4RuleAndForwardsTCP(t *testing.T) {
	harness := newCutoverHarness(t)
	defer harness.Close()

	const payload = "fixture:l4-round-trip"
	reply := harness.RoundTripTCPOverL4(payload)
	if reply != payload {
		t.Fatalf("RoundTripTCPOverL4() = %q, want %q", reply, payload)
	}

	stable := harness.WaitForStableApplyMetadata(harness.fixture.expectedRevision)
	if stable.DesiredRevision < harness.fixture.expectedRevision {
		t.Fatalf("DesiredRevision = %d, want >= %d", stable.DesiredRevision, harness.fixture.expectedRevision)
	}
	if stable.CurrentRevision != stable.DesiredRevision {
		t.Fatalf("CurrentRevision = %d, want DesiredRevision %d", stable.CurrentRevision, stable.DesiredRevision)
	}
	if stable.LastApplyRevision != stable.DesiredRevision {
		t.Fatalf("LastApplyRevision = %d, want DesiredRevision %d", stable.LastApplyRevision, stable.DesiredRevision)
	}
	if stable.LastApplyStatus != "success" {
		t.Fatalf("LastApplyStatus = %q, want success", stable.LastApplyStatus)
	}
	if stable.LastApplyMessage != "" {
		t.Fatalf("LastApplyMessage = %q, want empty", stable.LastApplyMessage)
	}
	if stable.LastSyncError != "" {
		t.Fatalf("LastSyncError = %q, want empty", stable.LastSyncError)
	}
	if stable.PersistedDesiredRevision != stable.DesiredRevision {
		t.Fatalf("PersistedDesiredRevision = %d, want DesiredRevision %d", stable.PersistedDesiredRevision, stable.DesiredRevision)
	}
	if stable.PersistedCurrentRevision != stable.CurrentRevision {
		t.Fatalf("PersistedCurrentRevision = %d, want CurrentRevision %d", stable.PersistedCurrentRevision, stable.CurrentRevision)
	}
	if stable.PersistedCurrentRevisionRaw != strconv.Itoa(stable.CurrentRevision) {
		t.Fatalf("PersistedCurrentRevisionRaw = %q, want %d", stable.PersistedCurrentRevisionRaw, stable.CurrentRevision)
	}
	if stable.PersistedLastApplyRevision != stable.LastApplyRevision {
		t.Fatalf("PersistedLastApplyRevision = %d, want LastApplyRevision %d", stable.PersistedLastApplyRevision, stable.LastApplyRevision)
	}
	if stable.PersistedLastApplyStatus != stable.LastApplyStatus {
		t.Fatalf("PersistedLastApplyStatus = %q, want %q", stable.PersistedLastApplyStatus, stable.LastApplyStatus)
	}
	if stable.PersistedLastApplyMessage != stable.LastApplyMessage {
		t.Fatalf("PersistedLastApplyMessage = %q, want %q", stable.PersistedLastApplyMessage, stable.LastApplyMessage)
	}
}

func TestMasterEmbeddedCutoverAppliesRelayListenerAndTrustChain(t *testing.T) {
	harness := newCutoverHarnessWithOptions(t, cutoverHarnessOptions{
		enableRelayPath: true,
		disableL4Path:   true,
	})
	defer harness.Close()

	const payload = "fixture:relay-round-trip"
	result := harness.RoundTripRelayDialWithTrust(payload)
	if result.Payload != payload {
		t.Fatalf("RoundTripRelayDialWithTrust().Payload = %q, want %q", result.Payload, payload)
	}
	if result.PeerSPKIPin != harness.fixture.relayPinSPKISHA256 {
		t.Fatalf("RoundTripRelayDialWithTrust().PeerSPKIPin = %q, want %q", result.PeerSPKIPin, harness.fixture.relayPinSPKISHA256)
	}
	if len(result.TrustedCAIDs) != 1 || result.TrustedCAIDs[0] != harness.fixture.relayInternalCAID {
		t.Fatalf("RoundTripRelayDialWithTrust().TrustedCAIDs = %+v, want [%d]", result.TrustedCAIDs, harness.fixture.relayInternalCAID)
	}
	if !harness.IsRelayListenerReachable() {
		t.Fatalf("relay listener is not reachable on port %d", harness.fixture.relayListenerPort)
	}
}

func TestMasterEmbeddedCutoverExposesManagedCertificateStateAndStableApplyMetadata(t *testing.T) {
	harness := newCutoverHarnessWithOptions(t, cutoverHarnessOptions{
		enableRelayPath: true,
	})
	defer harness.Close()

	certs := harness.ListGlobalManagedCertificates()
	if len(certs) < 3 {
		t.Fatalf("ListGlobalManagedCertificates() length = %d, want >= 3", len(certs))
	}

	uploaded := findManagedCertificateByID(t, certs, harness.fixture.relayCertificateID)
	if uploaded.CertificateType != "uploaded" || uploaded.IssuerMode != "local_http01" || uploaded.Usage != "relay_tunnel" || uploaded.Status != "active" {
		t.Fatalf("uploaded certificate semantics mismatch: %+v", uploaded)
	}

	internalCA := findManagedCertificateByID(t, certs, harness.fixture.relayInternalCAID)
	if internalCA.CertificateType != "internal_ca" || internalCA.Usage != "relay_ca" || internalCA.Status != "active" {
		t.Fatalf("internal CA certificate semantics mismatch: %+v", internalCA)
	}

	managedPolicy := findManagedCertificateByID(t, certs, harness.fixture.managedPolicyCertificateID)
	if managedPolicy.CertificateType != "acme" || managedPolicy.IssuerMode != "master_cf_dns" || managedPolicy.Status != "pending" {
		t.Fatalf("managed policy semantics mismatch: %+v", managedPolicy)
	}
	if strings.TrimSpace(managedPolicy.ACMEInfo.MainDomain) != harness.fixture.managedPolicyDomain {
		t.Fatalf("managed policy acme_info.main_domain = %q, want %q", managedPolicy.ACMEInfo.MainDomain, harness.fixture.managedPolicyDomain)
	}
	if strings.TrimSpace(managedPolicy.ACMEInfo.CA) == "" {
		t.Fatalf("managed policy acme_info.ca must be non-empty: %+v", managedPolicy.ACMEInfo)
	}

	stable := harness.WaitForStableApplyMetadata(harness.fixture.expectedRevision)
	if stable.DesiredRevision < harness.fixture.expectedRevision {
		t.Fatalf("DesiredRevision = %d, want >= %d", stable.DesiredRevision, harness.fixture.expectedRevision)
	}
	if stable.CurrentRevision != stable.DesiredRevision {
		t.Fatalf("CurrentRevision = %d, want DesiredRevision %d", stable.CurrentRevision, stable.DesiredRevision)
	}
	if stable.LastApplyRevision != stable.DesiredRevision {
		t.Fatalf("LastApplyRevision = %d, want DesiredRevision %d", stable.LastApplyRevision, stable.DesiredRevision)
	}
	if stable.LastApplyStatus != "success" {
		t.Fatalf("LastApplyStatus = %q, want success", stable.LastApplyStatus)
	}
	if stable.LastApplyMessage != "" {
		t.Fatalf("LastApplyMessage = %q, want empty", stable.LastApplyMessage)
	}
	if stable.LastSyncError != "" {
		t.Fatalf("LastSyncError = %q, want empty", stable.LastSyncError)
	}
	if stable.PersistedDesiredRevision != stable.DesiredRevision {
		t.Fatalf("PersistedDesiredRevision = %d, want DesiredRevision %d", stable.PersistedDesiredRevision, stable.DesiredRevision)
	}
	if stable.PersistedCurrentRevision != stable.CurrentRevision {
		t.Fatalf("PersistedCurrentRevision = %d, want CurrentRevision %d", stable.PersistedCurrentRevision, stable.CurrentRevision)
	}
	if stable.PersistedCurrentRevisionRaw != strconv.Itoa(stable.CurrentRevision) {
		t.Fatalf("PersistedCurrentRevisionRaw = %q, want %d", stable.PersistedCurrentRevisionRaw, stable.CurrentRevision)
	}
	if stable.PersistedLastApplyRevision != stable.LastApplyRevision {
		t.Fatalf("PersistedLastApplyRevision = %d, want LastApplyRevision %d", stable.PersistedLastApplyRevision, stable.LastApplyRevision)
	}
	if stable.PersistedLastApplyStatus != stable.LastApplyStatus {
		t.Fatalf("PersistedLastApplyStatus = %q, want %q", stable.PersistedLastApplyStatus, stable.LastApplyStatus)
	}
	if stable.PersistedLastApplyMessage != stable.LastApplyMessage {
		t.Fatalf("PersistedLastApplyMessage = %q, want %q", stable.PersistedLastApplyMessage, stable.LastApplyMessage)
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

func findManagedCertificateByID(t *testing.T, certs []managedCertificateView, certificateID int) managedCertificateView {
	t.Helper()
	for _, cert := range certs {
		if cert.ID == certificateID {
			return cert
		}
	}
	t.Fatalf("certificate %d not found in %+v", certificateID, certs)
	return managedCertificateView{}
}
