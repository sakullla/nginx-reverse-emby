package l4

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"
)

const l4PolicyBodyWindowBytes = 64 << 10

type udpPolicyFlowLock struct {
	mu   sync.Mutex
	refs int
}

func (s *Server) allowL4Policy(
	ctx context.Context,
	rule model.L4Rule,
	physicalPeer net.Addr,
	authenticatedSource net.Addr,
	sourceKind policy.SourceKind,
	requestID string,
	fields map[string][]byte,
	body policy.BodyWindow,
) bool {
	if rule.PolicyRef == nil {
		return true
	}
	if s == nil || s.policyEvaluator == nil {
		return false
	}
	metadata, err := policy.NewDirectMetadata(physicalPeer)
	if authenticatedSource != nil {
		metadata, err = policy.NewAuthenticatedMetadata(sourceKind, physicalPeer, authenticatedSource)
	}
	if err != nil {
		return false
	}
	input, err := policy.NewInput(policy.ExtensionL4, requestID, metadata, fields, body)
	if err != nil {
		return false
	}
	decision := s.policyEvaluator.Evaluate(ctx, rule.PolicyRef, input)
	return decision.Action == policy.ActionAllow || decision.Action == policy.ActionObserve
}

func (s *Server) allowTCPPolicy(rule model.L4Rule, client net.Conn, source io.Reader, proxyMetadata *proxyInfo) bool {
	if rule.PolicyRef == nil {
		return true
	}
	body, err := tcpPolicyBodyWindow(source)
	if err != nil {
		return false
	}
	var canonicalSource net.Addr
	if proxyMetadata != nil {
		allowlist, allowlistErr := policy.NewTrustedPeerAllowlist(rule.Tuning.ProxyProtocol.TrustedPeers)
		if allowlistErr != nil {
			return false
		}
		if allowlist.Contains(client.RemoteAddr()) {
			canonicalSource = proxyMetadata.Source
		}
	}
	return s.allowL4Policy(
		s.ctx,
		rule,
		client.RemoteAddr(),
		canonicalSource,
		policy.SourceProxyProtocol,
		s.l4PolicyRequestID("tcp", rule.ID, client.RemoteAddr()),
		l4PolicyFields(rule, "", true),
		body,
	)
}

func (s *Server) allowProxyEntryTCPPolicy(rule model.L4Rule, client net.Conn, target string, initialPayload []byte) bool {
	if rule.PolicyRef == nil {
		return true
	}
	body, err := l4PolicyBodyWindow(initialPayload, false, policy.BodyStreaming)
	if err != nil {
		return false
	}
	return s.allowL4Policy(
		s.ctx,
		rule,
		client.RemoteAddr(),
		nil,
		policy.SourceDirect,
		s.l4PolicyRequestID("tcp", rule.ID, client.RemoteAddr()),
		l4PolicyFields(rule, target, true),
		body,
	)
}

func (s *Server) allowUDPPolicy(rule model.L4Rule, peer *net.UDPAddr, target string, payload []byte, newFlow bool) bool {
	if rule.PolicyRef == nil {
		return true
	}
	body, err := l4PolicyBodyWindow(payload, true, policy.BodyNotSkipped)
	if err != nil {
		return false
	}
	return s.allowL4Policy(
		s.ctx,
		rule,
		peer,
		nil,
		policy.SourceDirect,
		s.l4PolicyRequestID("udp", rule.ID, peer),
		l4PolicyFields(rule, target, newFlow),
		body,
	)
}

func tcpPolicyBodyWindow(source io.Reader) (policy.BodyWindow, error) {
	buffered, ok := source.(*bufio.Reader)
	if !ok || buffered.Buffered() == 0 {
		return policy.NewBodyWindow(nil, false, policy.BodyStreaming)
	}
	size := buffered.Buffered()
	if size > l4PolicyBodyWindowBytes {
		size = l4PolicyBodyWindowBytes
	}
	prefix, err := buffered.Peek(size)
	if err != nil {
		return policy.BodyWindow{}, err
	}
	return policy.NewBodyWindow(prefix, false, policy.BodyStreaming)
}

func l4PolicyBodyWindow(payload []byte, complete bool, skipReason policy.BodySkipReason) (policy.BodyWindow, error) {
	if len(payload) > l4PolicyBodyWindowBytes {
		payload = payload[:l4PolicyBodyWindowBytes]
		complete = false
		skipReason = policy.BodyLimitExceeded
	}
	return policy.NewBodyWindow(payload, complete, skipReason)
}

func l4PolicyFields(rule model.L4Rule, target string, newFlow bool) map[string][]byte {
	fields := map[string][]byte{
		policy.FieldFlowProtocol: []byte(strings.ToLower(strings.TrimSpace(rule.Protocol))),
		policy.FieldFlowNew:      []byte(strconv.FormatBool(newFlow)),
	}
	if strings.TrimSpace(target) == "" {
		target = net.JoinHostPort(rule.ListenHost, strconv.Itoa(rule.ListenPort))
	}
	if host, port, err := net.SplitHostPort(strings.TrimSpace(target)); err == nil {
		fields[policy.FieldFlowTargetIP] = boundedL4PolicyField(host, 256)
		fields[policy.FieldFlowTargetPort] = boundedL4PolicyField(port, 16)
	}
	return fields
}

func boundedL4PolicyField(value string, limit int) []byte {
	if limit < 0 {
		limit = 0
	}
	if len(value) > limit {
		value = value[:limit]
	}
	return []byte(value)
}

func (s *Server) l4PolicyRequestID(protocol string, ruleID int, peer net.Addr) string {
	peerAddress := ""
	if peer != nil {
		peerAddress = peer.String()
	}
	sequence := s.policyRequestSeq.Add(1)
	return fmt.Sprintf("%s:%s:%d:%s:%d", s.generationID, protocol, ruleID, peerAddress, sequence)
}

func (s *Server) lockUDPPolicyFlow(key string) func() {
	s.udpPolicyLocksMu.Lock()
	if s.udpPolicyLocks == nil {
		s.udpPolicyLocks = make(map[string]*udpPolicyFlowLock)
	}
	lock := s.udpPolicyLocks[key]
	if lock == nil {
		lock = &udpPolicyFlowLock{}
		s.udpPolicyLocks[key] = lock
	}
	lock.refs++
	s.udpPolicyLocksMu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		s.udpPolicyLocksMu.Lock()
		lock.refs--
		if lock.refs == 0 && s.udpPolicyLocks[key] == lock {
			delete(s.udpPolicyLocks, key)
		}
		s.udpPolicyLocksMu.Unlock()
	}
}

func (s *Server) policyCheckedUDPSession(
	rule model.L4Rule,
	listener udpListener,
	peer *net.UDPAddr,
	target string,
	payload []byte,
) (*udpSession, error) {
	if rule.PolicyRef == nil {
		return s.sessionForUDPFlow(rule, listener, peer, target)
	}
	if peer == nil || peer.IP == nil {
		return nil, nil
	}
	flowKey := udpSessionKey(listener, peer, strings.TrimSpace(target))
	unlock := s.lockUDPPolicyFlow(flowKey)
	defer unlock()

	s.udpMu.Lock()
	existing := s.existingUDPSessionLocked(listener, peer, strings.TrimSpace(target))
	if existing != nil {
		blocked := s.currentTrafficBlockState().Blocked
		ready := existing.ready
		if ready == nil {
			existing.lastActive = s.currentTime()
		}
		key := existing.key
		s.udpMu.Unlock()
		if blocked {
			s.closeUDPSession(key)
			return nil, nil
		}
		if ready != nil {
			<-ready
			if existing.initErr != nil {
				return nil, existing.initErr
			}
		}
		return existing, nil
	}
	s.udpMu.Unlock()

	if !s.allowUDPPolicy(rule, peer, target, payload, true) {
		return nil, nil
	}
	return s.sessionForUDPFlow(rule, listener, peer, target)
}
