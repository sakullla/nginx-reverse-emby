package relay

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/quic-go/quic-go"
)

type sessionPool struct {
	mu       sync.Mutex
	sessions map[string]*quic.Conn
	closed   bool
}

func (p *sessionPool) close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	sessions := p.sessions
	p.sessions = make(map[string]*quic.Conn)
	p.closed = true
	p.mu.Unlock()
	var closeErr error
	for _, session := range sessions {
		closeErr = errors.Join(closeErr, session.CloseWithError(0, "relay generation released"))
	}
	return closeErr
}

func newSessionPool() *sessionPool {
	return &sessionPool{
		sessions: make(map[string]*quic.Conn),
	}
}

func (p *sessionPool) getOrDial(ctx context.Context, key string, dial func(context.Context) (*quic.Conn, error)) (*quic.Conn, error) {
	if existing := p.get(key); existing != nil {
		return existing, nil
	}
	if p.isClosed() {
		return nil, net.ErrClosed
	}

	conn, err := dial(ctx)
	if err != nil {
		return nil, err
	}
	stored := p.store(key, conn)
	if stored == nil {
		return nil, net.ErrClosed
	}
	return stored, nil
}

func (p *sessionPool) get(key string) *quic.Conn {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}

	conn := p.sessions[key]
	if conn == nil {
		return nil
	}
	if conn.Context().Err() != nil {
		delete(p.sessions, key)
		return nil
	}
	return conn
}

func (p *sessionPool) store(key string, conn *quic.Conn) *quic.Conn {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = conn.CloseWithError(0, "fenced relay session pool")
		return nil
	}
	existing := p.sessions[key]
	if existing != nil && existing.Context().Err() == nil {
		p.mu.Unlock()
		_ = conn.CloseWithError(0, "duplicate pooled relay session")
		return existing
	}
	p.sessions[key] = conn
	p.mu.Unlock()

	go func() {
		<-conn.Context().Done()
		p.remove(key, conn)
	}()

	return conn
}

func (p *sessionPool) isClosed() bool {
	if p == nil {
		return true
	}
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	return closed
}

func (p *sessionPool) remove(key string, conn *quic.Conn) {
	if conn == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if existing := p.sessions[key]; existing == conn {
		delete(p.sessions, key)
	}
}

func quicSessionPoolKey(hop Hop) (string, error) {
	serverName, err := verificationServerName(hop.Address, hop.ServerName)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"%d|%d|%s|%s|%s|%s",
		hop.Listener.ID,
		hop.Listener.Revision,
		hop.Address,
		serverName,
		normalizeListenerTransportModeValue(hop.Listener.TransportMode),
		strings.TrimSpace(hop.securityBinding),
	), nil
}
