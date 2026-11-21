//go:build !fast

package storage

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"testing"
	"time"
)

// Synthetic, self-signed leaf and CA exercise the same uploaded material path.
func listenerTestBundle(t *testing.T, domain string, ca bool, change ...func(*x509.Certificate)) ManagedCertificateBundle {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: domain}, DNSNames: []string{domain}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true, IsCA: ca, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	for _, f := range change {
		f(template)
	}
	if ca {
		template.KeyUsage |= x509.KeyUsageCertSign
	}
	cert, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return ManagedCertificateBundle{Domain: domain, CertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert})), KeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))}
}

func TestListenerCertificateAudienceLifecycle(t *testing.T) {
	s, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := t.Context()
	instance := pluginTargetNormalizationInstance("ss", ListenerCertificatePlugin, time.Now())
	instance.IncarnationID = "inc-1"
	for _, row := range []any{
		&InstalledPluginRow{PluginID: ListenerCertificatePlugin, DesiredLifecycle: "enabled", ActivePackageIdentity: "ss-package", ActivePackageDigest: "digest"},
		&PluginPackageRow{Identity: "ss-package", Digest: "digest", PluginID: ListenerCertificatePlugin, Version: "1.0", ManifestJSON: `{"id":"shadowsocks-server","version":"1.0"}`},
		&PluginGrantRow{ID: "read", GrantKey: "read", PluginID: ListenerCertificatePlugin, PackageIdentity: "ss-package", PackageDigest: "digest", Permission: "certificate.public.read"},
		&PluginGrantRow{ID: "tls", GrantKey: "tls", PluginID: ListenerCertificatePlugin, PackageIdentity: "ss-package", PackageDigest: "digest", Permission: "certificate.public.terminate"},
		&instance, &AgentRow{ID: "edge-a"}, &AgentRow{ID: "edge-b"},
		&ResourceBindingRow{ID: "agent-a", ResourceKind: "agent", ResourceID: "edge-a", ResourceGroupID: "default"},
		&ResourceBindingRow{ID: "agent-b", ResourceKind: "agent", ResourceID: "edge-b", ResourceGroupID: "other"},
		&ResourceBindingRow{ID: "cert7", ResourceKind: "certificate", ResourceID: "7", ResourceGroupID: "default"},
		&ResourceBindingRow{ID: "cert8", ResourceKind: "certificate", ResourceID: "8", ResourceGroupID: "default"},
		&ResourceBindingRow{ID: "cert9", ResourceKind: "certificate", ResourceID: "9", ResourceGroupID: "other"},
	} {
		if err := s.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	cert := ManagedCertificateRow{ID: 7, Domain: "selected.example", CertificateType: "uploaded", Enabled: true, SelfSigned: true, Usage: "https"}
	ca := cert
	ca.ID = 8
	ca.Domain = "ca.example"
	foreign := cert
	foreign.ID = 9
	foreign.Domain = "other.example"
	if err := s.SaveManagedCertificates(ctx, []ManagedCertificateRow{cert, ca, foreign}); err != nil {
		t.Fatal(err)
	}
	bundle := listenerTestBundle(t, cert.Domain, false)
	for _, b := range []ManagedCertificateBundle{bundle, listenerTestBundle(t, ca.Domain, true), listenerTestBundle(t, foreign.Domain, false)} {
		if err := s.SaveManagedCertificateMaterial(ctx, b.Domain, b); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := s.ListenerCertificateCatalog(ctx, "ss", "edge-a", 0)
	if err != nil || len(catalog) != 1 || catalog[0].ID != cert.ID {
		t.Fatalf("catalog %v, err %v", catalog, err)
	}
	if _, err := s.ListenerCertificateCatalog(ctx, "ss", "edge-b", 0); err == nil {
		t.Fatal("cross-group agent accepted")
	}
	checkSnapshot := func(agent string, want *ManagedCertificateBundle) {
		t.Helper()
		snapshot, err := s.LoadAgentSnapshot(ctx, agent, AgentSnapshotInput{})
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range snapshot.Certificates {
			if b.ID == 8 || b.ID == 9 {
				t.Fatal("unselected certificate dispatched")
			}
			if b.ID == cert.ID {
				if want == nil || b.CertPEM != want.CertPEM || b.KeyPEM != want.KeyPEM {
					t.Fatal("unexpected selected material")
				}
				return
			}
		}
		if want != nil {
			t.Fatal("selected material missing")
		}
	}
	checkSnapshot("edge-a", nil)
	ref := ListenerCertificateRef{ID: "listen-1", AgentID: "edge-a", CertificateID: cert.ID, Domain: cert.Domain}
	second := ref
	second.ID = "listen-2"
	dir := func(permit *ListenerCertificateRef, revision int64, refs ...ListenerCertificateRef) error {
		type entry struct {
			ListenerCertificateRef
			Protocol string `json:"protocol"`
		}
		listeners := make([]entry, 0, len(refs))
		for _, ref := range refs {
			listeners = append(listeners, entry{ref, "anytls"})
		}
		value, err := json.Marshal(listeners)
		if err != nil {
			t.Fatal(err)
		}
		row := PluginRuntimeStateRow{InstanceID: "ss", ResourceGroupID: "default", PluginID: ListenerCertificatePlugin, Key: "listens", Value: value}
		if permit == nil {
			return s.PutPluginRuntimeState(ctx, row)
		}
		return s.StorePluginListeners(ctx, row, &ListenerCertificateBinding{ListenerCertificateRef: *permit}, revision)
	}
	if err := dir(nil, 0, ref); err != nil {
		t.Fatal(err)
	}
	checkSnapshot("edge-a", nil)
	bad := ref
	bad.CertificateID = 8
	bad.Domain = ca.Domain
	if err := dir(&bad, 1, bad); err == nil {
		t.Fatal("CA key accepted")
	}
	bad = ref
	bad.CertificateID = 9
	bad.Domain = foreign.Domain
	if err := dir(&bad, 1, bad); err == nil {
		t.Fatal("cross-group key accepted")
	}
	if err := dir(&ref, 2, ref); err != nil {
		t.Fatal(err)
	}
	checkSnapshot("edge-a", &bundle)
	snapshot, err := s.LoadAgentSnapshot(ctx, "edge-a", AgentSnapshotInput{})
	if err != nil {
		t.Fatal(err)
	}
	var selected ManagedCertificateBundle
	for _, b := range snapshot.Certificates {
		if b.ID == cert.ID {
			selected = b
			break
		}
	}
	pair, err := tls.X509KeyPair([]byte(selected.CertPEM), []byte(selected.KeyPEM))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(bundle.CertPEM)) {
		t.Fatal("failed to trust synthetic leaf")
	}
	left, right := net.Pipe()
	deadline := time.Now().Add(3 * time.Second)
	left.SetDeadline(deadline)
	right.SetDeadline(deadline)
	served := make(chan error, 1)
	go func() {
		served <- tls.Server(left, &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}).HandshakeContext(ctx)
	}()
	clientErr := tls.Client(right, &tls.Config{RootCAs: roots, ServerName: cert.Domain, MinVersion: tls.VersionTLS12}).HandshakeContext(ctx)
	left.Close()
	right.Close()
	if serverErr := <-served; serverErr != nil || clientErr != nil {
		t.Fatal("trusted TLS handshake for selected self-signed leaf", serverErr, clientErr)
	}
	if err := s.BindResource(ctx, ResourceBindingRow{ResourceKind: "agent", ResourceID: "edge-a", ResourceGroupID: "other"}); !errors.Is(err, ErrListenerCertificateOwnership) {
		t.Fatal("bound agent moved across groups", err)
	}
	if err := s.UnbindResource(ctx, "certificate", "7"); err != nil {
		t.Fatal(err)
	}
	checkSnapshot("edge-a", &bundle) // explicit default and fallback default have identical access
	if err := s.BindResource(ctx, ResourceBindingRow{ID: "cert7", ResourceKind: "certificate", ResourceID: "7", ResourceGroupID: "default"}); err != nil {
		t.Fatal(err)
	}
	checkSnapshot("edge-b", nil)
	if ready, found, err := s.ListenerCertificateReady(ctx, "ss", ref.ID); err != nil || !found || ready {
		t.Fatalf("readiness before apply %t %t %v", ready, found, err)
	}
	if err := dir(nil, 0); err == nil {
		t.Fatal("ungranted removal accepted")
	}
	if err := dir(&second, 3, ref, second); err != nil {
		t.Fatal(err)
	}
	rotated := listenerTestBundle(t, cert.Domain, false)
	if err := s.SaveManagedCertificateMaterial(ctx, cert.Domain, rotated); err != nil {
		t.Fatal(err)
	}
	checkSnapshot("edge-a", &rotated)
	targets, err := s.ListenerCertificateAgentIDs(ctx, cert.ID, cert.Domain)
	if err != nil || len(targets) != 1 || targets[0] != "edge-a" {
		t.Fatalf("renewal targets %v, err %v", targets, err)
	}
	if err := dir(&ref, 4, second); err != nil {
		t.Fatal(err)
	}
	checkSnapshot("edge-a", &rotated)
	if err := dir(&second, 5); err != nil {
		t.Fatal(err)
	}
	checkSnapshot("edge-a", nil)
	if err := dir(&ref, 6, ref); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&PluginGrantRow{}).Where("id = ?", "tls").Update("resource_selector", "edge-b").Error; err != nil {
		t.Fatal(err)
	}
	checkSnapshot("edge-a", nil)
	if err := s.db.Model(&PluginGrantRow{}).Where("id = ?", "tls").Update("resource_selector", "").Error; err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&PluginInstanceRow{}).Where("id = ?", "ss").Update("desired_enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	checkSnapshot("edge-a", nil)
	if err := s.db.Model(&PluginInstanceRow{}).Where("id = ?", "ss").Update("desired_enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	checkSnapshot("edge-a", &rotated)
	if err := s.db.Model(&PluginInstanceRow{}).Where("id = ?", "ss").Update("incarnation_id", "inc-2").Error; err != nil {
		t.Fatal(err)
	}
	checkSnapshot("edge-a", nil)
	// Seed an existing non-default tenant with its own agent, certificate and listener.
	for _, model := range []any{&PluginInstanceRow{}, &PluginRuntimeStateRow{}} {
		if err := s.db.Model(model).Where("plugin_id = ?", ListenerCertificatePlugin).Update("resource_group_id", "tenant").Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := s.db.Model(&PluginInstanceRow{}).Where("id = ?", "ss").Update("incarnation_id", "inc-1").Error; err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&ResourceBindingRow{}).Where("id IN ?", []string{"agent-a", "cert7"}).Update("resource_group_id", "tenant").Error; err != nil {
		t.Fatal(err)
	}
	checkSnapshot("edge-a", &rotated)
	for _, target := range [][2]string{{"certificate", "7"}, {"agent", "edge-a"}} {
		if err := s.UnbindResource(ctx, target[0], target[1]); !errors.Is(err, ErrListenerCertificateOwnership) {
			t.Fatal("non-default unbind", err)
		}
	}
	if err := s.BindResource(ctx, ResourceBindingRow{ResourceKind: "certificate", ResourceID: "7", ResourceGroupID: "default"}); err == nil {
		t.Fatal("active certificate moved across groups")
	}
	checkSnapshot("edge-a", &rotated)
	if err := s.SaveManagedCertificates(ctx, []ManagedCertificateRow{ca, foreign}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveManagedCertificates(ctx, []ManagedCertificateRow{cert, ca, foreign}); err != nil {
		t.Fatal(err)
	}
	checkSnapshot("edge-a", nil)
	if ready, found, err := s.ListenerCertificateReady(ctx, "ss", ref.ID); err != nil || ready || !found {
		t.Fatal("retired listener reported legacy/ready", err)
	}
}

func TestListenerCertificateServerNamesAndMaterial(t *testing.T) {
	wildcard := listenerTestBundle(t, "*.example.test", false)
	if !ListenerServerLeafMaterial(wildcard) {
		t.Fatal("valid wildcard SAN rejected")
	}
	narrowed := wildcard
	narrowed.Domain = "api.example.test"
	if !ListenerServerLeafMaterial(narrowed) {
		t.Fatal("specific name covered by wildcard rejected")
	}
	narrowed.Domain = "too.deep.example.test"
	if ListenerServerLeafMaterial(narrowed) {
		t.Fatal("multi-label wildcard accepted")
	}
	literal := listenerTestBundle(t, "anytls.example.test", false)
	literal.Domain = "*.example.test"
	if ListenerServerLeafMaterial(literal) {
		t.Fatal("literal SAN accepted for a wildcard")
	}
	ip := listenerTestBundle(t, "192.0.2.1", false, func(c *x509.Certificate) { c.IPAddresses = []net.IP{net.ParseIP("192.0.2.1")}; c.DNSNames = nil })
	if !ListenerServerLeafMaterial(ip) {
		t.Fatal("IP SAN rejected")
	}
	expired := listenerTestBundle(t, "old.example", false, func(c *x509.Certificate) { c.NotAfter = time.Now().Add(-time.Minute) })
	if ListenerServerLeafMaterial(expired) {
		t.Fatal("expired certificate accepted")
	}
	extra := wildcard
	extra.KeyPEM += ip.KeyPEM
	if ListenerServerLeafMaterial(extra) {
		t.Fatal("unrelated trailing private key accepted")
	}
}
