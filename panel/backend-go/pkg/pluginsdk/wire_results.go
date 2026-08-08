package pluginsdk

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/encoding/protowire"
)

// ValidatePolicyEvaluateResponseFrame validates the v1 result algebra on raw
// bytes. Inspecting the frame before protobuf unmarshalling is required because
// ordinary oneof decoding uses last-one-wins and would hide conflicting fields.
func ValidatePolicyEvaluateResponseFrame(frame []byte) error {
	field, payload, err := consumeExclusiveResult(frame)
	if err != nil {
		return fmt.Errorf("policy evaluate response: %w", err)
	}
	switch field {
	case 1:
		return validatePolicySuccess(payload)
	case 2:
		return validateRuntimeError(payload)
	default:
		return errors.New("policy evaluate response result is missing")
	}
}

// ValidateRPCLifecycleResponseFrame applies the same fail-closed oneof checks
// to Prepare, Activate, and Stop responses.
func ValidateRPCLifecycleResponseFrame(frame []byte) error {
	field, payload, err := consumeExclusiveResult(frame)
	if err != nil {
		return fmt.Errorf("RPC lifecycle response: %w", err)
	}
	switch field {
	case 1:
		return validateLifecycleSuccess(payload)
	case 2:
		return validateRuntimeError(payload)
	default:
		return errors.New("RPC lifecycle response result is missing")
	}
}

func consumeExclusiveResult(frame []byte) (protowire.Number, []byte, error) {
	var resultField protowire.Number
	var resultPayload []byte
	for len(frame) > 0 {
		number, wireType, tagLength := protowire.ConsumeTag(frame)
		if tagLength < 0 {
			return 0, nil, protowire.ParseError(tagLength)
		}
		frame = frame[tagLength:]
		fieldLength := protowire.ConsumeFieldValue(number, wireType, frame)
		if fieldLength < 0 {
			return 0, nil, protowire.ParseError(fieldLength)
		}
		if number == 1 || number == 2 {
			if wireType != protowire.BytesType {
				return 0, nil, fmt.Errorf("result field %d has wire type %d, want bytes", number, wireType)
			}
			if resultField != 0 {
				return 0, nil, errors.New("conflicting or repeated result fields")
			}
			payload, payloadLength := protowire.ConsumeBytes(frame[:fieldLength])
			if payloadLength < 0 {
				return 0, nil, protowire.ParseError(payloadLength)
			}
			resultField, resultPayload = number, payload
		}
		frame = frame[fieldLength:]
	}
	if resultField == 0 {
		return 0, nil, errors.New("result is missing")
	}
	return resultField, resultPayload, nil
}

func validatePolicySuccess(payload []byte) error {
	seenAction := false
	seenPayload := false
	for len(payload) > 0 {
		number, wireType, tagLength := protowire.ConsumeTag(payload)
		if tagLength < 0 {
			return protowire.ParseError(tagLength)
		}
		payload = payload[tagLength:]
		fieldLength := protowire.ConsumeFieldValue(number, wireType, payload)
		if fieldLength < 0 {
			return protowire.ParseError(fieldLength)
		}
		if number == 1 {
			if wireType != protowire.VarintType || seenAction {
				return errors.New("policy success action is malformed or repeated")
			}
			action, length := protowire.ConsumeVarint(payload[:fieldLength])
			if length < 0 || action < 1 || action > 3 {
				return errors.New("policy success action is unspecified or unknown")
			}
			seenAction = true
		} else if number == 2 {
			if wireType != protowire.BytesType || seenPayload {
				return errors.New("policy success payload is malformed or repeated")
			}
			seenPayload = true
		}
		payload = payload[fieldLength:]
	}
	if !seenAction {
		return errors.New("policy success action is missing")
	}
	return nil
}

func validateLifecycleSuccess(payload []byte) error {
	seenReady := false
	for len(payload) > 0 {
		number, wireType, tagLength := protowire.ConsumeTag(payload)
		if tagLength < 0 {
			return protowire.ParseError(tagLength)
		}
		payload = payload[tagLength:]
		fieldLength := protowire.ConsumeFieldValue(number, wireType, payload)
		if fieldLength < 0 {
			return protowire.ParseError(fieldLength)
		}
		if number == 1 {
			if wireType != protowire.VarintType || seenReady {
				return errors.New("lifecycle readiness is malformed or repeated")
			}
			ready, length := protowire.ConsumeVarint(payload[:fieldLength])
			if length < 0 || ready != 1 {
				return errors.New("lifecycle success must explicitly declare ready=true")
			}
			seenReady = true
		}
		payload = payload[fieldLength:]
	}
	if !seenReady {
		return errors.New("lifecycle success readiness is missing")
	}
	return nil
}

func validateRuntimeError(payload []byte) error {
	seenCode := false
	seenMessage := false
	seenRetryable := false
	for len(payload) > 0 {
		number, wireType, tagLength := protowire.ConsumeTag(payload)
		if tagLength < 0 {
			return protowire.ParseError(tagLength)
		}
		payload = payload[tagLength:]
		fieldLength := protowire.ConsumeFieldValue(number, wireType, payload)
		if fieldLength < 0 {
			return protowire.ParseError(fieldLength)
		}
		if number == 1 {
			if wireType != protowire.VarintType || seenCode {
				return errors.New("runtime error code is malformed or repeated")
			}
			code, length := protowire.ConsumeVarint(payload[:fieldLength])
			if length < 0 || !ErrorCode(code).Valid() {
				return fmt.Errorf("runtime error code %d is unspecified or unknown", code)
			}
			seenCode = true
		} else if number == 2 {
			if wireType != protowire.BytesType || seenMessage {
				return errors.New("runtime error message is malformed or repeated")
			}
			seenMessage = true
		} else if number == 3 {
			if wireType != protowire.VarintType || seenRetryable {
				return errors.New("runtime error retryable flag is malformed or repeated")
			}
			retryable, length := protowire.ConsumeVarint(payload[:fieldLength])
			if length < 0 || retryable > 1 {
				return errors.New("runtime error retryable flag is not boolean")
			}
			seenRetryable = true
		}
		payload = payload[fieldLength:]
	}
	if !seenCode {
		return errors.New("runtime error code is missing")
	}
	return nil
}
