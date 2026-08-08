// Package compatfixture owns the deterministic nre:policy/v1 compatibility
// guest used to prove the SDK wire and calling convention end to end. It is a
// fixture generator, not an official plugin implementation.
package compatfixture

import (
	"encoding/binary"
	"encoding/hex"
)

const (
	readFieldRequestOffset = uint32(1024)
	hostResponseOffset     = uint32(2048)
	allocatorStart         = uint32(4096)
	guestMemoryMaximum     = uint32(16)
)

var (
	readFieldRequest = []byte{0x0a, 0x06, 'm', 'e', 't', 'h', 'o', 'd'}
	evaluateResponse = []byte{0x08, 0x01, 0x12, 0x08, 'g', 'u', 'e', 's', 't', '-', 'o', 'k'}
)

// PolicyV1GuestWASM returns a fresh deterministic WebAssembly 1.0 module. The
// guest exercises allocator ownership, protobuf request/response bytes, a
// Host import RESOURCE_EXHAUSTED retry, and the packed evaluate response.
func PolicyV1GuestWASM() []byte {
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

	imports := make([][]byte, 0, len(hostFunctionNames))
	for _, name := range hostFunctionNames {
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

	global := appendULEB(nil, 1)
	global = append(global, wasmI32, 0x01, opcodeI32Const)
	global = appendSLEB(global, int64(allocatorStart))
	global = append(global, opcodeEnd)
	module = appendSection(module, 6, global)

	exports := wasmVector(
		exportEntry("memory", 0x02, 0),
		exportEntry("nre_policy_version", 0x00, 6),
		exportEntry("nre_policy_alloc", 0x00, 7),
		exportEntry("nre_policy_free", 0x00, 8),
		exportEntry("nre_policy_init", 0x00, 9),
		exportEntry("nre_policy_evaluate", 0x00, 10),
		exportEntry("nre_policy_reset", 0x00, 11),
	)
	module = appendSection(module, 7, exports)

	versionBody := functionBody(nil, append(appendSLEB([]byte{opcodeI32Const}, 1), opcodeEnd))
	allocInstructions := []byte{opcodeGlobalGet, 0x00, opcodeLocalSet, 0x01, opcodeGlobalGet, 0x00, opcodeLocalGet, 0x00, opcodeI32Add, opcodeGlobalSet, 0x00, opcodeLocalGet, 0x01, opcodeEnd}
	allocBody := functionBody([]localDecl{{count: 1, valueType: wasmI32}}, allocInstructions)
	freeBody := functionBody(nil, []byte{opcodeEnd})
	initBody := functionBody(nil, []byte{opcodeI32Const, 0x00, opcodeEnd})
	evaluateBody := functionBody([]localDecl{{count: 1, valueType: wasmI64}, {count: 1, valueType: wasmI32}}, evaluateInstructions())
	resetBody := functionBody(nil, []byte{opcodeI32Const, 0x00, opcodeEnd})
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
	opcodeDrop          = byte(0x1a)
	opcodeLocalGet      = byte(0x20)
	opcodeLocalSet      = byte(0x21)
	opcodeLocalTee      = byte(0x22)
	opcodeGlobalGet     = byte(0x23)
	opcodeGlobalSet     = byte(0x24)
	opcodeI32Const      = byte(0x41)
	opcodeI64Const      = byte(0x42)
	opcodeI32Equal      = byte(0x46)
	opcodeI32Add        = byte(0x6a)
	opcodeI64Or         = byte(0x84)
	opcodeI64ShiftL     = byte(0x86)
	opcodeI64ShiftRU    = byte(0x88)
	opcodeI32WrapI64    = byte(0xa7)
	opcodeI64ExtendI32U = byte(0xad)
	opcodeI32Store      = byte(0x36)
	opcodeI64Store      = byte(0x37)
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

func evaluateInstructions() []byte {
	result := appendI32Const(nil, readFieldRequestOffset)
	result = appendI32Const(result, uint32(len(readFieldRequest)))
	result = appendI32Const(result, hostResponseOffset)
	result = appendI32Const(result, 1)
	result = append(result, opcodeCall, 0x00, opcodeLocalTee, 0x02)
	result = append(result, opcodeI64Const)
	result = appendSLEB(result, 32)
	result = append(result, opcodeI64ShiftRU, opcodeI32WrapI64, opcodeI32Const, 0x03, opcodeI32Equal, 0x04, 0x40)
	result = appendI32Const(result, readFieldRequestOffset)
	result = appendI32Const(result, uint32(len(readFieldRequest)))
	result = appendI32Const(result, hostResponseOffset)
	result = appendI32Const(result, 64)
	result = append(result, opcodeCall, 0x00, opcodeDrop, opcodeEnd)
	result = append(result, opcodeGlobalGet, 0x00, opcodeLocalSet, 0x03, opcodeGlobalGet, 0x00)
	result = appendI32Const(result, uint32(len(evaluateResponse)))
	result = append(result, opcodeI32Add, opcodeGlobalSet, 0x00)
	result = append(result, opcodeLocalGet, 0x03, opcodeI64Const)
	result = appendSLEB(result, int64(binary.LittleEndian.Uint64(evaluateResponse[:8])))
	result = append(result, opcodeI64Store, 0x03, 0x00)
	result = append(result, opcodeLocalGet, 0x03, opcodeI32Const)
	result = appendSLEB(result, int64(binary.LittleEndian.Uint32(evaluateResponse[8:12])))
	result = append(result, opcodeI32Store, 0x02, 0x08)
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
