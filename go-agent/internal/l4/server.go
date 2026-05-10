package l4

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayplan"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayroute"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)

const (
	relayInitialPayloadMax = 32 * 1024
	defaultUDPReplyTimeout = time.Second
)

type RelayMaterialProvider interface {
	relay.TLSMaterialProvider
}

type Server struct {
	ctx    context.Context
	cancel context.CancelFunc

	wg sync.WaitGroup

	cache *backends.Cache
	now   func() time.Time

	tcpListeners          []net.Listener
	udpConns              []*net.UDPConn
	udpMu                 sync.Mutex
	udpSessions           map[string]*udpSession
	udpReplyTimeout       time.Duration
	udpSessionIdleTimeout time.Duration
	upstreamScore         *upstream.ScoreStore

	relayListenersByID map[int]model.RelayListener
	relayProvider      RelayMaterialProvider
	relayPathDialer    relayplan.Dialer

	tcpMu    sync.Mutex
	tcpConns map[net.Conn]struct{}
	closing  bool

	trafficBlockState trafficBlockStateValue
}

type relayPathDialer struct {
	provider RelayMaterialProvider
}

func (d relayPathDialer) DialPath(ctx context.Context, req relayplan.Request, path relayplan.Path) (net.Conn, relay.DialResult, error) {
	options := relay.DialOptions{}
	if len(req.Options) > 0 {
		options = req.Options[0]
	}
	return relay.DialWithResult(ctx, req.Network, req.Target, path.Hops, d.provider, options)
}

type l4Candidate struct {
	address               string
	directUDPPath         bool
	backoffKey            string
	markBackoffOnFailure  bool
	backendObservationKey string
}

func NewServer(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
) (*Server, error) {
	return NewServerWithResources(ctx, rules, relayListeners, relayProvider, nil)
}

func NewServerWithResources(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
	cache *backends.Cache,
) (*Server, error) {
	ctx, cancel := context.WithCancel(ctx)
	relayListenersByID := make(map[int]model.RelayListener, len(relayListeners))
	for _, listener := range relayListeners {
		relayListenersByID[listener.ID] = listener
	}
	if cache == nil {
		cache = backends.NewCache(backends.Config{})
	}
	s := &Server{
		ctx:                   ctx,
		cancel:                cancel,
		cache:                 cache,
		now:                   time.Now,
		tcpConns:              make(map[net.Conn]struct{}),
		udpConns:              nil,
		udpSessions:           make(map[string]*udpSession),
		udpReplyTimeout:       defaultUDPReplyTimeout,
		udpSessionIdleTimeout: 30 * time.Second,
		upstreamScore:         upstream.NewScoreStore(time.Now),
		tcpListeners:          nil,
		relayListenersByID:    relayListenersByID,
		relayProvider:         relayProvider,
		relayPathDialer:       relayPathDialer{provider: relayProvider},
	}
	for _, rule := range rules {
		if err := ValidateRule(rule); err != nil {
			s.Close()
			return nil, err
		}
		if err := s.validateRelayChain(rule); err != nil {
			s.Close()
			return nil, err
		}

		switch strings.ToLower(rule.Protocol) {
		case "tcp":
			if err := s.startTCPListener(rule); err != nil {
				s.Close()
				return nil, err
			}
		case "udp":
			if err := s.startUDPListener(rule); err != nil {
				s.Close()
				return nil, err
			}
		default:
			s.Close()
			return nil, fmt.Errorf("unsupported protocol %q", rule.Protocol)
		}
	}
	return s, nil
}

func (s *Server) currentTrafficBlockState() TrafficBlockState {
	if s == nil {
		return TrafficBlockState{}
	}
	return s.trafficBlockState.Load()
}

func (s *Server) SetTrafficBlockState(state TrafficBlockState) {
	if s == nil {
		return
	}
	s.trafficBlockState.Store(state)
}

func (s *Server) dialTCPUpstream(rule model.L4Rule, dialOptions relay.DialOptions) (net.Conn, l4Candidate, time.Duration, error) {
	candidates, err := l4Candidates(s.ctx, s.cache, rule)
	if err != nil {
		return nil, l4Candidate{}, 0, err
	}

	var lastErr error
	for _, candidate := range candidates {
		if ctxErr := s.ctx.Err(); ctxErr != nil {
			return nil, l4Candidate{}, 0, ctxErr
		}
		target := candidate.address
		start := s.now()
		var upstream net.Conn
		if !ruleUsesRelay(rule) {
			upstream, err = (&net.Dialer{}).DialContext(s.ctx, "tcp", target)
		} else {
			upstream, err = s.dialRelayPath("tcp", target, rule, dialOptions)
		}
		if err != nil {
			if ctxErr := s.ctx.Err(); ctxErr != nil {
				return nil, l4Candidate{}, 0, ctxErr
			}
			s.observeCandidateFailure(candidate)
			lastErr = err
			continue
		}
		connectDuration := s.now().Sub(start)
		return upstream, candidate, connectDuration, nil
	}
	if lastErr != nil {
		return nil, l4Candidate{}, 0, lastErr
	}
	return nil, l4Candidate{}, 0, fmt.Errorf("all backends failed for %s:%d", rule.ListenHost, rule.ListenPort)
}

func (s *Server) dialRelayPath(network, target string, rule model.L4Rule, dialOptions relay.DialOptions) (net.Conn, error) {
	paths, err := s.resolveRelayPaths(rule)
	if err != nil {
		return nil, err
	}
	requestPaths := cloneRelayPlanPaths(paths)
	for i := range requestPaths {
		requestPaths[i].Key = relayplan.PathKey("relay_path", requestPaths[i].IDs, target)
	}
	dialer := s.relayPathDialer
	if dialer == nil {
		dialer = relayPathDialer{provider: s.relayProvider}
	}
	racer := relayplan.Racer{Dialer: dialer, Cache: s.cache, Concurrency: 3, MaxPaths: 32}
	result, err := racer.Race(s.ctx, relayplan.Request{
		Network: network,
		Target:  target,
		Paths:   requestPaths,
		Options: []relay.DialOptions{dialOptions},
	})
	if err != nil {
		return nil, err
	}
	return result.Conn, nil
}

func cloneRelayPlanPaths(paths []relayplan.Path) []relayplan.Path {
	cloned := make([]relayplan.Path, len(paths))
	for i, path := range paths {
		cloned[i] = path
		cloned[i].IDs = append([]int(nil), path.IDs...)
		cloned[i].Hops = append([]relay.Hop(nil), path.Hops...)
	}
	return cloned
}

func (s *Server) Close() error {
	if s.cancel != nil {
		s.cancel()
	}

	s.tcpMu.Lock()
	s.closing = true
	s.tcpMu.Unlock()

	for _, ln := range s.tcpListeners {
		ln.Close()
	}
	s.closeTCPConns()
	s.closeUDPSessions()
	for _, conn := range s.udpConns {
		conn.Close()
	}
	s.wg.Wait()
	return nil
}

func (s *Server) trackTCPConn(conn net.Conn) {
	if conn == nil {
		return
	}
	s.tcpMu.Lock()
	if s.tcpConns == nil {
		s.tcpConns = make(map[net.Conn]struct{})
	}
	closing := s.closing
	if !closing {
		s.tcpConns[conn] = struct{}{}
	}
	s.tcpMu.Unlock()

	if closing {
		conn.Close()
	}
}

func (s *Server) untrackTCPConn(conn net.Conn) {
	if conn == nil {
		return
	}
	s.tcpMu.Lock()
	defer s.tcpMu.Unlock()
	delete(s.tcpConns, conn)
}

func (s *Server) closeTCPConns() {
	s.tcpMu.Lock()
	conns := s.tcpConns
	s.tcpConns = nil
	s.tcpMu.Unlock()

	for conn := range conns {
		conn.Close()
	}
}

func l4Candidates(ctx context.Context, cache *backends.Cache, rule model.L4Rule) ([]l4Candidate, error) {
	if cache == nil {
		return nil, fmt.Errorf("backend cache is required")
	}

	rawBackends := rule.Backends
	if len(rawBackends) == 0 && rule.UpstreamHost != "" && rule.UpstreamPort > 0 {
		rawBackends = []model.L4Backend{{
			Host: rule.UpstreamHost,
			Port: rule.UpstreamPort,
		}}
	}
	if len(rawBackends) == 0 {
		return nil, fmt.Errorf("at least one backend is required for %s:%d", rule.ListenHost, rule.ListenPort)
	}

	placeholders := make([]backends.Candidate, 0, len(rawBackends))
	indexesByID := make(map[string][]int, len(rawBackends))
	duplicateCounts := make(map[string]int, len(rawBackends))
	for i := range rawBackends {
		id := backends.StableBackendID(net.JoinHostPort(rawBackends[i].Host, strconv.Itoa(rawBackends[i].Port)))
		placeholders = append(placeholders, backends.Candidate{Address: id})
		indexesByID[id] = append(indexesByID[id], i)
		duplicateCounts[id]++
	}

	scope := strings.ToLower(rule.Protocol) + ":" + net.JoinHostPort(rule.ListenHost, strconv.Itoa(rule.ListenPort))
	orderedBackends := cache.OrderLatencyOnly(scope, rule.LoadBalancing.Strategy, placeholders)
	out := make([]l4Candidate, 0, len(rawBackends))
	for _, ordered := range orderedBackends {
		indexes := indexesByID[ordered.Address]
		if len(indexes) == 0 {
			continue
		}
		backendIndex := indexes[0]
		indexesByID[ordered.Address] = indexes[1:]
		backend := rawBackends[backendIndex]
		backendID := backends.StableBackendID(net.JoinHostPort(backend.Host, strconv.Itoa(backend.Port)))
		if ruleUsesRelay(rule) {
			// Preserve the configured host for relay chains so the final hop resolves DNS.
			dialAddress := net.JoinHostPort(backend.Host, strconv.Itoa(backend.Port))
			bk := backends.RelayBackoffKeyForLayers(rule.RelayChain, rule.RelayLayers, dialAddress)
			if cache.IsInBackoff(bk) {
				continue
			}
			out = append(out, l4Candidate{
				address:               dialAddress,
				directUDPPath:         false,
				backoffKey:            bk,
				markBackoffOnFailure:  len(rule.RelayLayers) == 0,
				backendObservationKey: l4ObservationKey(scope, backendID, backendIndex, duplicateCounts[backendID]),
			})
			continue
		}
		endpoint := backends.Endpoint{
			Host: backend.Host,
			Port: backend.Port,
		}
		resolved, err := cache.Resolve(ctx, endpoint)
		if err != nil {
			if ctx != nil {
				if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
					return nil, ctxErr
				}
			}
			continue
		}
		resolved = cache.PreferResolvedCandidatesLatencyOnly(resolved)
		for _, candidate := range resolved {
			if cache.IsInBackoff(candidate.Address) {
				continue
			}
			out = append(out, l4Candidate{
				address:               candidate.Address,
				directUDPPath:         strings.ToLower(rule.Protocol) == "udp" && !ruleUsesRelay(rule),
				markBackoffOnFailure:  true,
				backendObservationKey: l4ObservationKey(scope, backendID, backendIndex, duplicateCounts[backendID]),
			})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no healthy backend candidates for %s:%d", rule.ListenHost, rule.ListenPort)
	}
	return out, nil
}

func l4ObservationKey(scope string, backendID string, backendIndex int, duplicateCount int) string {
	if duplicateCount <= 1 {
		return backends.BackendObservationKey(scope, backendID)
	}
	return backends.BackendObservationKey(scope, fmt.Sprintf("%s#%d", backendID, backendIndex+1))
}

func l4CandidateBackoffAddr(candidate l4Candidate) string {
	if candidate.backoffKey != "" {
		return candidate.backoffKey
	}
	return candidate.address
}

func (s *Server) observeCandidateFailure(candidate l4Candidate) {
	if s == nil || s.cache == nil {
		return
	}
	if candidate.backendObservationKey != "" {
		s.cache.ObserveBackendFailure(candidate.backendObservationKey)
	}
	if addr := l4CandidateBackoffAddr(candidate); addr != "" && candidate.markBackoffOnFailure {
		s.cache.MarkFailure(addr)
	}
}

func (s *Server) observeCandidateSuccess(candidate l4Candidate, headerLatency time.Duration) {
	if s == nil || s.cache == nil || candidate.address == "" {
		return
	}
	if candidate.backendObservationKey != "" {
		s.cache.ObserveBackendSuccess(candidate.backendObservationKey, headerLatency, 0, 0)
	}
	s.cache.ObserveSuccess(l4CandidateBackoffAddr(candidate), headerLatency)
}

func (s *Server) validateRelayChain(rule model.L4Rule) error {
	if !ruleUsesRelay(rule) {
		return nil
	}
	if s.relayProvider == nil {
		return fmt.Errorf("l4 rule %s:%d requires relay tls material provider", rule.ListenHost, rule.ListenPort)
	}
	_, err := s.resolveRelayHops(rule)
	return err
}

func (s *Server) resolveRelayHops(rule model.L4Rule) ([]relay.Hop, error) {
	paths, err := s.resolveRelayPaths(rule)
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	return paths[0].Hops, nil
}

func (s *Server) resolveRelayPaths(rule model.L4Rule) ([]relayplan.Path, error) {
	label := fmt.Sprintf("l4 rule %s:%d", rule.ListenHost, rule.ListenPort)
	return relayroute.ResolvePathsFromMapWithLabel(label, rule.RelayChain, rule.RelayLayers, s.relayListenersByID, "")
}

func ruleUsesRelay(rule model.L4Rule) bool {
	return relayroute.UsesRelay(rule.RelayChain, rule.RelayLayers)
}

func RelayInputsChanged(rules []model.L4Rule, previousRelayListeners, nextRelayListeners []model.RelayListener) bool {
	for _, rule := range rules {
		for _, listenerID := range rule.RelayChain {
			if relayListenerChangedByID(listenerID, previousRelayListeners, nextRelayListeners) {
				return true
			}
		}
		for _, layer := range rule.RelayLayers {
			for _, listenerID := range layer {
				if relayListenerChangedByID(listenerID, previousRelayListeners, nextRelayListeners) {
					return true
				}
			}
		}
	}
	return false
}

func relayListenerChangedByID(listenerID int, previous, next []model.RelayListener) bool {
	previousListener, previousOK := relayListenerByID(listenerID, previous)
	nextListener, nextOK := relayListenerByID(listenerID, next)
	if previousOK != nextOK {
		return true
	}
	if !previousOK {
		return false
	}
	return !reflect.DeepEqual(previousListener, nextListener)
}

func relayListenerByID(listenerID int, listeners []model.RelayListener) (model.RelayListener, bool) {
	for _, listener := range listeners {
		if listener.ID == listenerID {
			return listener, true
		}
	}
	return model.RelayListener{}, false
}
