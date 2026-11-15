package l4

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type flowTestListener struct {
	udpListener
	address net.Addr
}

func (l flowTestListener) LocalAddr() net.Addr { return l.address }

type flowTestUpstream struct {
	udpUpstream
	closed chan struct{}
	once   sync.Once
}

func (u *flowTestUpstream) Close() error                  { u.once.Do(func() { close(u.closed) }); return nil }
func (*flowTestUpstream) SetReadDeadline(time.Time) error { return nil }
func (u *flowTestUpstream) ReadPacket() (udpUpstreamPacket, error) {
	<-u.closed
	return udpUpstreamPacket{}, net.ErrClosed
}

func TestUDPFlowPinsSlowDialAndRetiredReaderCannotCloseReplacement(t *testing.T) {
	s, err := NewServer(context.Background(), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.cancel()
	defer func() { s.closeUDPSessions(); s.wg.Wait() }()
	listener := flowTestListener{address: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9000}}
	peer := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 30000}
	gate := make(chan struct{})
	entered := make(chan struct{}, 32)
	var dials atomic.Int32
	s.udpDialer = func(_ model.L4Rule, _ string) (udpUpstream, l4Candidate, error) {
		n := dials.Add(1)
		entered <- struct{}{}
		<-gate
		return &flowTestUpstream{closed: make(chan struct{})}, l4Candidate{address: fmt.Sprintf("127.0.0.1:%d", 9000+n)}, nil
	}
	rule := model.L4Rule{ID: 1, Protocol: "udp"}
	type outcome struct {
		session *udpSession
		err     error
	}
	results := make(chan outcome, 32)
	for i := 0; i < cap(results); i++ {
		go func() {
			session, err := s.sessionForUDPFlow(rule, listener, peer, "")
			results <- outcome{session, err}
		}()
	}
	<-entered
	close(gate)
	var first *udpSession
	for i := 0; i < cap(results); i++ {
		got := <-results
		if got.err != nil || got.session == nil {
			t.Fatalf("session = %v, %v", got.session, got.err)
		}
		if first == nil {
			first = got.session
		}
		if got.session != first {
			t.Fatal("concurrent packets did not share the selected backend")
		}
	}
	if dials.Load() != 1 {
		t.Fatalf("dials = %d", dials.Load())
	}
	s.closeUDPSession(first.key)
	next, err := s.sessionForUDPFlow(rule, listener, peer, "")
	if err != nil || next == first || next.targetAddr == first.targetAddr {
		t.Fatalf("replacement = %v, %v", next, err)
	}
	if err := (l4UDPSession{server: s, key: first.key, session: first}).ForceClose(context.Background(), "retired"); err != nil {
		t.Fatal(err)
	}
	s.markUDPSessionWrite(first)
	s.udpMu.Lock()
	got := s.existingUDPSessionLocked(listener, peer, "")
	pending := next.pendingReplies
	s.udpMu.Unlock()
	if got != next || pending != 0 {
		t.Fatal("retired session modified its replacement")
	}
	// Explicit proxy destinations from the same peer must remain separate flows.
	explicit, err := s.sessionForUDPFlow(rule, listener, peer, "example.test:53")
	if err != nil || explicit == next {
		t.Fatalf("explicit flow = %v, %v", explicit, err)
	}
	again, err := s.sessionForUDPFlow(rule, listener, peer, "example.test:53")
	if err != nil || again != explicit {
		t.Fatal("explicit destination was not reused")
	}
}

func TestUDPReplyBudgetAllowsUnmeasuredSlowPaths(t *testing.T) {
	s := &Server{udpReplyTimeout: defaultUDPReplyTimeout, upstreamScore: model.NewScoreStore(time.Now)}
	for _, direct := range []bool{false, true} {
		want := 30 * time.Second
		if direct {
			want = 5 * time.Second
		}
		if got := s.udpReplyTimeoutForCandidate(l4Candidate{address: "slow:53", directUDPPath: direct}); got != want {
			t.Fatalf("direct=%t budget=%v", direct, got)
		}
	}
	key := model.PathKey{Family: model.PathFamilyDirectUDP, Address: "fast:53"}
	s.upstreamScore.ObserveProbeSuccess(key, 0, 10*time.Millisecond, 1)
	if got := s.udpReplyTimeoutForCandidate(l4Candidate{address: key.Address, directUDPPath: true}); got >= 2*time.Second {
		t.Fatalf("measured fast path budget=%v", got)
	}
	s.udpReplyTimeout = 8 * time.Second
	if got := s.udpReplyTimeoutForCandidate(l4Candidate{}); got != 8*time.Second {
		t.Fatalf("override=%v", got)
	}
}

func BenchmarkUDPFlowLookup(b *testing.B) {
	for _, count := range []int{1, 1000, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			listener := flowTestListener{address: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9000}}
			s := &Server{udpSessions: make(map[string]*udpSession, count)}
			peers := make([]*net.UDPAddr, count)
			for i := range peers {
				peers[i] = &net.UDPAddr{IP: net.IPv4(10, 0, byte(i>>8), byte(i)), Port: 20000 + i}
				s.udpSessions[udpSessionKey(listener, peers[i], "")] = &udpSession{}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.udpMu.Lock()
				got := s.existingUDPSessionLocked(listener, peers[i%count], "")
				s.udpMu.Unlock()
				if got == nil {
					b.Fatal("session missing")
				}
			}
		})
	}
}
