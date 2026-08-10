package policy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"reflect"
	"strconv"
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

func TestPolicyConstructionRejectsOutOfOrderAndUnsignedStages(t *testing.T) {
	for name, policy := range map[string]model.PluginPolicy{
		"out of order": testPolicy("bad-order", model.PolicyKindRate, model.PolicyKindIP),
		"unsigned": func() model.PluginPolicy {
			policy := testPolicy("unsigned", model.PolicyKindIP)
			policy.Stages[0].SignatureVerified = false
			return policy
		}(),
		"concurrency over ceiling": func() model.PluginPolicy {
			policy := testPolicy("concurrency", model.PolicyKindIP)
			policy.Stages[0].ResourceBudget.Concurrency = MaxPolicyConcurrency + 1
			return policy
		}(),
		"missing resource group": func() model.PluginPolicy {
			policy := testPolicy("missing-group", model.PolicyKindIP)
			policy.Stages[0].ResourceGroupID = ""
			return policy
		}(),
		"duplicate declaration": func() model.PluginPolicy {
			policy := testPolicy("duplicate-declaration", model.PolicyKindIP)
			policy.Stages[0].DeclaredScopes = append(policy.Stages[0].DeclaredScopes, policy.Stages[0].DeclaredScopes[0])
			return policy
		}(),
		"noncanonical grant": func() model.PluginPolicy {
			policy := testPolicy("noncanonical-grant", model.PolicyKindIP)
			policy.Stages[0].GrantedScopes = append(policy.Stages[0].GrantedScopes, " policy.read")
			return policy
		}(),
		"grant outside declaration": func() model.PluginPolicy {
			policy := testPolicy("excess-grant", model.PolicyKindIP)
			policy.Stages[0].GrantedScopes = append(policy.Stages[0].GrantedScopes, "storage.write")
			return policy
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewGenerationEvaluator("generation-1", []model.PluginPolicy{policy}, &scriptedModule{}, nil); err == nil {
				t.Fatal("NewGenerationEvaluator() error = nil")
			}
		})
	}
}

func TestPolicyStageCloneOwnsDeclaredAndGrantedScopeMemory(t *testing.T) {
	original := testPolicy("clone-scopes", model.PolicyKindIP)
	cloned, err := validateAndClonePolicy(original)
	if err != nil {
		t.Fatal(err)
	}
	original.Stages[0].DeclaredScopes[0] = "storage.write"
	original.Stages[0].GrantedScopes[0] = "storage.write"
	if cloned.Stages[0].DeclaredScopes[0] == "storage.write" || cloned.Stages[0].GrantedScopes[0] == "storage.write" {
		t.Fatal("validated stage aliases caller-owned declaration or grant slices")
	}
}

func TestPolicyChainsMayReuseOneIdenticalAuthorityInstance(t *testing.T) {
	httpPolicy := testPolicy("http-chain", model.PolicyKindIP, model.PolicyKindRate, model.PolicyKindWAF)
	l4Policy := testPolicy("l4-chain", model.PolicyKindIP, model.PolicyKindRate)
	l4Policy.Stages[0] = cloneStage(httpPolicy.Stages[0])
	l4Policy.Stages[1] = cloneStage(httpPolicy.Stages[1])
	if _, err := NewGenerationEvaluator("generation-shared", []model.PluginPolicy{httpPolicy, l4Policy}, &scriptedModule{}, nil); err != nil {
		t.Fatalf("shared authority instance was rejected: %v", err)
	}
	l4Policy.Stages[0].ArtifactDigest = "conflicting-digest"
	if _, err := NewGenerationEvaluator("generation-conflict", []model.PluginPolicy{httpPolicy, l4Policy}, &scriptedModule{}, nil); err == nil {
		t.Fatal("conflicting shared authority instance was accepted")
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

func TestPolicyBodyWindowAndCanonicalSourceAreBoundedHostOwnedInputs(t *testing.T) {
	body, err := NewBodyWindow([]byte("prefix"), false, BodyStreaming)
	if err != nil {
		t.Fatalf("NewBodyWindow() error = %v", err)
	}
	metadata, err := NewAuthenticatedMetadata(SourceTrustedProxy,
		&net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 443},
		&net.TCPAddr{IP: net.ParseIP("198.51.100.25"), Port: 50000},
	)
	if err != nil {
		t.Fatalf("NewAuthenticatedMetadata() error = %v", err)
	}
	if _, err := NewInput(ExtensionHTTP, "request-1", metadata, map[string][]byte{"source.ip": []byte("203.0.113.99")}, body); err == nil {
		t.Fatal("spoofed host-owned source field was accepted")
	}
	input, err := NewInput(ExtensionHTTP, "request-1", metadata, map[string][]byte{FieldRequestPath: []byte("/upload")}, body)
	if err != nil {
		t.Fatalf("NewInput() error = %v", err)
	}
	host := &requestHost{
		input: input, generationID: "generation-1", instanceID: "instance", state: newGenerationState(),
		stage:             model.PolicyStage{PluginID: "official.policy", ResourceGroupID: "group-a", DeclaredScopes: []string{string(pluginsdk.CapabilityPolicyTrustedSource)}, GrantedScopes: []string{"http.inspect", string(pluginsdk.CapabilityPolicyTrustedSource)}},
		capabilityAuditor: acknowledgedCapabilityAudit{},
	}
	source, _ := host.ReadField(context.Background(), "source.ip")
	complete, _ := host.ReadField(context.Background(), "body.complete")
	skip, _ := host.ReadField(context.Background(), "body.skip_reason")
	window, _ := host.ReadBodyWindow(context.Background(), 2, 1000)
	if string(source) != "198.51.100.25" || string(complete) != "false" || string(skip) != string(BodyStreaming) || string(window) != "efix" {
		t.Fatalf("source/body = %q %q %q %q", source, complete, skip, window)
	}
	if _, err := NewBodyWindow(make([]byte, MaxBodyPrefixBytes+1), false, BodyLimitExceeded); err == nil {
		t.Fatal("oversized prefix was accepted")
	}
}

func TestCanonicalHTTPHeaderFieldIsBoundedAndCannotAliasHostSource(t *testing.T) {
	field, ok := CanonicalHTTPHeaderField("User-Agent")
	if !ok || field != "request.header.user-agent" {
		t.Fatalf("CanonicalHTTPHeaderField() = %q, %v", field, ok)
	}
	for _, invalid := range []string{"", "bad header", "source.ip\n"} {
		if field, ok := CanonicalHTTPHeaderField(invalid); ok || field != "" {
			t.Fatalf("invalid header %q = %q, %v", invalid, field, ok)
		}
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

func TestPolicyObserverFailureCannotAffectTrafficDecision(t *testing.T) {
	observer := observability.ObserverFunc(func(context.Context, observability.Event) { panic("telemetry unavailable") })
	evaluator, err := NewGenerationEvaluator("generation-observer", []model.PluginPolicy{testPolicy("ip", model.PolicyKindIP)}, &scriptedModule{}, observer)
	if err != nil {
		t.Fatalf("NewGenerationEvaluator() error = %v", err)
	}
	decision := evaluator.Evaluate(context.Background(), &model.PolicyRef{ID: "ip"}, testInput(t, ExtensionHTTP, nil, testCompleteBody(t, nil)))
	if decision.Action != ActionAllow || decision.Degraded {
		t.Fatalf("decision = %+v", decision)
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

func TestPolicyGenerationStateCapacityIsolatedPerInstance(t *testing.T) {
	state := newGenerationState()
	for index := 0; index < maxInstanceStateEntries; index++ {
		if err := state.put("instance-a", fmt.Sprintf("key-%04d", index), nil); err != nil {
			t.Fatalf("fill instance A entry %d: %v", index, err)
		}
	}
	if err := state.put("instance-a", "overflow", nil); failureKind(err) != FailureBudget {
		t.Fatalf("instance A overflow error = %v", err)
	}
	if err := state.put("instance-b", "available", []byte("value")); err != nil {
		t.Fatalf("instance B was exhausted by instance A: %v", err)
	}
	if value, ok := state.get("instance-b", "available"); !ok || string(value) != "value" {
		t.Fatalf("instance B value = %q, %v", value, ok)
	}
}

func TestPolicyGenerationStateRetainsTotalCeiling(t *testing.T) {
	state := newGenerationState()
	value := make([]byte, maxStateValueBytes)
	instances := maxGenerationStateBytes / maxInstanceStateBytes
	valuesPerInstance := maxInstanceStateBytes / maxStateValueBytes
	for instance := 0; instance < instances; instance++ {
		instanceID := fmt.Sprintf("instance-%02d", instance)
		for index := 0; index < valuesPerInstance; index++ {
			if err := state.put(instanceID, fmt.Sprintf("key-%04d", index), value); err != nil {
				t.Fatalf("fill generation instance/key %d/%d: %v", instance, index, err)
			}
		}
	}
	if err := state.put("instance-overflow", "key", []byte("x")); failureKind(err) != FailureBudget {
		t.Fatalf("generation overflow error = %v", err)
	}
}

func TestPolicyWireFrameBudgetExactBoundariesAndDimensions(t *testing.T) {
	budget := model.PolicyResourceBudget{
		TimeoutMS:   pluginsdk.PolicyV1MaxTimeoutMilliseconds,
		MemoryBytes: pluginsdk.PolicyV1MinMemoryBytes,
		Concurrency: 1,
		InputBytes:  pluginsdk.PolicyV1MinInputFrameBytes,
		OutputBytes: pluginsdk.PolicyV1MinOutputFrameBytes,
	}
	if err := ValidatePolicyResourceBudget(budget); err != nil {
		t.Fatalf("ValidatePolicyResourceBudget(exact minimum frames) error = %v", err)
	}
	if err := AdmitPolicyInputFrame(budget, int(budget.InputBytes)); err != nil {
		t.Fatalf("AdmitPolicyInputFrame(exact) error = %v", err)
	}
	if err := AdmitPolicyOutputFrame(budget, int(budget.OutputBytes)); err != nil {
		t.Fatalf("AdmitPolicyOutputFrame(exact) error = %v", err)
	}
	for name, testCase := range map[string]struct {
		err  error
		want pluginsdk.BudgetDimension
	}{
		"input":  {AdmitPolicyInputFrame(budget, int(budget.InputBytes)+1), pluginsdk.BudgetDimensionInput},
		"output": {AdmitPolicyOutputFrame(budget, int(budget.OutputBytes)+1), pluginsdk.BudgetDimensionOutput},
		"state":  {resourceExhausted("state-capacity", "full"), pluginsdk.BudgetDimensionState},
	} {
		t.Run(name, func(t *testing.T) {
			var dimensionError pluginsdk.BudgetDimensionError
			if !errors.As(testCase.err, &dimensionError) || dimensionError.BudgetDimension() != testCase.want {
				t.Fatalf("budget error = %v dimension = %v, want %v", testCase.err, dimensionError, testCase.want)
			}
			if failureKind(testCase.err) != FailureBudget {
				t.Fatalf("failureKind(%v) = %q", testCase.err, failureKind(testCase.err))
			}
		})
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

func TestPolicyHostCapabilitiesUseSignedProjectionAndNodeLocalPrimitives(t *testing.T) {
	input := testInput(t, ExtensionHTTP, nil, testCompleteBody(t, nil))
	capabilities := []string{
		string(pluginsdk.CapabilityPolicyAtomicState),
		string(pluginsdk.CapabilityPolicyMonotonicClock),
		string(pluginsdk.CapabilityPolicyTrustedSource),
	}
	newHost := func() *requestHost {
		return &requestHost{
			input: input, generationID: "generation-1", instanceID: "instance-1", policyID: "policy-1",
			stage: model.PolicyStage{PluginID: "official.policy", ResourceGroupID: "group-a", DeclaredScopes: append([]string(nil), capabilities...), GrantedScopes: append([]string{"http.inspect", "policy.read", "policy.write"}, capabilities...)},
			state: newGenerationState(), clock: hostapi.NewMonotonicClock(),
			capabilityAuditor: acknowledgedCapabilityAudit{},
		}
	}
	host := newHost()
	if source, err := host.ReadField(t.Context(), "source.ip"); err != nil || len(source) == 0 {
		t.Fatalf("trusted source = %q, %v", source, err)
	}
	first, err := host.ReadField(t.Context(), "clock.monotonic_ns")
	if err != nil {
		t.Fatal(err)
	}
	second, err := host.ReadField(t.Context(), "clock.monotonic_ns")
	if err != nil {
		t.Fatal(err)
	}
	firstValue, _ := strconv.ParseInt(string(first), 10, 64)
	secondValue, _ := strconv.ParseInt(string(second), 10, 64)
	if secondValue < firstValue {
		t.Fatalf("monotonic clock moved backwards: %d then %d", firstValue, secondValue)
	}
	if err := host.StatePut(t.Context(), "bucket", []byte("value")); err != nil {
		t.Fatal(err)
	}
	if value, found, err := host.StateGet(t.Context(), "bucket"); err != nil || !found || string(value) != "value" {
		t.Fatalf("atomic state = %q, %v, %v", value, found, err)
	}

	for name, mutate := range map[string]func(*requestHost){
		"missing declaration": func(candidate *requestHost) { candidate.stage.DeclaredScopes = nil },
		"missing grant":       func(candidate *requestHost) { candidate.stage.GrantedScopes = []string{"http.inspect"} },
		"missing group":       func(candidate *requestHost) { candidate.stage.ResourceGroupID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := newHost()
			mutate(candidate)
			if _, err := candidate.ReadField(t.Context(), "source.ip"); !isPermissionDenied(err) {
				t.Fatalf("trusted source denial error = %v", err)
			}
		})
	}
	t.Run("audit failure", func(t *testing.T) {
		candidate := newHost()
		candidate.capabilityAuditor = acknowledgedCapabilityAudit{err: errors.New("durable journal unavailable")}
		if _, err := candidate.ReadField(t.Context(), "source.ip"); !isPermissionDenied(err) {
			t.Fatalf("audit failure error = %v", err)
		}
	})
}

func TestPolicyHostPreservesEmptyFieldPresenceAndEmitsSafeEvent(t *testing.T) {
	input := testInput(t, ExtensionHTTP, map[string][]byte{
		FieldRequestQuery: {},
	}, testCompleteBody(t, nil))
	var observed observability.Event
	host := &requestHost{
		input: input, generationID: "generation-1", instanceID: "instance-1", policyID: "policy-1",
		stage: model.PolicyStage{Kind: model.PolicyKindWAF, PluginID: "official.waf", GrantedScopes: []string{"http.inspect", "event.emit"}},
		state: newGenerationState(), observer: observability.ObserverFunc(func(_ context.Context, event observability.Event) { observed = event }),
	}
	present, err := host.ReadField(context.Background(), FieldRequestQuery)
	if err != nil || present == nil || len(present) != 0 {
		t.Fatalf("present empty field = %#v, error = %v", present, err)
	}
	missing, err := host.ReadField(context.Background(), "request.header.x-missing")
	if err != nil || missing != nil {
		t.Fatalf("missing field = %#v, error = %v", missing, err)
	}
	if err := host.EmitEvent(context.Background(), pluginsdk.PolicySecurityEvent{Code: pluginsdk.PolicySecurityEventCodeWAFRuleMatch, Action: pluginsdk.PolicySecurityEventActionDeny}); err != nil {
		t.Fatalf("EmitEvent() error = %v", err)
	}
	if observed.SecurityCode != "waf.rule_match" || observed.SecurityAction != "deny" || observed.SecurityTemplate != "WAF rule matched" {
		t.Fatalf("observed event = %+v", observed)
	}
	if observed.RequestID != "request-1" || observed.PluginID != "official.waf" {
		t.Fatalf("observed identity = %+v", observed)
	}
	if err := host.EmitEvent(context.Background(), pluginsdk.PolicySecurityEvent{Code: 200, Action: pluginsdk.PolicySecurityEventActionDeny}); err == nil {
		t.Fatal("EmitEvent() accepted unknown guest event code")
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
