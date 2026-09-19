//go:build integration

package http

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/hostapi"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/wasm"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/wasm/testfixture"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type socketPolicyFactory struct {
	wasm.GenerationFactory
	mu        sync.Mutex
	responses map[string]policy.ModuleResponse
	errors    map[string]error
}

// This recording audit owner exercises authorization acknowledgement only.
// Disk durability/latency belongs to the production journal's separate tests.
type socketPolicyAudit struct {
	mu     sync.Mutex
	events []hostapi.AuditEvent
	reject bool
}

func (a *socketPolicyAudit) Audit(_ context.Context, event hostapi.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, event)
	if a.reject {
		return errors.New("fixture audit acknowledgement rejected")
	}
	return nil
}

func (factory *socketPolicyFactory) PrepareGeneration(ctx context.Context, spec policy.GenerationSpec) (policy.GenerationRuntime, error) {
	runtime, err := factory.GenerationFactory.PrepareGeneration(ctx, spec)
	if err != nil {
		return nil, err
	}
	return &socketPolicyRuntime{GenerationRuntime: runtime, factory: factory}, nil
}

type socketPolicyRuntime struct {
	policy.GenerationRuntime
	factory *socketPolicyFactory
}

func (runtime *socketPolicyRuntime) Evaluate(ctx context.Context, request policy.ModuleRequest) (policy.ModuleResponse, error) {
	response, err := runtime.GenerationRuntime.Evaluate(ctx, request)
	runtime.factory.mu.Lock()
	runtime.factory.responses[request.InstanceID] = response
	if runtime.factory.errors == nil {
		runtime.factory.errors = map[string]error{}
	}
	runtime.factory.errors[request.InstanceID] = err
	runtime.factory.mu.Unlock()
	return response, err
}

func TestIntegrationHTTPWASMSourceAndComposedModes(t *testing.T) {
	cases := []struct {
		name   string
		stages []testfixture.Stage
		status int
	}{
		{"observe IP", []testfixture.Stage{{Kind: model.PolicyKindIP, Mode: sdk.PolicyModeObserve, Action: sdk.PolicyActionDeny}}, 200},
		{"enforce IP", []testfixture.Stage{{Kind: model.PolicyKindIP, Mode: sdk.PolicyModeEnforce, Action: sdk.PolicyActionDeny}}, 403},
		{"observe IP preserves WAF", []testfixture.Stage{{Kind: model.PolicyKindIP, Mode: sdk.PolicyModeObserve, Action: sdk.PolicyActionDeny}, {Kind: model.PolicyKindWAF, Mode: sdk.PolicyModeEnforce, Action: sdk.PolicyActionDeny, Handling: sdk.PolicyModeHandlingLegacyWAF}}, 403},
		{"IP deny precedes observed WAF", []testfixture.Stage{{Kind: model.PolicyKindIP, Mode: sdk.PolicyModeEnforce, Action: sdk.PolicyActionDeny}, {Kind: model.PolicyKindWAF, Mode: sdk.PolicyModeObserve, Action: sdk.PolicyActionDeny, Handling: sdk.PolicyModeHandlingLegacyWAF}}, 403},
		{"WAF requires audit acknowledgement", []testfixture.Stage{{Kind: model.PolicyKindIP, Mode: sdk.PolicyModeObserve, Action: sdk.PolicyActionDeny}, {Kind: model.PolicyKindWAF, Mode: sdk.PolicyModeEnforce, Action: sdk.PolicyActionDeny, Handling: sdk.PolicyModeHandlingLegacyWAF}}, 503},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			runtime, err := wasm.NewRuntime(t.Context(), wasm.RuntimeOptions{MaxMemoryPages: 16})
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close(context.Background())
			factory := &socketPolicyFactory{GenerationFactory: wasm.GenerationFactory{Runtime: runtime}, responses: map[string]policy.ModuleResponse{}}
			snapshot, err := testfixture.Snapshot(t.Context(), t.TempDir(), 1, "192.0.2.0/24", test.stages)
			if err != nil {
				t.Fatal(err)
			}
			rule := model.HTTPRule{ID: 7, Enabled: true, PolicyRef: &model.PolicyRef{ID: "effective"}, TrustedProxyRanges: []string{"127.0.0.0/8"}}
			snapshot.Rules = []model.HTTPRule{rule}
			registry := module.NewRegistry()
			audit := &socketPolicyAudit{reject: test.name == "WAF requires audit acknowledgement"}
			observer := observability.CapabilityAuditObserver{Observer: observability.Default(), Auditor: audit}
			if err := registry.Register(policy.NewModule(factory, observer)); err != nil {
				t.Fatal(err)
			}
			identity, err := module.NewGenerationContext(model.Snapshot{}, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := registry.PrepareGeneration(t.Context(), identity)
			if err != nil {
				t.Fatal(err)
			}
			if err := candidate.Ready(t.Context()); err != nil {
				t.Fatal(err)
			}
			view, _ := candidate.Publish()
			defer view.Destroy(context.Background())
			value, _ := view.Resolve(policy.ProviderEvaluator)
			server := &Server{policyEvaluator: value.(policy.Evaluator)}
			var upstreamCalls atomic.Int32
			var decisionMu sync.Mutex
			var last policy.Decision
			frontend := httptest.NewServer(stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
				decision, allowed := server.allowPolicyRequest(request, rule)
				decisionMu.Lock()
				last = decision
				decisionMu.Unlock()
				if !allowed {
					writer.WriteHeader(decision.StatusCode)
					return
				}
				upstreamCalls.Add(1)
				writer.Write([]byte("business-response"))
			}))
			defer frontend.Close()
			request, _ := stdhttp.NewRequestWithContext(t.Context(), stdhttp.MethodGet, frontend.URL, nil)
			request.Header.Set("X-Forwarded-For", "192.0.2.7")
			response, err := frontend.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != test.status {
				factory.mu.Lock()
				defer factory.mu.Unlock()
				t.Fatalf("actual HTTP status=%d want%d, fixture runtime errors=%v", response.StatusCode, test.status, factory.errors)
			}
			factory.mu.Lock()
			payload := append([]byte(nil), factory.responses["fixture-ip"].Payload...)
			factory.mu.Unlock()
			status, sourceFrame, err := testfixture.Slot(payload, 2)
			if err != nil || status != sdk.PolicyStatusOK {
				t.Fatal("WASM source import did not execute", err)
			}
			source, err := sdk.UnmarshalPolicyTrustedSourceResponse(sourceFrame, 4096)
			if err != nil || source.Source == nil {
				t.Fatal(err)
			}
			if err := source.Source.ValidateFor("fixture-ip", view.ID(), "7"); err != nil || source.Source.Authority != sdk.PolicySourceXFF || source.Source.SourceAddress.String() != "192.0.2.7" || source.Source.PeerAddress.String() != "127.0.0.1" {
				t.Fatal("socket/XFF source was not authentically bound", err)
			}
			resolveStatus, frame, _ := testfixture.Slot(payload, 0)
			resolved, err := sdk.UnmarshalPolicyDatasetResolveResponse(frame, testfixture.ResolveRequest("regions"))
			if resolveStatus != sdk.PolicyStatusOK || err != nil || resolved.Reference == nil {
				t.Fatal("real init resolver unavailable", err)
			}
			queryStatus, queryFrame, _ := testfixture.Slot(payload, 1)
			query, err := sdk.UnmarshalPolicyDatasetQueryResponse(queryFrame, testfixture.QueryRequest(*resolved.Reference, "cn-44"))
			if err != nil || queryStatus != sdk.PolicyStatusOK || !query.Matches[0].Matched {
				t.Fatal("guest did not query authenticated XFF against real index", err)
			}
			if test.status == 200 && upstreamCalls.Load() != 1 || test.status != 200 && upstreamCalls.Load() != 0 {
				t.Fatal("denied request reached business")
			}
			if test.name == "observe IP preserves WAF" || audit.reject {
				audit.mu.Lock()
				events := append([]hostapi.AuditEvent(nil), audit.events...)
				audit.mu.Unlock()
				found := false
				for _, event := range events {
					found = found || event.Call.Validate() == nil && event.Call.InstanceID == "fixture-waf" && event.Call.Generation == view.ID() && event.Call.Capability == sdk.CapabilityPolicyTrustedSource && event.Outcome == "allowed"
				}
				if !found {
					t.Fatal("WAF normalized HTTP bypassed authorization acknowledgement")
				}
				if audit.reject {
					factory.mu.Lock()
					failure := factory.errors["fixture-waf"]
					factory.mu.Unlock()
					if failure == nil || !strings.Contains(failure.Error(), "normalized-http") {
						t.Fatal("503 did not come from rejected audit acknowledgement", failure)
					}
				}
			}

			request, _ = stdhttp.NewRequestWithContext(t.Context(), stdhttp.MethodGet, frontend.URL, nil)
			request.Header.Set("X-Forwarded-For", "not-an-ip")
			response, err = frontend.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			decisionMu.Lock()
			failed := last
			decisionMu.Unlock()
			if !failed.Degraded || !failed.Observed {
				t.Fatalf("source check failure was hidden: %+v", failed)
			}
			if test.status == 200 {
				if response.StatusCode != 200 {
					t.Fatal("observe source failure blocked business")
				}
			} else if response.StatusCode < 400 {
				t.Fatal("observe stage weakened remaining enforcement after source failure")
			}
		})
	}
}
