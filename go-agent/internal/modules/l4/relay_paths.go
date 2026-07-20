package l4

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay/relayplan"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay/relayroute"
)

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

func (s *Server) dialTCPUpstream(rule model.L4Rule, dialOptions relay.DialOptions) (net.Conn, l4Candidate, time.Duration, error) {
	return s.dialTCPUpstreamCandidates(rule, dialOptions)
}

func (s *Server) dialTCPUpstreamForClient(rule model.L4Rule, _ net.Conn, dialOptions relay.DialOptions) (net.Conn, l4Candidate, time.Duration, error) {
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
		start := s.currentTime()
		var upstream net.Conn
		if !ruleUsesRelay(rule) {
			upstream, err = s.dialTCPLocalEgress(rule, target)
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
		connectDuration := s.currentTime().Sub(start)
		return upstream, candidate, connectDuration, nil
	}
	if lastErr != nil {
		return nil, l4Candidate{}, 0, lastErr
	}
	return nil, l4Candidate{}, 0, fmt.Errorf("all backends failed for %s:%d", rule.ListenHost, rule.ListenPort)
}

func (s *Server) dialTCPDirect(target string) (net.Conn, error) {
	dialer := s.currentTCPDialer()
	if dialer == nil {
		dialer = (&net.Dialer{}).DialContext
	}
	return dialer(s.ctx, "tcp", target)
}

func (s *Server) dialTCPLocalEgress(rule model.L4Rule, target string) (net.Conn, error) {
	if rule.EgressProfileID == nil || *rule.EgressProfileID <= 0 {
		return s.dialTCPDirect(target)
	}
	if s.finalHopDialer != nil {
		return s.finalHopDialer.DialTCP(s.ctx, target, rule.EgressProfileID)
	}
	return s.egressDialer.DialTCP(s.ctx, target, rule.EgressProfileID)
}

func (s *Server) validateLocalEgressProfile(rule model.L4Rule) error {
	if ruleUsesRelay(rule) || rule.EgressProfileID == nil || *rule.EgressProfileID <= 0 {
		return nil
	}
	protocol := strings.ToLower(strings.TrimSpace(rule.Protocol))
	if protocol == "" {
		protocol = "tcp"
	}
	_, _, err := s.egressDialer.Resolver.Resolve(rule.EgressProfileID, protocol)
	return err
}

func (s *Server) dialRelayPath(network, target string, rule model.L4Rule, dialOptions relay.DialOptions) (net.Conn, error) {
	if dialOptions.EgressProfileID == nil {
		dialOptions.EgressProfileID = rule.EgressProfileID
	}
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
		cloned[i].IDs = slices.Clone(path.IDs)
		cloned[i].Hops = slices.Clone(path.Hops)
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

func l4ListenAddress(rule model.L4Rule) string {
	return net.JoinHostPort(rule.ListenHost, strconv.Itoa(rule.ListenPort))
}

func l4BindingKey(rule model.L4Rule) string {
	protocol := strings.ToLower(strings.TrimSpace(rule.Protocol))
	return protocol + ":" + l4ListenAddress(rule)
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
