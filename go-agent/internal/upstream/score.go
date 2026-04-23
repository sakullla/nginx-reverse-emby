package upstream

import (
	"sync"
	"time"
)

type PathFamily string

const (
	PathFamilyDirectHTTP PathFamily = "direct_http"
	PathFamilyRelayQUIC  PathFamily = "relay_quic"
)

type FailureKind string

const (
	FailureTimeout FailureKind = "timeout"
)

type PathKey struct {
	Family  PathFamily
	Address string
}

type PathState struct {
	ProbeOnly               bool
	ProbeSuccesses          int
	ConsecutiveHighSeverity int
}

type ScoreStore struct {
	mu    sync.Mutex
	now   func() time.Time
	state map[PathKey]PathState
}

func NewScoreStore(now func() time.Time) *ScoreStore {
	if now == nil {
		now = time.Now
	}

	return &ScoreStore{
		now:   now,
		state: make(map[PathKey]PathState),
	}
}

func (s *ScoreStore) ObserveFailure(key PathKey, kind FailureKind) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if kind != FailureTimeout {
		return
	}

	st := s.state[key]
	st.ConsecutiveHighSeverity++
	if st.ConsecutiveHighSeverity >= 2 {
		st.ProbeOnly = true
		st.ProbeSuccesses = 0
	}
	s.state[key] = st
}

func (s *ScoreStore) ObserveProbeSuccess(key PathKey, handshake time.Duration, firstByte time.Duration, bytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.state[key]
	if st.ProbeOnly {
		st.ProbeSuccesses++
		if st.ProbeSuccesses >= 3 {
			st.ProbeOnly = false
			st.ConsecutiveHighSeverity = 0
		}
	}
	s.state[key] = st
}

func (s *ScoreStore) State(key PathKey) PathState {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.state[key]
}
