package relay

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"time"
)

var (
	relayTimeoutMu        sync.RWMutex
	relayDialTimeout      = 5 * time.Second
	relayHandshakeTimeout = 5 * time.Second
	relayFrameTimeout     = 5 * time.Second
	relayIdleTimeout      = 2 * time.Minute
)

type TimeoutConfig struct {
	DialTimeout      time.Duration
	HandshakeTimeout time.Duration
	FrameTimeout     time.Duration
	IdleTimeout      time.Duration
}

func ConfigureTimeouts(cfg TimeoutConfig) func() {
	relayTimeoutMu.Lock()
	prevDial := relayDialTimeout
	prevHandshake := relayHandshakeTimeout
	prevFrame := relayFrameTimeout
	prevIdle := relayIdleTimeout

	if cfg.DialTimeout > 0 {
		relayDialTimeout = cfg.DialTimeout
	}
	if cfg.HandshakeTimeout > 0 {
		relayHandshakeTimeout = cfg.HandshakeTimeout
	}
	if cfg.FrameTimeout > 0 {
		relayFrameTimeout = cfg.FrameTimeout
	}
	if cfg.IdleTimeout > 0 {
		relayIdleTimeout = cfg.IdleTimeout
	}
	relayTimeoutMu.Unlock()

	return func() {
		relayTimeoutMu.Lock()
		relayDialTimeout = prevDial
		relayHandshakeTimeout = prevHandshake
		relayFrameTimeout = prevFrame
		relayIdleTimeout = prevIdle
		relayTimeoutMu.Unlock()
	}
}

func dialTCP(ctx context.Context, address string) (net.Conn, error) {
	dialTimeout := getRelayDialTimeout()
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	var dialer net.Dialer
	return dialer.DialContext(dialCtx, "tcp", address)
}

func handshakeTLS(ctx context.Context, conn *tls.Conn) error {
	handshakeTimeout := getRelayHandshakeTimeout()
	handshakeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()

	return withConnDeadline(conn, handshakeTimeout, func() error {
		return conn.HandshakeContext(handshakeCtx)
	})
}

func withFrameDeadline(conn net.Conn, fn func() error) error {
	return withConnDeadline(conn, getRelayFrameTimeout(), fn)
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
	idleTimeout := getRelayIdleTimeout()
	if idleTimeout <= 0 || conn == nil {
		return conn
	}
	return &idleDeadlineConn{Conn: conn, timeout: idleTimeout}
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

func getRelayDialTimeout() time.Duration {
	relayTimeoutMu.RLock()
	defer relayTimeoutMu.RUnlock()
	return relayDialTimeout
}

func getRelayHandshakeTimeout() time.Duration {
	relayTimeoutMu.RLock()
	defer relayTimeoutMu.RUnlock()
	return relayHandshakeTimeout
}

func getRelayFrameTimeout() time.Duration {
	relayTimeoutMu.RLock()
	defer relayTimeoutMu.RUnlock()
	return relayFrameTimeout
}

func getRelayIdleTimeout() time.Duration {
	relayTimeoutMu.RLock()
	defer relayTimeoutMu.RUnlock()
	return relayIdleTimeout
}
