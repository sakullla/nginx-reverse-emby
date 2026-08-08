package protoschema

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/pkg/pluginsdk/internal/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestCanonicalDescriptorsMatchCheckedInIDL(t *testing.T) {
	sdkRoot := filepath.Join("..", "..", "..", "..", "..", "plugin-sdk")
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
	if file.Syntax() != protoreflect.Proto3 || file.Services().Len() != 0 || file.Extensions().Len() != 0 || file.Enums().Len() != 1 {
		t.Fatalf("policy file surface changed: syntax=%s services=%d extensions=%d enums=%d", file.Syntax(), file.Services().Len(), file.Extensions().Len(), file.Enums().Len())
	}
	if got := file.Options().(*descriptorpb.FileOptions).GetGoPackage(); got != "github.com/sakullla/nginx-reverse-emby/panel/backend-go/pkg/pluginsdk/policyv1" {
		t.Fatalf("policy go_package = %q", got)
	}
	assertMessages(t, file.Messages(), []messageExpectation{
		{"InitRequest", []fieldExpectation{{"config", 1, protoreflect.BytesKind, false, ""}, {"granted_scopes", 2, protoreflect.StringKind, true, ""}, {"generation", 3, protoreflect.StringKind, false, ""}}},
		{"EvaluateRequest", []fieldExpectation{{"extension_point", 1, protoreflect.StringKind, false, ""}, {"request_id", 2, protoreflect.StringKind, false, ""}, {"payload", 3, protoreflect.BytesKind, false, ""}}},
		{"EvaluateResponse", []fieldExpectation{{"action", 1, protoreflect.EnumKind, false, "nre.plugin.policy.v1.EvaluateResponse.Action"}, {"payload", 2, protoreflect.BytesKind, false, ""}, {"error", 3, protoreflect.MessageKind, false, "nre.plugin.policy.v1.RuntimeError"}}},
		{"RuntimeError", []fieldExpectation{{"code", 1, protoreflect.StringKind, false, ""}, {"message", 2, protoreflect.StringKind, false, ""}, {"retryable", 3, protoreflect.BoolKind, false, ""}}},
		{"ReadFieldRequest", []fieldExpectation{{"name", 1, protoreflect.StringKind, false, ""}}},
		{"ReadBodyWindowRequest", []fieldExpectation{{"offset", 1, protoreflect.Uint32Kind, false, ""}, {"length", 2, protoreflect.Uint32Kind, false, ""}}},
		{"StateGetRequest", []fieldExpectation{{"key", 1, protoreflect.StringKind, false, ""}}},
		{"StatePutRequest", []fieldExpectation{{"key", 1, protoreflect.StringKind, false, ""}, {"value", 2, protoreflect.BytesKind, false, ""}}},
		{"EmitEventRequest", []fieldExpectation{{"kind", 1, protoreflect.StringKind, false, ""}, {"payload", 2, protoreflect.BytesKind, false, ""}}},
		{"AddMetricRequest", []fieldExpectation{{"name", 1, protoreflect.StringKind, false, ""}, {"delta", 2, protoreflect.Sint64Kind, false, ""}}},
		{"BytesResponse", []fieldExpectation{{"value", 1, protoreflect.BytesKind, false, ""}, {"found", 2, protoreflect.BoolKind, false, ""}}},
	})
	assertEnum(t, file.Enums().ByName("ABIStatus"), []enumValueExpectation{{"ABI_STATUS_OK", 0}, {"ABI_STATUS_INVALID_ARGUMENT", 1}, {"ABI_STATUS_PERMISSION_DENIED", 2}, {"ABI_STATUS_RESOURCE_EXHAUSTED", 3}, {"ABI_STATUS_DEADLINE_EXCEEDED", 4}, {"ABI_STATUS_UNAVAILABLE", 5}, {"ABI_STATUS_INCOMPATIBLE_ABI", 6}, {"ABI_STATUS_INTERNAL", 7}})
	action := file.Messages().ByName("EvaluateResponse").Enums().ByName("Action")
	assertEnum(t, action, []enumValueExpectation{{"ACTION_UNSPECIFIED", 0}, {"ALLOW", 1}, {"DENY", 2}, {"OBSERVE", 3}})
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
	if file.Syntax() != protoreflect.Proto3 || file.Services().Len() != 1 || file.Extensions().Len() != 0 || file.Enums().Len() != 0 {
		t.Fatalf("RPC file surface changed: syntax=%s services=%d extensions=%d enums=%d", file.Syntax(), file.Services().Len(), file.Extensions().Len(), file.Enums().Len())
	}
	if got := file.Options().(*descriptorpb.FileOptions).GetGoPackage(); got != "github.com/sakullla/nginx-reverse-emby/panel/backend-go/pkg/pluginsdk/rpcv1" {
		t.Fatalf("RPC go_package = %q", got)
	}
	assertMessages(t, file.Messages(), []messageExpectation{
		{"HandshakeRequest", []fieldExpectation{{"abi", 1, protoreflect.StringKind, false, ""}, {"plugin_id", 2, protoreflect.StringKind, false, ""}, {"plugin_version", 3, protoreflect.StringKind, false, ""}, {"package_digest", 4, protoreflect.StringKind, false, ""}, {"artifact_digest", 5, protoreflect.StringKind, false, ""}, {"granted_scopes", 6, protoreflect.StringKind, true, ""}, {"generation", 7, protoreflect.StringKind, false, ""}}},
		{"HandshakeResponse", []fieldExpectation{{"abi", 1, protoreflect.StringKind, false, ""}, {"capabilities", 2, protoreflect.StringKind, true, ""}}},
		{"LifecycleRequest", []fieldExpectation{{"generation", 1, protoreflect.StringKind, false, ""}, {"config", 2, protoreflect.BytesKind, false, ""}}},
		{"LifecycleResponse", []fieldExpectation{{"ready", 1, protoreflect.BoolKind, false, ""}, {"error", 2, protoreflect.MessageKind, false, "nre.plugin.rpc.v1.RuntimeError"}}},
		{"RuntimeError", []fieldExpectation{{"code", 1, protoreflect.StringKind, false, ""}, {"message", 2, protoreflect.StringKind, false, ""}, {"retryable", 3, protoreflect.BoolKind, false, ""}}},
	})
	services := file.Services()
	if services.Len() != 1 || services.Get(0).Name() != "PluginRuntime" {
		t.Fatalf("RPC services changed: %v", services.Len())
	}
	methods := services.Get(0).Methods()
	wantMethods := []struct{ name, input, output string }{
		{"Handshake", "nre.plugin.rpc.v1.HandshakeRequest", "nre.plugin.rpc.v1.HandshakeResponse"},
		{"Prepare", "nre.plugin.rpc.v1.LifecycleRequest", "nre.plugin.rpc.v1.LifecycleResponse"},
		{"Activate", "nre.plugin.rpc.v1.LifecycleRequest", "nre.plugin.rpc.v1.LifecycleResponse"},
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
