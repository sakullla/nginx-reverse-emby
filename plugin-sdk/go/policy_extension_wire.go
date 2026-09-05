package pluginsdk

import (
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/protoschema"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

var policyExtensionDescriptors struct {
	once     sync.Once
	messages map[string]protoreflect.MessageDescriptor
	err      error
}

func newPolicyExtensionMessage(name string) (*dynamicpb.Message, error) {
	switch name {
	case "DatasetQueryRequest", "DatasetQueryResponse", "DatasetResolveRequest", "DatasetResolveResponse", "TrustedSourceResponse", "EmitEventRequest":
	default:
		return nil, errors.New("unknown policy extension message")
	}
	policyExtensionDescriptors.once.Do(func() {
		files, err := protoschema.Files()
		if err != nil {
			policyExtensionDescriptors.err = err
			return
		}
		policyExtensionDescriptors.messages = make(map[string]protoreflect.MessageDescriptor, 6)
		for _, name := range []string{"DatasetQueryRequest", "DatasetQueryResponse", "DatasetResolveRequest", "DatasetResolveResponse", "TrustedSourceResponse", "EmitEventRequest"} {
			descriptor, err := files.FindDescriptorByName(protoreflect.FullName("nre.plugin.policy.v1." + name))
			if err != nil {
				policyExtensionDescriptors.err = err
				return
			}
			message, ok := descriptor.(protoreflect.MessageDescriptor)
			if !ok {
				policyExtensionDescriptors.err = errors.New("policy extension descriptor is not a message")
				return
			}
			policyExtensionDescriptors.messages[name] = message
		}
	})
	if policyExtensionDescriptors.err != nil {
		return nil, policyExtensionDescriptors.err
	}
	return dynamicpb.NewMessage(policyExtensionDescriptors.messages[name]), nil
}

func policyExtensionField(message protoreflect.Message, name protoreflect.Name) protoreflect.FieldDescriptor {
	return message.Descriptor().Fields().ByName(name)
}
func policyExtensionGet(message protoreflect.Message, name protoreflect.Name) protoreflect.Value {
	return message.Get(policyExtensionField(message, name))
}
func policyExtensionSet(message protoreflect.Message, name protoreflect.Name, value protoreflect.Value) {
	message.Set(policyExtensionField(message, name), value)
}
func policyExtensionChild(message protoreflect.Message, name protoreflect.Name) protoreflect.Message {
	return message.Mutable(policyExtensionField(message, name)).Message()
}

func marshalPolicyExtension(message proto.Message, frameBudget, ceiling int) ([]byte, error) {
	if frameBudget < 1 || frameBudget > ceiling {
		return nil, errors.New("invalid policy extension complete frame budget")
	}
	if proto.Size(message) > frameBudget {
		return nil, &RuntimeError{Code: ErrorResourceExhausted, Message: "policy extension exceeds complete frame budget"}
	}
	return (proto.MarshalOptions{Deterministic: true}).Marshal(message)
}

func unmarshalPolicyExtension(name string, frame []byte, frameBudget, ceiling int) (*dynamicpb.Message, error) {
	if frameBudget < 1 || frameBudget > ceiling {
		return nil, errors.New("invalid policy extension complete frame budget")
	}
	if len(frame) > frameBudget {
		return nil, &RuntimeError{Code: ErrorResourceExhausted, Message: "policy extension exceeds complete frame budget"}
	}
	message, err := newPolicyExtensionMessage(name)
	if err != nil {
		return nil, err
	}
	// Validate before ordinary decoding, which would erase duplicate scalars
	// and conflicting oneofs. Unknown fields cannot smuggle source assertions.
	if err := validatePolicyExtensionWire(message.Descriptor(), frame, 0); err != nil {
		return nil, err
	}
	if err := (proto.UnmarshalOptions{RecursionLimit: 8}).Unmarshal(frame, message); err != nil {
		return nil, err
	}
	return message, nil
}

func validatePolicyExtensionWire(descriptor protoreflect.MessageDescriptor, frame []byte, depth int) error {
	if depth > 8 {
		return errors.New("policy extension nesting exceeds bound")
	}
	counts := make(map[protoreflect.FieldNumber]int)
	oneofs := make(map[protoreflect.Name]bool)
	for len(frame) > 0 {
		number, wireType, tagBytes := protowire.ConsumeTag(frame)
		if tagBytes < 0 {
			return protowire.ParseError(tagBytes)
		}
		frame = frame[tagBytes:]
		field := descriptor.Fields().ByNumber(number)
		if field == nil {
			return fmt.Errorf("unknown policy extension field %d", number)
		}
		counts[number]++
		if (!field.IsList() && counts[number] > 1) || counts[number] > DatasetMaxQueryClassifications {
			return errors.New("repeated singular field or repeated-field count exceeds bound")
		}
		if oneof := field.ContainingOneof(); oneof != nil {
			if oneofs[oneof.Name()] {
				return errors.New("conflicting policy extension oneof fields")
			}
			oneofs[oneof.Name()] = true
		}
		switch field.Kind() {
		case protoreflect.MessageKind, protoreflect.StringKind, protoreflect.BytesKind:
			if wireType != protowire.BytesType {
				return errors.New("policy extension bytes field has wrong wire type")
			}
			value, length := protowire.ConsumeBytes(frame)
			if length < 0 {
				return protowire.ParseError(length)
			}
			if field.Kind() == protoreflect.MessageKind {
				if err := validatePolicyExtensionWire(field.Message(), value, depth+1); err != nil {
					return err
				}
			}
			frame = frame[length:]
		case protoreflect.Uint32Kind, protoreflect.EnumKind, protoreflect.BoolKind:
			if wireType != protowire.VarintType {
				return errors.New("policy extension scalar has wrong wire type")
			}
			value, length := protowire.ConsumeVarint(frame)
			if length < 0 {
				return protowire.ParseError(length)
			}
			if value > math.MaxUint32 || (field.Kind() == protoreflect.BoolKind && value > 1) {
				return errors.New("policy extension scalar overflows its type")
			}
			if field.Kind() == protoreflect.EnumKind && (value > math.MaxInt32 || field.Enum().Values().ByNumber(protoreflect.EnumNumber(value)) == nil) {
				return errors.New("unknown policy extension enum value")
			}
			frame = frame[length:]
		default:
			return errors.New("unsupported policy extension field kind")
		}
	}
	return nil
}
