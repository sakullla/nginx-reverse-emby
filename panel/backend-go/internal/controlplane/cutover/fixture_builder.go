package cutover

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	"gorm.io/gorm"
)

type cutoverFixtureInput struct {
	httpBackendURL    string
	tcpBackendAddr    string
	enableRelayPath   bool
	disableL4Path     bool
	httpFrontendPort  int
	l4FrontendPort    int
	relayListenerPort int
}

type cutoverFixture struct {
	dataDir                    string
	localAgentID               string
	panelToken                 string
	registerToken              string
	httpFrontendHost           string
	httpFrontendPort           int
	l4FrontendPort             int
	relayListenerPort          int
	managedCertDomain          string
	managedCertMaterialDir     string
	relayInternalCADomain      string
	relayInternalCAMaterialDir string
	relayInternalCAPEM         string
	managedPolicyDomain        string
	expectedRevision           int
	l4UsesRelay                bool
	tcpBackendAddr             string
	relayListenerID            int
	relayCertificateID         int
	relayInternalCAID          int
	managedPolicyCertificateID int
	relayPinSPKISHA256         string
	seededLocalCurrentRevision int
	seededLocalApplyStatus     string
	seededLocalApplyMessage    string
}

func buildCutoverFixture(t *testing.T, input cutoverFixtureInput) cutoverFixture {
	t.Helper()

	dataDir := t.TempDir()
	fixture := cutoverFixture{
		dataDir:                    dataDir,
		localAgentID:               "local",
		panelToken:                 "cutover-panel-token",
		registerToken:              "cutover-register-token",
		httpFrontendHost:           "fixture-http.example.test",
		httpFrontendPort:           resolveFixturePort(t, input.httpFrontendPort),
		l4FrontendPort:             resolveFixturePort(t, input.l4FrontendPort),
		relayListenerPort:          resolveFixturePort(t, input.relayListenerPort),
		managedCertDomain:          "fixture-cert.example.test",
		relayInternalCADomain:      "fixture-relay-ca.internal",
		managedPolicyDomain:        "fixture-managed-policy.example.test",
		expectedRevision:           7,
		l4UsesRelay:                input.enableRelayPath,
		tcpBackendAddr:             input.tcpBackendAddr,
		relayListenerID:            301,
		relayCertificateID:         401,
		relayInternalCAID:          402,
		managedPolicyCertificateID: 403,
		seededLocalCurrentRevision: 2,
		seededLocalApplyStatus:     "error",
		seededLocalApplyMessage:    "fixture-seeded-initial-state",
	}

	if err := bootstrapPanelDatabase(dataDir); err != nil {
		t.Fatalf("bootstrapPanelDatabase() error = %v", err)
	}

	store, err := storage.NewSQLiteStore(dataDir, fixture.localAgentID)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer func() {
		_ = store.Close()
	}()

	if err := seedCutoverFixture(t.Context(), store, &fixture, input); err != nil {
		t.Fatalf("seedCutoverFixture() error = %v", err)
	}

	return fixture
}

func bootstrapPanelDatabase(dataDir string) error {
	dbPath := filepath.Join(dataDir, "panel.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer func() {
		_ = sqlDB.Close()
	}()

	return storage.BootstrapSQLiteSchema(context.Background(), db)
}

func seedCutoverFixture(ctx context.Context, store *storage.SQLiteStore, fixture *cutoverFixture, input cutoverFixtureInput) error {
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:               fixture.localAgentID,
		Name:             "Local Agent",
		AgentToken:       "local-agent-token",
		Mode:             "pull",
		DesiredRevision:  0,
		CurrentRevision:  0,
		LastApplyStatus:  "success",
		CapabilitiesJSON: mustMarshalJSON([]string{"http_rules", "l4_rules", "relay"}),
		IsLocal:          true,
	}); err != nil {
		return err
	}

	httpBackendsJSON := mustMarshalJSON([]storage.HTTPBackend{
		{URL: input.httpBackendURL},
	})
	if err := store.SaveHTTPRules(ctx, fixture.localAgentID, []storage.HTTPRuleRow{{
		ID:                101,
		AgentID:           fixture.localAgentID,
		FrontendURL:       fmt.Sprintf("http://%s:%d", fixture.httpFrontendHost, fixture.httpFrontendPort),
		BackendURL:        input.httpBackendURL,
		BackendsJSON:      httpBackendsJSON,
		LoadBalancingJSON: `{"strategy":"round_robin"}`,
		Enabled:           true,
		ProxyRedirect:     false,
		PassProxyHeaders:  true,
		RelayChainJSON:    `[]`,
		CustomHeadersJSON: `[]`,
		TagsJSON:          `[]`,
		Revision:          fixture.expectedRevision,
	}}); err != nil {
		return err
	}

	tcpHost, tcpPort, err := splitHostPort(input.tcpBackendAddr)
	if err != nil {
		return err
	}

	relayCertPEM, relayKeyPEM, relayCAPEM, relayCAKeyPEM, relayPin, err := issueRelayLeafSignedByCA(fixture.managedCertDomain)
	if err != nil {
		return err
	}
	if err := store.SaveManagedCertificateMaterial(ctx, fixture.managedCertDomain, storage.ManagedCertificateBundle{
		Domain:  fixture.managedCertDomain,
		CertPEM: relayCertPEM,
		KeyPEM:  relayKeyPEM,
	}); err != nil {
		return err
	}
	expectedMaterialDir := filepath.Join(fixture.dataDir, "managed_certificates", normalizeManagedCertificateHostForFixture(fixture.managedCertDomain))
	if err := verifyPersistedManagedCertificateMaterial(expectedMaterialDir, relayCertPEM, relayKeyPEM); err != nil {
		return err
	}
	fixture.managedCertMaterialDir = expectedMaterialDir
	fixture.relayPinSPKISHA256 = relayPin

	if err := store.SaveManagedCertificateMaterial(ctx, fixture.relayInternalCADomain, storage.ManagedCertificateBundle{
		Domain:  fixture.relayInternalCADomain,
		CertPEM: relayCAPEM,
		KeyPEM:  relayCAKeyPEM,
	}); err != nil {
		return err
	}
	internalCADir := filepath.Join(fixture.dataDir, "managed_certificates", normalizeManagedCertificateHostForFixture(fixture.relayInternalCADomain))
	if err := verifyPersistedManagedCertificateMaterial(internalCADir, relayCAPEM, relayCAKeyPEM); err != nil {
		return err
	}
	fixture.relayInternalCAMaterialDir = internalCADir
	fixture.relayInternalCAPEM = relayCAPEM

	relayChainJSON := `[]`
	if fixture.l4UsesRelay {
		relayChainJSON = mustMarshalJSON([]int{fixture.relayListenerID})
	}
	if input.disableL4Path {
		if err := store.SaveL4Rules(ctx, fixture.localAgentID, nil); err != nil {
			return err
		}
	} else {
		if err := store.SaveL4Rules(ctx, fixture.localAgentID, []storage.L4RuleRow{{
			ID:                201,
			AgentID:           fixture.localAgentID,
			Name:              "fixture-l4",
			Protocol:          "tcp",
			ListenHost:        "127.0.0.1",
			ListenPort:        fixture.l4FrontendPort,
			UpstreamHost:      tcpHost,
			UpstreamPort:      tcpPort,
			BackendsJSON:      mustMarshalJSON([]storage.L4Backend{{Host: tcpHost, Port: tcpPort}}),
			LoadBalancingJSON: `{"strategy":"round_robin"}`,
			TuningJSON:        `{}`,
			RelayChainJSON:    relayChainJSON,
			Enabled:           true,
			TagsJSON:          `[]`,
			Revision:          fixture.expectedRevision,
		}}); err != nil {
			return err
		}
	}

	var certificateID *int
	listenerEnabled := false
	listenerTLSMode := "pin_or_ca"
	pinSetJSON := `[]`
	trustedCAIDsJSON := `[]`
	allowSelfSigned := false
	if fixture.l4UsesRelay {
		listenerEnabled = true
		listenerTLSMode = "pin_and_ca"
		pinSetJSON = mustMarshalJSON([]storage.RelayPin{{
			Type:  "spki_sha256",
			Value: relayPin,
		}})
		trustedCAIDsJSON = mustMarshalJSON([]int{fixture.relayInternalCAID})
		certificateID = intPtr(fixture.relayCertificateID)
	}

	if err := store.SaveRelayListeners(ctx, fixture.localAgentID, []storage.RelayListenerRow{{
		ID:                      fixture.relayListenerID,
		AgentID:                 fixture.localAgentID,
		Name:                    "fixture-relay",
		BindHostsJSON:           `["127.0.0.1"]`,
		ListenHost:              "127.0.0.1",
		ListenPort:              fixture.relayListenerPort,
		PublicHost:              "127.0.0.1",
		PublicPort:              fixture.relayListenerPort,
		Enabled:                 listenerEnabled,
		CertificateID:           certificateID,
		TLSMode:                 listenerTLSMode,
		PinSetJSON:              pinSetJSON,
		TrustedCACertificateIDs: trustedCAIDsJSON,
		AllowSelfSigned:         allowSelfSigned,
		TagsJSON:                `[]`,
		Revision:                fixture.expectedRevision,
	}}); err != nil {
		return err
	}

	if err := store.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{
		{
			ID:              fixture.relayCertificateID,
			Domain:          fixture.managedCertDomain,
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  mustMarshalJSON([]string{fixture.localAgentID}),
			Status:          "active",
			LastIssueAt:     "",
			LastError:       "",
			MaterialHash:    "",
			AgentReports:    `{}`,
			ACMEInfo:        `{"Main_Domain":"fixture-cert.example.test"}`,
			Usage:           "relay_tunnel",
			CertificateType: "uploaded",
			SelfSigned:      false,
			TagsJSON:        `[]`,
			Revision:        fixture.expectedRevision,
		},
		{
			ID:              fixture.relayInternalCAID,
			Domain:          fixture.relayInternalCADomain,
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  mustMarshalJSON([]string{fixture.localAgentID}),
			Status:          "active",
			LastIssueAt:     "",
			LastError:       "",
			MaterialHash:    "",
			AgentReports:    `{}`,
			ACMEInfo:        fmt.Sprintf(`{"Main_Domain":"%s","CA":"fixture-internal-ca"}`, fixture.relayInternalCADomain),
			Usage:           "relay_ca",
			CertificateType: "internal_ca",
			SelfSigned:      true,
			TagsJSON:        `[]`,
			Revision:        fixture.expectedRevision,
		},
		{
			ID:              fixture.managedPolicyCertificateID,
			Domain:          fixture.managedPolicyDomain,
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  mustMarshalJSON([]string{"edge-managed"}),
			Status:          "pending",
			LastIssueAt:     "",
			LastError:       "",
			MaterialHash:    "",
			AgentReports:    `{}`,
			ACMEInfo:        fmt.Sprintf(`{"Main_Domain":"%s","Profile":"dns","CA":"LetsEncrypt","Renew":"2026-05-01T00:00:00Z"}`, fixture.managedPolicyDomain),
			Usage:           "https",
			CertificateType: "acme",
			SelfSigned:      false,
			TagsJSON:        `[]`,
			Revision:        fixture.expectedRevision,
		},
	}); err != nil {
		return err
	}

	return store.SaveLocalRuntimeState(ctx, fixture.localAgentID, storage.RuntimeState{
		NodeID:          fixture.localAgentID,
		CurrentRevision: int64(fixture.seededLocalCurrentRevision),
		Status:          "error",
		Metadata: map[string]string{
			"last_apply_revision": strconv.Itoa(fixture.seededLocalCurrentRevision),
			"last_apply_status":   fixture.seededLocalApplyStatus,
			"last_apply_message":  fixture.seededLocalApplyMessage,
		},
	})
}

func splitHostPort(address string) (string, int, error) {
	host, portRaw, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil {
		return "", 0, err
	}
	return host, port, nil
}

func mustMarshalJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func issueRelayLeafSignedByCA(domain string) (string, string, string, string, string, error) {
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", "", "", "", err
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			CommonName: "fixture-relay-root-ca",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(48 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return "", "", "", "", "", err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return "", "", "", "", "", err
	}

	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", "", "", "", err
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano() + 1),
		Subject: pkix.Name{
			CommonName: domain,
		},
		DNSNames:              []string{domain},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return "", "", "", "", "", err
	}
	leafCert, err := x509.ParseCertificate(leafDER)
	if err != nil {
		return "", "", "", "", "", err
	}
	pinSum := sha256.Sum256(leafCert.RawSubjectPublicKeyInfo)
	pin := base64.StdEncoding.EncodeToString(pinSum[:])

	leafCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	leafKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)})
	caKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(caKey)})
	return string(append(append([]byte(nil), leafCertPEM...), caCertPEM...)), string(leafKeyPEM), string(caCertPEM), string(caKeyPEM), pin, nil
}

func pickFreeTCPPort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer func() {
		_ = ln.Close()
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func resolveFixturePort(t *testing.T, preferred int) int {
	t.Helper()
	if preferred > 0 {
		return preferred
	}
	return pickFreeTCPPort(t)
}

func intPtr(value int) *int {
	copyValue := value
	return &copyValue
}

func verifyPersistedManagedCertificateMaterial(dir string, expectedCertPEM string, expectedKeyPEM string) error {
	certPath := filepath.Join(dir, "cert")
	keyPath := filepath.Join(dir, "key")
	certRaw, err := os.ReadFile(certPath)
	if err != nil {
		return fmt.Errorf("read cert material: %w", err)
	}
	keyRaw, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("read key material: %w", err)
	}
	if string(certRaw) != expectedCertPEM {
		return fmt.Errorf("persisted cert mismatch at %s", certPath)
	}
	if string(keyRaw) != expectedKeyPEM {
		return fmt.Errorf("persisted key mismatch at %s", keyPath)
	}
	return nil
}

func normalizeManagedCertificateHostForFixture(domain string) string {
	normalized := strings.TrimSpace(domain)
	if strings.HasPrefix(normalized, "[") && strings.HasSuffix(normalized, "]") && len(normalized) >= 2 {
		normalized = normalized[1 : len(normalized)-1]
	}
	normalized = strings.ReplaceAll(normalized, "*.", "_wildcard_.")
	replacer := strings.NewReplacer("<", "_", ">", "_", ":", "_", "\"", "_", "/", "_", "\\", "_", "|", "_", "?", "_", "*", "_")
	return replacer.Replace(normalized)
}
