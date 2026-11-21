package certs

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestPublicCertificateHandshakeUsesRequestedServerCertificate(t *testing.T) {
	manager := mustNewManager(t, t.TempDir())
	t.Cleanup(func() { _ = manager.Close() })
	first := mustCreateTLSMaterial(t, certificateSpec{commonName: "one.example"})
	second := mustCreateTLSMaterial(t, certificateSpec{commonName: "two.example"})
	if err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{
		uploadedBundle(1, "one.example", first),
		uploadedBundle(2, "two.example", second),
	}, []model.ManagedCertificatePolicy{
		uploadedPolicy(1, "one.example", "uploaded", "https", true),
		uploadedPolicy(2, "two.example", "uploaded", "https", true),
	}); err != nil {
		t.Fatal(err)
	}

	app, client := handshakePublicCertificate(t, func(raw net.Conn) (net.Conn, error) {
		return NewModule(manager).HandshakeServer(context.Background(), 1, raw)
	}, "two.example")
	if _, isTLS := app.(*tls.Conn); isTLS {
		t.Fatal("handshake returned the private-key connection")
	}
	state := client.ConnectionState()
	if len(state.PeerCertificates) != 1 || !state.PeerCertificates[0].Equal(first.Leaf) {
		t.Fatal("handshake selected a certificate other than the requested id")
	}
	exchangePublicCertificate(t, app, client, "ping", "pong")
	_ = app.Close()
	_ = client.Close()

	renewed := mustCreateTLSMaterial(t, certificateSpec{commonName: "one.example"})
	if err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{
		uploadedBundle(1, "one.example", renewed),
		uploadedBundle(2, "two.example", second),
	}, []model.ManagedCertificatePolicy{
		uploadedPolicy(1, "one.example", "uploaded", "https", true),
		uploadedPolicy(2, "two.example", "uploaded", "https", true),
	}); err != nil {
		t.Fatal(err)
	}
	renewedApp, renewedClient := handshakePublicCertificate(t, func(raw net.Conn) (net.Conn, error) {
		return manager.HandshakeServer(context.Background(), 1, raw)
	}, "two.example")
	defer renewedApp.Close()
	defer renewedClient.Close()
	renewedState := renewedClient.ConnectionState()
	if len(renewedState.PeerCertificates) != 1 || !renewedState.PeerCertificates[0].Equal(renewed.Leaf) || renewedState.PeerCertificates[0].Equal(first.Leaf) {
		t.Fatal("renewed handshake did not present the current certificate")
	}
}

func TestPreparedPublicCertificateDoesNotFallBack(t *testing.T) {
	manager := mustNewManager(t, t.TempDir())
	t.Cleanup(func() { _ = manager.Close() })
	active := mustCreateTLSMaterial(t, certificateSpec{commonName: "active.example"})
	preparedMaterial := mustCreateTLSMaterial(t, certificateSpec{commonName: "prepared.example"})
	if err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{uploadedBundle(1, "active.example", active)}, []model.ManagedCertificatePolicy{
		uploadedPolicy(1, "active.example", "uploaded", "https", true),
	}); err != nil {
		t.Fatal(err)
	}
	state, err := manager.prepareActiveState(context.Background(), []model.ManagedCertificateBundle{uploadedBundle(4, "prepared.example", preparedMaterial)}, []model.ManagedCertificatePolicy{
		uploadedPolicy(4, "prepared.example", "uploaded", "https", true),
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared := preparedTLSMaterial{manager: manager, state: state}
	app, client := handshakePublicCertificate(t, func(raw net.Conn) (net.Conn, error) {
		return prepared.HandshakeServer(context.Background(), 4, raw)
	}, "active.example")
	defer app.Close()
	defer client.Close()
	peer := client.ConnectionState().PeerCertificates
	if len(peer) != 1 || !peer[0].Equal(preparedMaterial.Leaf) || peer[0].Equal(active.Leaf) {
		t.Fatal("prepared handshake fell back to another certificate")
	}
	serverRaw, clientRaw := net.Pipe()
	defer serverRaw.Close()
	defer clientRaw.Close()
	_, err = prepared.HandshakeServer(context.Background(), 1, serverRaw)
	if !errors.Is(err, ErrPublicCertificateUnavailable) {
		t.Fatalf("prepared missing id = %v", err)
	}
}

func TestPublicCertificateRefusesInternalDisabledAndMaterialFree(t *testing.T) {
	manager := mustNewManager(t, t.TempDir())
	t.Cleanup(func() { _ = manager.Close() })
	internal := mustCreateTLSMaterial(t, certificateSpec{commonName: "internal.example"})
	public := mustCreateTLSMaterial(t, certificateSpec{commonName: "public.example"})
	relay := mustCreateTLSMaterial(t, certificateSpec{commonName: internalRelayCADomain})
	ca := mustCreateTLSMaterial(t, certificateSpec{commonName: "ca.example", isCA: true})
	if err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{
		uploadedBundle(3, "internal.example", internal),
		uploadedBundle(5, "public.example", public),
		uploadedBundle(6, internalRelayCADomain, relay),
		uploadedBundle(7, "ca.example", ca),
	}, []model.ManagedCertificatePolicy{
		uploadedPolicy(3, "internal.example", "internal_ca", "https", true),
		uploadedPolicy(5, "public.example", "uploaded", "https", false),
		uploadedPolicy(6, internalRelayCADomain, "uploaded", "https", true),
		uploadedPolicy(7, "ca.example", "uploaded", "https", true),
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{3, 5, 6, 7, 99} {
		serverRaw, clientRaw := net.Pipe()
		_, err := manager.HandshakeServer(context.Background(), id, serverRaw)
		if !errors.Is(err, ErrPublicCertificateUnavailable) || strings.Contains(err.Error(), "PRIVATE") || strings.Contains(err.Error(), "BEGIN") {
			serverRaw.Close()
			clientRaw.Close()
			t.Fatalf("certificate %d = %v", id, err)
		}
		done := make(chan error, 1)
		go func() {
			_, writeErr := clientRaw.Write([]byte("open"))
			done <- writeErr
		}()
		buf := make([]byte, 4)
		n, readErr := serverRaw.Read(buf)
		serverRaw.Close()
		clientRaw.Close()
		if readErr != nil || string(buf[:n]) != "open" || <-done != nil {
			t.Fatalf("certificate %d closed the accepted connection: %q %v", id, buf[:n], readErr)
		}
	}
}

func uploadedPolicy(id int, domain, certType, usage string, enabled bool) model.ManagedCertificatePolicy {
	return model.ManagedCertificatePolicy{ID: id, Domain: domain, Enabled: enabled, Usage: usage, CertificateType: certType, Scope: "domain"}
}

func uploadedBundle(id int, domain string, material tlsMaterial) model.ManagedCertificateBundle {
	return model.ManagedCertificateBundle{ID: id, Domain: domain, CertPEM: string(material.CertPEM), KeyPEM: string(material.KeyPEM)}
}

func handshakePublicCertificate(t *testing.T, serve func(net.Conn) (net.Conn, error), serverName string) (net.Conn, *tls.Conn) {
	t.Helper()
	serverRaw, clientRaw := net.Pipe()
	errCh := make(chan error, 1)
	appCh := make(chan net.Conn, 1)
	go func() {
		app, err := serve(serverRaw)
		if err != nil {
			errCh <- err
			return
		}
		appCh <- app
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := tls.Client(clientRaw, &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true, ServerName: serverName})
	if err := client.HandshakeContext(ctx); err != nil {
		_ = serverRaw.Close()
		_ = clientRaw.Close()
		t.Fatalf("client handshake: %v", err)
	}
	select {
	case err := <-errCh:
		_ = client.Close()
		t.Fatalf("server handshake: %v", err)
	case app := <-appCh:
		return app, client
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	return nil, nil
}

func exchangePublicCertificate(t *testing.T, server net.Conn, client net.Conn, fromClient, fromServer string) {
	t.Helper()
	// net.Pipe writes block until the peer reads, so each direction has to run together.
	write := func(conn net.Conn, payload string) <-chan error {
		done := make(chan error, 1)
		go func() {
			_, err := io.WriteString(conn, payload)
			done <- err
		}()
		return done
	}
	clientWrite := write(client, fromClient)
	buf := make([]byte, len(fromClient))
	if _, err := io.ReadFull(server, buf); err != nil || string(buf) != fromClient {
		t.Fatalf("server read = %q %v", buf, err)
	}
	if err := <-clientWrite; err != nil {
		t.Fatal(err)
	}
	serverWrite := write(server, fromServer)
	buf = make([]byte, len(fromServer))
	if _, err := io.ReadFull(client, buf); err != nil || string(buf) != fromServer {
		t.Fatalf("client read = %q %v", buf, err)
	}
	if err := <-serverWrite; err != nil {
		t.Fatal(err)
	}
}
