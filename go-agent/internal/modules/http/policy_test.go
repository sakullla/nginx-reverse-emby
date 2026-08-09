package http

import (
	"bytes"
	"context"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"
)

type httpPolicyEvaluatorFunc func(context.Context, *model.PolicyRef, policy.Input) policy.Decision

func (f httpPolicyEvaluatorFunc) Evaluate(ctx context.Context, ref *model.PolicyRef, input policy.Input) policy.Decision {
	return f(ctx, ref, input)
}

type httpPolicyProviderResolver map[module.ProviderRef]any

func (r httpPolicyProviderResolver) Resolve(ref module.ProviderRef) (any, bool) {
	value, ok := r[ref]
	return value, ok
}

type policyOrderingRegistrar struct {
	registered atomic.Bool
}

func (r *policyOrderingRegistrar) RegisterSession(string, generation.EntityKey, string, generation.Session) (*generation.SessionHandle, error) {
	r.registered.Store(true)
	return nil, nil
}

func TestWAFPolicyStreamingBodyWindowPreservesBodyAndTrustedSource(t *testing.T) {
	payload := bytes.Repeat([]byte("streaming-body-"), 16*1024)
	var captured policy.Input
	evaluator := httpPolicyEvaluatorFunc(func(_ context.Context, _ *model.PolicyRef, input policy.Input) policy.Decision {
		captured = input
		return policy.Decision{Action: policy.ActionAllow}
	})
	server := &Server{policyEvaluator: evaluator}
	req := httptest.NewRequest(stdhttp.MethodPost, "http://media.example.test/upload?token=ignored", bytes.NewReader(payload))
	req.RemoteAddr = "192.0.2.40:43210"
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	req.Header.Set("User-Agent", "policy-waf-agent")
	req.Header.Set("X-Large", string(bytes.Repeat([]byte("x"), httpPolicyHeaderValueBytes)))

	decision, allowed := server.allowPolicyRequest(req, model.HTTPRule{PolicyRef: &model.PolicyRef{ID: "waf-policy"}})
	if !allowed || decision.Action != policy.ActionAllow {
		t.Fatalf("policy decision = %+v, allowed=%v", decision, allowed)
	}
	metadata := captured.Metadata()
	if got := metadata.Source().String(); got != "192.0.2.40:43210" {
		t.Fatalf("trusted source = %q, want physical RemoteAddr", got)
	}
	fields := captured.Fields()
	if got := string(fields["request.header.user-agent"]); got != "policy-waf-agent" {
		t.Fatalf("WAF user-agent field = %q", got)
	}
	if got := string(fields["request.header.x-forwarded-for"]); got != "203.0.113.99" {
		t.Fatalf("WAF X-Forwarded-For field = %q", got)
	}
	if got := len(fields["request.header.x-large"]); got != httpPolicyHeaderValueBytes {
		t.Fatalf("bounded WAF header length = %d, want %d", got, httpPolicyHeaderValueBytes)
	}
	if got := captured.Body().Prefix(); len(got) != httpPolicyBodyWindowBytes || !bytes.Equal(got, payload[:httpPolicyBodyWindowBytes]) {
		t.Fatalf("policy body prefix length = %d, want %d matching bytes", len(got), httpPolicyBodyWindowBytes)
	}
	if captured.Body().Complete() || captured.Body().SkipReason() != policy.BodyLimitExceeded {
		t.Fatalf("body window = complete %v skip %q", captured.Body().Complete(), captured.Body().SkipReason())
	}
	rebuilt, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read rebuilt request body: %v", err)
	}
	if !bytes.Equal(rebuilt, payload) {
		t.Fatalf("rebuilt body length = %d, want %d exact bytes", len(rebuilt), len(payload))
	}
}

func TestIPPolicyTrustedSourceWalksXFFOnlyThroughTrustedPeers(t *testing.T) {
	var captured policy.Input
	evaluator := httpPolicyEvaluatorFunc(func(_ context.Context, _ *model.PolicyRef, input policy.Input) policy.Decision {
		captured = input
		return policy.Decision{Action: policy.ActionAllow}
	})
	server := &Server{policyEvaluator: evaluator}
	rule := model.HTTPRule{
		PolicyRef:          &model.PolicyRef{ID: "ip-policy"},
		TrustedProxyRanges: []string{"192.0.2.0/24", "10.0.0.0/8"},
	}

	req := httptest.NewRequest(stdhttp.MethodGet, "http://media.example.test/", nil)
	req.RemoteAddr = "192.0.2.40:43210"
	req.Header.Set("X-Forwarded-For", "203.0.113.99, 198.51.100.22")
	decision, allowed := server.allowPolicyRequest(req, rule)
	if !allowed || decision.Action != policy.ActionAllow {
		t.Fatalf("policy decision = %+v, allowed=%v", decision, allowed)
	}
	if got := captured.Metadata().Source().String(); got != "198.51.100.22:0" {
		t.Fatalf("forged left XFF source = %q, want first untrusted hop", got)
	}
	if captured.Metadata().Kind() != policy.SourceTrustedProxy {
		t.Fatalf("trusted source kind = %q", captured.Metadata().Kind())
	}

	req = httptest.NewRequest(stdhttp.MethodGet, "http://media.example.test/", nil)
	req.RemoteAddr = "10.0.0.8:43210"
	req.Header.Set("X-Forwarded-For", "198.51.100.44, 192.0.2.12")
	if _, allowed := server.allowPolicyRequest(req, rule); !allowed {
		t.Fatal("trusted multi-hop XFF request was denied")
	}
	if got := captured.Metadata().Source().String(); got != "198.51.100.44:0" {
		t.Fatalf("trusted multi-hop source = %q", got)
	}
}

func TestIPPolicyUntrustedOrMalformedXFFCannotForgeSource(t *testing.T) {
	var calls int
	var captured policy.Input
	evaluator := httpPolicyEvaluatorFunc(func(_ context.Context, _ *model.PolicyRef, input policy.Input) policy.Decision {
		calls++
		captured = input
		return policy.Decision{Action: policy.ActionAllow}
	})
	server := &Server{policyEvaluator: evaluator}
	rule := model.HTTPRule{PolicyRef: &model.PolicyRef{ID: "ip-policy"}, TrustedProxyRanges: []string{"10.0.0.0/8"}}

	untrusted := httptest.NewRequest(stdhttp.MethodGet, "http://media.example.test/", nil)
	untrusted.RemoteAddr = "192.0.2.40:43210"
	untrusted.Header.Set("X-Forwarded-For", "203.0.113.99")
	if _, allowed := server.allowPolicyRequest(untrusted, rule); !allowed {
		t.Fatal("untrusted peer request was denied instead of using its physical source")
	}
	if got := captured.Metadata().Source().String(); got != "192.0.2.40:43210" || captured.Metadata().Kind() != policy.SourceDirect {
		t.Fatalf("untrusted XFF metadata = %q/%q", got, captured.Metadata().Kind())
	}

	malformed := httptest.NewRequest(stdhttp.MethodGet, "http://media.example.test/", nil)
	malformed.RemoteAddr = "10.0.0.4:43210"
	malformed.Header.Set("X-Forwarded-For", "not-an-ip")
	decision, allowed := server.allowPolicyRequest(malformed, rule)
	if allowed || decision.Reason != "invalid-source" || calls != 1 {
		t.Fatalf("malformed trusted XFF decision/calls = %+v/%d", decision, calls)
	}
}

func TestWAFPolicyRejectsIncompleteSecurityFieldProjection(t *testing.T) {
	for name, mutate := range map[string]func(*stdhttp.Request){
		"malicious query suffix": func(req *stdhttp.Request) {
			req.URL.RawQuery = strings.Repeat("a", httpPolicyFieldValueBytes) + "<script>"
		},
		"header value suffix": func(req *stdhttp.Request) {
			req.Header.Set("X-Policy-Input", strings.Repeat("a", httpPolicyHeaderValueBytes)+"malicious-suffix")
		},
		"aggregate header overflow": func(req *stdhttp.Request) {
			for index := 0; index < 9; index++ {
				req.Header.Set(fmt.Sprintf("X-Overflow-%02d", index), strings.Repeat("a", httpPolicyHeaderValueBytes-8))
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			var calls int
			server := &Server{policyEvaluator: httpPolicyEvaluatorFunc(func(context.Context, *model.PolicyRef, policy.Input) policy.Decision {
				calls++
				return policy.Decision{Action: policy.ActionAllow}
			})}
			req := httptest.NewRequest(stdhttp.MethodGet, "http://media.example.test/library", nil)
			mutate(req)
			decision, allowed := server.allowPolicyRequest(req, model.HTTPRule{PolicyRef: &model.PolicyRef{ID: "waf-policy"}})
			if allowed || decision.Reason != "input-projection" || calls != 0 {
				t.Fatalf("overflow decision/allowed/calls = %+v/%v/%d", decision, allowed, calls)
			}
		})
	}
}

func TestWAFPolicyFieldProjectionFitsCompleteHostResponseBoundary(t *testing.T) {
	tests := map[string]struct {
		field    string
		exact    func(*stdhttp.Request)
		overflow func(*stdhttp.Request)
	}{
		"path": {
			field: policy.FieldRequestPath,
			exact: func(req *stdhttp.Request) {
				req.URL.Path = "/" + strings.Repeat("p", httpPolicyFieldValueBytes-1)
			},
			overflow: func(req *stdhttp.Request) {
				req.URL.Path = "/" + strings.Repeat("p", httpPolicyFieldValueBytes-1) + "<script>"
			},
		},
		"query": {
			field: policy.FieldRequestQuery,
			exact: func(req *stdhttp.Request) {
				req.URL.RawQuery = strings.Repeat("q", httpPolicyFieldValueBytes)
			},
			overflow: func(req *stdhttp.Request) {
				req.URL.RawQuery = strings.Repeat("q", httpPolicyFieldValueBytes) + "<script>"
			},
		},
		"host": {
			field: policy.FieldRequestHost,
			exact: func(req *stdhttp.Request) {
				req.Host = strings.Repeat("h", httpPolicyFieldValueBytes)
			},
			overflow: func(req *stdhttp.Request) {
				req.Host = strings.Repeat("h", httpPolicyFieldValueBytes) + ".evil"
			},
		},
		"header": {
			field: "request.header.x-policy-boundary",
			exact: func(req *stdhttp.Request) {
				req.Header.Set("X-Policy-Boundary", strings.Repeat("h", httpPolicyFieldValueBytes))
			},
			overflow: func(req *stdhttp.Request) {
				req.Header.Set("X-Policy-Boundary", strings.Repeat("h", httpPolicyFieldValueBytes)+"malicious-suffix")
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var captured policy.Input
			calls := 0
			server := &Server{policyEvaluator: httpPolicyEvaluatorFunc(func(_ context.Context, _ *model.PolicyRef, input policy.Input) policy.Decision {
				calls++
				captured = input
				return policy.Decision{Action: policy.ActionAllow}
			})}
			rule := model.HTTPRule{PolicyRef: &model.PolicyRef{ID: "waf-policy"}}
			exact := httptest.NewRequest(stdhttp.MethodGet, "http://media.example.test/library", nil)
			test.exact(exact)
			if decision, allowed := server.allowPolicyRequest(exact, rule); !allowed {
				t.Fatalf("exact boundary denied: %+v", decision)
			}
			if got := len(captured.Fields()[test.field]); got != httpPolicyFieldValueBytes {
				t.Fatalf("projected field length = %d, want %d", got, httpPolicyFieldValueBytes)
			}

			overflow := httptest.NewRequest(stdhttp.MethodGet, "http://media.example.test/library", nil)
			test.overflow(overflow)
			decision, allowed := server.allowPolicyRequest(overflow, rule)
			if allowed || decision.Reason != "input-projection" || calls != 1 {
				t.Fatalf("overflow decision/allowed/calls = %+v/%v/%d", decision, allowed, calls)
			}
		})
	}
}

func TestGenerationPolicyRunsAfterHTTPSessionRegistrationBeforeUpstream(t *testing.T) {
	registrar := &policyOrderingRegistrar{}
	evaluator := httpPolicyEvaluatorFunc(func(_ context.Context, _ *model.PolicyRef, input policy.Input) policy.Decision {
		if !registrar.registered.Load() {
			t.Fatal("policy evaluated before HTTP session registration")
		}
		if input.RequestID() == "" {
			t.Fatal("generation request id is empty")
		}
		return policy.Decision{Action: policy.ActionDeny, StatusCode: stdhttp.StatusForbidden, Reason: "test-deny"}
	})
	rule := model.HTTPRule{
		ID: 1, FrontendURL: "http://media.example.test", Backends: []model.HTTPBackend{{URL: "http://127.0.0.1:1"}},
		PolicyRef: &model.PolicyRef{ID: "ip-policy"}, Enabled: true,
	}
	server, err := newServer(model.HTTPListener{Rules: []model.HTTPRule{rule}}, nil, Providers{PolicyEvaluator: evaluator}, model.NewCache(model.BackendCacheConfig{}), NewSharedTransport())
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}
	handler := &generationHTTPHandler{
		server:  server,
		tracker: newHTTPSessionTracker("generation-policy", registrar, true),
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(stdhttp.MethodGet, "http://media.example.test/library", nil)
	handler.serveActive(recorder, req)
	if recorder.Code != stdhttp.StatusForbidden {
		t.Fatalf("status = %d, want policy deny before upstream", recorder.Code)
	}
}

func TestGenerationProviderPolicyEvaluatorIsResolvedWithoutAffectingUnconfiguredRules(t *testing.T) {
	evaluator := httpPolicyEvaluatorFunc(func(context.Context, *model.PolicyRef, policy.Input) policy.Decision {
		return policy.Decision{Action: policy.ActionDeny}
	})
	providers, err := new(Module).runtimeProviders(httpPolicyProviderResolver{module.ProviderPolicyEvaluator: evaluator}, nil)
	if err != nil {
		t.Fatalf("runtimeProviders() error = %v", err)
	}
	if providers.PolicyEvaluator == nil {
		t.Fatal("generation policy evaluator was not resolved")
	}

	server := &Server{policyEvaluator: evaluator}
	req := httptest.NewRequest(stdhttp.MethodGet, "http://media.example.test/", nil)
	decision, allowed := server.allowPolicyRequest(req, model.HTTPRule{})
	if !allowed || decision.Action != policy.ActionAllow {
		t.Fatalf("unconfigured rule decision = %+v, allowed=%v", decision, allowed)
	}
}
