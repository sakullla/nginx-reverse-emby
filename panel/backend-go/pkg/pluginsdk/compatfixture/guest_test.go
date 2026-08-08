package compatfixture_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/pkg/pluginsdk"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/pkg/pluginsdk/compatfixture"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/pkg/pluginsdk/protoschema"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const policyV1GuestSHA256 = "c8f87657f28679373f3a74784194b86b56e244190bec409b385867e073174530"

func TestPolicyV1RuntimeGoldenGuestHostRoundTrip(t *testing.T) {
	generated := compatfixture.PolicyV1GuestWASM()
	digest := sha256.Sum256(generated)
	if got := hex.EncodeToString(digest[:]); got != policyV1GuestSHA256 {
		t.Fatalf("generated guest SHA-256 = %s, want %s", got, policyV1GuestSHA256)
	}
	fixtureName := filepath.Join("..", "..", "..", "..", "..", "plugin-sdk", "policy", "v1", "testdata", "compatible_guest.wasm.hex")
	encodedFixture, err := os.ReadFile(fixtureName)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := hex.DecodeString(strings.TrimSpace(string(encodedFixture)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fixture, generated) {
		t.Fatal("checked-in golden guest differs from the deterministic SDK generator")
	}

	evaluateRequest := newPolicyMessage(t, "EvaluateRequest")
	setPolicyString(t, evaluateRequest, "extension_point", "http.request")
	setPolicyString(t, evaluateRequest, "request_id", "request-1")
	setPolicyBytes(t, evaluateRequest, "payload", []byte("input"))
	evaluateWire := marshalPolicyMessage(t, evaluateRequest)
	decodedRequest := unmarshalPolicyMessage(t, "EvaluateRequest", evaluateWire)
	if policyString(t, decodedRequest, "extension_point") != "http.request" || policyString(t, decodedRequest, "request_id") != "request-1" || !bytes.Equal(policyBytes(t, decodedRequest, "payload"), []byte("input")) {
		t.Fatalf("EvaluateRequest IDL round trip = %v", decodedRequest)
	}

	ctx := context.Background()
	runtime := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCoreFeatures(api.CoreFeaturesV1))
	defer runtime.Close(ctx)
	hostBuilder := runtime.NewHostModuleBuilder(pluginsdk.PolicyHostModule)
	hostCalls := 0
	var capacities []uint32
	for name, signature := range pluginsdk.PolicyV1HostFunctions() {
		parameters := fixtureValueTypes(t, signature.Parameters)
		results := fixtureValueTypes(t, signature.Results)
		if name == pluginsdk.PolicyHostReadField {
			hostBuilder.NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, module api.Module, stack []uint64) {
				requestPointer, requestLength := api.DecodeU32(stack[0]), api.DecodeU32(stack[1])
				responsePointer, responseCapacity := api.DecodeU32(stack[2]), api.DecodeU32(stack[3])
				requestBytes, ok := module.Memory().Read(requestPointer, requestLength)
				if !ok {
					stack[0] = pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusInvalidArgument, 0)
					return
				}
				request, decodeErr := decodePolicyMessage("ReadFieldRequest", requestBytes)
				if decodeErr != nil || messageString(request, "name") != "method" {
					stack[0] = pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusInvalidArgument, 0)
					return
				}
				responseMessage, responseErr := newPolicyMessageValue("BytesResponse")
				if responseErr != nil {
					stack[0] = pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusInternal, 0)
					return
				}
				setMessageBytes(responseMessage, "value", []byte("GET"))
				setMessageBool(responseMessage, "found", true)
				response, responseErr := (proto.MarshalOptions{Deterministic: true}).Marshal(responseMessage)
				if responseErr != nil {
					stack[0] = pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusInternal, 0)
					return
				}
				hostCalls++
				capacities = append(capacities, responseCapacity)
				if responseCapacity < uint32(len(response)) {
					stack[0] = pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusResourceExhausted, uint32(len(response)))
					return
				}
				if !module.Memory().Write(responsePointer, response) {
					stack[0] = pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusInvalidArgument, 0)
					return
				}
				stack[0] = pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusOK, uint32(len(response)))
			}), parameters, results).Export(name)
			continue
		}
		hostBuilder.NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			stack[0] = pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusUnavailable, 0)
		}), parameters, results).Export(name)
	}
	if _, err := hostBuilder.Instantiate(ctx); err != nil {
		t.Fatal(err)
	}
	compiled, err := runtime.CompileModule(ctx, generated)
	if err != nil {
		t.Fatal(err)
	}
	defer compiled.Close(ctx)
	guest, err := runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithStartFunctions())
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close(ctx)

	version, err := guest.ExportedFunction(pluginsdk.PolicyExportVersion).Call(ctx)
	if err != nil || len(version) != 1 || uint32(version[0]) != pluginsdk.PolicyABIMajorVersion {
		t.Fatalf("ABI version = %v, %v", version, err)
	}
	inputPointer := callFixtureI32(t, ctx, guest.ExportedFunction(pluginsdk.PolicyExportAllocate), uint64(len(evaluateWire)))
	if inputPointer < 4096 || !guest.Memory().Write(inputPointer, evaluateWire) {
		t.Fatalf("allocator returned unusable pointer %d", inputPointer)
	}
	if status := callFixtureI32(t, ctx, guest.ExportedFunction(pluginsdk.PolicyExportInit), uint64(inputPointer), uint64(len(evaluateWire))); pluginsdk.PolicyStatus(status) != pluginsdk.PolicyStatusOK {
		t.Fatalf("init status = %d", status)
	}
	evaluated, err := guest.ExportedFunction(pluginsdk.PolicyExportEvaluate).Call(ctx, uint64(inputPointer), uint64(len(evaluateWire)))
	if err != nil || len(evaluated) != 1 {
		t.Fatalf("evaluate = %v, %v", evaluated, err)
	}
	responsePointer, responseLength := pluginsdk.UnpackPolicyBuffer(evaluated[0])
	if responsePointer != inputPointer+uint32(len(evaluateWire)) {
		t.Fatalf("evaluate response was not allocated from the guest heap: pointer=%d input=%d", responsePointer, inputPointer)
	}
	responseWire, ok := guest.Memory().Read(responsePointer, responseLength)
	if !ok {
		t.Fatal("evaluate response is outside guest memory")
	}
	response := unmarshalPolicyMessage(t, "EvaluateResponse", responseWire)
	actionField := requiredPolicyField(t, response, "action")
	action := actionField.Enum().Values().ByNumber(response.Get(actionField).Enum())
	if action == nil || action.Name() != "ALLOW" || string(policyBytes(t, response, "payload")) != "guest-ok" {
		t.Fatalf("EvaluateResponse IDL round trip = %v", response)
	}
	if hostCalls != 2 || len(capacities) != 2 || capacities[0] != 1 || capacities[1] != 64 {
		t.Fatalf("Host RESOURCE_EXHAUSTED retry calls=%d capacities=%v", hostCalls, capacities)
	}
	hostResponse := newPolicyMessage(t, "BytesResponse")
	setPolicyBytes(t, hostResponse, "value", []byte("GET"))
	setPolicyBool(t, hostResponse, "found", true)
	hostResponseWire := marshalPolicyMessage(t, hostResponse)
	hostWire, ok := guest.Memory().Read(2048, uint32(len(hostResponseWire)))
	if !ok {
		t.Fatal("Host response is outside guest memory")
	}
	decodedHostResponse := unmarshalPolicyMessage(t, "BytesResponse", hostWire)
	if !policyBool(t, decodedHostResponse, "found") || string(policyBytes(t, decodedHostResponse, "value")) != "GET" {
		t.Fatalf("BytesResponse IDL round trip = %v", decodedHostResponse)
	}
	if _, err := guest.ExportedFunction(pluginsdk.PolicyExportFree).Call(ctx, uint64(responsePointer), uint64(responseLength)); err != nil {
		t.Fatalf("free evaluate response: %v", err)
	}
	if _, err := guest.ExportedFunction(pluginsdk.PolicyExportFree).Call(ctx, uint64(inputPointer), uint64(len(evaluateWire))); err != nil {
		t.Fatalf("free evaluate request: %v", err)
	}
	if status := callFixtureI32(t, ctx, guest.ExportedFunction(pluginsdk.PolicyExportReset)); pluginsdk.PolicyStatus(status) != pluginsdk.PolicyStatusOK {
		t.Fatalf("reset status = %d", status)
	}
}

func newPolicyMessage(t *testing.T, name protoreflect.Name) *dynamicpb.Message {
	t.Helper()
	message, err := newPolicyMessageValue(name)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func newPolicyMessageValue(name protoreflect.Name) (*dynamicpb.Message, error) {
	descriptor, err := protoschema.Message(protoreflect.FullName("nre.plugin.policy.v1." + string(name)))
	if err != nil {
		return nil, err
	}
	return dynamicpb.NewMessage(descriptor), nil
}

func decodePolicyMessage(name protoreflect.Name, data []byte) (*dynamicpb.Message, error) {
	message, err := newPolicyMessageValue(name)
	if err != nil {
		return nil, err
	}
	if err := proto.Unmarshal(data, message); err != nil {
		return nil, err
	}
	return message, nil
}

func unmarshalPolicyMessage(t *testing.T, name protoreflect.Name, data []byte) *dynamicpb.Message {
	t.Helper()
	message, err := decodePolicyMessage(name, data)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func marshalPolicyMessage(t *testing.T, message proto.Message) []byte {
	t.Helper()
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func requiredPolicyField(t *testing.T, message protoreflect.ProtoMessage, name protoreflect.Name) protoreflect.FieldDescriptor {
	t.Helper()
	field := message.ProtoReflect().Descriptor().Fields().ByName(name)
	if field == nil {
		t.Fatalf("canonical %s.%s field is missing", message.ProtoReflect().Descriptor().FullName(), name)
	}
	return field
}

func setPolicyString(t *testing.T, message protoreflect.ProtoMessage, name protoreflect.Name, value string) {
	t.Helper()
	message.ProtoReflect().Set(requiredPolicyField(t, message, name), protoreflect.ValueOfString(value))
}

func setPolicyBytes(t *testing.T, message protoreflect.ProtoMessage, name protoreflect.Name, value []byte) {
	t.Helper()
	message.ProtoReflect().Set(requiredPolicyField(t, message, name), protoreflect.ValueOfBytes(value))
}

func setPolicyBool(t *testing.T, message protoreflect.ProtoMessage, name protoreflect.Name, value bool) {
	t.Helper()
	message.ProtoReflect().Set(requiredPolicyField(t, message, name), protoreflect.ValueOfBool(value))
}

func policyString(t *testing.T, message protoreflect.ProtoMessage, name protoreflect.Name) string {
	t.Helper()
	return message.ProtoReflect().Get(requiredPolicyField(t, message, name)).String()
}

func policyBytes(t *testing.T, message protoreflect.ProtoMessage, name protoreflect.Name) []byte {
	t.Helper()
	return message.ProtoReflect().Get(requiredPolicyField(t, message, name)).Bytes()
}

func policyBool(t *testing.T, message protoreflect.ProtoMessage, name protoreflect.Name) bool {
	t.Helper()
	return message.ProtoReflect().Get(requiredPolicyField(t, message, name)).Bool()
}

func messageField(message protoreflect.Message, name protoreflect.Name) protoreflect.FieldDescriptor {
	return message.Descriptor().Fields().ByName(name)
}

func messageString(message protoreflect.ProtoMessage, name protoreflect.Name) string {
	field := messageField(message.ProtoReflect(), name)
	if field == nil {
		return ""
	}
	return message.ProtoReflect().Get(field).String()
}

func setMessageBytes(message protoreflect.ProtoMessage, name protoreflect.Name, value []byte) {
	message.ProtoReflect().Set(messageField(message.ProtoReflect(), name), protoreflect.ValueOfBytes(value))
}

func setMessageBool(message protoreflect.ProtoMessage, name protoreflect.Name, value bool) {
	message.ProtoReflect().Set(messageField(message.ProtoReflect(), name), protoreflect.ValueOfBool(value))
}

func fixtureValueTypes(t *testing.T, values []pluginsdk.WASMValueType) []api.ValueType {
	t.Helper()
	result := make([]api.ValueType, len(values))
	for index, value := range values {
		switch value {
		case pluginsdk.WASMI32:
			result[index] = api.ValueTypeI32
		case pluginsdk.WASMI64:
			result[index] = api.ValueTypeI64
		default:
			t.Fatalf("unsupported fixture value type %#x", value)
		}
	}
	return result
}

func callFixtureI32(t *testing.T, ctx context.Context, function api.Function, parameters ...uint64) uint32 {
	t.Helper()
	if function == nil {
		t.Fatal("required fixture export is missing")
	}
	values, err := function.Call(ctx, parameters...)
	if err != nil || len(values) != 1 {
		t.Fatalf("fixture call = %v, %v", values, err)
	}
	return uint32(values[0])
}
