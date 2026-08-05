package relay

import (
	"errors"
	"sync"
)

var relayGenerationPools = struct {
	sync.Mutex
	entries map[string]*relayGenerationPoolEntry
}{entries: make(map[string]*relayGenerationPoolEntry)}

var relayGlobalPools = struct {
	sync.RWMutex
	scope *relayPoolScope
}{scope: newRelayPoolScope()}

type relayGenerationPoolEntry struct {
	scope *relayPoolScope
	refs  int
}

type relayPoolLease struct {
	key   string
	scope *relayPoolScope
	once  sync.Once
	err   error
}

type relayPoolScope struct {
	quic *sessionPool
	tls  *tlsTCPSessionPool
	once sync.Once
	err  error
}

func newRelayPoolScope() *relayPoolScope {
	return &relayPoolScope{quic: newSessionPool(), tls: newTLSTCPSessionPool()}
}

func (s *relayPoolScope) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		s.err = errors.Join(s.quic.close(), s.tls.close())
	})
	return s.err
}

func globalRelayPoolScope() *relayPoolScope {
	relayGlobalPools.RLock()
	scope := relayGlobalPools.scope
	relayGlobalPools.RUnlock()
	return scope
}

// fenceGlobalRelayPoolScope publishes fresh process-wide outbound pools before
// closing the old generation. Callers that already captured the old scope are
// rejected by the pools' closed state, so a concurrent dial cannot repopulate
// a fenced generation after the close completes.
func fenceGlobalRelayPoolScope() error {
	relayGlobalPools.Lock()
	previous := relayGlobalPools.scope
	relayGlobalPools.scope = newRelayPoolScope()
	relayGlobalPools.Unlock()
	return previous.Close()
}

func acquireRelayPoolScope(generationID string) *relayPoolLease {
	if generationID == "" {
		return &relayPoolLease{scope: newRelayPoolScope()}
	}
	relayGenerationPools.Lock()
	entry := relayGenerationPools.entries[generationID]
	if entry == nil {
		entry = &relayGenerationPoolEntry{scope: newRelayPoolScope()}
		relayGenerationPools.entries[generationID] = entry
	}
	entry.refs++
	relayGenerationPools.Unlock()
	return &relayPoolLease{key: generationID, scope: entry.scope}
}

func lookupRelayPoolScope(generationID string) *relayPoolScope {
	relayGenerationPools.Lock()
	entry := relayGenerationPools.entries[generationID]
	relayGenerationPools.Unlock()
	if entry == nil {
		return nil
	}
	return entry.scope
}

func (l *relayPoolLease) release() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		if l.key == "" {
			l.err = l.scope.Close()
			return
		}
		relayGenerationPools.Lock()
		entry := relayGenerationPools.entries[l.key]
		closeScope := false
		if entry != nil {
			entry.refs--
			if entry.refs == 0 {
				delete(relayGenerationPools.entries, l.key)
				closeScope = true
			}
		}
		relayGenerationPools.Unlock()
		if closeScope {
			l.err = entry.scope.Close()
		}
	})
	return l.err
}

func (l *relayPoolLease) rotate(previous *relayPoolScope) *relayPoolScope {
	if l == nil {
		return newRelayPoolScope()
	}
	if l.key == "" {
		l.scope = newRelayPoolScope()
		return l.scope
	}

	relayGenerationPools.Lock()
	entry := relayGenerationPools.entries[l.key]
	if entry == nil {
		entry = &relayGenerationPoolEntry{scope: newRelayPoolScope(), refs: 1}
		relayGenerationPools.entries[l.key] = entry
	} else if entry.scope == previous {
		entry.scope = newRelayPoolScope()
	}
	l.scope = entry.scope
	relayGenerationPools.Unlock()
	return l.scope
}
