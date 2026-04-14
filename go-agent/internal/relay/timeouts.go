package relay

import (
	"context"
	"crypto/tls"
	"net"
	"time"
)

var (
	relayDialTimeout      = 5 * time.Second
	relayHandshakeTimeout = 5 * time.Second
	relayFrameTimeout     = 5 * time.Second
	relayIdleTimeout      = 2 * time.Minute
)

func dialTCP(ctx context.Context, address string) (net.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, relayDialTimeout)
	defer cancel()

	var dialer net.Dialer
	return dialer.DialContext(dialCtx, "tcp", address)
}

func handshakeTLS(ctx context.Context, conn *tls.Conn) error {
	handshakeCtx, cancel := context.WithTimeout(ctx, relayHandshakeTimeout)
	defer cancel()

	return withConnDeadline(conn, relayHandshakeTimeout, func() error {
		return conn.HandshakeContext(handshakeCtx)
	})
}

func withFrameDeadline(conn net.Conn, fn func() error) error {
	return withConnDeadline(conn, relayFrameTimeout, fn)
}

func withWriteDeadline(conn net.Conn, timeout time.Duration, fn func() error) error {
	if timeout <= 0 {
		return fn()
	}
	if conn != nil {
		_ = conn.SetWriteDeadline(time.Now().Add(timeout))
		defer conn.SetWriteDeadline(time.Time{})
	}
	return fn()
}

func withConnDeadline(conn net.Conn, timeout time.Duration, fn func() error) error {
	if timeout <= 0 {
		return fn()
	}
	if conn != nil {
		_ = conn.SetDeadline(time.Now().Add(timeout))
		defer conn.SetDeadline(time.Time{})
	}
	return fn()
}

func wrapIdleConn(conn net.Conn) net.Conn {
	if relayIdleTimeout <= 0 || conn == nil {
		return conn
	}
	return &idleDeadlineConn{Conn: conn, timeout: relayIdleTimeout}
}

type idleDeadlineConn struct {
	net.Conn
	timeout time.Duration
}

func (c *idleDeadlineConn) Read(p []byte) (int, error) {
	_ = c.Conn.SetReadDeadline(time.Now().Add(c.timeout))
	return c.Conn.Read(p)
}

func (c *idleDeadlineConn) Write(p []byte) (int, error) {
	_ = c.Conn.SetWriteDeadline(time.Now().Add(c.timeout))
	return c.Conn.Write(p)
}

func (c *idleDeadlineConn) CloseWrite() error {
	if closer, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return closer.CloseWrite()
	}
	return c.Conn.Close()
}

func (c *idleDeadlineConn) CloseRead() error {
	if closer, ok := c.Conn.(interface{ CloseRead() error }); ok {
		return closer.CloseRead()
	}
	return nil
}
