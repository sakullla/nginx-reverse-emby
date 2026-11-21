package certs

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

const internalRelayCADomain = "__relay-ca.internal"

// ErrPublicCertificateUnavailable means the requested certificate cannot be
// used. The accepted connection is left untouched so the caller can fail
// without substituting another certificate.
var ErrPublicCertificateUnavailable = &publicCertificateError{kind: "unavailable", message: "public certificate is unavailable"}

// ErrPublicCertificateHandshake means the handshake with the requested
// certificate failed. The connection is closed and no other certificate is tried.
var ErrPublicCertificateHandshake = &publicCertificateError{kind: "handshake", message: "public certificate handshake failed"}

type publicCertificateError struct {
	kind    string
	message string
}

func (e *publicCertificateError) Error() string {
	if e == nil || e.message == "" {
		return ErrPublicCertificateUnavailable.message
	}
	return e.message
}

func (e *publicCertificateError) PublicCertificateFailure() string {
	if e == nil || e.kind == "" {
		return "unavailable"
	}
	return e.kind
}

// BindPublicCertificateTLS attaches this module to a host. The parameter is an
// unnamed interface so a setter method value has the same function type.
func (m *Module) BindPublicCertificateTLS(bind func(interface {
	HandshakeServer(context.Context, int, net.Conn) (net.Conn, error)
})) {
	if m == nil || bind == nil || m.manager == nil {
		return
	}
	bind(m)
}

// HandshakeServer completes a server handshake with the certificate id's current
// material. The private key stays inside the certificate module: the returned
// connection does not expose the tls.Conn or the key.
func (m *Module) HandshakeServer(ctx context.Context, certificateID int, raw net.Conn) (net.Conn, error) {
	if m == nil || m.manager == nil {
		return nil, ErrPublicCertificateUnavailable
	}
	manager, _ := m.manager.(*Manager)
	if prepared, ok := m.currentPreparedTLS(manager); ok && prepared.state != nil && (manager == nil || prepared.state == manager.activeState()) {
		return prepared.HandshakeServer(ctx, certificateID, raw)
	}
	if manager != nil {
		return manager.HandshakeServer(ctx, certificateID, raw)
	}
	return nil, ErrPublicCertificateUnavailable
}

func (m *Module) currentPreparedTLS(manager *Manager) (preparedTLSMaterial, bool) {
	if m == nil || m.selector == nil {
		return preparedTLSMaterial{}, false
	}
	active := m.selector.ActiveGeneration()
	if active == nil {
		return preparedTLSMaterial{}, false
	}
	provider, ok := active.Resolve(module.ProviderTLSMaterial)
	if !ok {
		return preparedTLSMaterial{}, false
	}
	prepared, ok := provider.(preparedTLSMaterial)
	if !ok || prepared.state == nil {
		return preparedTLSMaterial{}, false
	}
	if manager != nil && prepared.manager != nil && prepared.manager != manager {
		return preparedTLSMaterial{}, false
	}
	return prepared, true
}

func (m *Manager) HandshakeServer(ctx context.Context, certificateID int, raw net.Conn) (net.Conn, error) {
	if m == nil {
		return nil, ErrPublicCertificateUnavailable
	}
	entry, err := m.lookup(certificateID)
	if err != nil || !publicServerEntry(entry) {
		return nil, ErrPublicCertificateUnavailable
	}
	cert, err := m.ServerCertificate(ctx, certificateID)
	if err != nil || cert == nil {
		return nil, ErrPublicCertificateUnavailable
	}
	return handshakePublicServer(ctx, entry, cert, raw)
}

func (p preparedTLSMaterial) HandshakeServer(ctx context.Context, certificateID int, raw net.Conn) (net.Conn, error) {
	entry, err := p.lookup(certificateID)
	if err != nil || !publicServerEntry(entry) {
		return nil, ErrPublicCertificateUnavailable
	}
	cert, err := p.ServerCertificate(ctx, certificateID)
	if err != nil || cert == nil {
		return nil, ErrPublicCertificateUnavailable
	}
	return handshakePublicServer(ctx, entry, cert, raw)
}

func handshakePublicServer(ctx context.Context, entry *managedCertificate, cert *tls.Certificate, raw net.Conn) (net.Conn, error) {
	if ctx == nil || raw == nil || entry == nil || cert == nil || !publicServerEntry(entry) || cert.PrivateKey == nil || len(cert.Certificate) == 0 {
		return nil, ErrPublicCertificateUnavailable
	}
	leaf := cert.Leaf
	if leaf == nil {
		var err error
		leaf, err = x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return nil, ErrPublicCertificateUnavailable
		}
	}
	if leaf.IsCA {
		return nil, ErrPublicCertificateUnavailable
	}
	tlsConn := tls.Server(raw, &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{*cert},
	})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = tlsConn.Close()
		return nil, ErrPublicCertificateHandshake
	}
	return &publicApplicationConn{inner: tlsConn}, nil
}

func publicServerEntry(entry *managedCertificate) bool {
	if entry == nil {
		return false
	}
	certType := strings.ToLower(strings.TrimSpace(normalizeCertificateType(entry.info.CertificateType)))
	usage := strings.ToLower(strings.TrimSpace(normalizeUsage(entry.info.Usage)))
	domain := strings.TrimSpace(entry.info.Domain)
	if certType != "acme" && certType != "uploaded" {
		return false
	}
	if usage != "https" {
		return false
	}
	return !strings.EqualFold(domain, internalRelayCADomain)
}

// publicApplicationConn is the post-handshake byte stream. inner stays
// unexported so callers cannot reach the private key.
type publicApplicationConn struct {
	inner *tls.Conn
}

func (c *publicApplicationConn) Read(p []byte) (int, error) {
	if c == nil || c.inner == nil {
		return 0, net.ErrClosed
	}
	return c.inner.Read(p)
}

func (c *publicApplicationConn) Write(p []byte) (int, error) {
	if c == nil || c.inner == nil {
		return 0, net.ErrClosed
	}
	return c.inner.Write(p)
}

func (c *publicApplicationConn) Close() error {
	if c == nil || c.inner == nil {
		return nil
	}
	return c.inner.Close()
}

func (c *publicApplicationConn) CloseWrite() error {
	if c == nil || c.inner == nil {
		return net.ErrClosed
	}
	return c.inner.CloseWrite()
}

func (c *publicApplicationConn) LocalAddr() net.Addr {
	if c == nil || c.inner == nil {
		return &net.TCPAddr{}
	}
	return c.inner.LocalAddr()
}

func (c *publicApplicationConn) RemoteAddr() net.Addr {
	if c == nil || c.inner == nil {
		return &net.TCPAddr{}
	}
	return c.inner.RemoteAddr()
}

func (c *publicApplicationConn) SetDeadline(deadline time.Time) error {
	if c == nil || c.inner == nil {
		return net.ErrClosed
	}
	return c.inner.SetDeadline(deadline)
}

func (c *publicApplicationConn) SetReadDeadline(deadline time.Time) error {
	if c == nil || c.inner == nil {
		return net.ErrClosed
	}
	return c.inner.SetReadDeadline(deadline)
}

func (c *publicApplicationConn) SetWriteDeadline(deadline time.Time) error {
	if c == nil || c.inner == nil {
		return net.ErrClosed
	}
	return c.inner.SetWriteDeadline(deadline)
}
