package plugins

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/pkg/pluginsdk"
)

var wasmV1Header = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

type wasmFunctionType struct {
	parameters []pluginsdk.WASMValueType
	results    []pluginsdk.WASMValueType
}

type wasmImport struct {
	module    string
	name      string
	kind      byte
	typeIndex uint32
}

type wasmExport struct {
	kind  byte
	index uint32
}

type wasmPolicyModule struct {
	types             []wasmFunctionType
	imports           []wasmImport
	functionTypes     []uint32
	exports           map[string]wasmExport
	definedMemories   uint32
	codeBodies        uint32
	hasStart          bool
	importedFunctions uint32
}

func validatePolicyWASMArtifact(name string) error {
	data, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	module, err := parseWASMPolicyModule(data)
	if err != nil {
		return fmt.Errorf("invalid WebAssembly module: %w", err)
	}
	if err := module.validatePolicyV1ABI(); err != nil {
		return fmt.Errorf("incompatible %s module: %w", pluginsdk.PolicyABIV1, err)
	}
	return nil
}

func parseWASMPolicyModule(data []byte) (wasmPolicyModule, error) {
	module := wasmPolicyModule{exports: map[string]wasmExport{}}
	if len(data) <= len(wasmV1Header) || !bytes.Equal(data[:min(len(data), len(wasmV1Header))], wasmV1Header) {
		return module, errors.New("version 1 module header is missing or the module is empty")
	}
	reader := wasmReader{data: data[len(wasmV1Header):]}
	seen := map[byte]bool{}
	lastRank := 0
	for reader.remaining() > 0 {
		sectionID, err := reader.byte()
		if err != nil {
			return module, err
		}
		sectionSize, err := reader.u32()
		if err != nil {
			return module, fmt.Errorf("section %d size: %w", sectionID, err)
		}
		section, err := reader.sub(sectionSize)
		if err != nil {
			return module, fmt.Errorf("section %d: %w", sectionID, err)
		}
		if sectionID != 0 {
			rank, ok := wasmSectionRank(sectionID)
			if !ok {
				return module, fmt.Errorf("unsupported section id %d", sectionID)
			}
			if seen[sectionID] {
				return module, fmt.Errorf("section %d is duplicated", sectionID)
			}
			if rank < lastRank {
				return module, fmt.Errorf("section %d is out of order", sectionID)
			}
			seen[sectionID] = true
			lastRank = rank
		}
		switch sectionID {
		case 0:
			// Custom sections are length-framed and have no ABI meaning.
			section.offset = len(section.data)
		case 1:
			err = parseWASMTypes(&section, &module)
		case 2:
			err = parseWASMImports(&section, &module)
		case 3:
			err = parseWASMFunctions(&section, &module)
		case 5:
			err = parseWASMMemories(&section, &module)
		case 7:
			err = parseWASMExports(&section, &module)
		case 8:
			module.hasStart = true
			_, err = section.u32()
		case 10:
			err = parseWASMCode(&section, &module)
		default:
			// Other core sections are still checked for ordering and framing.
			section.offset = len(section.data)
		}
		if err != nil {
			return module, fmt.Errorf("section %d: %w", sectionID, err)
		}
		if section.remaining() != 0 {
			return module, fmt.Errorf("section %d has %d trailing bytes", sectionID, section.remaining())
		}
	}
	if len(module.functionTypes) != int(module.codeBodies) {
		return module, fmt.Errorf("function and code counts differ: %d != %d", len(module.functionTypes), module.codeBodies)
	}
	for _, typeIndex := range module.functionTypes {
		if int(typeIndex) >= len(module.types) {
			return module, fmt.Errorf("function type index %d is out of range", typeIndex)
		}
	}
	return module, nil
}

func wasmSectionRank(id byte) (int, bool) {
	// The data-count section precedes code and data despite its numeric id.
	for rank, candidate := range []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 12, 10, 11} {
		if id == candidate {
			return rank + 1, true
		}
	}
	return 0, false
}

func parseWASMTypes(reader *wasmReader, module *wasmPolicyModule) error {
	count, err := reader.vectorCount()
	if err != nil {
		return err
	}
	module.types = make([]wasmFunctionType, 0, count)
	for index := uint32(0); index < count; index++ {
		form, err := reader.byte()
		if err != nil {
			return err
		}
		if form != 0x60 {
			return fmt.Errorf("type %d is not a function", index)
		}
		parameters, err := reader.valueTypes()
		if err != nil {
			return fmt.Errorf("type %d parameters: %w", index, err)
		}
		results, err := reader.valueTypes()
		if err != nil {
			return fmt.Errorf("type %d results: %w", index, err)
		}
		module.types = append(module.types, wasmFunctionType{parameters: parameters, results: results})
	}
	return nil
}

func parseWASMImports(reader *wasmReader, module *wasmPolicyModule) error {
	count, err := reader.vectorCount()
	if err != nil {
		return err
	}
	for index := uint32(0); index < count; index++ {
		moduleName, err := reader.name()
		if err != nil {
			return err
		}
		name, err := reader.name()
		if err != nil {
			return err
		}
		kind, err := reader.byte()
		if err != nil {
			return err
		}
		entry := wasmImport{module: moduleName, name: name, kind: kind}
		switch kind {
		case 0:
			entry.typeIndex, err = reader.u32()
			module.importedFunctions++
		case 1:
			err = reader.tableType()
		case 2:
			err = reader.limits()
		case 3:
			err = reader.globalType()
		default:
			err = fmt.Errorf("import %s.%s has unsupported kind %d", moduleName, name, kind)
		}
		if err != nil {
			return err
		}
		module.imports = append(module.imports, entry)
	}
	return nil
}

func parseWASMFunctions(reader *wasmReader, module *wasmPolicyModule) error {
	count, err := reader.vectorCount()
	if err != nil {
		return err
	}
	module.functionTypes = make([]uint32, count)
	for index := range module.functionTypes {
		module.functionTypes[index], err = reader.u32()
		if err != nil {
			return err
		}
	}
	return nil
}

func parseWASMMemories(reader *wasmReader, module *wasmPolicyModule) error {
	count, err := reader.vectorCount()
	if err != nil {
		return err
	}
	module.definedMemories = count
	for index := uint32(0); index < count; index++ {
		if err := reader.limits(); err != nil {
			return err
		}
	}
	return nil
}

func parseWASMExports(reader *wasmReader, module *wasmPolicyModule) error {
	count, err := reader.vectorCount()
	if err != nil {
		return err
	}
	for index := uint32(0); index < count; index++ {
		name, err := reader.name()
		if err != nil {
			return err
		}
		kind, err := reader.byte()
		if err != nil {
			return err
		}
		itemIndex, err := reader.u32()
		if err != nil {
			return err
		}
		if _, duplicate := module.exports[name]; duplicate {
			return fmt.Errorf("export %q is duplicated", name)
		}
		module.exports[name] = wasmExport{kind: kind, index: itemIndex}
	}
	return nil
}

func parseWASMCode(reader *wasmReader, module *wasmPolicyModule) error {
	count, err := reader.vectorCount()
	if err != nil {
		return err
	}
	module.codeBodies = count
	for index := uint32(0); index < count; index++ {
		size, err := reader.u32()
		if err != nil {
			return err
		}
		body, err := reader.sub(size)
		if err != nil {
			return fmt.Errorf("function body %d: %w", index, err)
		}
		localGroups, err := body.vectorCount()
		if err != nil {
			return err
		}
		for local := uint32(0); local < localGroups; local++ {
			if _, err := body.u32(); err != nil {
				return err
			}
			if _, err := body.valueType(); err != nil {
				return err
			}
		}
		if body.remaining() == 0 || body.data[len(body.data)-1] != 0x0b {
			return fmt.Errorf("function body %d is missing its end opcode", index)
		}
		body.offset = len(body.data)
	}
	return nil
}

func (module wasmPolicyModule) validatePolicyV1ABI() error {
	if module.hasStart {
		return errors.New("start functions are not allowed")
	}
	if module.definedMemories != 1 {
		return fmt.Errorf("exactly one guest-defined memory is required, found %d", module.definedMemories)
	}
	memory, ok := module.exports[pluginsdk.PolicyExportMemory]
	if !ok || memory.kind != 2 || memory.index != 0 {
		return fmt.Errorf("required memory export %q is missing or invalid", pluginsdk.PolicyExportMemory)
	}

	requiredImports := pluginsdk.PolicyV1HostFunctions()
	seenImports := map[string]bool{}
	for _, imported := range module.imports {
		if imported.kind != 0 || imported.module != pluginsdk.PolicyHostModule {
			return fmt.Errorf("dangerous import %q.%q is not allowed", imported.module, imported.name)
		}
		want, allowed := requiredImports[imported.name]
		if !allowed {
			return fmt.Errorf("dangerous import %q.%q is not allowed", imported.module, imported.name)
		}
		if seenImports[imported.name] {
			return fmt.Errorf("host import %q is duplicated", imported.name)
		}
		if int(imported.typeIndex) >= len(module.types) || !sameWASMFunctionType(module.types[imported.typeIndex], want) {
			return fmt.Errorf("host import %q has the wrong function signature", imported.name)
		}
		seenImports[imported.name] = true
	}
	for name := range requiredImports {
		if !seenImports[name] {
			return fmt.Errorf("required host import %q.%q is missing", pluginsdk.PolicyHostModule, name)
		}
	}

	for name, want := range pluginsdk.PolicyV1GuestFunctions() {
		exported, ok := module.exports[name]
		if !ok || exported.kind != 0 {
			return fmt.Errorf("required function export %q is missing", name)
		}
		if exported.index < module.importedFunctions {
			return fmt.Errorf("required function export %q must be guest-defined", name)
		}
		definedIndex := exported.index - module.importedFunctions
		if int(definedIndex) >= len(module.functionTypes) {
			return fmt.Errorf("function export %q index is out of range", name)
		}
		typeIndex := module.functionTypes[definedIndex]
		if int(typeIndex) >= len(module.types) || !sameWASMFunctionType(module.types[typeIndex], want) {
			return fmt.Errorf("function export %q has the wrong signature", name)
		}
	}
	return nil
}

func sameWASMFunctionType(got wasmFunctionType, want pluginsdk.WASMFunctionSignature) bool {
	if len(got.parameters) != len(want.Parameters) || len(got.results) != len(want.Results) {
		return false
	}
	for index := range got.parameters {
		if got.parameters[index] != want.Parameters[index] {
			return false
		}
	}
	for index := range got.results {
		if got.results[index] != want.Results[index] {
			return false
		}
	}
	return true
}

type wasmReader struct {
	data   []byte
	offset int
}

func (reader *wasmReader) remaining() int { return len(reader.data) - reader.offset }

func (reader *wasmReader) byte() (byte, error) {
	if reader.remaining() < 1 {
		return 0, ioUnexpectedEOF()
	}
	value := reader.data[reader.offset]
	reader.offset++
	return value, nil
}

func (reader *wasmReader) u32() (uint32, error) {
	var value uint32
	for shift := uint(0); shift < 35; shift += 7 {
		current, err := reader.byte()
		if err != nil {
			return 0, err
		}
		if shift == 28 && current&0xf0 != 0 {
			return 0, errors.New("u32 LEB128 overflows")
		}
		value |= uint32(current&0x7f) << shift
		if current&0x80 == 0 {
			return value, nil
		}
	}
	return 0, errors.New("u32 LEB128 is too long")
}

func (reader *wasmReader) sub(size uint32) (wasmReader, error) {
	if uint64(size) > uint64(reader.remaining()) {
		return wasmReader{}, ioUnexpectedEOF()
	}
	start := reader.offset
	reader.offset += int(size)
	return wasmReader{data: reader.data[start:reader.offset]}, nil
}

func (reader *wasmReader) vectorCount() (uint32, error) {
	count, err := reader.u32()
	if err != nil {
		return 0, err
	}
	if count > 1<<20 {
		return 0, errors.New("vector count exceeds validator limit")
	}
	return count, nil
}

func (reader *wasmReader) name() (string, error) {
	length, err := reader.u32()
	if err != nil {
		return "", err
	}
	value, err := reader.sub(length)
	if err != nil {
		return "", err
	}
	return string(value.data), nil
}

func (reader *wasmReader) valueTypes() ([]pluginsdk.WASMValueType, error) {
	count, err := reader.vectorCount()
	if err != nil {
		return nil, err
	}
	values := make([]pluginsdk.WASMValueType, count)
	for index := range values {
		values[index], err = reader.valueType()
		if err != nil {
			return nil, err
		}
	}
	return values, nil
}

func (reader *wasmReader) valueType() (pluginsdk.WASMValueType, error) {
	value, err := reader.byte()
	if err != nil {
		return 0, err
	}
	switch pluginsdk.WASMValueType(value) {
	case pluginsdk.WASMI32, pluginsdk.WASMI64:
		return pluginsdk.WASMValueType(value), nil
	default:
		return 0, fmt.Errorf("value type 0x%x is outside the policy ABI", value)
	}
}

func (reader *wasmReader) limits() error {
	flags, err := reader.byte()
	if err != nil {
		return err
	}
	if flags != 0 && flags != 1 {
		return fmt.Errorf("unsupported limits flags 0x%x", flags)
	}
	minimum, err := reader.u32()
	if err != nil {
		return err
	}
	if flags == 1 {
		maximum, err := reader.u32()
		if err != nil {
			return err
		}
		if maximum < minimum {
			return errors.New("limits maximum is below minimum")
		}
	}
	return nil
}

func (reader *wasmReader) tableType() error {
	if _, err := reader.byte(); err != nil {
		return err
	}
	return reader.limits()
}

func (reader *wasmReader) globalType() error {
	if _, err := reader.valueType(); err != nil {
		return err
	}
	mutability, err := reader.byte()
	if err != nil {
		return err
	}
	if mutability > 1 {
		return errors.New("invalid global mutability")
	}
	return nil
}

func ioUnexpectedEOF() error { return errors.New("unexpected end of module") }
