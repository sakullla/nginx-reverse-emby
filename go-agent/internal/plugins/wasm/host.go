package wasm

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/protoschema"
	"github.com/tetratelabs/wazero/api"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

type hostContextKey struct{}

var policyMessageDescriptors sync.Map

type hostInvocation struct {
	generation string
	host       pluginsdk.PolicyHost
	budget     Budget
}

func contextWithHost(ctx context.Context, generation string, host pluginsdk.PolicyHost, budget Budget) context.Context {
	return context.WithValue(ctx, hostContextKey{}, hostInvocation{generation: generation, host: host, budget: budget})
}

func (runtime *Runtime) instantiateHost(ctx context.Context) error {
	builder := runtime.wasm.NewHostModuleBuilder(pluginsdk.PolicyHostModule)
	for name, signature := range pluginsdk.PolicyV1HostFunctions() {
		functionName := name
		builder.NewFunctionBuilder().WithGoModuleFunction(
			api.GoModuleFunc(func(callContext context.Context, module api.Module, stack []uint64) {
				stack[0] = runtime.callHost(callContext, module, functionName, stack)
			}),
			wasmValueTypes(signature.Parameters),
			wasmValueTypes(signature.Results),
		).Export(name)
	}
	_, err := builder.Instantiate(ctx)
	return err
}

func (runtime *Runtime) callHost(ctx context.Context, module api.Module, name string, stack []uint64) uint64 {
	invocation, ok := ctx.Value(hostContextKey{}).(hostInvocation)
	if !ok || invocation.host == nil {
		return pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusUnavailable, 0)
	}
	requestPointer, requestLength := api.DecodeU32(stack[0]), api.DecodeU32(stack[1])
	responsePointer, responseCapacity := api.DecodeU32(stack[2]), api.DecodeU32(stack[3])
	if requestLength > invocation.budget.MaxInputBytes {
		runtime.observeHostBudget(invocation.generation, name, ErrorInputBudget, pluginsdk.BudgetDimensionInput)
		return pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusResourceExhausted, 0)
	}
	if responseCapacity > invocation.budget.MaxOutputBytes {
		runtime.observeHostBudget(invocation.generation, name, ErrorOutputBudget, pluginsdk.BudgetDimensionOutput)
		return pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusResourceExhausted, 0)
	}
	request, readable := module.Memory().Read(requestPointer, requestLength)
	if !readable {
		return pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusInvalidArgument, 0)
	}
	response, status, dimension := dispatchHost(ctx, invocation.host, name, append([]byte(nil), request...))
	if status != pluginsdk.PolicyStatusOK {
		runtime.observer.ObserveWASM(Event{
			Generation: invocation.generation,
			Operation:  "host." + name,
			Code:       hostStatusCode(status, dimension),
			Dimension:  dimension,
		})
		return pluginsdk.PackPolicyHostResult(status, 0)
	}
	if uint64(len(response)) > uint64(invocation.budget.MaxOutputBytes) {
		runtime.observeHostBudget(invocation.generation, name, ErrorOutputBudget, pluginsdk.BudgetDimensionOutput)
		return pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusResourceExhausted, uint32(len(response)))
	}
	if uint32(len(response)) > responseCapacity {
		return pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusResourceExhausted, uint32(len(response)))
	}
	if len(response) != 0 && !module.Memory().Write(responsePointer, response) {
		return pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusInvalidArgument, 0)
	}
	return pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusOK, uint32(len(response)))
}

func (runtime *Runtime) observeHostBudget(generation, name string, code ErrorCode, dimension pluginsdk.BudgetDimension) {
	runtime.observer.ObserveWASM(Event{
		Generation: generation,
		Operation:  "host." + name,
		Code:       code,
		Dimension:  dimension,
	})
}

func dispatchHost(ctx context.Context, host pluginsdk.PolicyHost, name string, request []byte) ([]byte, pluginsdk.PolicyStatus, pluginsdk.BudgetDimension) {
	if err := ctx.Err(); err != nil {
		return nil, pluginsdk.PolicyStatusDeadlineExceeded, pluginsdk.BudgetDimensionDeadline
	}
	switch name {
	case pluginsdk.PolicyHostReadField:
		message, err := decodePolicyMessage("ReadFieldRequest", request)
		if err != nil {
			return nil, pluginsdk.PolicyStatusInvalidArgument, ""
		}
		value, err := host.ReadField(ctx, messageString(message, "name"))
		return encodeBytesResponse(value, value != nil, err)
	case pluginsdk.PolicyHostReadNormalizedHTTP:
		if _, err := decodePolicyMessage("ReadNormalizedHTTPRequest", request); err != nil {
			return nil, pluginsdk.PolicyStatusInvalidArgument, ""
		}
		normalizedHost, ok := host.(pluginsdk.PolicyNormalizedHTTPHost)
		if !ok {
			return nil, pluginsdk.PolicyStatusIncompatibleABI, ""
		}
		value, err := normalizedHost.ReadNormalizedHTTP(ctx)
		return encodeNormalizedHTTPResponse(value, err)
	case pluginsdk.PolicyHostReadBodyWindow:
		message, err := decodePolicyMessage("ReadBodyWindowRequest", request)
		if err != nil {
			return nil, pluginsdk.PolicyStatusInvalidArgument, ""
		}
		value, err := host.ReadBodyWindow(ctx, messageUint32(message, "offset"), messageUint32(message, "length"))
		return encodeBytesResponse(value, value != nil, err)
	case pluginsdk.PolicyHostStateGet:
		message, err := decodePolicyMessage("StateGetRequest", request)
		if err != nil {
			return nil, pluginsdk.PolicyStatusInvalidArgument, ""
		}
		value, found, err := host.StateGet(ctx, messageString(message, "key"))
		return encodeBytesResponse(value, found, err)
	case pluginsdk.PolicyHostStatePut:
		message, err := decodePolicyMessage("StatePutRequest", request)
		if err != nil {
			return nil, pluginsdk.PolicyStatusInvalidArgument, ""
		}
		return encodeEmpty(host.StatePut(ctx, messageString(message, "key"), messageBytes(message, "value")))
	case pluginsdk.PolicyHostEmitEvent:
		message, err := decodePolicyMessage("EmitEventRequest", request)
		if err != nil {
			return nil, pluginsdk.PolicyStatusInvalidArgument, ""
		}
		event, err := pluginsdk.PolicySecurityEventFromWire(messageEnum(message, "code"), messageEnum(message, "action"))
		if err != nil {
			return nil, pluginsdk.PolicyStatusInvalidArgument, ""
		}
		return encodeEmpty(host.EmitEvent(ctx, event))
	case pluginsdk.PolicyHostAddMetric:
		message, err := decodePolicyMessage("AddMetricRequest", request)
		if err != nil {
			return nil, pluginsdk.PolicyStatusInvalidArgument, ""
		}
		return encodeEmpty(host.AddMetric(ctx, messageString(message, "name"), messageInt64(message, "delta")))
	default:
		return nil, pluginsdk.PolicyStatusPermissionDenied, ""
	}
}

func encodeNormalizedHTTPResponse(value pluginsdk.PolicyNormalizedHTTP, hostErr error) ([]byte, pluginsdk.PolicyStatus, pluginsdk.BudgetDimension) {
	if hostErr != nil {
		return nil, statusForHostError(hostErr), budgetDimensionForHostError(hostErr)
	}
	message, err := newPolicyMessage("NormalizedHTTPResponse")
	if err != nil {
		return nil, pluginsdk.PolicyStatusInternal, ""
	}
	setMessageBytes(message, "path", value.Path)
	setMessageBytes(message, "query", value.Query)
	setMessageBytes(message, "headers", value.Headers)
	setMessageBytes(message, "trusted_source", value.TrustedSource)
	setMessageBool(message, "trusted_source_authenticated", value.TrustedSourceAuthenticated)
	setMessageBool(message, "body_window_complete", value.BodyWindowComplete)
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
	if err != nil {
		return nil, pluginsdk.PolicyStatusInternal, ""
	}
	return encoded, pluginsdk.PolicyStatusOK, ""
}

func encodeEmpty(err error) ([]byte, pluginsdk.PolicyStatus, pluginsdk.BudgetDimension) {
	if err != nil {
		return nil, statusForHostError(err), budgetDimensionForHostError(err)
	}
	return nil, pluginsdk.PolicyStatusOK, ""
}

func encodeBytesResponse(value []byte, found bool, err error) ([]byte, pluginsdk.PolicyStatus, pluginsdk.BudgetDimension) {
	if err != nil {
		return nil, statusForHostError(err), budgetDimensionForHostError(err)
	}
	message, err := newPolicyMessage("BytesResponse")
	if err != nil {
		return nil, pluginsdk.PolicyStatusInternal, ""
	}
	setMessageBytes(message, "value", value)
	setMessageBool(message, "found", found)
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
	if err != nil {
		return nil, pluginsdk.PolicyStatusInternal, ""
	}
	return encoded, pluginsdk.PolicyStatusOK, ""
}

func budgetDimensionForHostError(err error) pluginsdk.BudgetDimension {
	var dimensionError pluginsdk.BudgetDimensionError
	if errors.As(err, &dimensionError) {
		return dimensionError.BudgetDimension()
	}
	return ""
}

func statusForHostError(err error) pluginsdk.PolicyStatus {
	var runtimeError *pluginsdk.RuntimeError
	if errors.As(err, &runtimeError) {
		switch runtimeError.Code {
		case pluginsdk.ErrorInvalidArgument:
			return pluginsdk.PolicyStatusInvalidArgument
		case pluginsdk.ErrorPermissionDenied:
			return pluginsdk.PolicyStatusPermissionDenied
		case pluginsdk.ErrorResourceExhausted:
			return pluginsdk.PolicyStatusResourceExhausted
		case pluginsdk.ErrorDeadlineExceeded:
			return pluginsdk.PolicyStatusDeadlineExceeded
		case pluginsdk.ErrorUnavailable:
			return pluginsdk.PolicyStatusUnavailable
		default:
			return pluginsdk.PolicyStatusInternal
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return pluginsdk.PolicyStatusDeadlineExceeded
	}
	return pluginsdk.PolicyStatusUnavailable
}

func hostStatusCode(status pluginsdk.PolicyStatus, dimension pluginsdk.BudgetDimension) ErrorCode {
	switch status {
	case pluginsdk.PolicyStatusDeadlineExceeded:
		return ErrorDeadline
	case pluginsdk.PolicyStatusResourceExhausted:
		switch dimension {
		case pluginsdk.BudgetDimensionInput:
			return ErrorInputBudget
		case pluginsdk.BudgetDimensionOutput:
			return ErrorOutputBudget
		default:
			return ErrorHost
		}
	default:
		return ErrorHost
	}
}

func newPolicyMessage(name protoreflect.Name) (*dynamicpb.Message, error) {
	fullName := protoreflect.FullName("nre.plugin.policy.v1." + string(name))
	if cached, ok := policyMessageDescriptors.Load(fullName); ok {
		return dynamicpb.NewMessage(cached.(protoreflect.MessageDescriptor)), nil
	}
	descriptor, err := protoschema.Message(fullName)
	if err != nil {
		return nil, err
	}
	actual, _ := policyMessageDescriptors.LoadOrStore(fullName, descriptor)
	return dynamicpb.NewMessage(actual.(protoreflect.MessageDescriptor)), nil
}

func decodePolicyMessage(name protoreflect.Name, encoded []byte) (*dynamicpb.Message, error) {
	message, err := newPolicyMessage(name)
	if err != nil {
		return nil, err
	}
	if err := proto.Unmarshal(encoded, message); err != nil {
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	return message, nil
}

func policyField(message protoreflect.ProtoMessage, name protoreflect.Name) protoreflect.FieldDescriptor {
	return message.ProtoReflect().Descriptor().Fields().ByName(name)
}

func messageString(message protoreflect.ProtoMessage, name protoreflect.Name) string {
	return message.ProtoReflect().Get(policyField(message, name)).String()
}

func messageBytes(message protoreflect.ProtoMessage, name protoreflect.Name) []byte {
	return append([]byte(nil), message.ProtoReflect().Get(policyField(message, name)).Bytes()...)
}

func messageUint32(message protoreflect.ProtoMessage, name protoreflect.Name) uint32 {
	return uint32(message.ProtoReflect().Get(policyField(message, name)).Uint())
}

func messageInt64(message protoreflect.ProtoMessage, name protoreflect.Name) int64 {
	return message.ProtoReflect().Get(policyField(message, name)).Int()
}

func messageBool(message protoreflect.ProtoMessage, name protoreflect.Name) bool {
	return message.ProtoReflect().Get(policyField(message, name)).Bool()
}

func messageEnum(message protoreflect.ProtoMessage, name protoreflect.Name) int32 {
	return int32(message.ProtoReflect().Get(policyField(message, name)).Enum())
}

func setMessageBytes(message protoreflect.ProtoMessage, name protoreflect.Name, value []byte) {
	message.ProtoReflect().Set(policyField(message, name), protoreflect.ValueOfBytes(append([]byte(nil), value...)))
}

func setMessageBool(message protoreflect.ProtoMessage, name protoreflect.Name, value bool) {
	message.ProtoReflect().Set(policyField(message, name), protoreflect.ValueOfBool(value))
}
