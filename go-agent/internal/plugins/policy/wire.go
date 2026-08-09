package policy

import (
	"fmt"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/protoschema"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

func PolicyEvaluateRequestFrameBytes(extensionPoint, requestID string, payload []byte) (int, error) {
	return pluginsdk.PolicyV1EvaluateRequestFrameBytes(extensionPoint, requestID, payload)
}

func policyBytesResponseFrameBytes(value []byte, found bool) (int, error) {
	message, err := canonicalPolicyWireMessage("BytesResponse")
	if err != nil {
		return 0, err
	}
	setPolicyWireBytes(message, "value", value)
	foundField := message.Descriptor().Fields().ByName("found")
	message.Set(foundField, protoreflect.ValueOfBool(found))
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message.Interface())
	if err != nil {
		return 0, fmt.Errorf("marshal deterministic policy bytes response: %w", err)
	}
	return len(encoded), nil
}

func canonicalPolicyWireMessage(name string) (*dynamicpb.Message, error) {
	descriptor, err := protoschema.Message(protoreflect.FullName("nre.plugin.policy.v1." + name))
	if err != nil {
		return nil, err
	}
	return dynamicpb.NewMessage(descriptor), nil
}

func setPolicyWireBytes(message *dynamicpb.Message, name string, value []byte) {
	field := message.Descriptor().Fields().ByName(protoreflect.Name(name))
	message.Set(field, protoreflect.ValueOfBytes(value))
}
