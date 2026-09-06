//go:build !integration

package policy

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func typedSettings(mode sdk.PolicyMode, handling sdk.PolicyModeHandling) *sdk.PolicySettingsSnapshot {
	return &sdk.PolicySettingsSnapshot{Version: sdk.PolicySettingsVersion{Revision: 1, InstanceVersion: 1}, Settings: sdk.PolicyModeSettings{Handling: handling, DefaultMode: &mode}}
}
func TestTypedStageFailuresContinueAndPreserveEnforcedWAF(t *testing.T) {
	definition := testPolicy("shared", model.PolicyKindIP, model.PolicyKindRate, model.PolicyKindWAF)
	definition.Stages[0].PolicySettings = typedSettings(sdk.PolicyModeObserve, sdk.PolicyModeHandlingRaw)
	definition.Stages[1].PolicySettings = typedSettings(sdk.PolicyModeObserve, sdk.PolicyModeHandlingRaw)
	definition.Stages[2].PolicySettings = typedSettings(sdk.PolicyModeEnforce, sdk.PolicyModeHandlingLegacyWAF)
	module := &scriptedModule{errors: map[model.PolicyKind]error{model.PolicyKindIP: BudgetError("deadline", nil)}, responses: map[model.PolicyKind]ModuleResponse{model.PolicyKindRate: {Action: ActionAllow}, model.PolicyKindWAF: {Action: ActionDeny}}, inspect: func(request ModuleRequest) {
		if request.PolicyKind == model.PolicyKindWAF && string(request.Payload) != `{"mode":"deny"}` {
			t.Fatal("typed WAF did not receive raw-deny bridge overlay")
		}
	}}
	evaluator, err := NewGenerationEvaluator("generation", []model.PluginPolicy{definition}, module, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := evaluator.Evaluate(t.Context(), &model.PolicyRef{ID: definition.ID}, testInput(t, ExtensionHTTP, nil, testCompleteBody(t, nil)).WithEntryID("7"))
	if result.Action != ActionDeny || result.Stage != model.PolicyKindWAF || !result.Degraded || !result.Observed || !reflect.DeepEqual(module.calls, []model.PolicyKind{model.PolicyKindIP, model.PolicyKindRate, model.PolicyKindWAF}) {
		t.Fatalf("observe failure skipped remaining enforcement: %+v %v", result, module.calls)
	}
}
func TestTypedSourceFailureAndWAFObserveRemainFailedChecks(t *testing.T) {
	for _, mode := range []sdk.PolicyMode{sdk.PolicyModeObserve, sdk.PolicyModeEnforce} {
		definition := testPolicy("typed", model.PolicyKindWAF)
		definition.Stages[0].PolicySettings = typedSettings(mode, sdk.PolicyModeHandlingLegacyWAF)
		module := &scriptedModule{responses: map[model.PolicyKind]ModuleResponse{model.PolicyKindWAF: {Action: ActionObserve}}}
		evaluator, err := NewGenerationEvaluator("generation", []model.PluginPolicy{definition}, module, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, input := range []Input{NewFailedAdmissionInput(ExtensionHTTP, "request", "7", "source-unavailable"), testInput(t, ExtensionHTTP, nil, testCompleteBody(t, nil)).WithEntryID("7")} {
			result := evaluator.Evaluate(t.Context(), &model.PolicyRef{ID: definition.ID}, input)
			if !result.Degraded || !result.Observed || (mode == sdk.PolicyModeEnforce) != (result.Action == ActionDeny) || (result.Reason != "source-unavailable" && result.Reason != "invalid-result") {
				t.Fatalf("failed check became ordinary allow: %+v", result)
			}
		}
	}
}

func TestTypedEnforceDenialsEmitCanonicalRejections(t *testing.T) {
	for _, test := range []struct {
		name       string
		response   ModuleResponse
		err        error
		wantReason string
	}{
		{name: "match", response: ModuleResponse{Action: ActionDeny}, wantReason: "policy-deny"},
		{name: "failure", err: RuntimeError("trap", nil), wantReason: "guest-failure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			definition := testPolicy("typed", model.PolicyKindIP)
			definition.Stages[0].PolicySettings = typedSettings(sdk.PolicyModeEnforce, sdk.PolicyModeHandlingRaw)
			module := &scriptedModule{
				responses: map[model.PolicyKind]ModuleResponse{model.PolicyKindIP: test.response},
				errors:    map[model.PolicyKind]error{model.PolicyKindIP: test.err},
			}
			var events []observability.Event
			evaluator, err := NewGenerationEvaluator("generation", []model.PluginPolicy{definition}, module, observability.ObserverFunc(func(_ context.Context, event observability.Event) {
				events = append(events, event)
			}))
			if err != nil {
				t.Fatal(err)
			}
			decision := evaluator.Evaluate(t.Context(), &model.PolicyRef{ID: definition.ID}, testInput(t, ExtensionHTTP, nil, testCompleteBody(t, nil)))
			if decision.Action != ActionDeny || decision.Reason != test.wantReason {
				t.Fatalf("decision = %+v", decision)
			}
			var rejections []observability.Event
			for _, event := range events {
				if event.Name == observability.PolicyRejection {
					rejections = append(rejections, event)
				}
			}
			if len(rejections) != 1 {
				t.Fatalf("rejections = %+v, all events = %+v", rejections, events)
			}
			rejection := rejections[0]
			if rejection.Outcome != "denied" || rejection.PolicyID != definition.ID || rejection.PolicyStage != string(model.PolicyKindIP) || rejection.InstanceID != definition.Stages[0].InstanceID || rejection.Reason != test.wantReason {
				t.Fatalf("rejection = %+v", rejection)
			}
		})
	}
}

func TestTypedObserveOutcomesDoNotEmitPolicyRejections(t *testing.T) {
	definition := testPolicy("typed", model.PolicyKindIP)
	definition.Stages[0].PolicySettings = typedSettings(sdk.PolicyModeObserve, sdk.PolicyModeHandlingRaw)
	for _, module := range []*scriptedModule{
		{responses: map[model.PolicyKind]ModuleResponse{model.PolicyKindIP: {Action: ActionDeny}}},
		{errors: map[model.PolicyKind]error{model.PolicyKindIP: RuntimeError("trap", nil)}},
	} {
		var events []observability.Event
		evaluator, err := NewGenerationEvaluator("generation", []model.PluginPolicy{definition}, module, observability.ObserverFunc(func(_ context.Context, event observability.Event) {
			events = append(events, event)
		}))
		if err != nil {
			t.Fatal(err)
		}
		if decision := evaluator.Evaluate(t.Context(), &model.PolicyRef{ID: definition.ID}, testInput(t, ExtensionHTTP, nil, testCompleteBody(t, nil))); decision.Action != ActionAllow || !decision.Observed {
			t.Fatalf("observe decision = %+v", decision)
		}
		for _, event := range events {
			if event.Name == observability.PolicyRejection {
				t.Fatalf("observe outcome emitted rejection: %+v", event)
			}
		}
	}
}
func TestStageOverlayIsolationAndModeFloor(t *testing.T) {
	definition := testPolicy("mixed", model.PolicyKindIP, model.PolicyKindRate, model.PolicyKindWAF)
	definition.Stages[0].PolicySettings = typedSettings(sdk.PolicyModeEnforce, sdk.PolicyModeHandlingRaw)
	envelope := sdk.PolicyOverlayEnvelope{Schema: sdk.PolicyOverlaySchemaV1, Stages: []sdk.PolicyStageOverlay{{Kind: "ip", PolicyID: definition.Stages[0].PolicyID, Payload: json.RawMessage(`{"allow":["192.0.2.1"]}`)}, {Kind: "rate", PolicyID: definition.Stages[1].PolicyID, Payload: json.RawMessage(`{"limit":7}`)}, {Kind: "waf", PolicyID: definition.Stages[2].PolicyID, Payload: json.RawMessage(`{"mode":"deny"}`)}}}
	encoded, _ := json.Marshal(envelope)
	ref := &model.PolicyRef{ID: definition.ID, Overlay: encoded, OverlayFormat: sdk.PolicyOverlayFormatEnvelopeV1}
	index := 0
	module := &scriptedModule{inspect: func(request ModuleRequest) {
		if string(request.Payload) != string(envelope.Stages[index].Payload) {
			t.Fatal("overlay leaked across stages")
		}
		index++
	}}
	evaluator, err := NewGenerationEvaluator("generation", []model.PluginPolicy{definition}, module, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := evaluator.Evaluate(t.Context(), ref, testInput(t, ExtensionHTTP, nil, testCompleteBody(t, nil)))
	if result.Action != ActionAllow || index != 3 {
		t.Fatal(result, index)
	}
	observe := sdk.PolicyModeObserve
	settings := *model.ClonePolicySettings(definition.Stages[0].PolicySettings)
	settings.Settings.EntryMode = &observe
	ref.StageModes = []model.PolicyModeBinding{{Stage: sdk.PolicyStageIdentity{Kind: "ip", PolicyID: definition.Stages[0].PolicyID}, Snapshot: settings}}
	if evaluator.Evaluate(context.Background(), ref, testInput(t, ExtensionHTTP, nil, testCompleteBody(t, nil))).Action != ActionDeny {
		t.Fatal("entry observe weakened enforced instance floor")
	}
}
func TestLegacyWAFOverlayDoesNotReachOtherStages(t *testing.T) {
	definition := testPolicy("legacy", model.PolicyKindIP, model.PolicyKindRate, model.PolicyKindWAF)
	module := &scriptedModule{inspect: func(request ModuleRequest) {
		if request.PolicyKind != model.PolicyKindWAF && len(request.Payload) != 0 {
			t.Fatal("legacy WAF mode reached IP/rate")
		}
	}}
	evaluator, err := NewGenerationEvaluator("generation", []model.PluginPolicy{definition}, module, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := evaluator.Evaluate(t.Context(), &model.PolicyRef{ID: definition.ID, Overlay: json.RawMessage(`{"mode":"observe"}`)}, testInput(t, ExtensionHTTP, nil, testCompleteBody(t, nil)))
	if result.Action != ActionAllow {
		t.Fatal(result)
	}
	if evaluator.Evaluate(t.Context(), &model.PolicyRef{ID: definition.ID}, testInput(t, ExtensionL4, nil, testCompleteBody(t, nil))).Action != ActionDeny {
		t.Fatal("WAF admitted at L4")
	}
}
