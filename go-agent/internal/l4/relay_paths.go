package l4

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxyproto"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayplan"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayroute"
)

type relayPathDialer struct {
	provider          RelayMaterialProvider
	wireGuardProvider relay.WireGuardRuntimeProvider
}

func (d relayPathDialer) DialPath(ctx context.Context, req relayplan.Request, path relayplan.Path) (net.Conn, relay.DialResult, error) {
	options := relay.DialOptions{}
	if len(req.Options) > 0 {
		options = req.Options[0]
	}
	if options.WireGuardProvider == nil {
		options.WireGuardProvider = d.wireGuardProvider
	}
	return relay.DialWithResult(ctx, req.Network, req.Target, path.Hops, d.provider, options)
}

func (s *Server) dialTCPUpstream(rule model.L4Rule, dialOptions relay.DialOptions) (net.Conn, l4Candidate, time.Duration, error) {
	return s.dialTCPUpstreamCandidates(rule, dialOptions)
}

func (s *Server) dialTCPUpstreamForClient(rule model.L4Rule, client net.Conn, dialOptions relay.DialOptions) (net.Conn, l4Candidate, time.Duration, error) {
	if isWireGuardTransparentForwardRule(rule) {
		target, err := transparentTCPTargetFromConn(client)
		if err != nil {
			return nil, l4Candidate{}, 0, err
		}
		return s.dialTransparentTCPUpstream(rule, target, dialOptions)
	}
	return s.dialTCPUpstreamCandidates(rule, dialOptions)
}

func (s *Server) dialTCPUpstreamCandidates(rule model.L4Rule, dialOptions relay.DialOptions) (net.Conn, l4Candidate, time.Duration, error) {
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
			upstream, err = s.dialTCPDirect(target)
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

func (s *Server) dialTransparentTCPUpstream(rule model.L4Rule, target string, dialOptions relay.DialOptions) (net.Conn, l4Candidate, time.Duration, error) {
	candidate := l4Candidate{address: target}
	start := s.now()
	var (
		upstream net.Conn
		err      error
	)
	switch strings.ToLower(strings.TrimSpace(rule.ProxyEgressMode)) {
	case "":
		if ruleUsesRelay(rule) {
			upstream, err = s.dialRelayPath("tcp", target, rule, dialOptions)
		} else {
			upstream, err = s.dialTCPDirect(target)
		}
	case "relay":
		upstream, err = s.dialRelayPath("tcp", target, rule, dialOptions)
	case "wireguard":
		if ruleUsesRelay(rule) {
			upstream, err = s.dialRelayPath("tcp", target, rule, dialOptions)
		} else {
			runtime, runtimeErr := s.wireGuardRuntime(rule)
			if runtimeErr != nil {
				err = runtimeErr
				break
			}
			upstream, err = runtime.DialContext(s.ctx, "tcp", target)
		}
	case "proxy":
		if ruleUsesRelay(rule) {
			if _, parseErr := proxyproto.ParseProxyURL(rule.ProxyEgressURL); parseErr != nil {
				err = parseErr
				break
			}
			dialOptions.FinalHopProxyURL = rule.ProxyEgressURL
			upstream, err = s.dialRelayPath("tcp", target, rule, dialOptions)
		} else {
			upstream, err = proxyproto.Dial(s.ctx, rule.ProxyEgressURL, target)
		}
	default:
		err = fmt.Errorf("unsupported proxy_egress_mode %q", rule.ProxyEgressMode)
	}
	if err != nil {
		return nil, candidate, 0, err
	}
	return upstream, candidate, s.now().Sub(start), nil
}

func (s *Server) dialTCPDirect(target string) (net.Conn, error) {
	dialer := s.tcpDialer
	if dialer == nil {
		dialer = (&net.Dialer{}).DialContext
	}
	return dialer(s.ctx, "tcp", target)
}

func transparentTCPTargetFromConn(client net.Conn) (string, error) {
	if client == nil {
		return "", fmt.Errorf("transparent tcp downstream connection is nil")
	}
	addr := client.LocalAddr()
	if addr == nil {
		return "", fmt.Errorf("transparent tcp downstream destination is unavailable")
	}
	target := strings.TrimSpace(addr.String())
	if target == "" {
		return "", fmt.Errorf("transparent tcp downstream destination is empty")
	}
	return target, nil
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
		dialer = relayPathDialer{provider: s.relayProvider, wireGuardProvider: s.wireGuardProvider}
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
	return relayroute.ResolvePathsFromMapWithLabel(label, nil, rule.RelayLayers, s.relayListenersByID, "")
}

func ruleUsesRelay(rule model.L4Rule) bool {
	return relayroute.UsesRelay(nil, rule.RelayLayers)
}

func (s *Server) wireGuardRuntime(rule model.L4Rule) (relay.WireGuardRuntime, error) {
	if rule.WireGuardProfileID == nil || *rule.WireGuardProfileID <= 0 {
		return nil, fmt.Errorf("wireguard_profile_id is required")
	}
	if s.wireGuardProvider == nil {
		return nil, fmt.Errorf("wireguard runtime provider is required")
	}
	runtime, ok := s.wireGuardProvider.WireGuardRuntime(*rule.WireGuardProfileID)
	if !ok || runtime == nil {
		return nil, fmt.Errorf("wireguard profile %d runtime not found", *rule.WireGuardProfileID)
	}
	return runtime, nil
}

func l4ListenAddress(rule model.L4Rule) string {
	host := rule.ListenHost
	if strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard") {
		switch wireGuardInboundMode(rule) {
		case "transparent":
			host = ""
		case "address":
			if strings.TrimSpace(rule.WireGuardListenHost) != "" {
				host = rule.WireGuardListenHost
			}
		}
	}
	return net.JoinHostPort(host, strconv.Itoa(rule.ListenPort))
}

func l4BindingKey(rule model.L4Rule) string {
	protocol := strings.ToLower(strings.TrimSpace(rule.Protocol))
	if strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard") {
		return "wireguard:" + strconv.Itoa(valueOrZeroIntPtr(rule.WireGuardProfileID)) + ":" + protocol + ":" + l4ListenAddress(rule)
	}
	return protocol + ":" + l4ListenAddress(rule)
}

func valueOrZeroIntPtr(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func RelayInputsChanged(rules []model.L4Rule, previousRelayListeners, nextRelayListeners []model.RelayListener) bool {
	for _, rule := range rules {
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
