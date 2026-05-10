package relay

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)

type relayPathPlanner interface {
	Plan(input upstream.PlanInput) upstream.PlanResult
}

var relayPlanner relayPathPlanner
var relayRuntimeScore = upstream.NewScoreStore(time.Now)
var relayVerifiedFallbacks = newRelayVerifiedFallbackStore()

const relayQUICProbeInterval = 30 * time.Second

func setRelayPlannerForTest(planner relayPathPlanner) func() {
	prev := relayPlanner
	relayPlanner = planner
	return func() {
		relayPlanner = prev
	}
}

type relayVerifiedFallbackStore struct {
	mu       sync.Mutex
	verified map[string]struct{}
}

func newRelayVerifiedFallbackStore() *relayVerifiedFallbackStore {
	return &relayVerifiedFallbackStore{
		verified: make(map[string]struct{}),
	}
}

func (s *relayVerifiedFallbackStore) Mark(firstHop Hop) {
	key := relayHopIdentityKey(firstHop)
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verified[key] = struct{}{}
}

func (s *relayVerifiedFallbackStore) Clear(firstHop Hop) {
	key := relayHopIdentityKey(firstHop)
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.verified, key)
}

func (s *relayVerifiedFallbackStore) Has(firstHop Hop) bool {
	key := relayHopIdentityKey(firstHop)
	if key == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.verified[key]
	return ok
}

func selectRelayRuntimeTransport(firstHop Hop) string {
	transportMode := chooseRelayTransport(firstHop)
	if transportMode != ListenerTransportModeQUIC {
		if relayQUICProbeDue(firstHop) {
			return ListenerTransportModeQUIC
		}
		return transportMode
	}
	if relayQUICProbeDue(firstHop) {
		return ListenerTransportModeQUIC
	}
	if relayQUICBackoffActive(firstHop) && relayVerifiedFallbackAvailable(firstHop) {
		return ListenerTransportModeTLSTCP
	}
	return ListenerTransportModeQUIC
}

func chooseRelayTransport(firstHop Hop) string {
	planner := relayPlanner
	if planner == nil {
		planner = upstream.NewPlanner()
	}
	candidates := relayTransportCandidates(firstHop)
	if len(candidates) == 0 {
		return normalizeListenerTransportModeValue(firstHop.Listener.TransportMode)
	}
	result := planner.Plan(upstream.PlanInput{
		Paths:            candidates,
		Class:            upstream.TrafficClassUnknown,
		ResourcePressure: upstream.ResourcePressureLow,
	})
	if len(result.Ordered) == 0 {
		return normalizeListenerTransportModeValue(firstHop.Listener.TransportMode)
	}
	switch result.Ordered[0].Key.Family {
	case upstream.PathFamilyRelayQUIC:
		return ListenerTransportModeQUIC
	case upstream.PathFamilyRelayTLSTCP:
		return ListenerTransportModeTLSTCP
	}
	return normalizeListenerTransportModeValue(firstHop.Listener.TransportMode)
}

func relayTransportCandidates(firstHop Hop) []upstream.PathSnapshot {
	baseMode := normalizeListenerTransportModeValue(firstHop.Listener.TransportMode)
	if baseMode != ListenerTransportModeQUIC {
		return []upstream.PathSnapshot{{
			Key:        upstream.PathKey{Family: upstream.PathFamilyRelayTLSTCP, Address: firstHop.Address},
			Confidence: 1.0,
		}}
	}

	quicState := upstream.PathState{}
	quicKey := relayQUICPathKey(firstHop)
	if relayRuntimeScore != nil {
		quicState = relayRuntimeScore.State(quicKey)
	}
	return []upstream.PathSnapshot{{
		Key:        quicKey,
		Confidence: relayPathConfidence(quicState, false),
		ProbeOnly:  quicState.ProbeOnly,
	}}
}

func relayQUICProbeDue(firstHop Hop) bool {
	if relayRuntimeScore == nil || normalizeListenerTransportModeValue(firstHop.Listener.TransportMode) != ListenerTransportModeQUIC {
		return false
	}
	return relayRuntimeScore.ProbeOpportunityDue(relayQUICPathKey(firstHop))
}

func relayQUICBackoffActive(firstHop Hop) bool {
	if relayRuntimeScore == nil || normalizeListenerTransportModeValue(firstHop.Listener.TransportMode) != ListenerTransportModeQUIC {
		return false
	}
	return relayRuntimeScore.State(relayQUICPathKey(firstHop)).ProbeOnly
}

func consumeRelayQUICProbe(firstHop Hop) bool {
	if relayRuntimeScore == nil || normalizeListenerTransportModeValue(firstHop.Listener.TransportMode) != ListenerTransportModeQUIC {
		return true
	}
	key := relayQUICPathKey(firstHop)
	state := relayRuntimeScore.State(key)
	if !state.ProbeOnly {
		return true
	}
	return relayRuntimeScore.ConsumeProbeOpportunity(key, relayQUICProbeInterval)
}

func relayPathConfidence(state upstream.PathState, probeDue bool) float64 {
	if state.ProbeOnly {
		if probeDue {
			return 0.31
		}
		return 0.10
	}
	return 0.80
}

func relayQUICPathKey(firstHop Hop) upstream.PathKey {
	if sessionKey, err := quicSessionPoolKey(firstHop); err == nil && strings.TrimSpace(sessionKey) != "" {
		return upstream.PathKey{Family: upstream.PathFamilyRelayQUIC, Address: sessionKey}
	}
	return upstream.PathKey{Family: upstream.PathFamilyRelayQUIC, Address: firstHop.Address}
}

func relayHopIdentityKey(firstHop Hop) string {
	return relayQUICPathKey(firstHop).Address
}

func relayVerifiedFallbackAvailable(firstHop Hop) bool {
	if !firstHop.Listener.AllowTransportFallback || normalizeListenerTransportModeValue(firstHop.Listener.TransportMode) != ListenerTransportModeQUIC {
		return false
	}
	return relayVerifiedFallbacks != nil && relayVerifiedFallbacks.Has(firstHop)
}

func isRelayApplicationError(err error) bool {
	var appErr *relayApplicationError
	return errors.As(err, &appErr)
}

func markRelayVerifiedFallback(firstHop Hop) {
	if !firstHop.Listener.AllowTransportFallback || normalizeListenerTransportModeValue(firstHop.Listener.TransportMode) != ListenerTransportModeQUIC {
		return
	}
	if relayVerifiedFallbacks != nil {
		relayVerifiedFallbacks.Mark(firstHop)
	}
}

func clearRelayVerifiedFallback(firstHop Hop) {
	if relayVerifiedFallbacks != nil {
		relayVerifiedFallbacks.Clear(firstHop)
	}
}

func observeRelayQUICFailureForHop(firstHop Hop) {
	if relayRuntimeScore == nil {
		return
	}
	key := relayQUICPathKey(firstHop)
	relayRuntimeScore.ObserveFailure(key, upstream.FailureTimeout)
	relayRuntimeScore.ArmProbe(key, relayQUICProbeInterval)
}

func observeRelayQUICSuccessForHop(firstHop Hop) {
	if relayRuntimeScore == nil {
		return
	}
	relayRuntimeScore.ObserveProbeSuccess(
		relayQUICPathKey(firstHop),
		0,
		0,
		0,
	)
}

func setRelayRuntimeScoreForTest(score *upstream.ScoreStore) func() {
	prev := relayRuntimeScore
	relayRuntimeScore = score
	return func() {
		relayRuntimeScore = prev
	}
}

func setRelayVerifiedFallbacksForTest(store *relayVerifiedFallbackStore) func() {
	prev := relayVerifiedFallbacks
	relayVerifiedFallbacks = store
	return func() {
		relayVerifiedFallbacks = prev
	}
}
