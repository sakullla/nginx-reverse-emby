package http

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	stdhttp "net/http"
	"net/netip"
	"sort"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"
)

// Keep room inside the total policy input budget for canonical request fields.
// The original request body is rebuilt from this prefix and its unread tail, so
// policy inspection never requires whole-body buffering.
const (
	httpPolicyBodyWindowBytes  = 64 << 10
	httpPolicyFieldValueBytes  = policy.MaxPolicyReadFieldValueBytes
	httpPolicyHeaderValueBytes = httpPolicyFieldValueBytes
	httpPolicyHeadersBytes     = 32 << 10
)

type httpPolicyRequestIDContextKey struct{}

type httpRemoteAddr struct {
	network string
	address string
}

func (a httpRemoteAddr) Network() string { return a.network }
func (a httpRemoteAddr) String() string  { return a.address }

func withHTTPPolicyRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, httpPolicyRequestIDContextKey{}, strings.TrimSpace(requestID))
}

func httpPolicyRequestID(ctx context.Context) string {
	requestID, _ := ctx.Value(httpPolicyRequestIDContextKey{}).(string)
	return requestID
}

func (s *Server) allowPolicyRequest(req *stdhttp.Request, rule model.HTTPRule) (policy.Decision, bool) {
	if rule.PolicyRef == nil {
		return policy.Decision{Action: policy.ActionAllow}, true
	}
	if s == nil || s.policyEvaluator == nil || req == nil {
		return policy.Decision{Action: policy.ActionDeny, StatusCode: stdhttp.StatusServiceUnavailable, Reason: "runtime-unavailable", Degraded: true}, false
	}

	network := "tcp"
	if req.ProtoMajor == 3 {
		network = "udp"
	}
	metadata, err := httpPolicyMetadata(req, network, rule.TrustedProxyRanges)
	if err != nil {
		return policy.Decision{Action: policy.ActionDeny, StatusCode: stdhttp.StatusServiceUnavailable, Reason: "invalid-source", Degraded: true}, false
	}
	body, err := prepareHTTPPolicyBodyWindow(req)
	if err != nil {
		return policy.Decision{Action: policy.ActionDeny, StatusCode: stdhttp.StatusServiceUnavailable, Reason: "body-window", Degraded: true}, false
	}
	fields, err := httpPolicyFields(req)
	if err != nil {
		return policy.Decision{Action: policy.ActionDeny, StatusCode: stdhttp.StatusServiceUnavailable, Reason: "input-projection", Degraded: true}, false
	}
	input, err := policy.NewInput(policy.ExtensionHTTP, httpPolicyRequestID(req.Context()), metadata, fields, body)
	if err != nil {
		return policy.Decision{Action: policy.ActionDeny, StatusCode: stdhttp.StatusServiceUnavailable, Reason: "invalid-input", Degraded: true}, false
	}
	decision := s.policyEvaluator.Evaluate(req.Context(), rule.PolicyRef, input)
	return decision, decision.Action == policy.ActionAllow || decision.Action == policy.ActionObserve
}

func httpPolicyMetadata(req *stdhttp.Request, network string, trustedProxyRanges []string) (policy.CanonicalMetadata, error) {
	physicalPeer := httpRemoteAddr{network: network, address: req.RemoteAddr}
	direct, err := policy.NewDirectMetadata(physicalPeer)
	if err != nil {
		return policy.CanonicalMetadata{}, err
	}
	allowlist, err := policy.NewTrustedPeerAllowlist(trustedProxyRanges)
	if err != nil {
		return policy.CanonicalMetadata{}, err
	}
	forwardedValues := req.Header.Values("X-Forwarded-For")
	if len(forwardedValues) == 0 || !allowlist.Contains(physicalPeer) {
		return direct, nil
	}

	hops := strings.Split(strings.Join(forwardedValues, ","), ",")
	var canonicalSource net.Addr
	currentHop := net.Addr(physicalPeer)
	for index := len(hops) - 1; index >= 0 && allowlist.Contains(currentHop); index-- {
		hop := strings.TrimSpace(hops[index])
		address, parseErr := netip.ParseAddr(hop)
		if parseErr != nil || !address.IsValid() || address.IsUnspecified() {
			return policy.CanonicalMetadata{}, fmt.Errorf("invalid trusted X-Forwarded-For hop %q", hop)
		}
		address = address.Unmap()
		canonicalSource = &net.TCPAddr{IP: net.IP(address.AsSlice())}
		currentHop = canonicalSource
	}
	if canonicalSource == nil {
		return policy.CanonicalMetadata{}, errors.New("trusted X-Forwarded-For chain is empty")
	}
	return policy.NewAuthenticatedMetadata(policy.SourceTrustedProxy, physicalPeer, canonicalSource)
}

func prepareHTTPPolicyBodyWindow(req *stdhttp.Request) (policy.BodyWindow, error) {
	if req == nil || req.Body == nil || req.Body == stdhttp.NoBody {
		return policy.NewBodyWindow(nil, true, policy.BodyNotSkipped)
	}

	readLimit := int64(httpPolicyBodyWindowBytes + 1)
	if req.ContentLength > httpPolicyBodyWindowBytes {
		readLimit = httpPolicyBodyWindowBytes
	}
	source := req.Body
	prefix, err := io.ReadAll(io.LimitReader(source, readLimit))
	if err != nil {
		_ = source.Close()
		return policy.BodyWindow{}, err
	}
	req.Body = &closeWrappedReader{
		Reader: io.MultiReader(bytes.NewReader(prefix), source),
		Closer: source,
	}

	if req.ContentLength > httpPolicyBodyWindowBytes || len(prefix) > httpPolicyBodyWindowBytes {
		if len(prefix) > httpPolicyBodyWindowBytes {
			prefix = prefix[:httpPolicyBodyWindowBytes]
		}
		return policy.NewBodyWindow(prefix, false, policy.BodyLimitExceeded)
	}
	return policy.NewBodyWindow(prefix, true, policy.BodyNotSkipped)
}

func httpPolicyFields(req *stdhttp.Request) (map[string][]byte, error) {
	if req == nil {
		return nil, nil
	}
	method, err := exactHTTPPolicyField(req.Method, 32, "method")
	if err != nil {
		return nil, err
	}
	host, err := exactHTTPPolicyField(req.Host, httpPolicyFieldValueBytes, "host")
	if err != nil {
		return nil, err
	}
	scheme, err := exactHTTPPolicyField(requestScheme(req), 16, "scheme")
	if err != nil {
		return nil, err
	}
	fields := map[string][]byte{
		policy.FieldRequestMethod: method,
		policy.FieldRequestHost:   host,
		policy.FieldRequestScheme: scheme,
		policy.FieldFlowNew:       []byte("true"),
	}
	if req.URL != nil {
		fields[policy.FieldRequestPath], err = exactHTTPPolicyField(req.URL.EscapedPath(), httpPolicyFieldValueBytes, "path")
		if err != nil {
			return nil, err
		}
		fields[policy.FieldRequestQuery], err = exactHTTPPolicyField(req.URL.RawQuery, httpPolicyFieldValueBytes, "query")
		if err != nil {
			return nil, err
		}
	}
	if err := projectHTTPPolicyHeaders(fields, req.Header); err != nil {
		return nil, err
	}
	return fields, nil
}

func projectHTTPPolicyHeaders(fields map[string][]byte, headers stdhttp.Header) error {
	if len(headers) == 0 {
		return nil
	}
	rawNames := make([]string, 0, len(headers))
	for name := range headers {
		rawNames = append(rawNames, name)
	}
	sort.Strings(rawNames)
	valuesByField := make(map[string][]string, len(rawNames))
	for _, rawName := range rawNames {
		field, ok := policy.CanonicalHTTPHeaderField(rawName)
		if !ok {
			return fmt.Errorf("header name %q cannot be projected completely", rawName)
		}
		valuesByField[field] = append(valuesByField[field], headers[rawName]...)
	}

	canonicalNames := make([]string, 0, len(valuesByField))
	for name := range valuesByField {
		canonicalNames = append(canonicalNames, name)
	}
	sort.Strings(canonicalNames)
	remaining := httpPolicyHeadersBytes
	for _, name := range canonicalNames {
		value := strings.Join(valuesByField[name], "\n")
		if len(value) > httpPolicyHeaderValueBytes {
			return fmt.Errorf("header %q exceeds the policy value projection bound", name)
		}
		projectedBytes := len(name) + len(value)
		if projectedBytes > remaining {
			return errors.New("aggregate headers exceed the policy projection bound")
		}
		fields[name] = []byte(value)
		remaining -= projectedBytes
	}
	return nil
}

func exactHTTPPolicyField(value string, limit int, name string) ([]byte, error) {
	if limit < 0 {
		limit = 0
	}
	if len(value) > limit {
		return nil, fmt.Errorf("%s exceeds the policy projection bound", name)
	}
	return []byte(value), nil
}

func writeHTTPPolicyDecision(w stdhttp.ResponseWriter, decision policy.Decision) {
	status := decision.StatusCode
	if status < 400 || status > 599 {
		status = stdhttp.StatusForbidden
	}
	stdhttp.Error(w, "request denied by policy", status)
}
