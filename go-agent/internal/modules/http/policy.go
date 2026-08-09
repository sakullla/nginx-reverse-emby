package http

import (
	"bytes"
	"context"
	"io"
	stdhttp "net/http"
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
	httpPolicyHeaderValueBytes = 4 << 10
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
	metadata, err := policy.NewDirectMetadata(httpRemoteAddr{network: network, address: req.RemoteAddr})
	if err != nil {
		return policy.Decision{Action: policy.ActionDeny, StatusCode: stdhttp.StatusServiceUnavailable, Reason: "invalid-source", Degraded: true}, false
	}
	body, err := prepareHTTPPolicyBodyWindow(req)
	if err != nil {
		return policy.Decision{Action: policy.ActionDeny, StatusCode: stdhttp.StatusServiceUnavailable, Reason: "body-window", Degraded: true}, false
	}
	input, err := policy.NewInput(policy.ExtensionHTTP, httpPolicyRequestID(req.Context()), metadata, httpPolicyFields(req), body)
	if err != nil {
		return policy.Decision{Action: policy.ActionDeny, StatusCode: stdhttp.StatusServiceUnavailable, Reason: "invalid-input", Degraded: true}, false
	}
	decision := s.policyEvaluator.Evaluate(req.Context(), rule.PolicyRef, input)
	return decision, decision.Action == policy.ActionAllow || decision.Action == policy.ActionObserve
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

func httpPolicyFields(req *stdhttp.Request) map[string][]byte {
	if req == nil {
		return nil
	}
	fields := map[string][]byte{
		policy.FieldRequestMethod: boundedHTTPPolicyField(req.Method, 32),
		policy.FieldRequestHost:   boundedHTTPPolicyField(req.Host, 1024),
		policy.FieldRequestScheme: boundedHTTPPolicyField(requestScheme(req), 16),
		policy.FieldFlowNew:       []byte("true"),
	}
	if req.URL != nil {
		fields[policy.FieldRequestPath] = boundedHTTPPolicyField(req.URL.EscapedPath(), 8<<10)
		fields[policy.FieldRequestQuery] = boundedHTTPPolicyField(req.URL.RawQuery, 8<<10)
	}
	projectHTTPPolicyHeaders(fields, req.Header)
	return fields
}

func projectHTTPPolicyHeaders(fields map[string][]byte, headers stdhttp.Header) {
	if len(headers) == 0 {
		return
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
			continue
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
		if len(name) >= remaining {
			break
		}
		valueLimit := httpPolicyHeaderValueBytes
		if available := remaining - len(name); valueLimit > available {
			valueLimit = available
		}
		fields[name] = boundedHTTPPolicyField(strings.Join(valuesByField[name], "\n"), valueLimit)
		remaining -= len(name) + len(fields[name])
	}
}

func boundedHTTPPolicyField(value string, limit int) []byte {
	if limit < 0 {
		limit = 0
	}
	if len(value) > limit {
		value = value[:limit]
	}
	return []byte(value)
}

func writeHTTPPolicyDecision(w stdhttp.ResponseWriter, decision policy.Decision) {
	status := decision.StatusCode
	if status < 400 || status > 599 {
		status = stdhttp.StatusForbidden
	}
	stdhttp.Error(w, "request denied by policy", status)
}
