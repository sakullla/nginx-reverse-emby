package policy

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/hostapi"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type GenerationEvaluator struct {
	datasets          *DatasetGeneration
	generationID      string
	policies          map[string]model.PluginPolicy
	modules           ModuleEvaluator
	observer          observability.Observer
	state             *generationState
	clock             *hostapi.MonotonicClock
	capabilityAuditor hostapi.Auditor
}

func NewGenerationEvaluator(generationID string, definitions []model.PluginPolicy, modules ModuleEvaluator, observer observability.Observer) (*GenerationEvaluator, error) {
	generationID = strings.TrimSpace(generationID)
	if generationID == "" {
		return nil, errors.New("policy generation id is required")
	}
	policies := make(map[string]model.PluginPolicy, len(definitions))
	instances := make(map[string]model.PolicyStage)
	for _, definition := range definitions {
		cloned, err := validateAndClonePolicy(definition)
		if err != nil {
			return nil, fmt.Errorf("policy %q: %w", definition.ID, err)
		}
		if _, exists := policies[cloned.ID]; exists {
			return nil, fmt.Errorf("duplicate policy id %q", cloned.ID)
		}
		for _, stage := range cloned.Stages {
			if existing, exists := instances[stage.InstanceID]; exists && !reflect.DeepEqual(existing, stage) {
				return nil, fmt.Errorf("policy instance %q has conflicting generation definitions", stage.InstanceID)
			}
			instances[stage.InstanceID] = cloneStage(stage)
		}
		policies[cloned.ID] = cloned
	}
	if observer == nil {
		observer = observability.Default()
	}
	capabilityAuditor, _ := observer.(hostapi.Auditor)
	return &GenerationEvaluator{
		generationID:      generationID,
		policies:          policies,
		modules:           modules,
		observer:          observer,
		state:             newGenerationState(),
		clock:             hostapi.NewMonotonicClock(),
		capabilityAuditor: capabilityAuditor,
	}, nil
}

func (e *GenerationEvaluator) Evaluate(ctx context.Context, ref *model.PolicyRef, input Input) Decision {
	if ctx == nil {
		ctx = context.Background()
	}
	if ref == nil || strings.TrimSpace(ref.ID) == "" {
		return Decision{Action: ActionAllow}
	}
	if !canonicalIdentity(ref.ID) {
		return unavailableDecision("", "invalid-policy-ref")
	}
	if e == nil || input.extensionPoint == "" {
		return unavailableDecision(strings.TrimSpace(ref.ID), "invalid-input")
	}
	policyID := strings.TrimSpace(ref.ID)
	definition, ok := e.policies[policyID]
	if !ok {
		e.observe(ctx, observability.PolicyRejection, "denied", model.PolicyStage{}, policyID, "missing-policy", 0)
		return unavailableDecision(policyID, "missing-policy")
	}
	if input.extensionPoint == ExtensionL4 && containsStage(definition.Stages, model.PolicyKindWAF) {
		e.observe(ctx, observability.PolicyRejection, "denied", model.PolicyStage{}, policyID, "waf-not-supported-on-l4", 0)
		return unavailableDecision(policyID, "waf-not-supported-on-l4")
	}
	if err := validateEntryModeBindings(ref, definition); err != nil {
		return unavailableDecision(policyID, "invalid-mode-binding")
	}
	envelope, err := resolveEntryOverlays(ref, definition)
	if err != nil {
		return unavailableDecision(policyID, "invalid-overlay")
	}

	decision := Decision{Action: ActionAllow, PolicyID: policyID}
	for _, stage := range definition.Stages {
		projection, modeErr := stageModeProjection(stage, ref)
		if modeErr != nil {
			return unavailableDecision(policyID, "invalid-mode-binding")
		}
		payload, overlayErr := selectedStageOverlay(envelope, projection)
		if overlayErr != nil {
			return unavailableDecision(policyID, "invalid-overlay")
		}
		typed := projection.Settings.Handling != pluginsdk.PolicyModeHandlingLegacy
		requestHost := &requestHost{
			input:             input,
			datasets:          e.datasets,
			generationID:      e.generationID,
			instanceID:        stage.InstanceID,
			policyID:          definition.ID,
			stage:             stage,
			state:             e.state,
			observer:          e.observer,
			clock:             e.clock,
			capabilityAuditor: e.capabilityAuditor,
		}
		deadline := time.Duration(stage.ResourceBudget.TimeoutMS) * time.Millisecond
		stageCtx, cancel := context.WithTimeout(ctx, deadline)
		started := time.Now()
		var response ModuleResponse
		var err error
		preflight := input.preflightFailure
		if !input.metadata.authorized && preflight == "" {
			preflight = "source-unavailable"
		}
		if preflight != "" {
			if !typed {
				cancel()
				if preflight == "source-unavailable" {
					preflight = "invalid-source"
				}
				return unavailableDecision(policyID, preflight)
			}
			requestHost.checkFailure = preflight
		} else if !containsString(stage.ExtensionPoints, input.extensionPoint) {
			err = RuntimeError("extension-point-unavailable", nil)
		} else if e.modules == nil {
			err = RuntimeError("runtime-unavailable", nil)
		} else {
			response, err = e.modules.Evaluate(stageCtx, ModuleRequest{
				GenerationID:   e.generationID,
				PolicyID:       stage.PolicyID,
				PolicyKind:     stage.Kind,
				InstanceID:     stage.InstanceID,
				ExtensionPoint: input.extensionPoint,
				RequestID:      input.requestID,
				Payload:        payload,
				Budget:         stage.ResourceBudget,
				Host:           requestHost,
			})
		}
		cancel()
		duration := time.Since(started)
		if typed {
			check := pluginsdk.PolicyCheckResult{Outcome: pluginsdk.PolicyCheckAllow}
			if requestHost.checkFailure != "" {
				check = pluginsdk.PolicyCheckResult{Outcome: pluginsdk.PolicyCheckError, Failure: requestHost.checkFailure}
			} else if err != nil {
				check = pluginsdk.PolicyCheckResult{Outcome: pluginsdk.PolicyCheckError, Failure: "guest-failure"}
				if failureKind(err) == FailureBudget {
					check.Failure = "budget-exceeded"
				}
			} else if response.Action == ActionDeny {
				check.Outcome = pluginsdk.PolicyCheckDeny
			} else if response.Action != ActionAllow {
				check = pluginsdk.PolicyCheckResult{Outcome: pluginsdk.PolicyCheckError, Failure: "invalid-result"}
			}
			applied, modeErr := pluginsdk.ApplyPolicyMode(projection, check, decision.Action == ActionDeny)
			if modeErr != nil {
				return unavailableDecision(policyID, "invalid-mode-result")
			}
			decision.Observed = decision.Observed || applied.WouldDeny || applied.CheckFailed
			decision.Degraded = decision.Degraded || applied.CheckFailed
			if applied.CheckFailed {
				decision.Reason = applied.Failure
				e.observe(ctx, observability.PolicyDegraded, "check-failed", stage, policyID, applied.Failure, duration)
			} else {
				e.observe(ctx, observability.PolicyEvaluation, map[bool]string{true: "would-deny", false: "checked"}[applied.WouldDeny], stage, policyID, "", duration)
			}
			if applied.Denied {
				status := denyStatus(input.extensionPoint, stage.Kind)
				reason := "policy-deny"
				if applied.CheckFailed {
					status = unavailableStatus(decision)
					reason = applied.Failure
				}
				return Decision{Action: ActionDeny, StatusCode: status, Stage: stage.Kind, PolicyID: policyID, Reason: reason, Observed: decision.Observed, Degraded: decision.Degraded}
			}
			continue
		}
		if err != nil {
			failed := e.handleFailure(ctx, definition.ID, stage, failureKind(err), failureCode(err), decision)
			if failed.Action == ActionDeny {
				return failed
			}
			decision = failed
			continue
		}
		if !response.Action.valid() {
			failed := e.handleFailure(ctx, definition.ID, stage, FailureRuntime, "invalid-action", decision)
			if failed.Action == ActionDeny {
				return failed
			}
			decision = failed
			continue
		}

		outcome := map[Action]string{ActionAllow: "allowed", ActionDeny: "denied", ActionObserve: "observed"}[response.Action]
		e.observe(ctx, observability.PolicyEvaluation, outcome, stage, definition.ID, "", duration)

		switch response.Action {
		case ActionDeny:
			status := denyStatus(input.extensionPoint, stage.Kind)
			e.observe(ctx, observability.PolicyRejection, "denied", stage, definition.ID, "policy-deny", duration)
			return Decision{Action: ActionDeny, StatusCode: status, Stage: stage.Kind, PolicyID: definition.ID, Reason: "policy-deny", Observed: decision.Observed, Degraded: decision.Degraded}
		case ActionObserve:
			decision.Observed = true
		}
	}
	return decision
}

func (e *GenerationEvaluator) handleFailure(ctx context.Context, policyID string, stage model.PolicyStage, kind FailureKind, reason string, prior Decision) Decision {
	eventName := observability.PolicyDegraded
	if kind == FailureBudget {
		eventName = observability.PolicyBudget
	}
	e.observe(ctx, eventName, map[FailureKind]string{FailureBudget: "exhausted", FailureRuntime: "degraded"}[kind], stage, policyID, reason, 0)
	failureAction := stage.FailurePolicy.OnError
	if kind == FailureBudget {
		failureAction = stage.FailurePolicy.OnBudget
	}
	if failureAction == "fail-closed" {
		e.observe(ctx, observability.PolicyRejection, "denied", stage, policyID, reason, 0)
		return Decision{Action: ActionDeny, StatusCode: unavailableStatus(prior), Stage: stage.Kind, PolicyID: policyID, Reason: reason, Degraded: true, Observed: prior.Observed}
	}
	prior.Action = ActionAllow
	prior.PolicyID = policyID
	prior.Degraded = true
	prior.Reason = reason
	return prior
}

func (e *GenerationEvaluator) observe(ctx context.Context, name, outcome string, stage model.PolicyStage, policyID, reason string, duration time.Duration) {
	if e == nil || e.observer == nil {
		return
	}
	observeEvent(ctx, e.observer, observability.Event{
		Name: name, Outcome: outcome, GenerationID: e.generationID, Duration: duration,
		PluginID: stage.PluginID, InstanceID: stage.InstanceID, PolicyID: policyID,
		PolicyStage: string(stage.Kind), Reason: reason, NodeLocal: stage.Kind == model.PolicyKindRate,
	})
}

func validateAndClonePolicy(policy model.PluginPolicy) (model.PluginPolicy, error) {
	if !canonicalIdentity(policy.ID) {
		return model.PluginPolicy{}, errors.New("id is missing or non-canonical")
	}
	if policy.Revision <= 0 {
		return model.PluginPolicy{}, errors.New("revision must be positive")
	}
	if len(policy.Stages) == 0 || len(policy.Stages) > 3 {
		return model.PluginPolicy{}, errors.New("chain must contain one to three stages")
	}
	cloned := policy
	cloned.Stages = make([]model.PolicyStage, len(policy.Stages))
	lastOrder := -1
	for index, stage := range policy.Stages {
		order := stageOrder(stage.Kind)
		if order < 0 || order <= lastOrder {
			return model.PluginPolicy{}, errors.New("stages must be unique and ordered ip, rate, waf")
		}
		lastOrder = order
		if err := validateStage(stage); err != nil {
			return model.PluginPolicy{}, fmt.Errorf("stage %s: %w", stage.Kind, err)
		}
		cloned.Stages[index] = cloneStage(stage)
	}
	return cloned, nil
}

func validateStage(stage model.PolicyStage) error {
	if stage.PolicySettings != nil {
		if stage.PolicySettings.Validate() != nil || stage.PolicySettings.Settings.EntryMode != nil {
			return errors.New("invalid trusted instance policy settings")
		}
	}
	identities := []struct{ name, value string }{
		{"policy id", stage.PolicyID}, {"plugin id", stage.PluginID}, {"plugin version", stage.PluginVersion},
		{"instance id", stage.InstanceID}, {"package digest", stage.PackageDigest}, {"artifact path", stage.ArtifactPath},
		{"artifact digest", stage.ArtifactDigest}, {"signer key id", stage.SignerKeyID}, {"signer fingerprint", stage.SignerFingerprint},
	}
	for _, identity := range identities {
		if err := pluginsdk.ValidatePolicyIdentity(identity.value); err != nil {
			return fmt.Errorf("%s is missing or non-canonical: %w", identity.name, err)
		}
	}
	if err := pluginsdk.ValidatePolicyIdentity(stage.ResourceGroupID); err != nil {
		return fmt.Errorf("resource group id is missing or non-canonical: %w", err)
	}
	declared, err := canonicalScopeSet("declared scope", stage.DeclaredScopes)
	if err != nil {
		return err
	}
	granted, err := canonicalScopeSet("granted scope", stage.GrantedScopes)
	if err != nil {
		return err
	}
	for scope := range granted {
		if _, ok := declared[scope]; !ok {
			return fmt.Errorf("granted scope %q is absent from signed declarations", scope)
		}
	}
	if !stage.SignatureVerified {
		return errors.New("artifact signature verification evidence is required")
	}
	if stage.ABI != model.PolicyABIV1 {
		return fmt.Errorf("unsupported ABI %q", stage.ABI)
	}
	if len(stage.ExtensionPoints) == 0 {
		return errors.New("extension points are required")
	}
	for _, point := range stage.ExtensionPoints {
		if point != ExtensionHTTP && point != ExtensionL4 {
			return fmt.Errorf("unsupported extension point %q", point)
		}
		if stage.Kind == model.PolicyKindWAF && point != ExtensionHTTP {
			return errors.New("waf only supports http.request")
		}
	}
	if err := ValidatePolicyResourceBudget(stage.ResourceBudget); err != nil {
		return fmt.Errorf("resource budget exceeds the policy host contract: %w", err)
	}
	if !validFailureAction(stage.FailurePolicy.OnError, true) || !validFailureAction(stage.FailurePolicy.OnBudget, false) ||
		stage.FailurePolicy.Restart != "never" || stage.FailurePolicy.CoreFallback != "preserve" {
		return errors.New("failure policy is outside the policy isolation allowlist")
	}
	return nil
}

func cloneStage(stage model.PolicyStage) model.PolicyStage { return model.ClonePolicyStage(stage) }

func canonicalScopeSet(name string, scopes []string) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if err := pluginsdk.ValidatePolicyIdentity(scope); err != nil {
			return nil, fmt.Errorf("%s %q is non-canonical: %w", name, scope, err)
		}
		if _, duplicate := result[scope]; duplicate {
			return nil, fmt.Errorf("%s %q is duplicated", name, scope)
		}
		result[scope] = struct{}{}
	}
	return result, nil
}

func validFailureAction(action string, allowDegraded bool) bool {
	return action == "fail-open" || action == "fail-closed" || (allowDegraded && action == "degraded")
}

func canonicalIdentity(value string) bool {
	return pluginsdk.ValidatePolicyIdentity(value) == nil
}

func stageOrder(kind model.PolicyKind) int {
	switch kind {
	case model.PolicyKindIP:
		return 0
	case model.PolicyKindRate:
		return 1
	case model.PolicyKindWAF:
		return 2
	default:
		return -1
	}
}

func containsStage(stages []model.PolicyStage, kind model.PolicyKind) bool {
	for _, stage := range stages {
		if stage.Kind == kind {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func failureCode(err error) string {
	var evaluationError *EvaluationError
	if errors.As(err, &evaluationError) && strings.TrimSpace(evaluationError.Code) != "" {
		if code := canonicalSignalName(evaluationError.Code); code != "" {
			return code
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline"
	}
	return "runtime-error"
}

func unavailableDecision(policyID, reason string) Decision {
	return Decision{Action: ActionDeny, StatusCode: 503, PolicyID: policyID, Reason: reason, Degraded: true}
}

func denyStatus(extensionPoint string, kind model.PolicyKind) int {
	if extensionPoint != ExtensionHTTP {
		return 0
	}
	if kind == model.PolicyKindRate {
		return 429
	}
	return 403
}

func unavailableStatus(Decision) int { return 503 }

var _ Evaluator = (*GenerationEvaluator)(nil)

func (e *GenerationEvaluator) EvaluateAdmissionFailure(ctx context.Context, ref *model.PolicyRef, extension, entryID, reason string) Decision {
	return e.Evaluate(ctx, ref, NewFailedAdmissionInput(extension, "admission-failure", entryID, reason))
}
func AdmissionFailure(ctx context.Context, evaluator Evaluator, ref *model.PolicyRef, extension, entryID, reason string) Decision {
	if handler, ok := evaluator.(interface {
		EvaluateAdmissionFailure(context.Context, *model.PolicyRef, string, string, string) Decision
	}); ok {
		return handler.EvaluateAdmissionFailure(ctx, ref, extension, entryID, reason)
	}
	if reason == "source-unavailable" {
		reason = "invalid-source"
	}
	return unavailableDecision(ref.ID, reason)
}
