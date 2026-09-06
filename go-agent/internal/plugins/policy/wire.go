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

// PolicyWAFEvaluateRequestFrameBytes returns the complete worst-case frame
// emitted by the Agent's HTTP WAF adapter, including Host-authored normalized
// path, query, headers, trusted source, and body-window metadata.
func PolicyWAFEvaluateRequestFrameBytes(extensionPoint, requestID string, payload []byte) (int, error) {
	normalized, err := canonicalPolicyWireMessage("NormalizedHTTPResponse")
	if err != nil {
		return 0, err
	}
	setPolicyWireBytes(normalized, "path", make([]byte, MaxWAFHTTPFieldValueBytes))
	setPolicyWireBytes(normalized, "query", make([]byte, MaxWAFHTTPFieldValueBytes))
	setPolicyWireBytes(normalized, "headers", make([]byte, MaxWAFHTTPHeadersBytes))
	setPolicyWireBytes(normalized, "trusted_source", make([]byte, MaxWAFHTTPTrustedSourceBytes))
	normalized.Set(normalized.Descriptor().Fields().ByName("trusted_source_authenticated"), protoreflect.ValueOfBool(true))
	normalized.Set(normalized.Descriptor().Fields().ByName("body_window_complete"), protoreflect.ValueOfBool(true))
	normalized.Set(normalized.Descriptor().Fields().ByName("body_window_length"), protoreflect.ValueOfUint32(MaxWAFHTTPBodyWindowBytes))
	normalizedFrame, err := (proto.MarshalOptions{Deterministic: true}).Marshal(normalized)
	if err != nil {
		return 0, fmt.Errorf("marshal deterministic normalized HTTP projection: %w", err)
	}

	request, err := canonicalPolicyWireMessage("EvaluateRequest")
	if err != nil {
		return 0, err
	}
	request.Set(request.Descriptor().Fields().ByName("extension_point"), protoreflect.ValueOfString(extensionPoint))
	request.Set(request.Descriptor().Fields().ByName("request_id"), protoreflect.ValueOfString(requestID))
	setPolicyWireBytes(request, "payload", payload)
	setPolicyWireBytes(request, "normalized_http", normalizedFrame)
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(request)
	if err != nil {
		return 0, fmt.Errorf("marshal deterministic WAF evaluate request: %w", err)
	}
	return len(encoded), nil
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
