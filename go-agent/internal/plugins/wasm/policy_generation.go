package wasm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/protoschema"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

type preparedPolicyStage struct {
	definition model.PolicyStage
	generation *Generation
}

// PolicyGeneration is the generation-scoped adapter between the policy chain
// and the canonical nre:policy/v1 wire contract.
type PolicyGeneration struct {
	id       string
	stages   map[string]preparedPolicyStage
	mu       sync.RWMutex
	closed   bool
	close    sync.Once
	closeErr error
}

// PreparePolicyGeneration treats every supplied stage as required and prepares
// them atomically. GenerationFactory applies the required/optional policy
// distinction from policy.GenerationSpec.
func PreparePolicyGeneration(ctx context.Context, runtime *Runtime, generationID string, stages []model.PolicyStage, observer Observer) (*PolicyGeneration, error) {
	required := make(map[string]struct{}, len(stages))
	for _, stage := range stages {
		required[stage.InstanceID] = struct{}{}
	}
	return preparePolicyGeneration(ctx, runtime, generationID, stages, required, observer)
}

func preparePolicyGeneration(ctx context.Context, runtime *Runtime, generationID string, stages []model.PolicyStage, required map[string]struct{}, observer Observer) (*PolicyGeneration, error) {
	if runtime == nil {
		return nil, errors.New("wasm runtime is required")
	}
	generationID = strings.TrimSpace(generationID)
	if generationID == "" {
		return nil, errors.New("policy generation id is required")
	}
	if observer == nil {
		observer = discardObserver{}
	}
	prepared := &PolicyGeneration{id: generationID, stages: make(map[string]preparedPolicyStage, len(stages))}
	rollback := func() {
		for _, stage := range prepared.stages {
			_ = stage.generation.Rollback(context.WithoutCancel(ctx))
		}
	}
	for _, stage := range stages {
		var stageErr error
		if _, duplicate := prepared.stages[stage.InstanceID]; duplicate {
			stageErr = fmt.Errorf("duplicate policy instance %q", stage.InstanceID)
		}
		var compiled preparedPolicyStage
		if stageErr == nil {
			compiled, stageErr = preparePolicyStage(ctx, runtime, generationID, stage)
		}
		if stageErr != nil {
			if _, isRequired := required[stage.InstanceID]; isRequired {
				rollback()
				return nil, fmt.Errorf("prepare required policy stage %q: %w", stage.InstanceID, stageErr)
			}
			observer.ObserveWASM(Event{Generation: generationID, Operation: "prepare_optional." + stage.InstanceID, Code: ErrorOptionalDegraded})
			continue
		}
		prepared.stages[stage.InstanceID] = compiled
	}
	return prepared, nil
}

func preparePolicyStage(ctx context.Context, runtime *Runtime, generationID string, stage model.PolicyStage) (preparedPolicyStage, error) {
	if err := validatePolicyStageEvidence(stage); err != nil {
		return preparedPolicyStage{}, err
	}
	wasmBytes, err := os.ReadFile(stage.ArtifactPath)
	if err != nil {
		return preparedPolicyStage{}, fmt.Errorf("read artifact: %w", err)
	}
	artifact, err := AcceptVerifiedArtifact(wasmBytes, stage.ArtifactDigest, stage.SignatureVerified)
	if err != nil {
		return preparedPolicyStage{}, fmt.Errorf("verify artifact: %w", err)
	}
	initRequest, err := marshalInitRequest(stage.Config, stage.GrantedScopes, generationID)
	if err != nil {
		return preparedPolicyStage{}, fmt.Errorf("marshal init request: %w", err)
	}
	if err := policy.AdmitPolicyInputFrame(stage.ResourceBudget, len(initRequest)); err != nil {
		return preparedPolicyStage{}, fmt.Errorf("admit init request frame: %w", err)
	}
	generation, err := runtime.CompileGeneration(ctx, artifact, GenerationConfig{
		ID: generationID + "/" + stage.InstanceID, InitRequest: initRequest, Budget: stageBudget(stage.ResourceBudget),
	})
	if err != nil {
		return preparedPolicyStage{}, fmt.Errorf("compile artifact: %w", err)
	}
	if err := generation.Ready(ctx); err != nil {
		_ = generation.Rollback(context.WithoutCancel(ctx))
		return preparedPolicyStage{}, fmt.Errorf("initialize artifact: %w", err)
	}
	return preparedPolicyStage{definition: clonePolicyStage(stage), generation: generation}, nil
}

// GenerationFactory adapts the process-scoped compiler runtime to policy's
// candidate-generation transaction contract.
type GenerationFactory struct {
	Runtime  *Runtime
	Observer Observer
}

func (factory GenerationFactory) PrepareGeneration(ctx context.Context, spec policy.GenerationSpec) (policy.GenerationRuntime, error) {
	requiredPolicies := make(map[string]struct{}, len(spec.RequiredPolicyIDs))
	for _, policyID := range spec.RequiredPolicyIDs {
		requiredPolicies[policyID] = struct{}{}
	}
	stages := make([]model.PolicyStage, 0)
	stageIndex := make(map[string]int)
	requiredStages := make(map[string]struct{})
	for _, definition := range spec.Policies {
		_, policyRequired := requiredPolicies[definition.ID]
		for _, stage := range definition.Stages {
			if index, duplicate := stageIndex[stage.InstanceID]; duplicate {
				if !reflect.DeepEqual(stages[index], stage) {
					return nil, fmt.Errorf("conflicting policy definitions for instance %q", stage.InstanceID)
				}
			} else {
				stageIndex[stage.InstanceID] = len(stages)
				stages = append(stages, stage)
			}
			if policyRequired {
				requiredStages[stage.InstanceID] = struct{}{}
			}
		}
	}
	return preparePolicyGeneration(ctx, factory.Runtime, spec.ID, stages, requiredStages, factory.Observer)
}

func (generation *PolicyGeneration) Ready(context.Context) error {
	if generation == nil {
		return errors.New("policy generation is nil")
	}
	generation.mu.RLock()
	defer generation.mu.RUnlock()
	if generation.closed {
		return errors.New("policy generation is closed")
	}
	return nil
}

func (generation *PolicyGeneration) Evaluate(ctx context.Context, request policy.ModuleRequest) (policy.ModuleResponse, error) {
	if generation == nil {
		return policy.ModuleResponse{}, policy.RuntimeError("runtime-unavailable", errors.New("policy generation is nil"))
	}
	generation.mu.RLock()
	if generation.closed {
		generation.mu.RUnlock()
		return policy.ModuleResponse{}, policy.RuntimeError("generation-draining", errors.New("policy generation is closed"))
	}
	stage, ok := generation.stages[request.InstanceID]
	generation.mu.RUnlock()
	if !ok {
		return policy.ModuleResponse{}, policy.RuntimeError("unknown-instance", fmt.Errorf("policy instance %q is unavailable", request.InstanceID))
	}
	if request.GenerationID != generation.id || request.PolicyID != stage.definition.PolicyID || request.PolicyKind != stage.definition.Kind {
		return policy.ModuleResponse{}, policy.RuntimeError("stage-binding", errors.New("policy request does not match the prepared stage"))
	}
	if !slices.Contains(stage.definition.ExtensionPoints, request.ExtensionPoint) {
		return policy.ModuleResponse{}, policy.RuntimeError("extension-point", fmt.Errorf("extension point %q is not granted", request.ExtensionPoint))
	}
	if request.Host == nil {
		return policy.ModuleResponse{}, policy.RuntimeError("host-unavailable", errors.New("policy host is required"))
	}
	if request.Budget != stage.definition.ResourceBudget {
		return policy.ModuleResponse{}, policy.RuntimeError("budget-binding", errors.New("policy request budget differs from the prepared stage"))
	}
	wireRequest, err := marshalEvaluateRequest(request.ExtensionPoint, request.RequestID, request.Payload)
	if err != nil {
		return policy.ModuleResponse{}, policy.RuntimeError("request-wire", err)
	}
	if err := policy.AdmitPolicyInputFrame(request.Budget, len(wireRequest)); err != nil {
		return policy.ModuleResponse{}, err
	}
	wireResponse, err := stage.generation.Evaluate(ctx, request.Host, wireRequest)
	if err != nil {
		return policy.ModuleResponse{}, mapRuntimeError(err)
	}
	if err := policy.AdmitPolicyOutputFrame(request.Budget, len(wireResponse)); err != nil {
		return policy.ModuleResponse{}, err
	}
	return decodeEvaluateResponse(wireResponse)
}

func (generation *PolicyGeneration) Drain(ctx context.Context) error { return generation.Close(ctx) }

func (generation *PolicyGeneration) Close(ctx context.Context) error {
	if generation == nil {
		return nil
	}
	generation.close.Do(func() {
		generation.mu.Lock()
		generation.closed = true
		stages := make([]preparedPolicyStage, 0, len(generation.stages))
		for _, stage := range generation.stages {
			stages = append(stages, stage)
		}
		generation.mu.Unlock()
		for _, stage := range stages {
			generation.closeErr = errors.Join(generation.closeErr, stage.generation.Drain(ctx))
		}
	})
	return generation.closeErr
}

func validatePolicyStageEvidence(stage model.PolicyStage) error {
	if strings.TrimSpace(stage.InstanceID) == "" || stage.InstanceID != strings.TrimSpace(stage.InstanceID) {
		return errors.New("instance id is missing or non-canonical")
	}
	if strings.TrimSpace(stage.PolicyID) == "" || stage.PolicyID != strings.TrimSpace(stage.PolicyID) {
		return errors.New("policy id is missing or non-canonical")
	}
	if stage.ABI != pluginsdk.PolicyABIV1 {
		return fmt.Errorf("unsupported policy ABI %q", stage.ABI)
	}
	if !stage.SignatureVerified || strings.TrimSpace(stage.SignerKeyID) == "" || strings.TrimSpace(stage.SignerFingerprint) == "" {
		return errors.New("artifact signature verification evidence is incomplete")
	}
	if strings.TrimSpace(stage.ArtifactPath) == "" || strings.TrimSpace(stage.ArtifactDigest) == "" {
		return errors.New("artifact path and digest are required")
	}
	if err := policy.ValidatePolicyResourceBudget(stage.ResourceBudget); err != nil {
		return fmt.Errorf("policy stage resource budget is invalid: %w", err)
	}
	return nil
}

func stageBudget(budget model.PolicyResourceBudget) Budget {
	return Budget{
		MaxInputBytes:  uint32(budget.InputBytes),
		MaxOutputBytes: uint32(budget.OutputBytes),
		MaxMemoryPages: uint32((budget.MemoryBytes + 65535) / 65536),
		MaxConcurrency: budget.Concurrency,
		Timeout:        time.Duration(budget.TimeoutMS) * time.Millisecond,
	}
}

func clonePolicyStage(stage model.PolicyStage) model.PolicyStage {
	stage.ExtensionPoints = append([]string(nil), stage.ExtensionPoints...)
	stage.GrantedScopes = append([]string(nil), stage.GrantedScopes...)
	stage.Config = append([]byte(nil), stage.Config...)
	return stage
}

func marshalInitRequest(config []byte, grants []string, generation string) ([]byte, error) {
	message, err := canonicalPolicyMessage("InitRequest")
	if err != nil {
		return nil, err
	}
	setCanonicalBytes(message, "config", config)
	grantedScopes := message.Mutable(canonicalField(message, "granted_scopes")).List()
	for _, grant := range grants {
		grantedScopes.Append(protoreflect.ValueOfString(grant))
	}
	setCanonicalString(message, "generation", generation)
	return (proto.MarshalOptions{Deterministic: true}).Marshal(message.Interface())
}

func marshalEvaluateRequest(extensionPoint, requestID string, payload []byte) ([]byte, error) {
	message, err := canonicalPolicyMessage("EvaluateRequest")
	if err != nil {
		return nil, err
	}
	setCanonicalString(message, "extension_point", extensionPoint)
	setCanonicalString(message, "request_id", requestID)
	setCanonicalBytes(message, "payload", payload)
	return (proto.MarshalOptions{Deterministic: true}).Marshal(message.Interface())
}

func decodeEvaluateResponse(encoded []byte) (policy.ModuleResponse, error) {
	if err := pluginsdk.ValidatePolicyEvaluateResponseFrame(encoded); err != nil {
		return policy.ModuleResponse{}, policy.RuntimeError("response-wire", err)
	}
	message, err := canonicalPolicyMessage("EvaluateResponse")
	if err != nil {
		return policy.ModuleResponse{}, policy.RuntimeError("response-schema", err)
	}
	if err := proto.Unmarshal(encoded, message.Interface()); err != nil {
		return policy.ModuleResponse{}, policy.RuntimeError("response-wire", err)
	}
	if successField := canonicalField(message, "success"); message.Has(successField) {
		success := message.Get(successField).Message()
		action := success.Get(canonicalField(success, "action")).Enum()
		mapped := map[protoreflect.EnumNumber]policy.Action{1: policy.ActionAllow, 2: policy.ActionDeny, 3: policy.ActionObserve}[action]
		if mapped == "" {
			return policy.ModuleResponse{}, policy.RuntimeError("invalid-action", fmt.Errorf("unknown action %d", action))
		}
		return policy.ModuleResponse{Action: mapped, Payload: append([]byte(nil), success.Get(canonicalField(success, "payload")).Bytes()...)}, nil
	}
	runtimeError := message.Get(canonicalField(message, "error")).Message()
	code := pluginsdk.ErrorCode(runtimeError.Get(canonicalField(runtimeError, "code")).Enum())
	messageText := runtimeError.Get(canonicalField(runtimeError, "message")).String()
	err = &pluginsdk.RuntimeError{Code: code, Message: messageText, Retryable: runtimeError.Get(canonicalField(runtimeError, "retryable")).Bool()}
	if code == pluginsdk.ErrorResourceExhausted || code == pluginsdk.ErrorDeadlineExceeded {
		return policy.ModuleResponse{}, policy.BudgetError(code.String(), err)
	}
	return policy.ModuleResponse{}, policy.RuntimeError(code.String(), err)
}

func mapRuntimeError(err error) error {
	var runtimeError *RuntimeError
	if errors.As(err, &runtimeError) {
		switch runtimeError.Code {
		case ErrorInputBudget:
			return policy.BudgetErrorFor(pluginsdk.BudgetDimensionInput, string(runtimeError.Code), err)
		case ErrorOutputBudget:
			return policy.BudgetErrorFor(pluginsdk.BudgetDimensionOutput, string(runtimeError.Code), err)
		case ErrorMemoryBudget:
			return policy.BudgetErrorFor(pluginsdk.BudgetDimensionMemory, string(runtimeError.Code), err)
		case ErrorConcurrencyBudget:
			return policy.BudgetErrorFor(pluginsdk.BudgetDimensionConcurrency, string(runtimeError.Code), err)
		case ErrorDeadline:
			return policy.BudgetErrorFor(pluginsdk.BudgetDimensionDeadline, string(runtimeError.Code), err)
		}
		return policy.RuntimeError(string(runtimeError.Code), err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return policy.BudgetErrorFor(pluginsdk.BudgetDimensionDeadline, "deadline", err)
	}
	return policy.RuntimeError("runtime-error", err)
}

func canonicalPolicyMessage(name protoreflect.Name) (protoreflect.Message, error) {
	descriptor, err := protoschema.Message(protoreflect.FullName("nre.plugin.policy.v1." + string(name)))
	if err != nil {
		return nil, err
	}
	return dynamicpb.NewMessage(descriptor).ProtoReflect(), nil
}

func canonicalField(message protoreflect.Message, name protoreflect.Name) protoreflect.FieldDescriptor {
	return message.Descriptor().Fields().ByName(name)
}

func setCanonicalString(message protoreflect.Message, name protoreflect.Name, value string) {
	message.Set(canonicalField(message, name), protoreflect.ValueOfString(value))
}

func setCanonicalBytes(message protoreflect.Message, name protoreflect.Name, value []byte) {
	message.Set(canonicalField(message, name), protoreflect.ValueOfBytes(append([]byte(nil), value...)))
}

var _ policy.GenerationRuntime = (*PolicyGeneration)(nil)
var _ policy.GenerationFactory = GenerationFactory{}
