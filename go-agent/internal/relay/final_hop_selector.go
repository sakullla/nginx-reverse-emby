package relay

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
)

type finalHopSelectorConfig struct {
	Resolver   backends.Resolver
	Now        func() time.Time
	RandomIntn func(int) int
}

type finalHopSelector struct {
	cache *backends.Cache
	now   func() time.Time
}

func newFinalHopSelector(cfg finalHopSelectorConfig) *finalHopSelector {
	cache := backends.NewCache(backends.Config{
		Resolver:   cfg.Resolver,
		Now:        cfg.Now,
		RandomIntn: cfg.RandomIntn,
	})
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &finalHopSelector{cache: cache, now: nowFn}
}

func (s *finalHopSelector) resolvedCandidates(ctx context.Context, target string) ([]backends.Candidate, error) {
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return nil, fmt.Errorf("invalid relay target %q: %w", target, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, fmt.Errorf("invalid relay target %q: %w", target, err)
	}
	var resolved []backends.Candidate
	if literal, address := literalHostCandidate(host, port); literal {
		resolved = []backends.Candidate{{Address: address}}
	} else {
		resolved, err = s.cache.Resolve(ctx, backends.Endpoint{Host: host, Port: port})
		if err != nil {
			return nil, err
		}
		resolved = s.cache.PreferResolvedCandidatesLatencyOnly(resolved)
	}

	filtered := make([]backends.Candidate, 0, len(resolved))
	for _, candidate := range resolved {
		if s.cache.IsInBackoff(candidate.Address) {
			continue
		}
		filtered = append(filtered, candidate)
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("no healthy relay target candidates for %s", target)
	}
	return filtered, nil
}

func literalHostCandidate(host string, port int) (bool, string) {
	trimmedHost := strings.TrimSpace(host)
	if ip := net.ParseIP(trimmedHost); ip != nil {
		return true, net.JoinHostPort(ip.String(), strconv.Itoa(port))
	}
	if zoneIndex := strings.LastIndex(trimmedHost, "%"); zoneIndex > 0 {
		if ip := net.ParseIP(trimmedHost[:zoneIndex]); ip != nil && ip.To4() == nil {
			return true, net.JoinHostPort(trimmedHost, strconv.Itoa(port))
		}
	}
	return false, ""
}

func (s *finalHopSelector) dialTCP(ctx context.Context, target string) (net.Conn, string, error) {
	candidates, err := s.resolvedCandidates(ctx, target)
	if err != nil {
		return nil, "", err
	}

	var lastErr error
	for _, candidate := range candidates {
		start := s.now()
		conn, err := dialTCP(ctx, candidate.Address)
		if err != nil {
			s.cache.MarkFailure(candidate.Address)
			lastErr = err
			continue
		}
		s.cache.ObserveTransferSuccess(candidate.Address, s.now().Sub(start), 0, 0)
		return conn, candidate.Address, nil
	}
	return nil, "", lastErr
}

type observedUDPPeer struct {
	udpPacketPeer
	selector          *finalHopSelector
	address           string
	openedAt          time.Time
	firstReplyTimeout time.Duration
	success           sync.Once
	failure           sync.Once
	hasSucceeded      atomic.Bool
	localClosed       atomic.Bool
}

func (p *observedUDPPeer) Close() error {
	p.localClosed.Store(true)
	return p.udpPacketPeer.Close()
}

func (p *observedUDPPeer) WritePacket(payload []byte) error {
	if !p.hasSucceeded.Load() && p.firstReplyTimeout > 0 {
		_ = p.udpPacketPeer.SetReadDeadline(time.Now().Add(p.firstReplyTimeout))
	}
	if err := p.udpPacketPeer.WritePacket(payload); err != nil {
		p.failure.Do(func() { p.selector.cache.MarkFailure(p.address) })
		return err
	}
	return nil
}

func (p *observedUDPPeer) ReadPacket() ([]byte, error) {
	payload, err := p.udpPacketPeer.ReadPacket()
	if err != nil {
		if !p.hasSucceeded.Load() && !p.localClosed.Load() {
			p.failure.Do(func() { p.selector.cache.MarkFailure(p.address) })
		}
		return nil, err
	}
	p.hasSucceeded.Store(true)
	if p.firstReplyTimeout > 0 {
		_ = p.udpPacketPeer.SetReadDeadline(time.Time{})
	}
	p.success.Do(func() {
		p.selector.cache.ObserveTransferSuccess(p.address, p.selector.now().Sub(p.openedAt), 0, 0)
	})
	return payload, nil
}

func (s *finalHopSelector) openUDPPeer(ctx context.Context, target string) (udpPacketPeer, string, error) {
	candidates, err := s.resolvedCandidates(ctx, target)
	if err != nil {
		return nil, "", err
	}

	var lastErr error
	for _, candidate := range candidates {
		addr, err := net.ResolveUDPAddr("udp", candidate.Address)
		if err != nil {
			s.cache.MarkFailure(candidate.Address)
			lastErr = err
			continue
		}
		conn, err := net.DialUDP("udp", nil, addr)
		if err != nil {
			s.cache.MarkFailure(candidate.Address)
			lastErr = err
			continue
		}
		return &observedUDPPeer{
			udpPacketPeer:     newUDPSocketPeer(conn),
			selector:          s,
			address:           candidate.Address,
			openedAt:          s.now(),
			firstReplyTimeout: getRelayFrameTimeout(),
		}, candidate.Address, nil
	}
	return nil, "", lastErr
}
