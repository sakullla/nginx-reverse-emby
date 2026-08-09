package pluginsdk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

const (
	WASMPageSizeBytes      uint64 = 65536
	WASMModuleV1HeaderSize        = 8
)

var wasmModuleV1Header = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

// ValidatePolicyV1WASM statically validates the complete nre:policy/v1 module
// declaration against a manifest memory budget. It compiles declarations but
// never instantiates the module or executes guest code. This is the canonical
// validator shared by package admission and every runtime host.
func ValidatePolicyV1WASM(module []byte, memoryBudgetBytes int64) error {
	if len(module) == len(wasmModuleV1Header) && bytes.Equal(module, wasmModuleV1Header) {
		return errors.New("invalid WebAssembly module: version 1 module header is present but the module is empty")
	}
	if memoryBudgetBytes < PolicyV1MinMemoryBytes || memoryBudgetBytes > PolicyV1MaxMemoryBytes {
		return fmt.Errorf("manifest WebAssembly memory budget must be within %d..%d bytes", PolicyV1MinMemoryBytes, PolicyV1MaxMemoryBytes)
	}
	declaration, err := inspectPolicyV1Declarations(module)
	if err != nil {
		return fmt.Errorf("incompatible %s module: %w", PolicyABIV1, err)
	}

	ctx := context.Background()
	runtime := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCoreFeatures(api.CoreFeaturesV1))
	defer runtime.Close(context.Background())
	compiled, err := runtime.CompileModule(ctx, module)
	if err != nil {
		return fmt.Errorf("invalid WebAssembly module: %w", err)
	}
	defer compiled.Close(ctx)
	if err := validatePolicyV1CompiledModule(compiled, memoryBudgetBytes); err != nil {
		return fmt.Errorf("incompatible %s module: %w", PolicyABIV1, err)
	}
	if err := declaration.validateStaticMajor(); err != nil {
		return fmt.Errorf("incompatible %s module: %w", PolicyABIV1, err)
	}
	return nil
}

func WASMPagesToBytes(pages uint64) (uint64, error) {
	if pages > math.MaxUint64/WASMPageSizeBytes {
		return 0, errors.New("WebAssembly page-to-byte conversion overflows uint64")
	}
	return pages * WASMPageSizeBytes, nil
}

func validatePolicyV1CompiledModule(module wazero.CompiledModule, memoryBudgetBytes int64) error {
	requiredImports := PolicyV1HostFunctions()
	seenImports := make(map[string]bool, len(requiredImports))
	for _, imported := range module.ImportedFunctions() {
		moduleName, name, ok := imported.Import()
		if !ok || moduleName != PolicyHostModule {
			return fmt.Errorf("dangerous import %q.%q is not allowed", moduleName, name)
		}
		want, allowed := requiredImports[name]
		if !allowed {
			return fmt.Errorf("dangerous import %q.%q is not allowed", moduleName, name)
		}
		if seenImports[name] {
			return fmt.Errorf("host import %q is duplicated", name)
		}
		if !sameWASMFunctionSignature(imported, want) {
			return fmt.Errorf("host import %q has the wrong function signature", name)
		}
		seenImports[name] = true
	}
	for name := range requiredImports {
		if !seenImports[name] {
			return fmt.Errorf("required host import %q.%q is missing", PolicyHostModule, name)
		}
	}

	if imported := module.ImportedMemories(); len(imported) != 0 {
		return errors.New("guest memory must be defined by the policy module, not imported")
	}
	memory, ok := module.ExportedMemories()[PolicyExportMemory]
	if !ok {
		return fmt.Errorf("required memory export %q is missing or invalid", PolicyExportMemory)
	}
	if _, _, imported := memory.Import(); imported {
		return errors.New("guest memory must be defined by the policy module, not imported")
	}
	maximumPages, maximumEncoded := memory.Max()
	if !maximumEncoded {
		return fmt.Errorf("memory export %q must declare an explicit maximum", PolicyExportMemory)
	}
	minimumBytes, err := WASMPagesToBytes(uint64(memory.Min()))
	if err != nil {
		return fmt.Errorf("memory minimum: %w", err)
	}
	maximumBytes, err := WASMPagesToBytes(uint64(maximumPages))
	if err != nil {
		return fmt.Errorf("memory maximum: %w", err)
	}
	if minimumBytes > uint64(PolicyV1MaxMemoryBytes) {
		return fmt.Errorf("initial memory %d bytes exceeds ABI validation ceiling %d bytes", minimumBytes, PolicyV1MaxMemoryBytes)
	}
	if minimumBytes > uint64(memoryBudgetBytes) || maximumBytes > uint64(memoryBudgetBytes) {
		return fmt.Errorf("memory range %d..%d bytes exceeds manifest resource budget %d bytes", minimumBytes, maximumBytes, memoryBudgetBytes)
	}

	exports := module.ExportedFunctions()
	for name, want := range PolicyV1GuestFunctions() {
		exported, ok := exports[name]
		if !ok {
			return fmt.Errorf("required function export %q is missing", name)
		}
		if _, _, imported := exported.Import(); imported {
			return fmt.Errorf("required function export %q must be guest-defined", name)
		}
		if !sameWASMFunctionSignature(exported, want) {
			return fmt.Errorf("function export %q has the wrong signature", name)
		}
	}
	return nil
}

func sameWASMFunctionSignature(got api.FunctionDefinition, want WASMFunctionSignature) bool {
	parameters, results := got.ParamTypes(), got.ResultTypes()
	if len(parameters) != len(want.Parameters) || len(results) != len(want.Results) {
		return false
	}
	for index := range parameters {
		if byte(parameters[index]) != byte(want.Parameters[index]) {
			return false
		}
	}
	for index := range results {
		if byte(results[index]) != byte(want.Results[index]) {
			return false
		}
	}
	return true
}

type policyV1Declaration struct {
	importedFunctions uint32
	versionFunction   uint32
	functionCount     uint32
	codeBodies        [][]byte
}

func inspectPolicyV1Declarations(module []byte) (policyV1Declaration, error) {
	if len(module) < len(wasmModuleV1Header) || !bytes.Equal(module[:len(wasmModuleV1Header)], wasmModuleV1Header) {
		return policyV1Declaration{}, errors.New("invalid WebAssembly version 1 header")
	}
	result := policyV1Declaration{versionFunction: math.MaxUint32}
	reader := wasmReader{data: module[len(wasmModuleV1Header):]}
	for reader.remaining() > 0 {
		sectionID, err := reader.byte()
		if err != nil {
			return result, err
		}
		sectionSize, err := reader.u32()
		if err != nil {
			return result, err
		}
		section, err := reader.sub(sectionSize)
		if err != nil {
			return result, err
		}
		parsed := true
		switch sectionID {
		case 2:
			count, err := section.u32()
			if err != nil {
				return result, err
			}
			for index := uint32(0); index < count; index++ {
				moduleName, err := section.name()
				if err != nil {
					return result, err
				}
				name, err := section.name()
				if err != nil {
					return result, err
				}
				kind, err := section.byte()
				if err != nil {
					return result, err
				}
				if kind != byte(api.ExternTypeFunc) {
					return result, fmt.Errorf("dangerous non-function import %q.%q is not allowed", moduleName, name)
				}
				if _, err := section.u32(); err != nil {
					return result, err
				}
				result.importedFunctions++
			}
		case 3:
			count, err := section.u32()
			if err != nil {
				return result, err
			}
			result.functionCount = count
			for index := uint32(0); index < count; index++ {
				if _, err := section.u32(); err != nil {
					return result, err
				}
			}
		case 4:
			count, err := section.u32()
			if err != nil {
				return result, err
			}
			if count != 0 {
				return result, errors.New("WebAssembly tables are not allowed by the policy v1 ABI")
			}
		case 7:
			count, err := section.u32()
			if err != nil {
				return result, err
			}
			for index := uint32(0); index < count; index++ {
				name, err := section.name()
				if err != nil {
					return result, err
				}
				kind, err := section.byte()
				if err != nil {
					return result, err
				}
				exportIndex, err := section.u32()
				if err != nil {
					return result, err
				}
				if name == PolicyExportVersion && kind == byte(api.ExternTypeFunc) {
					result.versionFunction = exportIndex
				}
			}
		case 8:
			return result, errors.New("start functions are not allowed")
		case 10:
			count, err := section.u32()
			if err != nil {
				return result, err
			}
			result.codeBodies = make([][]byte, 0, count)
			for index := uint32(0); index < count; index++ {
				size, err := section.u32()
				if err != nil {
					return result, err
				}
				body, err := section.sub(size)
				if err != nil {
					return result, err
				}
				result.codeBodies = append(result.codeBodies, body.data)
			}
		default:
			parsed = false
		}
		if parsed && section.remaining() != 0 {
			return result, fmt.Errorf("WebAssembly section %d has trailing declaration bytes", sectionID)
		}
	}
	return result, nil
}

func (declaration policyV1Declaration) validateStaticMajor() error {
	if declaration.versionFunction == math.MaxUint32 || declaration.versionFunction < declaration.importedFunctions {
		return fmt.Errorf("required function export %q must be a guest-defined static ABI declaration", PolicyExportVersion)
	}
	definedIndex := declaration.versionFunction - declaration.importedFunctions
	if definedIndex >= declaration.functionCount || int(definedIndex) >= len(declaration.codeBodies) {
		return fmt.Errorf("required function export %q has no function body", PolicyExportVersion)
	}
	body := declaration.codeBodies[definedIndex]
	if len(body) != 4 || body[0] != 0x00 || body[1] != 0x41 || body[2]&0x80 != 0 || body[3] != 0x0b {
		return fmt.Errorf("%q must use the canonical static ABI major declaration", PolicyExportVersion)
	}
	major := int32(body[2])
	if body[2]&0x40 != 0 {
		major -= 0x80
	}
	if uint32(major) != PolicyABIMajorVersion {
		return fmt.Errorf("%q declares ABI major %d, want %d", PolicyExportVersion, major, PolicyABIMajorVersion)
	}
	return nil
}

type wasmReader struct {
	data   []byte
	offset int
}

func (reader *wasmReader) remaining() int { return len(reader.data) - reader.offset }

func (reader *wasmReader) byte() (byte, error) {
	if reader.remaining() < 1 {
		return 0, errors.New("unexpected end of validated WebAssembly module")
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
		value |= uint32(current&0x7f) << shift
		if current&0x80 == 0 {
			return value, nil
		}
	}
	return 0, errors.New("u32 LEB128 is too long")
}

func (reader *wasmReader) sub(size uint32) (wasmReader, error) {
	if uint64(size) > uint64(reader.remaining()) {
		return wasmReader{}, errors.New("unexpected end of validated WebAssembly module")
	}
	start := reader.offset
	reader.offset += int(size)
	return wasmReader{data: reader.data[start:reader.offset]}, nil
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
