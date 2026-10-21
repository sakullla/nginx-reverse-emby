//go:build !integration

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
	"math/big"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestTunnelSecurityFenceModeKeepsListenersForUnrelatedRevocation(t *testing.T) {
	base := tunnelSecurityStateForFenceTest(t, nil, nil)
	revoked := base
	revoked.Snapshot.SecurityRevision++
	revoked.Snapshot.RevokedIdentityIDs = []string{"deleted-agent"}
	if got := tunnelSecurityFenceMode(base, revoked); got != tunnelFenceSessions {
		t.Fatalf("revocation fence mode = %v, want sessions", got)
	}

	distrusted := base
	distrusted.Snapshot.SecurityRevision++
	distrusted.Snapshot.TrustRoots = append([]model.PKITrustRoot(nil), base.Snapshot.TrustRoots...)
	distrusted.Snapshot.TrustRoots[0].Status = "retired"
	if got := tunnelSecurityFenceMode(base, distrusted); got != tunnelFenceListeners {
		t.Fatalf("trust fence mode = %v, want listeners", got)
	}
	if got := tunnelSecurityFenceMode(base, base); got != tunnelFenceNone {
		t.Fatalf("unchanged fence mode = %v, want none", got)
	}
}

func TestUnrelatedPKIRevocationKeepsRelayListenerBound(t *testing.T) {
	if testing.Short() {
		t.Skip("real listener and certificate lifecycle")
	}
	provider := &fenceSecurityProvider{security: tunnelSecurityStateForFenceTest(t, nil, nil)}
	provider.credential = TunnelCredentialMetadata{
		Generation: "agent-generation", CredentialFingerprintSHA256: strings.Repeat("ab", 32),
		IdentityID: "local-agent", CertificateID: "local-certificate", Purpose: model.PKICertificatePurposeClient,
		AuthorityID: "fence-authority", CAGeneration: 1, PKIDomainID: provider.security.Snapshot.PKIDomainID,
		PKIEpoch: 1, SecurityRevision: provider.security.Snapshot.SecurityRevision, AgentID: "local-agent",
	}
	material := newFakeTLSMaterialProvider()
	listener, _ := newRelayEndpoint(t, material, 1, "fence-relay", "pin_only", true, false)
	server, err := Start(context.Background(), []Listener{listener}, material)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	server.trackTunnelListener(Listener{ID: listener.ID, TLSMode: TLSModePKIMTLS})

	relayModule := NewModule(Config{AgentID: "local-agent"})
	relayModule.SetTunnelCredentialProvider(provider)
	relayModule.trackRuntime(server)
	relayModule.mu.Lock()
	relayModule.runtime = server
	relayModule.mu.Unlock()

	if err := relayModule.ReconcileTunnelSecurity(context.Background()); err != nil {
		t.Fatal(err)
	}
	provider.security.Snapshot.SecurityRevision++
	provider.security.Snapshot.RevokedIdentityIDs = []string{"deleted-agent"}
	if err := relayModule.ReconcileTunnelSecurity(context.Background()); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(listener.ListenHost, strconv.Itoa(listener.ListenPort)), 2*time.Second)
	if err != nil {
		t.Fatalf("listener closed after unrelated revocation: %v", err)
	}
	_ = conn.Close()

	provider.security.Snapshot.SecurityRevision++
	provider.security.Snapshot.TrustRoots[0].Status = "retired"
	if err := relayModule.ReconcileTunnelSecurity(context.Background()); err != nil && !strings.Contains(err.Error(), "usable trust root") {
		t.Fatal(err)
	}
	if _, err := net.DialTimeout("tcp", net.JoinHostPort(listener.ListenHost, strconv.Itoa(listener.ListenPort)), 2*time.Second); err == nil {
		t.Fatal("listener stayed open after the trust root was retired")
	}
}

func TestQUICSecurityFenceRejectsPendingAdmission(t *testing.T) {
	if testing.Short() {
		t.Skip("real QUIC listener and certificate lifecycle")
	}
	for _, onPacket := range []bool{false, true} {
		t.Run(strconv.FormatBool(onPacket), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			provider := newFakeTLSMaterialProvider()
			listener, hop := newRelayEndpoint(t, provider, 1, "quic-fence", "pin_only", true, false)
			server := newRelayServer(ctx, provider, StartOptions{})
			t.Cleanup(func() { _ = server.Close() })
			var handle *quicListenerHandle
			var err error
			if onPacket {
				packet, listenErr := net.ListenPacket("udp", "127.0.0.1:0")
				if listenErr != nil {
					t.Fatal(listenErr)
				}
				t.Cleanup(func() { _ = packet.Close() })
				handle, err = server.startQUICListenerOnPacket(ctx, provider, listener, packet, newRelayQUICClassifier())
			} else {
				handle, err = server.startQUICListener(ctx, provider, listener, "127.0.0.1:0")
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = handle.Close() })
			address := handle.listener.Addr().String()
			config, err := clientQUICTLSConfig(ctx, provider, listener, address, hop.ServerName)
			if err != nil {
				t.Fatal(err)
			}
			accept := func() *quic.Conn {
				t.Helper()
				client, err := quic.DialAddr(ctx, address, config, newRelayQUICConfig())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = client.CloseWithError(0, "test done") })
				conn, err := handle.listener.Accept(ctx)
				if err != nil {
					t.Fatal(err)
				}
				return conn
			}
			active := accept()
			if !server.trackQUICConn(active) {
				t.Fatal("initial handshake was rejected")
			}
			// Hold a completed handshake immediately before admission. This is
			// the same gap as a connection queued inside Listener.Accept.
			pending := accept()
			relayModule := NewModule(Config{})
			relayModule.trackRuntime(server)
			if err := relayModule.refreshTunnelSessions(); err != nil {
				t.Fatal(err)
			}
			if server.trackQUICConn(pending) {
				t.Fatal("handshake from before the security fence was admitted")
			}
			for _, conn := range []*quic.Conn{active, pending} {
				select {
				case <-conn.Context().Done():
				case <-ctx.Done():
					t.Fatal("security fence did not close old connection")
				}
			}
			fresh := accept()
			if !server.trackQUICConn(fresh) {
				t.Fatal("fresh handshake on the preserved listener was rejected")
			}
		})
	}
}

type fenceSecurityProvider struct {
	security   TunnelSecurityState
	credential TunnelCredentialMetadata
}

func (p *fenceSecurityProvider) InstallTunnelCertificate(context.Context, string, *tls.Config) (TunnelCredentialMetadata, error) {
	return p.credential, nil
}

func (p *fenceSecurityProvider) LoadTunnelCredential(context.Context, string) (TunnelCredentialMetadata, error) {
	return p.credential, nil
}

func (p *fenceSecurityProvider) LoadTunnelSecurity(context.Context) (TunnelSecurityState, error) {
	return p.security, nil
}

func tunnelSecurityStateForFenceTest(t *testing.T, revokedIdentities, revokedSerials []string) TunnelSecurityState {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fence-authority"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return TunnelSecurityState{
		Hash: "fence-security",
		Snapshot: model.PKISecuritySnapshot{
			PKIDomainID: "fence.test", PKIEpoch: 1, SecurityRevision: 4, Full: true,
			RevokedIdentityIDs: append([]string(nil), revokedIdentities...),
			RevokedSerials:     append([]string(nil), revokedSerials...),
			TrustRoots: []model.PKITrustRoot{{
				AuthorityID: "fence-authority", Generation: 1, Status: "active",
				CertificatePEM:    string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
				FingerprintSHA256: hex.EncodeToString(sum[:]),
				NotBefore:         parsed.NotBefore, NotAfter: parsed.NotAfter,
			}},
		},
	}
}
