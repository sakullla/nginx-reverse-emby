package wasm

import (
	"bytes"
	"errors"
	"fmt"
)

var wasmV1Header = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

// validateBinaryEnvelope rejects capabilities that aren't represented by the
// compiled function/memory metadata before wazero instantiates anything.
func validateBinaryEnvelope(module []byte) error {
	if len(module) < len(wasmV1Header) || !bytes.Equal(module[:len(wasmV1Header)], wasmV1Header) {
		return errors.New("artifact is not a WebAssembly 1.0 module")
	}
	remaining := module[len(wasmV1Header):]
	for len(remaining) > 0 {
		sectionID := remaining[0]
		remaining = remaining[1:]
		sectionLength, consumed, ok := consumeULEB32(remaining)
		if !ok || uint64(sectionLength) > uint64(len(remaining)-consumed) {
			return errors.New("malformed WebAssembly section length")
		}
		section := remaining[consumed : consumed+int(sectionLength)]
		remaining = remaining[consumed+int(sectionLength):]
		switch sectionID {
		case 2:
			if err := validateImportKinds(section); err != nil {
				return err
			}
		case 8:
			return errors.New("WebAssembly start section is forbidden")
		}
	}
	return nil
}

func validateImportKinds(section []byte) error {
	count, consumed, ok := consumeULEB32(section)
	if !ok {
		return errors.New("malformed WebAssembly import vector")
	}
	section = section[consumed:]
	for index := uint32(0); index < count; index++ {
		var valid bool
		section, valid = consumeWASMName(section)
		if !valid {
			return errors.New("malformed WebAssembly import module")
		}
		section, valid = consumeWASMName(section)
		if !valid || len(section) == 0 {
			return errors.New("malformed WebAssembly import name")
		}
		kind := section[0]
		section = section[1:]
		if kind != 0 {
			return fmt.Errorf("WebAssembly import kind %d is forbidden", kind)
		}
		_, consumed, valid = consumeULEB32(section)
		if !valid {
			return errors.New("malformed WebAssembly function import")
		}
		section = section[consumed:]
	}
	if len(section) != 0 {
		return errors.New("trailing WebAssembly import data")
	}
	return nil
}

func consumeWASMName(encoded []byte) ([]byte, bool) {
	length, consumed, ok := consumeULEB32(encoded)
	if !ok || uint64(length) > uint64(len(encoded)-consumed) {
		return nil, false
	}
	return encoded[consumed+int(length):], true
}

func consumeULEB32(encoded []byte) (uint32, int, bool) {
	var result uint32
	for index := 0; index < len(encoded) && index < 5; index++ {
		current := encoded[index]
		if index == 4 && current&0xf0 != 0 {
			return 0, 0, false
		}
		result |= uint32(current&0x7f) << (7 * index)
		if current&0x80 == 0 {
			return result, index + 1, true
		}
	}
	return 0, 0, false
}
