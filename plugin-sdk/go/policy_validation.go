package pluginsdk

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/protoschema"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const (
	PolicyIdentityMaxBytes  = 512
	PolicyRequestIDMaxBytes = 256
)

// ValidatePolicyIdentity is the canonical validation boundary for policy,
// chain, plugin, package, instance, signer, and artifact identity fields.
func ValidatePolicyIdentity(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("policy identity is empty")
	}
	if value != strings.TrimSpace(value) {
		return errors.New("policy identity has surrounding whitespace")
	}
	if len(value) > PolicyIdentityMaxBytes {
		return fmt.Errorf("policy identity exceeds %d bytes", PolicyIdentityMaxBytes)
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return errors.New("policy identity contains a control delimiter")
	}
	return nil
}

// PolicyV1EvaluateRequestFrameBytes returns the size of the complete
// deterministic nre:policy/v1 EvaluateRequest protobuf frame.
func PolicyV1EvaluateRequestFrameBytes(extensionPoint, requestID string, input []byte) (int, error) {
	descriptor, err := protoschema.Message("nre.plugin.policy.v1.EvaluateRequest")
	if err != nil {
		return 0, err
	}
	message := dynamicpb.NewMessage(descriptor)
	setString := func(name protoreflect.Name, value string) {
		message.Set(descriptor.Fields().ByName(name), protoreflect.ValueOfString(value))
	}
	setString("extension_point", extensionPoint)
	setString("request_id", requestID)
	message.Set(descriptor.Fields().ByName("payload"), protoreflect.ValueOfBytes(append([]byte(nil), input...)))
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
	if err != nil {
		return 0, fmt.Errorf("marshal deterministic policy evaluate request: %w", err)
	}
	return len(encoded), nil
}
