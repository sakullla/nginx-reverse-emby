// Package protoschema exposes the canonical protobuf descriptors compiled from
// plugin-sdk/policy/v1/policy.proto and plugin-sdk/rpc/v1/plugin.proto.
package protoschema

import (
	"encoding/base64"
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

const (
	PolicyV1File = "policy/v1/policy.proto"
	RPCV1File    = "rpc/v1/plugin.proto"
)

func DescriptorSetBytes() ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(canonicalDescriptorSetBase64)
	if err != nil {
		return nil, fmt.Errorf("decode canonical descriptor set: %w", err)
	}
	return data, nil
}

func DescriptorSet() (*descriptorpb.FileDescriptorSet, error) {
	data, err := DescriptorSetBytes()
	if err != nil {
		return nil, err
	}
	set := new(descriptorpb.FileDescriptorSet)
	if err := proto.Unmarshal(data, set); err != nil {
		return nil, fmt.Errorf("unmarshal canonical descriptor set: %w", err)
	}
	return set, nil
}

func Files() (*protoregistry.Files, error) {
	set, err := DescriptorSet()
	if err != nil {
		return nil, err
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		return nil, fmt.Errorf("link canonical descriptor set: %w", err)
	}
	return files, nil
}

func Message(fullName protoreflect.FullName) (protoreflect.MessageDescriptor, error) {
	files, err := Files()
	if err != nil {
		return nil, err
	}
	descriptor, err := files.FindDescriptorByName(fullName)
	if err != nil {
		return nil, fmt.Errorf("find canonical message %q: %w", fullName, err)
	}
	message, ok := descriptor.(protoreflect.MessageDescriptor)
	if !ok {
		return nil, fmt.Errorf("canonical symbol %q is not a message", fullName)
	}
	return message, nil
}
