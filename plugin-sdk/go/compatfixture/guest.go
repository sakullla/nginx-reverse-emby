// Package compatfixture owns the deterministic nre:policy/v1 compatibility
// guest used to prove the SDK wire and calling convention end to end. It is a
// fixture generator, not an official plugin implementation.
package compatfixture

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/protoschema"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const (
	readFieldRequestOffset = uint32(1024)
	hostResponseOffset     = uint32(2048)
	allocatorStart         = uint32(4096)
	guestMemoryMaximum     = uint32(16)
	maxHostResponseBytes   = allocatorStart - hostResponseOffset
)

var (
	canonicalInitRequest = mustMarshalPolicyMessage("InitRequest", func(message protoreflect.Message) {
		message.Set(requiredField(message, "config"), protoreflect.ValueOfBytes([]byte(`{"mode":"compat"}`)))
		grants := message.Mutable(requiredField(message, "granted_scopes")).List()
		grants.Append(protoreflect.ValueOfString("http.inspect"))
		grants.Append(protoreflect.ValueOfString("state.read"))
		message.Set(requiredField(message, "generation"), protoreflect.ValueOfString("compat-generation-1"))
	})
	canonicalEvaluateRequest = mustMarshalPolicyMessage("EvaluateRequest", func(message protoreflect.Message) {
		message.Set(requiredField(message, "extension_point"), protoreflect.ValueOfString("http.request"))
		message.Set(requiredField(message, "request_id"), protoreflect.ValueOfString("request-1"))
		message.Set(requiredField(message, "payload"), protoreflect.ValueOfBytes([]byte("input")))
	})
	readFieldRequest = mustMarshalPolicyMessage("ReadFieldRequest", func(message protoreflect.Message) {
		message.Set(requiredField(message, "name"), protoreflect.ValueOfString("method"))
	})
	evaluateResponse = mustMarshalPolicyMessage("EvaluateResponse", func(message protoreflect.Message) {
		successField := requiredField(message, "success")
		success := message.NewField(successField).Message()
		action := requiredField(success, "action")
		allow := action.Enum().Values().ByName("ALLOW")
		if allow == nil {
			panic("canonical EvaluateSuccess.Action.ALLOW is missing")
		}
		success.Set(action, protoreflect.ValueOfEnum(allow.Number()))
		success.Set(requiredField(success, "payload"), protoreflect.ValueOfBytes([]byte("guest-ok")))
		message.Set(successField, protoreflect.ValueOfMessage(success))
	})
)

// CanonicalPolicyV1InitRequest returns the non-empty InitRequest consumed by
// the compatibility guest. A fresh slice prevents tests or callers from
// mutating the deterministic generator input.
func CanonicalPolicyV1InitRequest() []byte {
	return append([]byte(nil), canonicalInitRequest...)
}

// CanonicalPolicyV1EvaluateRequest returns the EvaluateRequest consumed by the
// compatibility guest.
func CanonicalPolicyV1EvaluateRequest() []byte {
	return append([]byte(nil), canonicalEvaluateRequest...)
}

func mustMarshalPolicyMessage(name protoreflect.Name, populate func(protoreflect.Message)) []byte {
	descriptor, err := protoschema.Message(protoreflect.FullName("nre.plugin.policy.v1." + string(name)))
	if err != nil {
		panic(err)
	}
	message := dynamicpb.NewMessage(descriptor)
	populate(message.ProtoReflect())
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
	if err != nil {
		panic(fmt.Errorf("marshal canonical %s: %w", name, err))
	}
	return encoded
}

func requiredField(message protoreflect.Message, name protoreflect.Name) protoreflect.FieldDescriptor {
	field := message.Descriptor().Fields().ByName(name)
	if field == nil {
		panic(fmt.Sprintf("canonical %s.%s field is missing", message.Descriptor().FullName(), name))
	}
	return field
}

// PolicyV1GuestWASM returns a fresh deterministic WebAssembly 1.0 module. The
// guest exercises allocator ownership, protobuf request/response bytes, a
// Host import RESOURCE_EXHAUSTED retry, and the packed evaluate response.
func PolicyV1GuestWASM() []byte {
	return policyV1GuestWASM(hostFunctionNames)
}

// PolicyV1GuestWASMWithOptionalImports derives a compatibility module with
// additive imports while keeping the original checked-in guest unchanged.
func PolicyV1GuestWASMWithOptionalImports(names ...string) ([]byte, error) {
	imports := append([]string(nil), hostFunctionNames...)
	seen := make(map[string]bool)
	for _, name := range names {
		switch name {
		case "nre_host_read_normalized_http", "nre_host_read_trusted_source", "nre_host_dataset_query":
		default:
			return nil, fmt.Errorf("unsupported optional policy import %q", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate optional policy import %q", name)
		}
		seen[name] = true
		imports = append(imports, name)
	}
	return policyV1GuestWASM(imports), nil
}

func policyV1GuestWASM(hostImports []string) []byte {
	module := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

	types := wasmVector(
		functionType([]byte{wasmI32, wasmI32, wasmI32, wasmI32}, []byte{wasmI64}),
		functionType(nil, []byte{wasmI32}),
		functionType([]byte{wasmI32}, []byte{wasmI32}),
		functionType([]byte{wasmI32, wasmI32}, nil),
		functionType([]byte{wasmI32, wasmI32}, []byte{wasmI32}),
		functionType([]byte{wasmI32, wasmI32}, []byte{wasmI64}),
	)
	module = appendSection(module, 1, types)

	imports := make([][]byte, 0, len(hostImports))
	for _, name := range hostImports {
		entry := appendName(nil, "nre:policy/v1")
		entry = appendName(entry, name)
		entry = append(entry, 0x00)
		entry = appendULEB(entry, 0)
		imports = append(imports, entry)
	}
	module = appendSection(module, 2, wasmVector(imports...))
	module = appendSection(module, 3, wasmU32Vector(1, 2, 3, 4, 5, 1))

	memory := appendULEB(nil, 1)
	memory = append(memory, 0x01)
	memory = appendULEB(memory, 1)
	memory = appendULEB(memory, uint64(guestMemoryMaximum))
	module = appendSection(module, 5, memory)

	module = appendSection(module, 6, wasmVector(
		mutableI32Global(allocatorStart), // heap cursor
		mutableI32Global(0),              // input pointer
		mutableI32Global(0),              // input length
		mutableI32Global(0),              // input allocation active
		mutableI32Global(0),              // response pointer
		mutableI32Global(0),              // response length
		mutableI32Global(0),              // response allocation active
	))

	firstGuestFunction := uint64(len(hostImports))
	exports := wasmVector(
		exportEntry("memory", 0x02, 0),
		exportEntry("nre_policy_version", 0x00, firstGuestFunction),
		exportEntry("nre_policy_alloc", 0x00, firstGuestFunction+1),
		exportEntry("nre_policy_free", 0x00, firstGuestFunction+2),
		exportEntry("nre_policy_init", 0x00, firstGuestFunction+3),
		exportEntry("nre_policy_evaluate", 0x00, firstGuestFunction+4),
		exportEntry("nre_policy_reset", 0x00, firstGuestFunction+5),
	)
	module = appendSection(module, 7, exports)

	versionBody := functionBody(nil, append(appendSLEB([]byte{opcodeI32Const}, 1), opcodeEnd))
	allocInstructions := allocateInstructions()
	allocBody := functionBody([]localDecl{{count: 1, valueType: wasmI32}}, allocInstructions)
	freeBody := functionBody(nil, freeInstructions())
	initBody := functionBody(nil, initInstructions())
	evaluateBody := functionBody([]localDecl{{count: 1, valueType: wasmI64}, {count: 2, valueType: wasmI32}}, evaluateInstructions())
	resetBody := functionBody(nil, resetInstructions())
	module = appendSection(module, 10, wasmVector(versionBody, allocBody, freeBody, initBody, evaluateBody, resetBody))

	data := wasmVector(activeDataSegment(readFieldRequestOffset, readFieldRequest))
	module = appendSection(module, 11, data)
	return module
}

func PolicyV1GuestHex() string {
	return hex.EncodeToString(PolicyV1GuestWASM()) + "\n"
}

var hostFunctionNames = []string{
	"nre_host_read_field",
	"nre_host_read_body_window",
	"nre_host_state_get",
	"nre_host_state_put",
	"nre_host_emit_event",
	"nre_host_add_metric",
}

const (
	wasmI32 = byte(0x7f)
	wasmI64 = byte(0x7e)

	opcodeEnd           = byte(0x0b)
	opcodeCall          = byte(0x10)
	opcodeReturn        = byte(0x0f)
	opcodeDrop          = byte(0x1a)
	opcodeLocalGet      = byte(0x20)
	opcodeLocalSet      = byte(0x21)
	opcodeLocalTee      = byte(0x22)
	opcodeGlobalGet     = byte(0x23)
	opcodeGlobalSet     = byte(0x24)
	opcodeI32Const      = byte(0x41)
	opcodeI64Const      = byte(0x42)
	opcodeI32EqualZero  = byte(0x45)
	opcodeI32Equal      = byte(0x46)
	opcodeI32NotEqual   = byte(0x47)
	opcodeI32GreaterU   = byte(0x4b)
	opcodeI32LessEqualU = byte(0x4d)
	opcodeI32Add        = byte(0x6a)
	opcodeI32And        = byte(0x71)
	opcodeI32Or         = byte(0x72)
	opcodeI64Or         = byte(0x84)
	opcodeI64ShiftL     = byte(0x86)
	opcodeI64ShiftRU    = byte(0x88)
	opcodeI32WrapI64    = byte(0xa7)
	opcodeI64ExtendI32U = byte(0xad)
	opcodeI32Store      = byte(0x36)
	opcodeI64Store      = byte(0x37)
	opcodeI32Store16    = byte(0x3b)
	opcodeI32Load8U     = byte(0x2d)
)

type localDecl struct {
	count     uint32
	valueType byte
}

func functionType(parameters, results []byte) []byte {
	result := []byte{0x60}
	result = appendULEB(result, uint64(len(parameters)))
	result = append(result, parameters...)
	result = appendULEB(result, uint64(len(results)))
	return append(result, results...)
}

func exportEntry(name string, kind byte, index uint64) []byte {
	result := appendName(nil, name)
	result = append(result, kind)
	return appendULEB(result, index)
}

func functionBody(locals []localDecl, instructions []byte) []byte {
	body := appendULEB(nil, uint64(len(locals)))
	for _, local := range locals {
		body = appendULEB(body, uint64(local.count))
		body = append(body, local.valueType)
	}
	body = append(body, instructions...)
	return appendULEB(nil, uint64(len(body)), body...)
}

func mutableI32Global(value uint32) []byte {
	result := []byte{wasmI32, 0x01}
	result = appendI32Const(result, value)
	return append(result, opcodeEnd)
}

func allocateInstructions() []byte {
	result := []byte{opcodeGlobalGet, 0x03, opcodeGlobalGet, 0x06, opcodeI32Or}
	result = appendReturnI32WhenTrue(result, 0)
	result = append(result, opcodeLocalGet, 0x00, opcodeI32EqualZero)
	result = appendReturnI32WhenTrue(result, 0)
	result = append(result, opcodeGlobalGet, 0x00, opcodeLocalSet, 0x01)
	result = append(result, opcodeGlobalGet, 0x00, opcodeLocalGet, 0x00, opcodeI32Add, opcodeGlobalSet, 0x00)
	result = append(result, opcodeLocalGet, 0x01, opcodeGlobalSet, 0x01)
	result = append(result, opcodeLocalGet, 0x00, opcodeGlobalSet, 0x02)
	result = appendI32Const(result, 1)
	result = append(result, opcodeGlobalSet, 0x03, opcodeLocalGet, 0x01, opcodeEnd)
	return result
}

func freeInstructions() []byte {
	result := appendInputOwnershipCondition(nil)
	result = append(result, 0x04, 0x40)
	result = appendI32Const(result, 0)
	result = append(result, opcodeGlobalSet, 0x03, opcodeReturn, opcodeEnd)
	result = appendResponseOwnershipCondition(result)
	result = append(result, 0x04, 0x40)
	result = appendI32Const(result, 0)
	result = append(result, opcodeGlobalSet, 0x06, opcodeReturn, opcodeEnd, opcodeEnd)
	return result
}

func initInstructions() []byte {
	result := appendInputOwnershipCondition(nil)
	result = append(result, opcodeI32EqualZero)
	result = appendReturnI32WhenTrue(result, 1)
	result = appendCanonicalMessageCheck(result, canonicalInitRequest, 1, false)
	result = appendI32Const(result, 0)
	return append(result, opcodeEnd)
}

func resetInstructions() []byte {
	result := []byte{opcodeGlobalGet, 0x03, opcodeGlobalGet, 0x06, opcodeI32Or}
	result = appendReturnI32WhenTrue(result, 1)
	result = appendI32Const(result, 0)
	return append(result, opcodeEnd)
}

func appendInputOwnershipCondition(target []byte) []byte {
	target = append(target, opcodeGlobalGet, 0x03, opcodeLocalGet, 0x00, opcodeGlobalGet, 0x01, opcodeI32Equal, opcodeI32And)
	return append(target, opcodeLocalGet, 0x01, opcodeGlobalGet, 0x02, opcodeI32Equal, opcodeI32And)
}

func appendResponseOwnershipCondition(target []byte) []byte {
	target = append(target, opcodeGlobalGet, 0x06, opcodeLocalGet, 0x00, opcodeGlobalGet, 0x04, opcodeI32Equal, opcodeI32And)
	return append(target, opcodeLocalGet, 0x01, opcodeGlobalGet, 0x05, opcodeI32Equal, opcodeI32And)
}

func appendReturnI32WhenTrue(target []byte, value uint32) []byte {
	target = append(target, 0x04, 0x40)
	target = appendI32Const(target, value)
	return append(target, opcodeReturn, opcodeEnd)
}

func appendReturnI64ZeroWhenTrue(target []byte) []byte {
	target = append(target, 0x04, 0x40, opcodeI64Const, 0x00, opcodeReturn, opcodeEnd)
	return target
}

// appendCanonicalMessageCheck implements a bounded protobuf wire parser for
// the fixture's canonical message: it validates the exact deterministic frame
// length and every encoded tag, length, and payload byte before dispatch.
func appendCanonicalMessageCheck(target, canonical []byte, invalidStatus uint32, returnI64 bool) []byte {
	target = append(target, opcodeLocalGet, 0x01)
	target = appendI32Const(target, uint32(len(canonical)))
	target = append(target, opcodeI32NotEqual)
	if returnI64 {
		target = appendReturnI64ZeroWhenTrue(target)
	} else {
		target = appendReturnI32WhenTrue(target, invalidStatus)
	}
	for offset, value := range canonical {
		target = append(target, opcodeLocalGet, 0x00, opcodeI32Load8U, 0x00)
		target = appendULEB(target, uint64(offset))
		target = appendI32Const(target, uint32(value))
		target = append(target, opcodeI32NotEqual)
		if returnI64 {
			target = appendReturnI64ZeroWhenTrue(target)
		} else {
			target = appendReturnI32WhenTrue(target, invalidStatus)
		}
	}
	return target
}

func evaluateInstructions() []byte {
	result := appendInputOwnershipCondition(nil)
	result = append(result, opcodeI32EqualZero)
	result = appendReturnI64ZeroWhenTrue(result)
	result = append(result, opcodeGlobalGet, 0x06)
	result = appendReturnI64ZeroWhenTrue(result)
	result = appendCanonicalMessageCheck(result, canonicalEvaluateRequest, 0, true)
	result = appendI32Const(result, readFieldRequestOffset)
	result = appendI32Const(result, uint32(len(readFieldRequest)))
	result = appendI32Const(result, hostResponseOffset)
	result = appendI32Const(result, 1)
	result = append(result, opcodeCall, 0x00, opcodeLocalTee, 0x02)
	result = append(result, opcodeI64Const)
	result = appendSLEB(result, 32)
	result = append(result, opcodeI64ShiftRU, opcodeI32WrapI64, opcodeI32Const, 0x03, opcodeI32NotEqual)
	result = appendReturnI64ZeroWhenTrue(result)
	result = append(result, opcodeLocalGet, 0x02, opcodeI32WrapI64, opcodeLocalSet, 0x04)
	result = append(result, opcodeLocalGet, 0x04)
	result = appendI32Const(result, 1)
	result = append(result, opcodeI32LessEqualU)
	result = appendReturnI64ZeroWhenTrue(result)
	result = append(result, opcodeLocalGet, 0x04)
	result = appendI32Const(result, maxHostResponseBytes)
	result = append(result, opcodeI32GreaterU)
	result = appendReturnI64ZeroWhenTrue(result)
	result = append(result, opcodeLocalGet, 0x04, opcodeLocalSet, 0x03)
	result = appendI32Const(result, readFieldRequestOffset)
	result = appendI32Const(result, uint32(len(readFieldRequest)))
	result = appendI32Const(result, hostResponseOffset)
	result = append(result, opcodeLocalGet, 0x03)
	result = append(result, opcodeCall, 0x00, opcodeLocalTee, 0x02)
	result = append(result, opcodeI64Const)
	result = appendSLEB(result, 32)
	result = append(result, opcodeI64ShiftRU, opcodeI32WrapI64, opcodeI32Const, 0x00, opcodeI32NotEqual)
	result = appendReturnI64ZeroWhenTrue(result)
	result = append(result, opcodeLocalGet, 0x02, opcodeI32WrapI64, opcodeLocalSet, 0x04)
	result = append(result, opcodeLocalGet, 0x04, opcodeLocalGet, 0x03, opcodeI32GreaterU)
	result = appendReturnI64ZeroWhenTrue(result)
	result = append(result, opcodeGlobalGet, 0x00, opcodeLocalSet, 0x03, opcodeGlobalGet, 0x00)
	result = appendI32Const(result, uint32(len(evaluateResponse)))
	result = append(result, opcodeI32Add, opcodeGlobalSet, 0x00)
	result = append(result, opcodeLocalGet, 0x03, opcodeGlobalSet, 0x04)
	result = appendI32Const(result, uint32(len(evaluateResponse)))
	result = append(result, opcodeGlobalSet, 0x05)
	result = appendI32Const(result, 1)
	result = append(result, opcodeGlobalSet, 0x06)
	result = append(result, opcodeLocalGet, 0x03, opcodeI64Const)
	result = appendSLEB(result, int64(binary.LittleEndian.Uint64(evaluateResponse[:8])))
	result = append(result, opcodeI64Store, 0x03, 0x00)
	result = append(result, opcodeLocalGet, 0x03, opcodeI32Const)
	result = appendSLEB(result, int64(binary.LittleEndian.Uint32(evaluateResponse[8:12])))
	result = append(result, opcodeI32Store, 0x02, 0x08)
	result = append(result, opcodeLocalGet, 0x03, opcodeI32Const)
	result = appendSLEB(result, int64(binary.LittleEndian.Uint16(evaluateResponse[12:14])))
	result = append(result, opcodeI32Store16, 0x01, 0x0c)
	result = append(result, opcodeLocalGet, 0x03, opcodeI64ExtendI32U, opcodeI64Const)
	result = appendSLEB(result, 32)
	result = append(result, opcodeI64ShiftL, opcodeI64Const)
	result = appendSLEB(result, int64(len(evaluateResponse)))
	result = append(result, opcodeI64Or)
	return append(result, opcodeEnd)
}

func appendI32Const(target []byte, value uint32) []byte {
	target = append(target, opcodeI32Const)
	return appendSLEB(target, int64(value))
}

func activeDataSegment(offset uint32, data []byte) []byte {
	result := appendULEB(nil, 0)
	result = appendI32Const(result, offset)
	result = append(result, opcodeEnd)
	result = appendULEB(result, uint64(len(data)))
	return append(result, data...)
}

func wasmU32Vector(values ...uint64) []byte {
	result := appendULEB(nil, uint64(len(values)))
	for _, value := range values {
		result = appendULEB(result, value)
	}
	return result
}

func wasmVector(values ...[]byte) []byte {
	result := appendULEB(nil, uint64(len(values)))
	for _, value := range values {
		result = append(result, value...)
	}
	return result
}

func appendSection(module []byte, id byte, payload []byte) []byte {
	module = append(module, id)
	module = appendULEB(module, uint64(len(payload)))
	return append(module, payload...)
}

func appendName(target []byte, value string) []byte {
	target = appendULEB(target, uint64(len(value)))
	return append(target, value...)
}

func appendULEB(target []byte, value uint64, suffix ...byte) []byte {
	for {
		current := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			current |= 0x80
		}
		target = append(target, current)
		if value == 0 {
			return append(target, suffix...)
		}
	}
}

func appendSLEB(target []byte, value int64) []byte {
	for {
		current := byte(value & 0x7f)
		value >>= 7
		signSet := current&0x40 != 0
		done := (value == 0 && !signSet) || (value == -1 && signSet)
		if !done {
			current |= 0x80
		}
		target = append(target, current)
		if done {
			return target
		}
	}
}
