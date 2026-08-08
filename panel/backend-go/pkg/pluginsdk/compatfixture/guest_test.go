package compatfixture_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/pkg/pluginsdk"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/pkg/pluginsdk/compatfixture"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"google.golang.org/protobuf/encoding/protowire"
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

	evaluateRequest := policyEvaluateRequest{ExtensionPoint: "http.request", RequestID: "request-1", Payload: []byte("input")}
	evaluateWire := marshalEvaluateRequest(evaluateRequest)
	if decoded, err := unmarshalEvaluateRequest(evaluateWire); err != nil || decoded.ExtensionPoint != evaluateRequest.ExtensionPoint || decoded.RequestID != evaluateRequest.RequestID || !bytes.Equal(decoded.Payload, evaluateRequest.Payload) {
		t.Fatalf("EvaluateRequest IDL round trip = %+v, %v", decoded, err)
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
				field, decodeErr := unmarshalReadFieldRequest(requestBytes)
				if decodeErr != nil || field != "method" {
					stack[0] = pluginsdk.PackPolicyHostResult(pluginsdk.PolicyStatusInvalidArgument, 0)
					return
				}
				response := marshalBytesResponse([]byte("GET"), true)
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
	response, err := unmarshalEvaluateResponse(responseWire)
	if err != nil || response.Action != 1 || string(response.Payload) != "guest-ok" {
		t.Fatalf("EvaluateResponse IDL round trip = %+v, %v", response, err)
	}
	if hostCalls != 2 || len(capacities) != 2 || capacities[0] != 1 || capacities[1] != 64 {
		t.Fatalf("Host RESOURCE_EXHAUSTED retry calls=%d capacities=%v", hostCalls, capacities)
	}
	hostWire, ok := guest.Memory().Read(2048, uint32(len(marshalBytesResponse([]byte("GET"), true))))
	if !ok {
		t.Fatal("Host response is outside guest memory")
	}
	value, found, err := unmarshalBytesResponse(hostWire)
	if err != nil || !found || string(value) != "GET" {
		t.Fatalf("BytesResponse IDL round trip = %q, %t, %v", value, found, err)
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

type policyEvaluateRequest struct {
	ExtensionPoint string
	RequestID      string
	Payload        []byte
}

type policyEvaluateResponse struct {
	Action  uint64
	Payload []byte
}

func marshalEvaluateRequest(value policyEvaluateRequest) []byte {
	result := appendStringField(nil, 1, value.ExtensionPoint)
	result = appendStringField(result, 2, value.RequestID)
	return appendBytesField(result, 3, value.Payload)
}

func unmarshalEvaluateRequest(data []byte) (policyEvaluateRequest, error) {
	var result policyEvaluateRequest
	for len(data) > 0 {
		number, wireType, tagLength := protowire.ConsumeTag(data)
		if tagLength < 0 {
			return result, protowire.ParseError(tagLength)
		}
		data = data[tagLength:]
		switch number {
		case 1, 2:
			value, length := protowire.ConsumeString(data)
			if wireType != protowire.BytesType || length < 0 {
				return result, fmt.Errorf("invalid EvaluateRequest string field %d", number)
			}
			if number == 1 {
				result.ExtensionPoint = value
			} else {
				result.RequestID = value
			}
			data = data[length:]
		case 3:
			value, length := protowire.ConsumeBytes(data)
			if wireType != protowire.BytesType || length < 0 {
				return result, errorsForWire("EvaluateRequest payload", length)
			}
			result.Payload = append([]byte(nil), value...)
			data = data[length:]
		default:
			length := protowire.ConsumeFieldValue(number, wireType, data)
			if length < 0 {
				return result, protowire.ParseError(length)
			}
			data = data[length:]
		}
	}
	return result, nil
}

func unmarshalReadFieldRequest(data []byte) (string, error) {
	number, wireType, tagLength := protowire.ConsumeTag(data)
	if tagLength < 0 || number != 1 || wireType != protowire.BytesType {
		return "", errorsForWire("ReadFieldRequest", tagLength)
	}
	value, length := protowire.ConsumeString(data[tagLength:])
	if length < 0 || tagLength+length != len(data) {
		return "", errorsForWire("ReadFieldRequest name", length)
	}
	return value, nil
}

func marshalBytesResponse(value []byte, found bool) []byte {
	result := appendBytesField(nil, 1, value)
	result = protowire.AppendTag(result, 2, protowire.VarintType)
	return protowire.AppendVarint(result, protowire.EncodeBool(found))
}

func unmarshalBytesResponse(data []byte) ([]byte, bool, error) {
	var value []byte
	var found bool
	for len(data) > 0 {
		number, wireType, tagLength := protowire.ConsumeTag(data)
		if tagLength < 0 {
			return nil, false, protowire.ParseError(tagLength)
		}
		data = data[tagLength:]
		switch number {
		case 1:
			decoded, length := protowire.ConsumeBytes(data)
			if wireType != protowire.BytesType || length < 0 {
				return nil, false, errorsForWire("BytesResponse value", length)
			}
			value = append([]byte(nil), decoded...)
			data = data[length:]
		case 2:
			decoded, length := protowire.ConsumeVarint(data)
			if wireType != protowire.VarintType || length < 0 {
				return nil, false, errorsForWire("BytesResponse found", length)
			}
			found = protowire.DecodeBool(decoded)
			data = data[length:]
		default:
			length := protowire.ConsumeFieldValue(number, wireType, data)
			if length < 0 {
				return nil, false, protowire.ParseError(length)
			}
			data = data[length:]
		}
	}
	return value, found, nil
}

func unmarshalEvaluateResponse(data []byte) (policyEvaluateResponse, error) {
	var result policyEvaluateResponse
	for len(data) > 0 {
		number, wireType, tagLength := protowire.ConsumeTag(data)
		if tagLength < 0 {
			return result, protowire.ParseError(tagLength)
		}
		data = data[tagLength:]
		switch number {
		case 1:
			value, length := protowire.ConsumeVarint(data)
			if wireType != protowire.VarintType || length < 0 {
				return result, errorsForWire("EvaluateResponse action", length)
			}
			result.Action = value
			data = data[length:]
		case 2:
			value, length := protowire.ConsumeBytes(data)
			if wireType != protowire.BytesType || length < 0 {
				return result, errorsForWire("EvaluateResponse payload", length)
			}
			result.Payload = append([]byte(nil), value...)
			data = data[length:]
		default:
			length := protowire.ConsumeFieldValue(number, wireType, data)
			if length < 0 {
				return result, protowire.ParseError(length)
			}
			data = data[length:]
		}
	}
	return result, nil
}

func appendStringField(target []byte, number protowire.Number, value string) []byte {
	target = protowire.AppendTag(target, number, protowire.BytesType)
	return protowire.AppendString(target, value)
}

func appendBytesField(target []byte, number protowire.Number, value []byte) []byte {
	target = protowire.AppendTag(target, number, protowire.BytesType)
	return protowire.AppendBytes(target, value)
}

func errorsForWire(name string, code int) error {
	if code < 0 {
		return fmt.Errorf("%s: %w", name, protowire.ParseError(code))
	}
	return fmt.Errorf("%s has the wrong protobuf wire type", name)
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
