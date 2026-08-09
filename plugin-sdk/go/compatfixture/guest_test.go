package compatfixture_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/compatfixture"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/protoschema"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const policyV1GuestSHA256 = "fc89d8d018d749de23b6979774e917f71b22883dd302d931b3791e37f50ea7ec"

func TestPolicyV1RuntimeGoldenGuestHostRoundTrip(t *testing.T) {
	generated := compatfixture.PolicyV1GuestWASM()
	digest := sha256.Sum256(generated)
	if got := hex.EncodeToString(digest[:]); got != policyV1GuestSHA256 {
		t.Fatalf("generated guest SHA-256 = %s, want %s", got, policyV1GuestSHA256)
	}
	fixtureName := filepath.Join("..", "..", "policy", "v1", "testdata", "compatible_guest.wasm.hex")
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
	readmeName := filepath.Join("..", "..", "README.md")
	readme, err := os.ReadFile(readmeName)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), policyV1GuestSHA256) {
		t.Fatalf("plugin SDK README does not declare golden guest SHA-256 %s", policyV1GuestSHA256)
	}

	initRequest := newPolicyMessage(t, "InitRequest")
	setPolicyBytes(t, initRequest, "config", []byte(`{"mode":"compat"}`))
	grantsField := requiredPolicyField(t, initRequest, "granted_scopes")
	grants := initRequest.ProtoReflect().Mutable(grantsField).List()
	grants.Append(protoreflect.ValueOfString("http.inspect"))
	grants.Append(protoreflect.ValueOfString("state.read"))
	setPolicyString(t, initRequest, "generation", "compat-generation-1")
	initWire := marshalPolicyMessage(t, initRequest)
	if !bytes.Equal(initWire, compatfixture.CanonicalPolicyV1InitRequest()) {
		t.Fatal("fixture InitRequest differs from the canonical non-empty SDK message")
	}
	decodedInit := unmarshalPolicyMessage(t, "InitRequest", initWire)
	decodedGrantsField := requiredPolicyField(t, decodedInit, "granted_scopes")
	decodedGrants := decodedInit.ProtoReflect().Get(decodedGrantsField).List()
	if string(policyBytes(t, decodedInit, "config")) != `{"mode":"compat"}` || policyString(t, decodedInit, "generation") != "compat-generation-1" || decodedGrants.Len() != 2 || decodedGrants.Get(0).String() != "http.inspect" || decodedGrants.Get(1).String() != "state.read" {
		t.Fatalf("InitRequest IDL round trip = %v", decodedInit)
	}

	evaluateRequest := newPolicyMessage(t, "EvaluateRequest")
	setPolicyString(t, evaluateRequest, "extension_point", "http.request")
	setPolicyString(t, evaluateRequest, "request_id", "request-1")
	setPolicyBytes(t, evaluateRequest, "payload", []byte("input"))
	evaluateWire := marshalPolicyMessage(t, evaluateRequest)
	if !bytes.Equal(evaluateWire, compatfixture.CanonicalPolicyV1EvaluateRequest()) {
		t.Fatal("fixture EvaluateRequest differs from the canonical SDK message")
	}
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
	allocateAndWrite := func(wire []byte) uint32 {
		t.Helper()
		pointer := callFixtureI32(t, ctx, guest.ExportedFunction(pluginsdk.PolicyExportAllocate), uint64(len(wire)))
		if pointer < 4096 || !guest.Memory().Write(pointer, wire) {
			t.Fatalf("allocator returned unusable pointer %d", pointer)
		}
		return pointer
	}
	free := func(pointer uint32, wire []byte) {
		t.Helper()
		if _, err := guest.ExportedFunction(pluginsdk.PolicyExportFree).Call(ctx, uint64(pointer), uint64(len(wire))); err != nil {
			t.Fatalf("free fixture allocation: %v", err)
		}
	}
	if status := callFixtureI32(t, ctx, guest.ExportedFunction(pluginsdk.PolicyExportInit), 1024, uint64(len(initWire))); pluginsdk.PolicyStatus(status) != pluginsdk.PolicyStatusInvalidArgument {
		t.Fatalf("unowned init status = %d", status)
	}
	malformedInit := append([]byte(nil), initWire[:len(initWire)-1]...)
	malformedPointer := allocateAndWrite(malformedInit)
	if status := callFixtureI32(t, ctx, guest.ExportedFunction(pluginsdk.PolicyExportInit), uint64(malformedPointer), uint64(len(malformedInit))); pluginsdk.PolicyStatus(status) != pluginsdk.PolicyStatusInvalidArgument {
		t.Fatalf("malformed init status = %d", status)
	}
	free(malformedPointer, malformedInit)
	wrongPointer := allocateAndWrite(evaluateWire)
	if status := callFixtureI32(t, ctx, guest.ExportedFunction(pluginsdk.PolicyExportInit), uint64(wrongPointer), uint64(len(evaluateWire))); pluginsdk.PolicyStatus(status) != pluginsdk.PolicyStatusInvalidArgument {
		t.Fatalf("wrong-message init status = %d", status)
	}
	free(wrongPointer, evaluateWire)
	freedPointer := allocateAndWrite(initWire)
	free(freedPointer, initWire)
	if status := callFixtureI32(t, ctx, guest.ExportedFunction(pluginsdk.PolicyExportInit), uint64(freedPointer), uint64(len(initWire))); pluginsdk.PolicyStatus(status) != pluginsdk.PolicyStatusInvalidArgument {
		t.Fatalf("freed init ownership status = %d", status)
	}
	if evaluated, err := guest.ExportedFunction(pluginsdk.PolicyExportEvaluate).Call(ctx, uint64(freedPointer), uint64(len(initWire))); err != nil || len(evaluated) != 1 || evaluated[0] != 0 {
		t.Fatalf("freed evaluate ownership = %v, %v", evaluated, err)
	}
	malformedEvaluate := append([]byte(nil), evaluateWire[:len(evaluateWire)-1]...)
	malformedEvaluatePointer := allocateAndWrite(malformedEvaluate)
	if evaluated, err := guest.ExportedFunction(pluginsdk.PolicyExportEvaluate).Call(ctx, uint64(malformedEvaluatePointer), uint64(len(malformedEvaluate))); err != nil || len(evaluated) != 1 || evaluated[0] != 0 {
		t.Fatalf("malformed evaluate message = %v, %v", evaluated, err)
	}
	free(malformedEvaluatePointer, malformedEvaluate)
	wrongEvaluatePointer := allocateAndWrite(initWire)
	if evaluated, err := guest.ExportedFunction(pluginsdk.PolicyExportEvaluate).Call(ctx, uint64(wrongEvaluatePointer), uint64(len(initWire))); err != nil || len(evaluated) != 1 || evaluated[0] != 0 {
		t.Fatalf("wrong evaluate message = %v, %v", evaluated, err)
	}
	free(wrongEvaluatePointer, initWire)

	initPointer := allocateAndWrite(initWire)
	if second := callFixtureI32(t, ctx, guest.ExportedFunction(pluginsdk.PolicyExportAllocate), uint64(len(evaluateWire))); second != 0 {
		t.Fatalf("allocator accepted overlapping host input at %d", second)
	}
	if status := callFixtureI32(t, ctx, guest.ExportedFunction(pluginsdk.PolicyExportInit), uint64(initPointer), uint64(len(initWire))); pluginsdk.PolicyStatus(status) != pluginsdk.PolicyStatusOK {
		t.Fatalf("canonical init status = %d", status)
	}
	free(initPointer, initWire)

	inputPointer := allocateAndWrite(evaluateWire)
	if inputPointer == initPointer {
		t.Fatal("init and evaluate unexpectedly reused one allocation")
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
	if err := pluginsdk.ValidatePolicyEvaluateResponseFrame(responseWire); err != nil {
		t.Fatalf("EvaluateResponse frame validation: %v", err)
	}
	successField := requiredPolicyField(t, response, "success")
	success := response.Get(successField).Message()
	actionField := requiredMessageField(t, success, "action")
	action := actionField.Enum().Values().ByNumber(success.Get(actionField).Enum())
	if action == nil || action.Name() != "ALLOW" || string(success.Get(requiredMessageField(t, success, "payload")).Bytes()) != "guest-ok" {
		t.Fatalf("EvaluateResponse IDL round trip = %v", response)
	}
	if hostCalls != 2 || len(capacities) != 2 || capacities[0] != 1 || capacities[1] != uint32(len(hostResponseWireForTest(t))) {
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
	free(responsePointer, responseWire)
	free(inputPointer, evaluateWire)
	if status := callFixtureI32(t, ctx, guest.ExportedFunction(pluginsdk.PolicyExportReset)); pluginsdk.PolicyStatus(status) != pluginsdk.PolicyStatusOK {
		t.Fatalf("reset status = %d", status)
	}
}

func TestPolicyV1GoldenGuestRejectsInvalidHostRetryResults(t *testing.T) {
	validLength := uint32(len(hostResponseWireForTest(t)))
	tests := []struct {
		name       string
		results    []uint64
		capacities []uint32
	}{
		{name: "first non retryable", results: []uint64{pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusInvalidArgument, 0)}, capacities: []uint32{1}},
		{name: "required does not exceed capacity", results: []uint64{pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusResourceExhausted, 1)}, capacities: []uint32{1}},
		{name: "required exceeds fixed response window", results: []uint64{pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusResourceExhausted, 2049)}, capacities: []uint32{1}},
		{name: "repeated exhaustion", results: []uint64{pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusResourceExhausted, validLength), pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusResourceExhausted, validLength)}, capacities: []uint32{1, validLength}},
		{name: "second non ok", results: []uint64{pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusResourceExhausted, validLength), pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusUnavailable, 0)}, capacities: []uint32{1, validLength}},
		{name: "second length exceeds capacity", results: []uint64{pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusResourceExhausted, validLength), pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusOK, validLength+1)}, capacities: []uint32{1, validLength}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, capacities := runPolicyFixtureWithHostResults(t, test.results)
			if result != 0 {
				t.Fatalf("evaluate returned success buffer %#x", result)
			}
			if !slices.Equal(capacities, test.capacities) {
				t.Fatalf("host capacities = %v, want %v", capacities, test.capacities)
			}
		})
	}
}

func runPolicyFixtureWithHostResults(t *testing.T, scripted []uint64) (uint64, []uint32) {
	t.Helper()
	ctx := context.Background()
	runtime := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCoreFeatures(api.CoreFeaturesV1))
	t.Cleanup(func() { _ = runtime.Close(ctx) })
	hostBuilder := runtime.NewHostModuleBuilder(pluginsdk.PolicyHostModule)
	callIndex := 0
	capacities := make([]uint32, 0, len(scripted))
	for name, signature := range pluginsdk.PolicyV1HostFunctions() {
		parameters := fixtureValueTypes(t, signature.Parameters)
		results := fixtureValueTypes(t, signature.Results)
		if name == pluginsdk.PolicyHostReadField {
			hostBuilder.NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
				capacities = append(capacities, api.DecodeU32(stack[3]))
				if callIndex >= len(scripted) {
					stack[0] = pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusInternal, 0)
					return
				}
				stack[0] = scripted[callIndex]
				callIndex++
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
	guest, err := runtime.Instantiate(ctx, compatfixture.PolicyV1GuestWASM())
	if err != nil {
		t.Fatal(err)
	}
	allocate := guest.ExportedFunction(pluginsdk.PolicyExportAllocate)
	free := guest.ExportedFunction(pluginsdk.PolicyExportFree)
	initWire := compatfixture.CanonicalPolicyV1InitRequest()
	initPointer := callFixtureI32(t, ctx, allocate, uint64(len(initWire)))
	if !guest.Memory().Write(initPointer, initWire) {
		t.Fatal("write init request")
	}
	if status := callFixtureI32(t, ctx, guest.ExportedFunction(pluginsdk.PolicyExportInit), uint64(initPointer), uint64(len(initWire))); pluginsdk.PolicyStatus(status) != pluginsdk.PolicyStatusOK {
		t.Fatalf("init status = %d", status)
	}
	if _, err := free.Call(ctx, uint64(initPointer), uint64(len(initWire))); err != nil {
		t.Fatal(err)
	}
	evaluateWire := compatfixture.CanonicalPolicyV1EvaluateRequest()
	pointer := callFixtureI32(t, ctx, allocate, uint64(len(evaluateWire)))
	if !guest.Memory().Write(pointer, evaluateWire) {
		t.Fatal("write evaluate request")
	}
	result, err := guest.ExportedFunction(pluginsdk.PolicyExportEvaluate).Call(ctx, uint64(pointer), uint64(len(evaluateWire)))
	if err != nil || len(result) != 1 {
		t.Fatalf("evaluate = %v, %v", result, err)
	}
	return result[0], capacities
}

func hostResponseWireForTest(t *testing.T) []byte {
	t.Helper()
	response := newPolicyMessage(t, "BytesResponse")
	setPolicyBytes(t, response, "value", []byte("GET"))
	setPolicyBool(t, response, "found", true)
	return marshalPolicyMessage(t, response)
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

func requiredMessageField(t *testing.T, message protoreflect.Message, name protoreflect.Name) protoreflect.FieldDescriptor {
	t.Helper()
	field := message.Descriptor().Fields().ByName(name)
	if field == nil {
		t.Fatalf("canonical %s.%s field is missing", message.Descriptor().FullName(), name)
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
