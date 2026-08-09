package plugins

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

const wasmPageSizeBytes = uint64(65536)

const policyABIVersionValidationMemoryBytes = uint64(16 << 20)

var wasmV1Header = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

func validatePolicyWASMArtifact(name string, memoryBudgetBytes int64) error {
	data, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	if len(data) == len(wasmV1Header) && bytes.Equal(data, wasmV1Header) {
		return errors.New("invalid WebAssembly module: version 1 module header is present but the module is empty")
	}

	if memoryBudgetBytes <= 0 {
		return errors.New("manifest resource budget must provide positive WebAssembly memory")
	}
	// Inspect declarations before handing the module to wazero. Policy v1 has
	// no table ABI, so rejecting tables here prevents hostile initial table
	// sizes from reaching module instantiation.
	if err := validatePolicyV1Declarations(data); err != nil {
		return fmt.Errorf("incompatible %s module: %w", pluginsdk.PolicyABIV1, err)
	}

	ctx := context.Background()
	runtimeConfig := wazero.NewRuntimeConfig().WithCoreFeatures(api.CoreFeaturesV1)
	runtime := wazero.NewRuntimeWithConfig(ctx, runtimeConfig)
	defer runtime.Close(context.Background())
	compiled, err := runtime.CompileModule(ctx, data)
	if err != nil {
		return fmt.Errorf("invalid WebAssembly module: %w", err)
	}
	defer compiled.Close(ctx)

	if err := validatePolicyV1CompiledModule(compiled, memoryBudgetBytes); err != nil {
		return fmt.Errorf("incompatible %s module: %w", pluginsdk.PolicyABIV1, err)
	}
	if err := validatePolicyV1StaticMajorVersion(data); err != nil {
		return fmt.Errorf("incompatible %s module: %w", pluginsdk.PolicyABIV1, err)
	}
	return nil
}

// validatePolicyV1StaticMajorVersion proves the ABI major without executing
// attacker-controlled guest code. Policy v1 deliberately requires the
// exported version function to have the canonical body `i32.const 1; end`.
// This permits a package to declare a legitimate high memory maximum while
// preventing a version probe from using memory.grow to allocate that maximum.
func validatePolicyV1StaticMajorVersion(data []byte) error {
	reader := wasmDeclarationReader{data: data[len(wasmV1Header):]}
	var importedFunctions uint32
	versionFunction := uint32(math.MaxUint32)
	var functionCount uint32
	var codeBodies [][]byte
	for reader.remaining() > 0 {
		sectionID, err := reader.byte()
		if err != nil {
			return err
		}
		sectionSize, err := reader.u32()
		if err != nil {
			return err
		}
		section, err := reader.sub(sectionSize)
		if err != nil {
			return err
		}
		switch sectionID {
		case 2:
			count, err := section.u32()
			if err != nil {
				return err
			}
			for index := uint32(0); index < count; index++ {
				if _, err := section.name(); err != nil {
					return err
				}
				if _, err := section.name(); err != nil {
					return err
				}
				kind, err := section.byte()
				if err != nil {
					return err
				}
				if kind != byte(api.ExternTypeFunc) {
					return errors.New("static ABI declaration encountered a non-function import")
				}
				if _, err := section.u32(); err != nil {
					return err
				}
				importedFunctions++
			}
		case 3:
			count, err := section.u32()
			if err != nil {
				return err
			}
			functionCount = count
			for index := uint32(0); index < count; index++ {
				if _, err := section.u32(); err != nil {
					return err
				}
			}
		case 7:
			count, err := section.u32()
			if err != nil {
				return err
			}
			for index := uint32(0); index < count; index++ {
				name, err := section.name()
				if err != nil {
					return err
				}
				kind, err := section.byte()
				if err != nil {
					return err
				}
				functionIndex, err := section.u32()
				if err != nil {
					return err
				}
				if name == pluginsdk.PolicyExportVersion && kind == byte(api.ExternTypeFunc) {
					versionFunction = functionIndex
				}
			}
		case 10:
			count, err := section.u32()
			if err != nil {
				return err
			}
			codeBodies = make([][]byte, 0, count)
			for index := uint32(0); index < count; index++ {
				size, err := section.u32()
				if err != nil {
					return err
				}
				body, err := section.sub(size)
				if err != nil {
					return err
				}
				codeBodies = append(codeBodies, body.data)
			}
		}
	}
	if versionFunction == math.MaxUint32 || versionFunction < importedFunctions {
		return fmt.Errorf("required function export %q must be a guest-defined static ABI declaration", pluginsdk.PolicyExportVersion)
	}
	definedIndex := versionFunction - importedFunctions
	if definedIndex >= functionCount || int(definedIndex) >= len(codeBodies) {
		return fmt.Errorf("required function export %q has no function body", pluginsdk.PolicyExportVersion)
	}
	body := codeBodies[definedIndex]
	if len(body) != 4 || body[0] != 0x00 || body[1] != 0x41 || body[2]&0x80 != 0 || body[3] != 0x0b {
		return fmt.Errorf("%q must use the canonical static ABI major declaration", pluginsdk.PolicyExportVersion)
	}
	major := int32(body[2])
	if body[2]&0x40 != 0 {
		major -= 0x80
	}
	if uint32(major) != pluginsdk.PolicyABIMajorVersion {
		return fmt.Errorf("%q declares ABI major %d, want %d", pluginsdk.PolicyExportVersion, major, pluginsdk.PolicyABIMajorVersion)
	}
	return nil
}

func validatePolicyV1CompiledModule(module wazero.CompiledModule, memoryBudgetBytes int64) error {
	requiredImports := pluginsdk.PolicyV1HostFunctions()
	seenImports := make(map[string]bool, len(requiredImports))
	for _, imported := range module.ImportedFunctions() {
		moduleName, name, ok := imported.Import()
		if !ok || moduleName != pluginsdk.PolicyHostModule {
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
			return fmt.Errorf("required host import %q.%q is missing", pluginsdk.PolicyHostModule, name)
		}
	}

	if imported := module.ImportedMemories(); len(imported) != 0 {
		return errors.New("guest memory must be defined by the policy module, not imported")
	}
	memory, ok := module.ExportedMemories()[pluginsdk.PolicyExportMemory]
	if !ok {
		return fmt.Errorf("required memory export %q is missing or invalid", pluginsdk.PolicyExportMemory)
	}
	if _, _, imported := memory.Import(); imported {
		return errors.New("guest memory must be defined by the policy module, not imported")
	}
	maximumPages, maximumEncoded := memory.Max()
	if !maximumEncoded {
		return fmt.Errorf("memory export %q must declare an explicit maximum", pluginsdk.PolicyExportMemory)
	}
	minimumBytes, err := wasmPagesToBytes(uint64(memory.Min()))
	if err != nil {
		return fmt.Errorf("memory minimum: %w", err)
	}
	maximumBytes, err := wasmPagesToBytes(uint64(maximumPages))
	if err != nil {
		return fmt.Errorf("memory maximum: %w", err)
	}
	if memoryBudgetBytes < 0 || minimumBytes > uint64(memoryBudgetBytes) || maximumBytes > uint64(memoryBudgetBytes) {
		return fmt.Errorf("memory range %d..%d bytes exceeds manifest resource budget %d bytes", minimumBytes, maximumBytes, memoryBudgetBytes)
	}
	if minimumBytes > policyABIVersionValidationMemoryBytes {
		return fmt.Errorf("initial memory %d bytes exceeds ABI validation ceiling %d bytes", minimumBytes, policyABIVersionValidationMemoryBytes)
	}

	exports := module.ExportedFunctions()
	for name, want := range pluginsdk.PolicyV1GuestFunctions() {
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

func sameWASMFunctionSignature(got api.FunctionDefinition, want pluginsdk.WASMFunctionSignature) bool {
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

func wasmPagesToBytes(pages uint64) (uint64, error) {
	if pages > math.MaxUint64/wasmPageSizeBytes {
		return 0, errors.New("WebAssembly page-to-byte conversion overflows uint64")
	}
	return pages * wasmPageSizeBytes, nil
}

// validatePolicyV1Declarations checks bounded ABI declarations before wazero
// can instantiate the module. CompileModule remains the semantic validator.
func validatePolicyV1Declarations(data []byte) error {
	if len(data) < len(wasmV1Header) || !bytes.Equal(data[:len(wasmV1Header)], wasmV1Header) {
		return errors.New("invalid WebAssembly version 1 header")
	}
	reader := wasmDeclarationReader{data: data[len(wasmV1Header):]}
	for reader.remaining() > 0 {
		sectionID, err := reader.byte()
		if err != nil {
			return err
		}
		sectionSize, err := reader.u32()
		if err != nil {
			return err
		}
		section, err := reader.sub(sectionSize)
		if err != nil {
			return err
		}
		switch sectionID {
		case 2:
			count, err := section.u32()
			if err != nil {
				return err
			}
			for index := uint32(0); index < count; index++ {
				moduleName, err := section.name()
				if err != nil {
					return err
				}
				name, err := section.name()
				if err != nil {
					return err
				}
				kind, err := section.byte()
				if err != nil {
					return err
				}
				if kind != byte(api.ExternTypeFunc) {
					return fmt.Errorf("dangerous non-function import %q.%q is not allowed", moduleName, name)
				}
				if _, err := section.u32(); err != nil {
					return err
				}
			}
		case 4:
			count, err := section.u32()
			if err != nil {
				return err
			}
			if count != 0 {
				return errors.New("WebAssembly tables are not allowed by the policy v1 ABI")
			}
		case 8:
			return errors.New("start functions are not allowed")
		}
	}
	return nil
}

type wasmDeclarationReader struct {
	data   []byte
	offset int
}

func (reader *wasmDeclarationReader) remaining() int { return len(reader.data) - reader.offset }

func (reader *wasmDeclarationReader) byte() (byte, error) {
	if reader.remaining() < 1 {
		return 0, errors.New("unexpected end of validated WebAssembly module")
	}
	value := reader.data[reader.offset]
	reader.offset++
	return value, nil
}

func (reader *wasmDeclarationReader) u32() (uint32, error) {
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

func (reader *wasmDeclarationReader) sub(size uint32) (wasmDeclarationReader, error) {
	if uint64(size) > uint64(reader.remaining()) {
		return wasmDeclarationReader{}, errors.New("unexpected end of validated WebAssembly module")
	}
	start := reader.offset
	reader.offset += int(size)
	return wasmDeclarationReader{data: reader.data[start:reader.offset]}, nil
}

func (reader *wasmDeclarationReader) name() (string, error) {
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
