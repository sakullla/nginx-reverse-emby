package relay

import (
	"context"
	"fmt"
	"sync"

	"github.com/quic-go/quic-go"
)

type sessionPool struct {
	mu       sync.Mutex
	sessions map[string]*quic.Conn
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

	conn, err := dial(ctx)
	if err != nil {
		return nil, err
	}
	return p.store(key, conn), nil
}

func (p *sessionPool) get(key string) *quic.Conn {
	p.mu.Lock()
	defer p.mu.Unlock()

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
		"%d|%d|%s|%s|%s",
		hop.Listener.ID,
		hop.Listener.Revision,
		hop.Address,
		serverName,
		normalizeListenerTransportModeValue(hop.Listener.TransportMode),
	), nil
}
