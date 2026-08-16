//go:build !integration

package policy

import (
	"bytes"
	"context"
	"errors"

	"log/slog"
	"net"
	"reflect"

	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/hostapi"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type scriptedModule struct {
	calls     []model.PolicyKind
	responses map[model.PolicyKind]ModuleResponse
	errors    map[model.PolicyKind]error
	inspect   func(ModuleRequest)
}

type acknowledgedCapabilityAudit struct{ err error }

func (audit acknowledgedCapabilityAudit) Audit(context.Context, hostapi.AuditEvent) error {
	return audit.err
}

type eventExfiltrationModule struct {
	headerSecret string
	bodySecret   string
	errors       []error
}

func (module *eventExfiltrationModule) Evaluate(ctx context.Context, request ModuleRequest) (ModuleResponse, error) {
	module.errors = append(module.errors,
		request.Host.EmitEvent(ctx, pluginsdk.PolicySecurityEvent{Code: 200, Action: pluginsdk.PolicySecurityEventActionDeny}),
		request.Host.EmitEvent(ctx, pluginsdk.PolicySecurityEvent{Code: pluginsdk.PolicySecurityEventCodeWAFRuleMatch, Action: 200}),
		request.Host.EmitEvent(ctx, pluginsdk.PolicySecurityEvent{Code: pluginsdk.PolicySecurityEventCodeWAFRuleMatch, Action: pluginsdk.PolicySecurityEventActionObserve}),
	)
	// Keep the request-derived values live in the guest implementation: the
	// typed event surface provides no string/byte field in which to place them.
	_ = module.headerSecret
	_ = module.bodySecret
	return ModuleResponse{Action: ActionAllow}, nil
}

func (module *scriptedModule) Evaluate(_ context.Context, request ModuleRequest) (ModuleResponse, error) {
	module.calls = append(module.calls, request.PolicyKind)
	if module.inspect != nil {
		module.inspect(request)
	}
	if err := module.errors[request.PolicyKind]; err != nil {
		return ModuleResponse{}, err
	}
	if response, ok := module.responses[request.PolicyKind]; ok {
		return response, nil
	}
	return ModuleResponse{Action: ActionAllow}, nil
}

func TestPolicyChainRunsIPRateWAFInStrictOrderAndStopsOnRateDeny(t *testing.T) {
	module := &scriptedModule{responses: map[model.PolicyKind]ModuleResponse{
		model.PolicyKindIP:   {Action: ActionAllow},
		model.PolicyKindRate: {Action: ActionDeny},
		model.PolicyKindWAF:  {Action: ActionDeny},
	}}
	evaluator, err := NewGenerationEvaluator("generation-1", []model.PluginPolicy{testPolicy("shared", model.PolicyKindIP, model.PolicyKindRate, model.PolicyKindWAF)}, module, nil)
	if err != nil {
		t.Fatalf("NewGenerationEvaluator() error = %v", err)
	}
	input := testInput(t, ExtensionHTTP, nil, testCompleteBody(t, nil))
	decision := evaluator.Evaluate(context.Background(), &model.PolicyRef{ID: "shared"}, input)
	if decision.Action != ActionDeny || decision.StatusCode != 429 || decision.Stage != model.PolicyKindRate {
		t.Fatalf("decision = %+v", decision)
	}
	want := []model.PolicyKind{model.PolicyKindIP, model.PolicyKindRate}
	if !reflect.DeepEqual(module.calls, want) {
		t.Fatalf("calls = %v, want %v", module.calls, want)
	}
}

func TestPolicyFailureOnlyAffectsDependentTrafficAndHonorsFailureMode(t *testing.T) {
	moduleErr := RuntimeError("trap", errors.New("guest failed"))
	module := &scriptedModule{errors: map[model.PolicyKind]error{model.PolicyKindIP: moduleErr}}
	openPolicy := testPolicy("open", model.PolicyKindIP)
	closedPolicy := testPolicy("closed", model.PolicyKindIP)
	closedPolicy.Stages[0].FailurePolicy.OnError = "fail-closed"
	evaluator, err := NewGenerationEvaluator("generation-1", []model.PluginPolicy{openPolicy, closedPolicy}, module, nil)
	if err != nil {
		t.Fatalf("NewGenerationEvaluator() error = %v", err)
	}
	input := testInput(t, ExtensionHTTP, nil, testCompleteBody(t, nil))

	unrelated := evaluator.Evaluate(context.Background(), nil, input)
	if unrelated.Action != ActionAllow || unrelated.Degraded || len(module.calls) != 0 {
		t.Fatalf("unrelated decision/calls = %+v/%v", unrelated, module.calls)
	}
	open := evaluator.Evaluate(context.Background(), &model.PolicyRef{ID: "open"}, input)
	if open.Action != ActionAllow || !open.Degraded || open.Reason != "trap" {
		t.Fatalf("fail-open decision = %+v", open)
	}
	closed := evaluator.Evaluate(context.Background(), &model.PolicyRef{ID: "closed"}, input)
	if closed.Action != ActionDeny || closed.StatusCode != 503 || !closed.Degraded {
		t.Fatalf("fail-closed decision = %+v", closed)
	}
}

func TestPolicyBudgetFailureIsObservableAndFailOpenByDefault(t *testing.T) {
	var events []observability.Event
	observer := observability.ObserverFunc(func(_ context.Context, event observability.Event) { events = append(events, event) })
	module := &scriptedModule{errors: map[model.PolicyKind]error{model.PolicyKindWAF: BudgetError("deadline", context.DeadlineExceeded)}}
	evaluator, err := NewGenerationEvaluator("generation-budget", []model.PluginPolicy{testPolicy("waf", model.PolicyKindWAF)}, module, observer)
	if err != nil {
		t.Fatalf("NewGenerationEvaluator() error = %v", err)
	}
	decision := evaluator.Evaluate(context.Background(), &model.PolicyRef{ID: "waf"}, testInput(t, ExtensionHTTP, nil, testCompleteBody(t, nil)))
	if decision.Action != ActionAllow || !decision.Degraded || decision.Reason != "deadline" {
		t.Fatalf("decision = %+v", decision)
	}
	if len(events) != 1 || events[0].Name != observability.PolicyBudget || events[0].Outcome != "exhausted" || events[0].PolicyStage != "waf" {
		t.Fatalf("events = %+v", events)
	}
}

func TestPolicyGenerationStateIsInstanceScopedAndCopiesValues(t *testing.T) {
	state := newGenerationState()
	if err := state.put("one", "bucket", []byte("value")); err != nil {
		t.Fatalf("put() error = %v", err)
	}
	value, ok := state.get("one", "bucket")
	if !ok || string(value) != "value" {
		t.Fatalf("get(one) = %q, %v", value, ok)
	}
	value[0] = 'X'
	again, _ := state.get("one", "bucket")
	if string(again) != "value" {
		t.Fatalf("stored value was aliased: %q", again)
	}
	if _, ok := state.get("two", "bucket"); ok {
		t.Fatal("state leaked across plugin instances")
	}
	if err := state.put("one", "large", make([]byte, maxStateValueBytes+1)); failureKind(err) != FailureBudget {
		t.Fatalf("oversized state error = %v", err)
	}
}

func TestPolicyHostAPIsRequireExplicitGrantedScopes(t *testing.T) {
	input := testInput(t, ExtensionHTTP, nil, testCompleteBody(t, nil))
	host := &requestHost{input: input, generationID: "generation-1", instanceID: "instance", state: newGenerationState(), stage: model.PolicyStage{PluginID: "official.policy", ResourceGroupID: "group-a"}, capabilityAuditor: acknowledgedCapabilityAudit{}}
	if _, err := host.ReadField(context.Background(), FieldRequestPath); !isPermissionDenied(err) {
		t.Fatalf("ReadField() error = %v", err)
	}
	if err := host.StatePut(context.Background(), "bucket", []byte("value")); !isPermissionDenied(err) {
		t.Fatalf("StatePut() error = %v", err)
	}
	if err := host.EmitEvent(context.Background(), pluginsdk.PolicySecurityEvent{Code: pluginsdk.PolicySecurityEventCodeWAFRuleMatch, Action: pluginsdk.PolicySecurityEventActionDeny}); !isPermissionDenied(err) {
		t.Fatalf("EmitEvent() error = %v", err)
	}
	host.stage.DeclaredScopes = []string{string(pluginsdk.CapabilityPolicyAtomicState)}
	host.stage.GrantedScopes = []string{"http.inspect", "policy.read", "policy.write", "event.emit", string(pluginsdk.CapabilityPolicyAtomicState)}
	host.authorizer = nil
	if _, err := host.ReadField(context.Background(), FieldRequestPath); err != nil {
		t.Fatalf("granted ReadField() error = %v", err)
	}
	if err := host.StatePut(context.Background(), "bucket", []byte("value")); err != nil {
		t.Fatalf("granted StatePut() error = %v", err)
	}
}

func TestPolicyGuestCannotRelayHeaderOrBodySecretsToRecorder(t *testing.T) {
	headerSecret := "actual-header-credential-7c993"
	bodySecret := "actual-body-secret-1fa26"
	body, err := NewBodyWindow([]byte(bodySecret), true, BodyNotSkipped)
	if err != nil {
		t.Fatal(err)
	}
	input := testInput(t, ExtensionHTTP, map[string][]byte{
		"request.header.authorization": []byte(headerSecret),
	}, body)
	var log bytes.Buffer
	recorder := observability.NewRecorder(slog.New(slog.NewTextHandler(&log, nil)))
	policyDefinition := testPolicy("policy-1", model.PolicyKindWAF)
	policyDefinition.Stages[0].GrantedScopes = []string{"http.inspect", "event.emit"}
	guest := &eventExfiltrationModule{headerSecret: headerSecret, bodySecret: bodySecret}
	evaluator, err := NewGenerationEvaluator("generation-1", []model.PluginPolicy{policyDefinition}, guest, recorder)
	if err != nil {
		t.Fatal(err)
	}
	decision := evaluator.Evaluate(context.Background(), &model.PolicyRef{ID: "policy-1"}, input)
	if decision.Action != ActionAllow {
		t.Fatalf("decision = %+v", decision)
	}
	if len(guest.errors) != 3 || guest.errors[0] == nil || guest.errors[1] == nil || guest.errors[2] != nil {
		t.Fatalf("guest event errors = %v, want rejected/rejected/accepted", guest.errors)
	}
	output := log.String()
	if strings.Contains(output, headerSecret) || strings.Contains(output, bodySecret) {
		t.Fatalf("recorder leaked request-derived secret: %s", output)
	}
	for _, fixed := range []string{"security_code=waf.rule_match", "security_action=observe", `security_template="WAF rule matched"`} {
		if !strings.Contains(output, fixed) {
			t.Fatalf("recorder output missing host-owned field %q: %s", fixed, output)
		}
	}
}

func isPermissionDenied(err error) bool {
	var runtimeError *pluginsdk.RuntimeError
	return errors.As(err, &runtimeError) && runtimeError.Code == pluginsdk.ErrorPermissionDenied
}

func testPolicy(id string, kinds ...model.PolicyKind) model.PluginPolicy {
	policy := model.PluginPolicy{ID: id, Revision: 1}
	for _, kind := range kinds {
		extensions := []string{ExtensionHTTP, ExtensionL4}
		if kind == model.PolicyKindWAF {
			extensions = []string{ExtensionHTTP}
		}
		policy.Stages = append(policy.Stages, model.PolicyStage{
			Kind: kind, PolicyID: id + "-" + string(kind), PluginID: "official." + string(kind), PluginVersion: "1.0.0",
			InstanceID: id + "-instance-" + string(kind), PackageDigest: "package-digest", ArtifactPath: "verified/policy.wasm",
			ArtifactDigest: "artifact-digest", SignatureVerified: true, SignerKeyID: "official-release", SignerFingerprint: "signer-fingerprint",
			ABI: model.PolicyABIV1, ExtensionPoints: extensions,
			DeclaredScopes: []string{"policy.read", "policy.write", "event.emit", "http.inspect", "l4.inspect", string(pluginsdk.CapabilityPolicyAtomicState), string(pluginsdk.CapabilityPolicyMonotonicClock), string(pluginsdk.CapabilityPolicyTrustedSource)},
			GrantedScopes:  []string{"policy.read", string(pluginsdk.CapabilityPolicyAtomicState), string(pluginsdk.CapabilityPolicyMonotonicClock), string(pluginsdk.CapabilityPolicyTrustedSource)}, ResourceGroupID: "group-a",
			ResourceBudget: model.PolicyResourceBudget{TimeoutMS: 2, MemoryBytes: 1 << 20, Concurrency: 2, InputBytes: 4096, OutputBytes: 1024},
			FailurePolicy:  model.PolicyFailurePolicy{OnError: "fail-open", OnBudget: "fail-open", Restart: "never", CoreFallback: "preserve"},
		})
	}
	return policy
}

func testInput(t *testing.T, extension string, fields map[string][]byte, body BodyWindow) Input {
	t.Helper()
	metadata, err := NewDirectMetadata(&net.TCPAddr{IP: net.ParseIP("198.51.100.10"), Port: 50000})
	if err != nil {
		t.Fatalf("NewDirectMetadata() error = %v", err)
	}
	input, err := NewInput(extension, "request-1", metadata, fields, body)
	if err != nil {
		t.Fatalf("NewInput() error = %v", err)
	}
	return input
}

func testCompleteBody(t *testing.T, prefix []byte) BodyWindow {
	t.Helper()
	body, err := NewBodyWindow(prefix, true, BodyNotSkipped)
	if err != nil {
		t.Fatalf("NewBodyWindow() error = %v", err)
	}
	return body
}
