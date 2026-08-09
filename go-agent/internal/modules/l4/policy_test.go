package l4

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"
)

type l4PolicyEvaluatorFunc func(context.Context, *model.PolicyRef, policy.Input) policy.Decision

func (f l4PolicyEvaluatorFunc) Evaluate(ctx context.Context, ref *model.PolicyRef, input policy.Input) policy.Decision {
	return f(ctx, ref, input)
}

type l4PolicyProviderResolver map[module.ProviderRef]any

func (r l4PolicyProviderResolver) Resolve(ref module.ProviderRef) (any, bool) {
	value, ok := r[ref]
	return value, ok
}

type l4PolicyConn struct {
	net.Conn
	remote net.Addr
}

func (c *l4PolicyConn) RemoteAddr() net.Addr { return c.remote }

type blockingPolicyUDPUpstream struct {
	closed chan struct{}
	once   sync.Once
}

func newBlockingPolicyUDPUpstream() *blockingPolicyUDPUpstream {
	return &blockingPolicyUDPUpstream{closed: make(chan struct{})}
}

func (u *blockingPolicyUDPUpstream) Close() error {
	u.once.Do(func() { close(u.closed) })
	return nil
}

func (*blockingPolicyUDPUpstream) SetReadDeadline(time.Time) error  { return nil }
func (*blockingPolicyUDPUpstream) SetWriteDeadline(time.Time) error { return nil }
func (u *blockingPolicyUDPUpstream) ReadPacket() (udpUpstreamPacket, error) {
	<-u.closed
	return udpUpstreamPacket{}, net.ErrClosed
}
func (*blockingPolicyUDPUpstream) WritePacket([]byte) error { return nil }

func TestIPPolicyTCPUsesAuthenticatedProxyProtocolSource(t *testing.T) {
	var captured policy.Input
	evaluator := l4PolicyEvaluatorFunc(func(_ context.Context, _ *model.PolicyRef, input policy.Input) policy.Decision {
		captured = input
		return policy.Decision{Action: policy.ActionAllow}
	})
	server := &Server{ctx: context.Background(), policyEvaluator: evaluator, generationID: "generation-trusted-source"}
	client := &l4PolicyConn{remote: &net.TCPAddr{IP: net.ParseIP("192.0.2.30"), Port: 40000}}
	source := bufio.NewReader(bytes.NewReader([]byte("buffered-prefix")))
	if _, err := source.Peek(len("buffered-prefix")); err != nil {
		t.Fatalf("prime buffered source: %v", err)
	}
	rule := model.L4Rule{
		ID: 7, Protocol: "tcp", ListenHost: "127.0.0.1", ListenPort: 8443,
		PolicyRef: &model.PolicyRef{ID: "ip-policy"},
		Tuning: model.L4Tuning{ProxyProtocol: model.L4ProxyProtocolTuning{
			Decode: true, TrustedPeers: []string{"192.0.2.0/24"},
		}},
	}
	proxyMetadata := &proxyInfo{Source: &net.TCPAddr{IP: net.ParseIP("198.51.100.22"), Port: 12345}, Version: 2}

	if !server.allowTCPPolicy(rule, client, source, proxyMetadata) {
		t.Fatal("trusted PROXY policy request was denied")
	}
	metadata := captured.Metadata()
	if got := metadata.Source().String(); got != "198.51.100.22:12345" {
		t.Fatalf("canonical source = %q", got)
	}
	if got := metadata.Peer().String(); got != "192.0.2.30:40000" {
		t.Fatalf("physical peer = %q", got)
	}
	if metadata.Kind() != policy.SourceProxyProtocol {
		t.Fatalf("source kind = %q", metadata.Kind())
	}
	if got := string(captured.Body().Prefix()); got != "buffered-prefix" {
		t.Fatalf("TCP body window = %q", got)
	}
	if got := string(captured.Fields()[policy.FieldFlowNew]); got != "true" {
		t.Fatalf("flow.new = %q, want true for accepted TCP connection", got)
	}
}

func TestIPPolicyTCPRejectsForgedProxySourceFromUntrustedPeer(t *testing.T) {
	var captured policy.Input
	evaluator := l4PolicyEvaluatorFunc(func(_ context.Context, _ *model.PolicyRef, input policy.Input) policy.Decision {
		captured = input
		return policy.Decision{Action: policy.ActionAllow}
	})
	server := &Server{ctx: context.Background(), policyEvaluator: evaluator, generationID: "generation-untrusted-source"}
	client := &l4PolicyConn{remote: &net.TCPAddr{IP: net.ParseIP("203.0.113.30"), Port: 40000}}
	rule := model.L4Rule{
		ID: 8, Protocol: "tcp", ListenHost: "127.0.0.1", ListenPort: 8443,
		PolicyRef: &model.PolicyRef{ID: "ip-policy"},
		Tuning: model.L4Tuning{ProxyProtocol: model.L4ProxyProtocolTuning{
			Decode: true, TrustedPeers: []string{"192.0.2.0/24"},
		}},
	}
	forged := &proxyInfo{Source: &net.TCPAddr{IP: net.ParseIP("198.51.100.22"), Port: 12345}, Version: 2}
	if !server.allowTCPPolicy(rule, client, bufio.NewReader(nil), forged) {
		t.Fatal("untrusted PROXY claim should be ignored, not affect policy availability")
	}
	if got := captured.Metadata().Source().String(); got != "203.0.113.30:40000" {
		t.Fatalf("forged PROXY source = %q, want physical peer", got)
	}
	if captured.Metadata().Kind() != policy.SourceDirect {
		t.Fatalf("forged PROXY source kind = %q", captured.Metadata().Kind())
	}
}

func TestRateLimitPolicyUDPDatagramsOnlyMarkNewFlowOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var flowNew []string
	var bodies []string
	deny := false
	evaluator := l4PolicyEvaluatorFunc(func(_ context.Context, _ *model.PolicyRef, input policy.Input) policy.Decision {
		flowNew = append(flowNew, string(input.Fields()[policy.FieldFlowNew]))
		bodies = append(bodies, string(input.Body().Prefix()))
		if deny {
			return policy.Decision{Action: policy.ActionDeny}
		}
		return policy.Decision{Action: policy.ActionAllow}
	})
	upstream := newBlockingPolicyUDPUpstream()
	server := &Server{
		ctx: ctx, cancel: cancel, cache: model.NewCache(model.BackendCacheConfig{}), now: time.Now,
		udpSessions: make(map[string]*udpSession), udpPolicyLocks: make(map[string]*udpPolicyFlowLock),
		revokedRules: make(map[int]struct{}), policyEvaluator: evaluator, udpReplyTimeout: time.Second,
	}
	server.udpDialer = func(model.L4Rule, string) (udpUpstream, l4Candidate, error) {
		return upstream, l4Candidate{address: "127.0.0.1:9000"}, nil
	}
	defer server.Close()

	listener := &dropTestUDPListener{addr: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8000}}
	peer := &net.UDPAddr{IP: net.ParseIP("192.0.2.44"), Port: 50000}
	rule := model.L4Rule{ID: 9, Protocol: "udp", ListenHost: "127.0.0.1", ListenPort: 8000, PolicyRef: &model.PolicyRef{ID: "rate-policy"}}
	first, err := server.policyCheckedUDPSession(rule, listener, peer, "", []byte("first"))
	if err != nil || first == nil {
		t.Fatalf("first UDP policy/session = %v, %v", first, err)
	}
	deny = true
	second, err := server.policyCheckedUDPSession(rule, listener, peer, "", []byte("second"))
	if err != nil || second != first {
		t.Fatalf("second UDP policy/session = %v, %v, want existing %v", second, err, first)
	}
	if len(flowNew) != 1 || flowNew[0] != "true" {
		t.Fatalf("flow.new evaluations = %v, want one authoritative new-flow admission", flowNew)
	}
	if len(bodies) != 1 || bodies[0] != "first" {
		t.Fatalf("new-flow body windows = %v", bodies)
	}

	server.closeUDPSession(first.key)
	replacement, err := server.policyCheckedUDPSession(rule, listener, peer, "", []byte("replacement"))
	if err != nil || replacement != nil {
		t.Fatalf("expired flow replacement = %v, error = %v, want policy denial", replacement, err)
	}
	if len(flowNew) != 2 || flowNew[1] != "true" || len(bodies) != 2 || bodies[1] != "replacement" {
		t.Fatalf("replacement admission flow.new/bodies = %v/%v", flowNew, bodies)
	}
}

func TestPolicyDenyBlocksOnlyDependentUDPFlowBeforeDial(t *testing.T) {
	var dials atomic.Int32
	evaluator := l4PolicyEvaluatorFunc(func(context.Context, *model.PolicyRef, policy.Input) policy.Decision {
		return policy.Decision{Action: policy.ActionDeny}
	})
	server := &Server{
		ctx: context.Background(), cache: model.NewCache(model.BackendCacheConfig{}), now: time.Now,
		udpSessions: make(map[string]*udpSession), udpPolicyLocks: make(map[string]*udpPolicyFlowLock),
		revokedRules: make(map[int]struct{}), policyEvaluator: evaluator,
	}
	server.udpDialer = func(model.L4Rule, string) (udpUpstream, l4Candidate, error) {
		dials.Add(1)
		return newBlockingPolicyUDPUpstream(), l4Candidate{address: "127.0.0.1:9000"}, nil
	}
	listener := &dropTestUDPListener{addr: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8001}}
	peer := &net.UDPAddr{IP: net.ParseIP("192.0.2.45"), Port: 50001}
	dependent := model.L4Rule{ID: 10, Protocol: "udp", ListenHost: "127.0.0.1", ListenPort: 8001, PolicyRef: &model.PolicyRef{ID: "deny-policy"}}
	if session, err := server.policyCheckedUDPSession(dependent, listener, peer, "", []byte("blocked")); err != nil || session != nil {
		t.Fatalf("dependent denied session = %v, error = %v", session, err)
	}
	if dials.Load() != 0 {
		t.Fatalf("upstream dials after deny = %d", dials.Load())
	}

	if !server.allowUDPPolicy(model.L4Rule{}, peer, "", []byte("unconfigured"), true) {
		t.Fatal("unconfigured L4 rule was affected by policy runtime")
	}
}

func TestGenerationPolicyEvaluatorProviderIsResolvedForL4(t *testing.T) {
	evaluator := l4PolicyEvaluatorFunc(func(context.Context, *model.PolicyRef, policy.Input) policy.Decision {
		return policy.Decision{Action: policy.ActionAllow}
	})
	providers := new(Module).runtimeProviders(l4PolicyProviderResolver{module.ProviderPolicyEvaluator: evaluator}, nil)
	if providers.PolicyEvaluator == nil {
		t.Fatal("generation policy evaluator was not resolved for L4")
	}
}
