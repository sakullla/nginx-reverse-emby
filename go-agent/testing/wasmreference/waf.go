// Package wasmreference builds deterministic WASM policy artifacts used by
// Agent acceptance and performance gates. These are test fixtures, not an
// official plugin implementation.
package wasmreference

import "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"

// WAFOptions controls the representative guest without changing its ABI.
// ScanRounds exists so the performance gate can prove that additional work in
// guest code is visible to its enabled-path measurement.
type WAFOptions struct {
	ScanRounds     uint32
	MemoryMinPages uint32
	MemoryMaxPages uint32
}

// WAFGuest returns a WebAssembly 1.0 nre:policy/v1 module. Evaluate scans the
// complete deterministic EvaluateRequest frame inside the guest for a small,
// fixed representative rule set: script-tag prefixes, encoded quote/traversal
// prefixes, and literal parent-directory traversal. Matching frames return a
// DENY response; all other frames return ALLOW.
func WAFGuest(options WAFOptions) []byte {
	if options.ScanRounds == 0 {
		options.ScanRounds = 1
	}
	if options.MemoryMinPages == 0 {
		options.MemoryMinPages = 1
	}
	if options.MemoryMaxPages == 0 {
		options.MemoryMaxPages = options.MemoryMinPages
	}

	module := append([]byte(nil), 0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00)
	types := wasmVector(
		functionType([]byte{wasmI32, wasmI32, wasmI32, wasmI32}, []byte{wasmI64}),
		functionType(nil, []byte{wasmI32}),
		functionType([]byte{wasmI32}, []byte{wasmI32}),
		functionType([]byte{wasmI32, wasmI32}, nil),
		functionType([]byte{wasmI32, wasmI32}, []byte{wasmI32}),
		functionType([]byte{wasmI32, wasmI32}, []byte{wasmI64}),
	)
	module = appendSection(module, 1, types)

	imports := make([][]byte, 0, 6)
	for _, name := range []string{
		pluginsdk.PolicyHostReadField,
		pluginsdk.PolicyHostReadBodyWindow,
		pluginsdk.PolicyHostStateGet,
		pluginsdk.PolicyHostStatePut,
		pluginsdk.PolicyHostEmitEvent,
		pluginsdk.PolicyHostAddMetric,
	} {
		entry := appendName(nil, pluginsdk.PolicyHostModule)
		entry = appendName(entry, name)
		entry = append(entry, 0x00)
		entry = appendULEB(entry, 0)
		imports = append(imports, entry)
	}
	module = appendSection(module, 2, wasmVector(imports...))
	module = appendSection(module, 3, wasmU32Vector(1, 2, 3, 4, 5, 1))
	module = appendSection(module, 5, append(append(appendULEB(nil, 1), 0x01), appendULEB(appendULEB(nil, uint64(options.MemoryMinPages)), uint64(options.MemoryMaxPages))...))

	const importedFunctions = uint32(6)
	exports := wasmVector(
		exportEntry(pluginsdk.PolicyExportVersion, 0x00, importedFunctions),
		exportEntry(pluginsdk.PolicyExportAllocate, 0x00, importedFunctions+1),
		exportEntry(pluginsdk.PolicyExportFree, 0x00, importedFunctions+2),
		exportEntry(pluginsdk.PolicyExportInit, 0x00, importedFunctions+3),
		exportEntry(pluginsdk.PolicyExportEvaluate, 0x00, importedFunctions+4),
		exportEntry(pluginsdk.PolicyExportReset, 0x00, importedFunctions+5),
		exportEntry(pluginsdk.PolicyExportMemory, 0x02, 0),
	)
	module = appendSection(module, 7, exports)

	versionBody := functionBody(nil, []byte{opcodeI32Const, 0x01, opcodeEnd})
	allocateBody := functionBody(nil, appendI32Const(nil, inputPointer, opcodeEnd))
	freeBody := functionBody(nil, []byte{opcodeEnd})
	initBody := functionBody(nil, []byte{opcodeI32Const, 0x00, opcodeEnd})
	evaluateBody := functionBody([]byte{0x01, 0x03, wasmI32}, evaluateInstructions(options.ScanRounds))
	resetBody := functionBody(nil, []byte{opcodeI32Const, 0x00, opcodeEnd})
	module = appendSection(module, 10, wasmVector(versionBody, allocateBody, freeBody, initBody, evaluateBody, resetBody))
	module = appendSection(module, 11, wasmVector(
		activeDataSegment(allowResponsePointer, []byte{0x0a, 0x02, 0x08, 0x01}),
		activeDataSegment(denyResponsePointer, []byte{0x0a, 0x02, 0x08, 0x02}),
	))
	return module
}

const (
	wasmI32 = byte(0x7f)
	wasmI64 = byte(0x7e)

	opcodeBlock        = byte(0x02)
	opcodeLoop         = byte(0x03)
	opcodeIf           = byte(0x04)
	opcodeElse         = byte(0x05)
	opcodeEnd          = byte(0x0b)
	opcodeBranch       = byte(0x0c)
	opcodeBranchIf     = byte(0x0d)
	opcodeLocalGet     = byte(0x20)
	opcodeLocalSet     = byte(0x21)
	opcodeI32Load8U    = byte(0x2d)
	opcodeI32Const     = byte(0x41)
	opcodeI64Const     = byte(0x42)
	opcodeI32EqualZero = byte(0x45)
	opcodeI32Equal     = byte(0x46)
	opcodeI32GreaterEq = byte(0x4f)
	opcodeI32Add       = byte(0x6a)
	opcodeI32And       = byte(0x71)
	opcodeI32Or        = byte(0x72)

	allowResponsePointer = uint32(1024)
	denyResponsePointer  = uint32(1032)
	inputPointer         = uint32(4096)
	responseLength       = uint32(4)
)

func evaluateInstructions(scanRounds uint32) []byte {
	// Locals: 0=input pointer, 1=input length, 2=cursor, 3=match
	// score, 4=completed scan rounds.
	result := appendI32Const(nil, 0, opcodeLocalSet, 0x03)
	result = appendI32Const(result, 0, opcodeLocalSet, 0x04)
	result = append(result, opcodeBlock, 0x40, opcodeLoop, 0x40)
	result = append(result, opcodeLocalGet, 0x04)
	result = appendI32Const(result, scanRounds, opcodeI32GreaterEq, opcodeBranchIf, 0x01)
	result = appendI32Const(result, 0, opcodeLocalSet, 0x02)
	result = append(result, opcodeBlock, 0x40, opcodeLoop, 0x40)
	result = append(result, opcodeLocalGet, 0x02)
	result = appendI32Const(result, 2, opcodeI32Add, opcodeLocalGet, 0x01, opcodeI32GreaterEq, opcodeBranchIf, 0x01)

	// score += current == '<'
	result = append(result, opcodeLocalGet, 0x03)
	result = appendLoadByte(result, 0)
	result = appendI32Const(result, '<', opcodeI32Equal)

	// OR (current == '%' && next == '2' && (third == '7' || third == 'e')).
	result = appendLoadByte(result, 0)
	result = appendI32Const(result, '%', opcodeI32Equal)
	result = appendLoadByte(result, 1)
	result = appendI32Const(result, '2', opcodeI32Equal, opcodeI32And)
	result = appendLoadByte(result, 2)
	result = appendI32Const(result, '7', opcodeI32Equal)
	result = appendLoadByte(result, 2)
	result = appendI32Const(result, 'e', opcodeI32Equal, opcodeI32Or, opcodeI32And, opcodeI32Or)

	// OR (current == '.' && next == '.').
	result = appendLoadByte(result, 0)
	result = appendI32Const(result, '.', opcodeI32Equal)
	result = appendLoadByte(result, 1)
	result = appendI32Const(result, '.', opcodeI32Equal, opcodeI32And, opcodeI32Or)
	result = append(result, opcodeI32Add, opcodeLocalSet, 0x03)

	result = append(result, opcodeLocalGet, 0x02)
	result = appendI32Const(result, 1, opcodeI32Add, opcodeLocalSet, 0x02, opcodeBranch, 0x00, opcodeEnd, opcodeEnd)
	result = append(result, opcodeLocalGet, 0x04)
	result = appendI32Const(result, 1, opcodeI32Add, opcodeLocalSet, 0x04, opcodeBranch, 0x00, opcodeEnd, opcodeEnd)

	result = append(result, opcodeLocalGet, 0x03, opcodeI32EqualZero, opcodeIf, wasmI64)
	result = appendI64Const(result, packResponse(allowResponsePointer), opcodeElse)
	result = appendI64Const(result, packResponse(denyResponsePointer), opcodeEnd, opcodeEnd)
	return result
}

func appendLoadByte(target []byte, delta uint32) []byte {
	target = append(target, opcodeLocalGet, 0x00, opcodeLocalGet, 0x02, opcodeI32Add)
	if delta != 0 {
		target = appendI32Const(target, delta, opcodeI32Add)
	}
	return append(target, opcodeI32Load8U, 0x00, 0x00)
}

func packResponse(pointer uint32) int64 {
	return int64(uint64(pointer)<<32 | uint64(responseLength))
}

func functionType(parameters, results []byte) []byte {
	result := append([]byte{0x60}, appendULEB(nil, uint64(len(parameters)))...)
	result = append(result, parameters...)
	result = appendULEB(result, uint64(len(results)))
	return append(result, results...)
}

func functionBody(locals, instructions []byte) []byte {
	if len(locals) == 0 {
		locals = []byte{0x00}
	}
	body := append(append([]byte(nil), locals...), instructions...)
	result := appendULEB(nil, uint64(len(body)))
	return append(result, body...)
}

func exportEntry(name string, kind byte, index uint32) []byte {
	result := appendName(nil, name)
	result = append(result, kind)
	return appendULEB(result, uint64(index))
}

func activeDataSegment(offset uint32, data []byte) []byte {
	result := appendULEB(nil, 0)
	result = appendI32Const(result, offset, opcodeEnd)
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

func appendI32Const(target []byte, value uint32, suffix ...byte) []byte {
	target = append(target, opcodeI32Const)
	target = appendSLEB(target, int64(int32(value)))
	return append(target, suffix...)
}

func appendI64Const(target []byte, value int64, suffix ...byte) []byte {
	target = append(target, opcodeI64Const)
	target = appendSLEB(target, value)
	return append(target, suffix...)
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
		done := (value == 0 && current&0x40 == 0) || (value == -1 && current&0x40 != 0)
		if !done {
			current |= 0x80
		}
		target = append(target, current)
		if done {
			return target
		}
	}
}
