package http

import (
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

const (
	unknownAgentHeartbeatLogInterval  = 5 * time.Minute
	unknownAgentHeartbeatLogCacheSize = 1024
)

// heartbeatFailureLogLimiter prevents a deleted or de-authorized agent from
// turning its fixed-interval heartbeat retries into an unbounded log stream.
// The limiter is intentionally local to the HTTP process: it is observability
// state, not agent authorization state.
type heartbeatFailureLogLimiter struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (l *heartbeatFailureLogLimiter) allow(agentID string, now time.Time) bool {
	key := strings.TrimSpace(agentID)
	if key == "" {
		key = "<unknown>"
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last == nil {
		l.last = make(map[string]time.Time)
	}
	if previous, ok := l.last[key]; ok && now.Sub(previous) < unknownAgentHeartbeatLogInterval {
		return false
	}
	if len(l.last) >= unknownAgentHeartbeatLogCacheSize {
		l.evictOne(now)
	}
	l.last[key] = now
	return true
}

func (l *heartbeatFailureLogLimiter) evictOne(now time.Time) {
	for key, previous := range l.last {
		if now.Sub(previous) >= unknownAgentHeartbeatLogInterval {
			delete(l.last, key)
			return
		}
	}
	for key := range l.last {
		delete(l.last, key)
		return
	}
}

var unknownAgentHeartbeatLog = &heartbeatFailureLogLimiter{}

func logHeartbeatFailure(agentID string, err error) {
	if errors.Is(err, service.ErrAgentNotFound) || errors.Is(err, service.ErrAgentUnauthorized) {
		if !unknownAgentHeartbeatLog.allow(agentID, time.Now().UTC()) {
			return
		}
		log.Printf("[agents] heartbeat rejected for unknown or de-authorized agent %q: %v; stop the old agent or re-register it", strings.TrimSpace(agentID), err)
		return
	}
	log.Printf("[agents] heartbeat failed for agent %q: %v", strings.TrimSpace(agentID), err)
}
