package l4

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayplan"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayroute"
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
