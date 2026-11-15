package rpc

import (
	"context"
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
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	certs "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/certs"
	managed "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/network"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestPublicCertificateTLSUsesOnlyTheRequestedCertificate(t *testing.T) {
	manager := newPublicCertificateManager(t)
	firstLeaf, firstPEM, firstKey := publicCertificatePEM(t, "one.example")
	secondLeaf, secondPEM, secondKey := publicCertificatePEM(t, "two.example")
	applyPublicCertificates(t, manager, []model.ManagedCertificateBundle{
		{ID: 1, Domain: "one.example", CertPEM: firstPEM, KeyPEM: firstKey},
		{ID: 2, Domain: "two.example", CertPEM: secondPEM, KeyPEM: secondKey},
	}, []model.ManagedCertificatePolicy{
		{ID: 1, Domain: "one.example", Enabled: true, Usage: "https", CertificateType: "uploaded", Scope: "domain"},
		{ID: 2, Domain: "two.example", Enabled: true, Usage: "https", CertificateType: "uploaded", Scope: "domain"},
	})
	host := &Host{}
	certs.NewModule(manager).BindPublicCertificateTLS(host.SetPublicCertificateTLS)
	if !host.PublicCertificateTLSBound() {
		t.Fatal("certificate module was not bound")
	}
	owner := newPublicCertificateOwner(t)
	attempt := &hostAttempt{}
	parent, client := acceptPublicCertificateTCP(t, owner)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan sdk.HostRuntimeResponse, 1)
	go func() {
		done <- host.dispatchHostRuntime(ctx, publicCertificateCandidate(owner), owner, attempt, publicCertificateCall(parent, 1))
	}()
	tlsClient := tls.Client(client, &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true, ServerName: "two.example"})
	if err := tlsClient.HandshakeContext(ctx); err != nil {
		t.Fatal(err)
	}
	defer tlsClient.Close()
	response := <-done
	if response.Error != nil {
		t.Fatal(response.Error.Message)
	}
	var terminated sdk.PublicCertificateTLSResponse
	if err := json.Unmarshal(response.Payload, &terminated); err != nil {
		t.Fatal(err)
	}
	assertNoCertificateMaterial(t, response.Payload)
	peer := tlsClient.ConnectionState().PeerCertificates
	if len(peer) != 1 || !peer[0].Equal(firstLeaf) || peer[0].Equal(secondLeaf) {
		t.Fatal("terminated handshake selected another certificate")
	}
	if _, err := tlsClient.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	read := dispatchManaged(t, ctx, host, owner, attempt, managedRequest(sdk.ManagedNetworkRead, terminated.Handle, nil))
	if read.Error != nil || string(mustManagedData(t, read)) != "ping" {
		t.Fatalf("plaintext read = %+v %s", read.Error, read.Payload)
	}
	write := dispatchManaged(t, ctx, host, owner, attempt, managedRequest(sdk.ManagedNetworkWrite, terminated.Handle, []byte("pong")))
	if write.Error != nil {
		t.Fatal(write.Error.Message)
	}
	buf := make([]byte, 4)
	if _, err := tlsClient.Read(buf); err != nil || string(buf) != "pong" {
		t.Fatalf("client read = %q %v", buf, err)
	}
	reserved := dispatchManaged(t, ctx, host, owner, attempt, managedRequest(sdk.ManagedNetworkRead, parent, nil))
	if reserved.Error == nil || reserved.Error.Message != "managed stream is reserved" || containsPrivateMaterial(reserved.Error.Message) {
		t.Fatalf("parent stream = %+v", reserved.Error)
	}
	again := host.dispatchHostRuntime(ctx, publicCertificateCandidate(owner), owner, attempt, publicCertificateCall(parent, 2))
	if again.Error == nil || again.Error.Message != "managed stream is reserved" {
		t.Fatalf("second terminate = %+v", again.Error)
	}
}

func TestPublicCertificateTLSFailureDoesNotSubstituteOrLeak(t *testing.T) {
	manager := newPublicCertificateManager(t)
	_, certPEM, keyPEM := publicCertificatePEM(t, "two.example")
	applyPublicCertificates(t, manager, []model.ManagedCertificateBundle{{ID: 2, Domain: "two.example", CertPEM: certPEM, KeyPEM: keyPEM}}, []model.ManagedCertificatePolicy{{ID: 2, Domain: "two.example", Enabled: true, Usage: "https", CertificateType: "uploaded", Scope: "domain"}})
	host := &Host{}
	certs.NewModule(manager).BindPublicCertificateTLS(host.SetPublicCertificateTLS)
	captured := &publicTLSBridgeCapture{next: host.publicCertificateTerminator()}
	host.SetPublicCertificateTLS(captured)
	owner := newPublicCertificateOwner(t)
	parent, client := acceptPublicCertificateTCP(t, owner)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	missing := terminatePublicCertificate(t, ctx, host, owner, parent, 1)
	if missing.Error == nil || missing.Error.Code != sdk.ErrorUnavailable || missing.Error.Message != "public certificate is unavailable" || containsPrivateMaterial(missing.Error.Message) {
		t.Fatalf("missing certificate = %+v", missing.Error)
	}
	bridge, ok := captured.conn.(*publicTLSBridge)
	if !ok || bridge.ctx.Err() == nil || bridge.closed {
		t.Fatal("unavailable certificate left the bridge context running or closed the accepted connection")
	}
	_ = client.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := client.Write([]byte("still-open")); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buf := make([]byte, 8)
	n, err := client.Read(buf)
	if n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("missing certificate consumed the connection: n=%d %v", n, err)
	}

	parent, client = acceptPublicCertificateTCP(t, owner)
	done := make(chan sdk.HostRuntimeResponse, 1)
	go func() {
		done <- terminatePublicCertificate(t, ctx, host, owner, parent, 2)
	}()
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Write([]byte("not-a-handshake")); err != nil {
		t.Fatal(err)
	}
	failed := <-done
	if failed.Error == nil || failed.Error.Message != "public certificate handshake failed" || containsPrivateMaterial(failed.Error.Message) || len(failed.Payload) != 0 {
		t.Fatalf("handshake failure = %+v %s", failed.Error, failed.Payload)
	}
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	n, err = client.Read(buf)
	if n != 0 || err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("failed handshake left the connection open: n=%d %v", n, err)
	}

	deniedHost := &Host{}
	certs.NewModule(manager).BindPublicCertificateTLS(deniedHost.SetPublicCertificateTLS)
	parent, client = acceptPublicCertificateTCP(t, owner)
	denied := deniedHost.dispatchHostRuntime(ctx, HostCandidate{InstanceID: owner.Binding().InstanceID, Generation: owner.Binding().Generation, Scopes: []string{sdk.PermissionManagedNetworkListen}, Grants: []model.PluginGrantProjection{{Name: sdk.PermissionManagedNetworkListen}}}, owner, &hostAttempt{}, publicCertificateCall(parent, 2))
	if denied.Error == nil || denied.Error.Code != sdk.ErrorPermissionDenied || containsPrivateMaterial(denied.Error.Message) {
		t.Fatalf("permission = %+v", denied.Error)
	}
	_ = client.Close()
}

type publicTLSBridgeCapture struct {
	next interface {
		HandshakeServer(context.Context, int, net.Conn) (net.Conn, error)
	}
	conn net.Conn
}

func (c *publicTLSBridgeCapture) HandshakeServer(ctx context.Context, certificateID int, raw net.Conn) (net.Conn, error) {
	c.conn = raw
	return c.next.HandshakeServer(ctx, certificateID, raw)
}

func newPublicCertificateManager(t *testing.T) *certs.Manager {
	t.Helper()
	manager, err := certs.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func applyPublicCertificates(t *testing.T, manager *certs.Manager, bundles []model.ManagedCertificateBundle, policies []model.ManagedCertificatePolicy) {
	t.Helper()
	if err := manager.Apply(context.Background(), bundles, policies); err != nil {
		t.Fatal(err)
	}
}

func publicCertificatePEM(t *testing.T, domain string) (*x509.Certificate, string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: domain}, DNSNames: []string{domain},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return leaf, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

func newPublicCertificateOwner(t *testing.T) *managed.Owner {
	t.Helper()
	manager := managed.NewManager()
	t.Cleanup(func() { _ = manager.Close() })
	owner, err := manager.Stage(managed.Authority{
		InstanceID: "instance-1", Generation: "generation-1",
		Grants:        []model.PluginGrantProjection{{Name: sdk.PermissionManagedNetworkListen}, {Name: sdk.PermissionPublicCertificateTerminate}},
		AdmitProtocol: func(context.Context, string, sdk.ManagedSourceMetadata) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	owner.Activate()
	t.Cleanup(func() { _ = owner.Close() })
	return owner
}

func acceptPublicCertificateTCP(t *testing.T, owner *managed.Owner) (sdk.ManagedNetworkHandle, net.Conn) {
	t.Helper()
	var last error
	for range 5 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		port := listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()
		binding := owner.Binding()
		opened, err := owner.Handle(context.Background(), sdk.ManagedNetworkRequest{
			Action: sdk.ManagedNetworkListen, Binding: binding, RequestID: publicTLSRequestID(),
			Endpoint: &sdk.ManagedNetworkEndpoint{Host: "127.0.0.1", Port: port}, Protocol: "tcp",
			MaxFlows: 4, IdleMS: 30000,
		})
		if err != nil {
			last = err
			continue
		}
		client, err := net.Dial("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		accepted, err := owner.Handle(context.Background(), sdk.ManagedNetworkRequest{
			Action: sdk.ManagedNetworkAccept, Binding: binding, RequestID: publicTLSRequestID(),
			Handle: opened.Handle, WaitMS: 2000,
		})
		if err != nil {
			_ = client.Close()
			t.Fatal(err)
		}
		return *accepted.Handle, client
	}
	t.Fatal(last)
	return sdk.ManagedNetworkHandle{}, nil
}

func terminatePublicCertificate(t *testing.T, ctx context.Context, host *Host, owner *managed.Owner, parent sdk.ManagedNetworkHandle, certificateID int) sdk.HostRuntimeResponse {
	t.Helper()
	return host.dispatchHostRuntime(ctx, publicCertificateCandidate(owner), owner, &hostAttempt{}, publicCertificateCall(parent, certificateID))
}

func publicCertificateCandidate(owner *managed.Owner) HostCandidate {
	return HostCandidate{
		InstanceID: owner.Binding().InstanceID, Generation: owner.Binding().Generation,
		Scopes: []string{sdk.PermissionManagedNetworkListen, sdk.PermissionPublicCertificateTerminate},
		Grants: []model.PluginGrantProjection{{Name: sdk.PermissionManagedNetworkListen}, {Name: sdk.PermissionPublicCertificateTerminate}},
	}
}

func publicCertificateCall(parent sdk.ManagedNetworkHandle, certificateID int) sdk.HostRuntimeCall {
	payload, _ := json.Marshal(sdk.PublicCertificateTLSRequest{CertificateID: certificateID, Handle: parent, RequestID: "request-public-cert"})
	return sdk.HostRuntimeCall{Operation: sdk.HostRuntimePublicCertificateTLS, OperationID: "request-public-cert", Payload: payload}
}

func dispatchManaged(t *testing.T, ctx context.Context, host *Host, owner *managed.Owner, attempt *hostAttempt, request sdk.ManagedNetworkRequest) sdk.HostRuntimeResponse {
	t.Helper()
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return host.dispatchHostRuntime(ctx, publicCertificateCandidate(owner), owner, attempt, sdk.HostRuntimeCall{Operation: sdk.HostRuntimeManagedNetwork, OperationID: request.RequestID, Payload: payload})
}

func managedRequest(action string, handle sdk.ManagedNetworkHandle, data []byte) sdk.ManagedNetworkRequest {
	request := sdk.ManagedNetworkRequest{Action: action, Binding: handle.Binding, RequestID: publicTLSRequestID(), Handle: &handle, WaitMS: 2000}
	switch action {
	case sdk.ManagedNetworkRead:
		request.MaxBytes = 32
	case sdk.ManagedNetworkWrite:
		request.Data = data
	}
	return request
}

func mustManagedData(t *testing.T, response sdk.HostRuntimeResponse) []byte {
	t.Helper()
	var decoded sdk.ManagedNetworkResponse
	if err := json.Unmarshal(response.Payload, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.Data
}

func assertNoCertificateMaterial(t *testing.T, payload []byte) {
	t.Helper()
	if containsPrivateMaterial(string(payload)) {
		t.Fatalf("response carried certificate material: %s", payload)
	}
}

func containsPrivateMaterial(value string) bool {
	upper := strings.ToUpper(value)
	return strings.Contains(upper, "PRIVATE KEY") || strings.Contains(value, "BEGIN ")
}
