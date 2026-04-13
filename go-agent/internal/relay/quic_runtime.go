package relay

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/quic-go/quic-go"
)

const relayQUICALPN = "nre-relay-quic/1"

var (
	quicDialAddr     = quic.DialAddr
	quicListenAddr   = quic.ListenAddr
	relaySessionPool = newSessionPool()
)

type quicStreamConn struct {
	conn   *quic.Conn
	stream *quic.Stream
}

func startQUICListener(ctx context.Context, provider TLSMaterialProvider, listener Listener, address string) (*quic.Listener, error) {
	tlsConfig, err := serverQUICTLSConfig(ctx, provider, listener)
	if err != nil {
		return nil, err
	}
	return quicListenAddr(address, tlsConfig, newRelayQUICConfig())
}

func dialQUIC(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider) (net.Conn, error) {
	if !strings.EqualFold(network, "tcp") {
		return nil, fmt.Errorf("udp relay is not supported")
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("relay chain is required")
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		return nil, fmt.Errorf("invalid relay target %q: %w", target, err)
	}

	firstHop := chain[0]
	tlsConfig, err := clientQUICTLSConfig(ctx, provider, firstHop.Listener, firstHop.Address, firstHop.ServerName)
	if err != nil {
		return nil, err
	}
	sessionKey, err := quicSessionPoolKey(firstHop)
	if err != nil {
		return nil, err
	}

	session, stream, err := openQUICStream(ctx, sessionKey, func(dialCtx context.Context) (*quic.Conn, error) {
		return quicDialAddr(dialCtx, firstHop.Address, tlsConfig, newRelayQUICConfig())
	})
	if err != nil {
		return nil, err
	}

	conn := &quicStreamConn{conn: session, stream: stream}
	request := relayOpenFrame{
		Kind:   "tcp",
		Target: target,
		Chain:  append([]Hop(nil), chain[1:]...),
	}
	if err := withFrameDeadline(conn, func() error {
		return writeRelayOpenFrame(conn, request)
	}); err != nil {
		conn.Close()
		return nil, err
	}

	var response relayResponse
	err = withFrameDeadline(conn, func() error {
		var readErr error
		response, readErr = readRelayResponse(conn)
		return readErr
	})
	if err != nil {
		conn.Close()
		return nil, err
	}
	if !response.OK {
		conn.Close()
		if response.Error == "" {
			return nil, fmt.Errorf("relay connection failed")
		}
		return nil, fmt.Errorf("relay connection failed: %s", response.Error)
	}

	return conn, nil
}

func openQUICStream(ctx context.Context, sessionKey string, dial func(context.Context) (*quic.Conn, error)) (*quic.Conn, *quic.Stream, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		session, err := relaySessionPool.getOrDial(ctx, sessionKey, dial)
		if err != nil {
			return nil, nil, err
		}

		streamCtx, cancel := context.WithTimeout(ctx, relayHandshakeTimeout)
		stream, err := session.OpenStreamSync(streamCtx)
		cancel()
		if err == nil {
			return session, stream, nil
		}

		lastErr = err
		relaySessionPool.remove(sessionKey, session)
		_ = session.CloseWithError(0, "relay stream open failed")
	}
	if lastErr == nil {
		lastErr = errors.New("failed to open relay stream")
	}
	return nil, nil, lastErr
}

func newRelayQUICConfig() *quic.Config {
	config := &quic.Config{
		HandshakeIdleTimeout: relayHandshakeTimeout,
		MaxIdleTimeout:       relayIdleTimeout,
	}
	if relayIdleTimeout > 0 {
		config.KeepAlivePeriod = relayIdleTimeout / 3
		if config.KeepAlivePeriod <= 0 {
			config.KeepAlivePeriod = relayIdleTimeout
		}
	}
	return config
}

func serverQUICTLSConfig(ctx context.Context, provider TLSMaterialProvider, listener Listener) (*tls.Config, error) {
	base, err := serverTLSConfig(ctx, provider, listener)
	if err != nil {
		return nil, err
	}
	return relayQUICTLSConfig(base), nil
}

func clientQUICTLSConfig(ctx context.Context, provider TLSMaterialProvider, listener Listener, address, serverNameOverride string) (*tls.Config, error) {
	base, err := clientTLSConfig(ctx, provider, listener, address, serverNameOverride)
	if err != nil {
		return nil, err
	}
	return relayQUICTLSConfig(base), nil
}

func relayQUICTLSConfig(base *tls.Config) *tls.Config {
	cfg := base.Clone()
	cfg.MinVersion = tls.VersionTLS13
	cfg.NextProtos = []string{relayQUICALPN}
	return cfg
}

func (c *quicStreamConn) Read(p []byte) (int, error) {
	return c.stream.Read(p)
}

func (c *quicStreamConn) Write(p []byte) (int, error) {
	return c.stream.Write(p)
}

func (c *quicStreamConn) Close() error {
	if c.stream == nil {
		return nil
	}
	_ = c.stream.Close()
	c.stream.CancelRead(0)
	c.stream.CancelWrite(0)
	return nil
}

func (c *quicStreamConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *quicStreamConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

func (c *quicStreamConn) SetDeadline(t time.Time) error {
	return c.stream.SetDeadline(t)
}

func (c *quicStreamConn) SetReadDeadline(t time.Time) error {
	return c.stream.SetReadDeadline(t)
}

func (c *quicStreamConn) SetWriteDeadline(t time.Time) error {
	return c.stream.SetWriteDeadline(t)
}

func (c *quicStreamConn) CloseWrite() error {
	return c.stream.Close()
}

func (c *quicStreamConn) CloseRead() error {
	c.stream.CancelRead(0)
	return nil
}
