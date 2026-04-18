package relay

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"time"
)

var (
	relayTimeoutMu               sync.RWMutex
	relayTimeoutNextID           uint64
	relayTimeoutOverrides        []relayTimeoutOverride
	relayDialTimeout             = 5 * time.Second
	relayHandshakeTimeout        = 5 * time.Second
	relayFrameTimeout            = 5 * time.Second
	relayIdleTimeout             = 2 * time.Minute
	defaultRelayDialTimeout      = 5 * time.Second
	defaultRelayHandshakeTimeout = 5 * time.Second
	defaultRelayFrameTimeout     = 5 * time.Second
	defaultRelayIdleTimeout      = 2 * time.Minute
)

const relayBulkSocketBufferBytes = 1 << 20

type relayTimeoutOverride struct {
	id  uint64
	cfg TimeoutConfig
}

type TimeoutConfig struct {
	DialTimeout      time.Duration
	HandshakeTimeout time.Duration
	FrameTimeout     time.Duration
	IdleTimeout      time.Duration
}

type relayTCPBufferTuner interface {
	SetReadBuffer(bytes int) error
	SetWriteBuffer(bytes int) error
}

type relayDialContextFunc func(ctx context.Context, network, address string) (net.Conn, error)

var relayDialContext relayDialContextFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, address)
}

func ConfigureTimeouts(cfg TimeoutConfig) func() {
	relayTimeoutMu.Lock()
	relayTimeoutNextID++
	overrideID := relayTimeoutNextID
	relayTimeoutOverrides = append(relayTimeoutOverrides, relayTimeoutOverride{id: overrideID, cfg: cfg})
	applyRelayTimeoutOverridesLocked()
	relayTimeoutMu.Unlock()

	return func() {
		relayTimeoutMu.Lock()
		for i, override := range relayTimeoutOverrides {
			if override.id != overrideID {
				continue
			}
			relayTimeoutOverrides = append(relayTimeoutOverrides[:i], relayTimeoutOverrides[i+1:]...)
			break
		}
		applyRelayTimeoutOverridesLocked()
		relayTimeoutMu.Unlock()
	}
}

func applyRelayTimeoutOverridesLocked() {
	relayDialTimeout = defaultRelayDialTimeout
	relayHandshakeTimeout = defaultRelayHandshakeTimeout
	relayFrameTimeout = defaultRelayFrameTimeout
	relayIdleTimeout = defaultRelayIdleTimeout
	for _, override := range relayTimeoutOverrides {
		if override.cfg.DialTimeout > 0 {
			relayDialTimeout = override.cfg.DialTimeout
		}
		if override.cfg.HandshakeTimeout > 0 {
			relayHandshakeTimeout = override.cfg.HandshakeTimeout
		}
		if override.cfg.FrameTimeout > 0 {
			relayFrameTimeout = override.cfg.FrameTimeout
		}
		if override.cfg.IdleTimeout > 0 {
			relayIdleTimeout = override.cfg.IdleTimeout
		}
	}
}

func dialTCP(ctx context.Context, address string) (net.Conn, error) {
	return dialTCPWithTuning(ctx, address, false)
}

func dialRelayTCP(ctx context.Context, address string) (net.Conn, error) {
	return dialTCPWithTuning(ctx, address, true)
}

func dialTCPWithTuning(ctx context.Context, address string, tuneBuffers bool) (net.Conn, error) {
	dialTimeout := getRelayDialTimeout()
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	conn, err := relayDialContext(dialCtx, "tcp", address)
	if err != nil {
		return nil, err
	}
	if tuneBuffers {
		tuneBulkRelayConn(conn)
	}
	return conn, nil
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

func tuneBulkRelayConn(conn any) {
	tuner, ok := conn.(relayTCPBufferTuner)
	if !ok {
		return
	}
	_ = tuner.SetReadBuffer(relayBulkSocketBufferBytes)
	_ = tuner.SetWriteBuffer(relayBulkSocketBufferBytes)
}
