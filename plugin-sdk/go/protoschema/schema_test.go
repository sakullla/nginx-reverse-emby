package protoschema

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/internal/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestCanonicalDescriptorsMatchCheckedInIDL(t *testing.T) {
	sdkRoot := filepath.Join("..", "..")
	compiled, err := protogen.CompileDescriptorSet(context.Background(), sdkRoot)
	if err != nil {
		t.Fatal(err)
	}
	checkedIn, err := DescriptorSetBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(compiled, checkedIn) {
		t.Fatal("checked-in descriptor set differs from canonical policy.proto/plugin.proto compilation")
	}
	digest := sha256.Sum256(checkedIn)
	if got := hex.EncodeToString(digest[:]); got != CanonicalDescriptorSetSHA256 {
		t.Fatalf("descriptor checksum = %s, want %s", got, CanonicalDescriptorSetSHA256)
	}
	generated, err := protogen.RenderGo(compiled)
	if err != nil {
		t.Fatal(err)
	}
	checkedSource, err := os.ReadFile("descriptors_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generated, checkedSource) {
		t.Fatal("descriptor generator output is not reproducible")
	}
}

func TestPolicyV1DescriptorSurfaceIsStable(t *testing.T) {
	files, err := Files()
	if err != nil {
		t.Fatal(err)
	}
	file, err := files.FindFileByPath(PolicyV1File)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(file.Package()); got != "nre.plugin.policy.v1" {
		t.Fatalf("policy package = %q", got)
	}
	if file.Syntax() != protoreflect.Proto3 || file.Services().Len() != 0 || file.Extensions().Len() != 0 || file.Enums().Len() != 2 {
		t.Fatalf("policy file surface changed: syntax=%s services=%d extensions=%d enums=%d", file.Syntax(), file.Services().Len(), file.Extensions().Len(), file.Enums().Len())
	}
	if got := file.Options().(*descriptorpb.FileOptions).GetGoPackage(); got != "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/policyv1" {
		t.Fatalf("policy go_package = %q", got)
	}
	assertMessages(t, file.Messages(), []messageExpectation{
		{"InitRequest", []fieldExpectation{{"config", 1, protoreflect.BytesKind, false, ""}, {"granted_scopes", 2, protoreflect.StringKind, true, ""}, {"generation", 3, protoreflect.StringKind, false, ""}}},
		{"EvaluateRequest", []fieldExpectation{{"extension_point", 1, protoreflect.StringKind, false, ""}, {"request_id", 2, protoreflect.StringKind, false, ""}, {"payload", 3, protoreflect.BytesKind, false, ""}}},
		{"EvaluateResponse", []fieldExpectation{{"success", 1, protoreflect.MessageKind, false, "nre.plugin.policy.v1.EvaluateSuccess"}, {"error", 2, protoreflect.MessageKind, false, "nre.plugin.policy.v1.RuntimeError"}}},
		{"EvaluateSuccess", []fieldExpectation{{"action", 1, protoreflect.EnumKind, false, "nre.plugin.policy.v1.EvaluateSuccess.Action"}, {"payload", 2, protoreflect.BytesKind, false, ""}}},
		{"RuntimeError", []fieldExpectation{{"code", 1, protoreflect.EnumKind, false, "nre.plugin.policy.v1.RuntimeErrorCode"}, {"message", 2, protoreflect.StringKind, false, ""}, {"retryable", 3, protoreflect.BoolKind, false, ""}}},
		{"ReadFieldRequest", []fieldExpectation{{"name", 1, protoreflect.StringKind, false, ""}}},
		{"ReadNormalizedHTTPRequest", []fieldExpectation{}},
		{"NormalizedHTTPResponse", []fieldExpectation{{"path", 1, protoreflect.BytesKind, false, ""}, {"query", 2, protoreflect.BytesKind, false, ""}, {"headers", 3, protoreflect.BytesKind, false, ""}, {"trusted_source", 4, protoreflect.BytesKind, false, ""}, {"trusted_source_authenticated", 5, protoreflect.BoolKind, false, ""}, {"body_window_complete", 6, protoreflect.BoolKind, false, ""}, {"body_window_length", 7, protoreflect.Uint32Kind, false, ""}}},
		{"ReadBodyWindowRequest", []fieldExpectation{{"offset", 1, protoreflect.Uint32Kind, false, ""}, {"length", 2, protoreflect.Uint32Kind, false, ""}}},
		{"StateGetRequest", []fieldExpectation{{"key", 1, protoreflect.StringKind, false, ""}}},
		{"StatePutRequest", []fieldExpectation{{"key", 1, protoreflect.StringKind, false, ""}, {"value", 2, protoreflect.BytesKind, false, ""}}},
		{"EmitEventRequest", []fieldExpectation{{"code", 1, protoreflect.EnumKind, false, "nre.plugin.policy.v1.EmitEventRequest.Code"}, {"action", 2, protoreflect.EnumKind, false, "nre.plugin.policy.v1.EmitEventRequest.Action"}}},
		{"AddMetricRequest", []fieldExpectation{{"name", 1, protoreflect.StringKind, false, ""}, {"delta", 2, protoreflect.Sint64Kind, false, ""}}},
		{"BytesResponse", []fieldExpectation{{"value", 1, protoreflect.BytesKind, false, ""}, {"found", 2, protoreflect.BoolKind, false, ""}}},
	})
	assertEnum(t, file.Enums().ByName("ABIStatus"), []enumValueExpectation{{"ABI_STATUS_OK", 0}, {"ABI_STATUS_INVALID_ARGUMENT", 1}, {"ABI_STATUS_PERMISSION_DENIED", 2}, {"ABI_STATUS_RESOURCE_EXHAUSTED", 3}, {"ABI_STATUS_DEADLINE_EXCEEDED", 4}, {"ABI_STATUS_UNAVAILABLE", 5}, {"ABI_STATUS_INCOMPATIBLE_ABI", 6}, {"ABI_STATUS_INTERNAL", 7}})
	assertRuntimeErrorEnum(t, file.Enums().ByName("RuntimeErrorCode"))
	assertExclusiveResult(t, file.Messages().ByName("EvaluateResponse"), 2, nil)
	action := file.Messages().ByName("EvaluateSuccess").Enums().ByName("Action")
	assertEnum(t, action, []enumValueExpectation{{"ACTION_UNSPECIFIED", 0}, {"ALLOW", 1}, {"DENY", 2}, {"OBSERVE", 3}})
	emitEvent := file.Messages().ByName("EmitEventRequest")
	assertEnum(t, emitEvent.Enums().ByName("Code"), []enumValueExpectation{{"SECURITY_EVENT_CODE_UNSPECIFIED", 0}, {"SECURITY_EVENT_CODE_WAF_RULE_MATCH", 1}})
	assertEnum(t, emitEvent.Enums().ByName("Action"), []enumValueExpectation{{"SECURITY_EVENT_ACTION_UNSPECIFIED", 0}, {"SECURITY_EVENT_ACTION_OBSERVE", 1}, {"SECURITY_EVENT_ACTION_DENY", 2}})
}

func TestRPCV1ServiceAndMessageSurfaceIsStable(t *testing.T) {
	files, err := Files()
	if err != nil {
		t.Fatal(err)
	}
	file, err := files.FindFileByPath(RPCV1File)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(file.Package()); got != "nre.plugin.rpc.v1" {
		t.Fatalf("RPC package = %q", got)
	}
	if file.Syntax() != protoreflect.Proto3 || file.Services().Len() != 1 || file.Extensions().Len() != 0 || file.Enums().Len() != 2 {
		t.Fatalf("RPC file surface changed: syntax=%s services=%d extensions=%d enums=%d", file.Syntax(), file.Services().Len(), file.Extensions().Len(), file.Enums().Len())
	}
	if got := file.Options().(*descriptorpb.FileOptions).GetGoPackage(); got != "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/rpcv1" {
		t.Fatalf("RPC go_package = %q", got)
	}
	assertMessages(t, file.Messages(), []messageExpectation{
		{"HandshakeRequest", []fieldExpectation{{"abi", 1, protoreflect.StringKind, false, ""}, {"plugin_id", 2, protoreflect.StringKind, false, ""}, {"plugin_version", 3, protoreflect.StringKind, false, ""}, {"package_digest", 4, protoreflect.StringKind, false, ""}, {"artifact_digest", 5, protoreflect.StringKind, false, ""}, {"granted_scopes", 6, protoreflect.StringKind, true, ""}, {"generation", 7, protoreflect.StringKind, false, ""}, {"required_features", 8, protoreflect.StringKind, true, ""}}},
		{"HandshakeResponse", []fieldExpectation{{"abi", 1, protoreflect.StringKind, false, ""}, {"capabilities", 2, protoreflect.StringKind, true, ""}, {"features", 3, protoreflect.StringKind, true, ""}}},
		{"LifecycleRequest", []fieldExpectation{{"generation", 1, protoreflect.StringKind, false, ""}, {"config", 2, protoreflect.BytesKind, false, ""}}},
		{"LifecycleResponse", []fieldExpectation{{"success", 1, protoreflect.MessageKind, false, "nre.plugin.rpc.v1.LifecycleSuccess"}, {"error", 2, protoreflect.MessageKind, false, "nre.plugin.rpc.v1.RuntimeError"}}},
		{"LifecycleSuccess", []fieldExpectation{{"ready", 1, protoreflect.BoolKind, false, ""}}},
		{"ActionRequest", []fieldExpectation{{"generation", 1, protoreflect.StringKind, false, ""}, {"action_id", 2, protoreflect.StringKind, false, ""}, {"target_kind", 3, protoreflect.StringKind, false, ""}, {"target_id", 4, protoreflect.StringKind, false, ""}, {"operation_id", 5, protoreflect.StringKind, false, ""}, {"resource_handle", 6, protoreflect.StringKind, false, ""}, {"resource_results", 7, protoreflect.MessageKind, true, "nre.plugin.rpc.v1.ResourceResult"}}},
		{"ResourceCall", []fieldExpectation{{"request_id", 1, protoreflect.StringKind, false, ""}, {"resource_handle", 2, protoreflect.StringKind, false, ""}, {"operation", 3, protoreflect.EnumKind, false, "nre.plugin.rpc.v1.ResourceOperation"}, {"input", 4, protoreflect.BytesKind, false, ""}}},
		{"ResourceResult", []fieldExpectation{{"request_id", 1, protoreflect.StringKind, false, ""}, {"value", 2, protoreflect.BytesKind, false, ""}, {"error", 3, protoreflect.MessageKind, false, "nre.plugin.rpc.v1.RuntimeError"}}},
		{"ActionPlanResponse", []fieldExpectation{{"calls", 1, protoreflect.MessageKind, true, "nre.plugin.rpc.v1.ResourceCall"}, {"error", 2, protoreflect.MessageKind, false, "nre.plugin.rpc.v1.RuntimeError"}}},
		{"ActionResponse", []fieldExpectation{{"operation_id", 3, protoreflect.StringKind, false, ""}, {"success", 1, protoreflect.MessageKind, false, "nre.plugin.rpc.v1.ActionSuccess"}, {"error", 2, protoreflect.MessageKind, false, "nre.plugin.rpc.v1.RuntimeError"}, {"pending", 4, protoreflect.MessageKind, false, "nre.plugin.rpc.v1.ActionPending"}, {"missing", 5, protoreflect.MessageKind, false, "nre.plugin.rpc.v1.ActionMissing"}}},
		{"ActionSuccess", []fieldExpectation{{"accepted", 1, protoreflect.BoolKind, false, ""}}},
		{"ActionPending", []fieldExpectation{}},
		{"ActionMissing", []fieldExpectation{}},
		{"ActionQueryRequest", []fieldExpectation{{"generation", 1, protoreflect.StringKind, false, ""}, {"operation_id", 2, protoreflect.StringKind, false, ""}}},
		{"RuntimeError", []fieldExpectation{{"code", 1, protoreflect.EnumKind, false, "nre.plugin.rpc.v1.RuntimeErrorCode"}, {"message", 2, protoreflect.StringKind, false, ""}, {"retryable", 3, protoreflect.BoolKind, false, ""}}},
	})
	assertRuntimeErrorEnum(t, file.Enums().ByName("RuntimeErrorCode"))
	assertEnum(t, file.Enums().ByName("ResourceOperation"), []enumValueExpectation{{"RESOURCE_OPERATION_UNSPECIFIED", 0}, {"RESOURCE_OPERATION_INSPECT", 1}, {"RESOURCE_OPERATION_PROBE", 2}, {"RESOURCE_OPERATION_TRAFFIC_SUMMARY", 3}, {"RESOURCE_OPERATION_DNS_APPLY", 4}, {"RESOURCE_OPERATION_DOCKER_REQUEST", 5}})
	assertExclusiveResult(t, file.Messages().ByName("LifecycleResponse"), 2, nil)
	assertExclusiveResult(t, file.Messages().ByName("ActionResponse"), 4, map[protoreflect.Name]struct{}{"operation_id": {}})
	assertExclusiveResult(t, file.Messages().ByName("ResourceResult"), 2, map[protoreflect.Name]struct{}{"request_id": {}})
	services := file.Services()
	if services.Len() != 1 || services.Get(0).Name() != "PluginRuntime" {
		t.Fatalf("RPC services changed: %v", services.Len())
	}
	methods := services.Get(0).Methods()
	wantMethods := []struct{ name, input, output string }{
		{"Handshake", "nre.plugin.rpc.v1.HandshakeRequest", "nre.plugin.rpc.v1.HandshakeResponse"},
		{"Prepare", "nre.plugin.rpc.v1.LifecycleRequest", "nre.plugin.rpc.v1.LifecycleResponse"},
		{"Activate", "nre.plugin.rpc.v1.LifecycleRequest", "nre.plugin.rpc.v1.LifecycleResponse"},
		{"PlanAction", "nre.plugin.rpc.v1.ActionRequest", "nre.plugin.rpc.v1.ActionPlanResponse"},
		{"InvokeAction", "nre.plugin.rpc.v1.ActionRequest", "nre.plugin.rpc.v1.ActionResponse"},
		{"QueryAction", "nre.plugin.rpc.v1.ActionQueryRequest", "nre.plugin.rpc.v1.ActionResponse"},
		{"Stop", "nre.plugin.rpc.v1.LifecycleRequest", "nre.plugin.rpc.v1.LifecycleResponse"},
	}
	if methods.Len() != len(wantMethods) {
		t.Fatalf("RPC method count = %d", methods.Len())
	}
	for index, want := range wantMethods {
		method := methods.Get(index)
		if string(method.Name()) != want.name || string(method.Input().FullName()) != want.input || string(method.Output().FullName()) != want.output || method.IsStreamingClient() || method.IsStreamingServer() {
			t.Fatalf("RPC method %d changed: %s(%s) returns (%s), client_stream=%t server_stream=%t", index, method.Name(), method.Input().FullName(), method.Output().FullName(), method.IsStreamingClient(), method.IsStreamingServer())
		}
	}
}

type fieldExpectation struct {
	name     protoreflect.Name
	number   protoreflect.FieldNumber
	kind     protoreflect.Kind
	repeated bool
	typeName protoreflect.FullName
}

type messageExpectation struct {
	name   protoreflect.Name
	fields []fieldExpectation
}

func assertMessages(t *testing.T, messages protoreflect.MessageDescriptors, want []messageExpectation) {
	t.Helper()
	if messages.Len() != len(want) {
		t.Fatalf("message count = %d, want %d", messages.Len(), len(want))
	}
	for messageIndex, expectedMessage := range want {
		message := messages.Get(messageIndex)
		if message.Name() != expectedMessage.name {
			t.Fatalf("message %d = %q, want %q", messageIndex, message.Name(), expectedMessage.name)
		}
		fields := message.Fields()
		if fields.Len() != len(expectedMessage.fields) {
			t.Fatalf("message %s field count = %d, want %d", message.Name(), fields.Len(), len(expectedMessage.fields))
		}
		for fieldIndex, expectedField := range expectedMessage.fields {
			field := fields.Get(fieldIndex)
			var typeName protoreflect.FullName
			if field.Message() != nil {
				typeName = field.Message().FullName()
			} else if field.Enum() != nil {
				typeName = field.Enum().FullName()
			}
			if field.Name() != expectedField.name || field.Number() != expectedField.number || field.Kind() != expectedField.kind || (field.Cardinality() == protoreflect.Repeated) != expectedField.repeated || typeName != expectedField.typeName {
				t.Fatalf("message %s field %d changed: name=%s number=%d kind=%s repeated=%t type=%s", message.Name(), fieldIndex, field.Name(), field.Number(), field.Kind(), field.Cardinality() == protoreflect.Repeated, typeName)
			}
		}
	}
}

type enumValueExpectation struct {
	name   protoreflect.Name
	number protoreflect.EnumNumber
}

func assertEnum(t *testing.T, enum protoreflect.EnumDescriptor, want []enumValueExpectation) {
	t.Helper()
	if enum == nil {
		t.Fatal("enum surface changed: descriptor is missing")
	}
	if enum.Values().Len() != len(want) {
		t.Fatalf("enum surface changed: descriptor=%v values=%d", enum, enum.Values().Len())
	}
	for index, expected := range want {
		value := enum.Values().Get(index)
		if value.Name() != expected.name || value.Number() != expected.number {
			t.Fatalf("enum value %d changed: %s=%d", index, value.Name(), value.Number())
		}
	}
}

func assertRuntimeErrorEnum(t *testing.T, enum protoreflect.EnumDescriptor) {
	t.Helper()
	assertEnum(t, enum, []enumValueExpectation{
		{"RUNTIME_ERROR_CODE_UNSPECIFIED", 0},
		{"RUNTIME_ERROR_CODE_INVALID_ARGUMENT", 1},
		{"RUNTIME_ERROR_CODE_PERMISSION_DENIED", 2},
		{"RUNTIME_ERROR_CODE_RESOURCE_EXHAUSTED", 3},
		{"RUNTIME_ERROR_CODE_DEADLINE_EXCEEDED", 4},
		{"RUNTIME_ERROR_CODE_UNAVAILABLE", 5},
		{"RUNTIME_ERROR_CODE_INCOMPATIBLE_ABI", 6},
		{"RUNTIME_ERROR_CODE_INTERNAL", 7},
	})
}

func assertExclusiveResult(t *testing.T, message protoreflect.MessageDescriptor, resultFields int, envelopeFields map[protoreflect.Name]struct{}) {
	t.Helper()
	if message == nil || message.Oneofs().Len() != 1 || message.Oneofs().Get(0).Name() != "result" || message.Oneofs().Get(0).Fields().Len() != resultFields {
		t.Fatalf("%v does not define the canonical exclusive result oneof", message)
	}
	for index := 0; index < message.Fields().Len(); index++ {
		field := message.Fields().Get(index)
		if _, ok := envelopeFields[field.Name()]; ok {
			if field.ContainingOneof() != nil {
				t.Fatalf("%s.%s must remain outside the result oneof", message.FullName(), field.Name())
			}
			continue
		}
		if field.ContainingOneof() != message.Oneofs().Get(0) {
			t.Fatalf("%s.%s is outside the result oneof", message.FullName(), field.Name())
		}
	}
}
