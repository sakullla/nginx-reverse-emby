package pluginsdk_test

import (
	"bytes"
	"strings"
	"testing"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/compatfixture"
)

func TestValidatePolicyV1WASMOwnsCanonicalDeclaration(t *testing.T) {
	fixture := compatfixture.PolicyV1GuestWASM()
	assertRejected := func(t *testing.T, module []byte, marker string) {
		t.Helper()
		err := pluginsdk.ValidatePolicyV1WASM(module, 1<<20)
		if err == nil || !strings.Contains(err.Error(), marker) {
			t.Fatalf("error = %v, want marker %q", err, marker)
		}
	}
	if err := pluginsdk.ValidatePolicyV1WASM(fixture, 1<<20); err != nil {
		t.Fatalf("canonical fixture rejected: %v", err)
	}

	wrongMajor := bytes.Replace(fixture, []byte{0x04, 0x00, 0x41, 0x01, 0x0b}, []byte{0x04, 0x00, 0x41, 0x02, 0x0b}, 1)
	assertRejected(t, wrongMajor, "declares ABI major 2, want 1")

	duplicateImport := bytes.Replace(fixture, []byte(pluginsdk.PolicyHostEmitEvent), []byte(pluginsdk.PolicyHostReadField), 1)
	assertRejected(t, duplicateImport, "host import \"nre_host_read_field\" is duplicated")

	tablePayload := []byte{0x01, 0x70, 0x00, 0x01}
	withTable := insertWASMSectionBefore(t, fixture, 5, 4, tablePayload)
	assertRejected(t, withTable, "tables are not allowed")

	withStart := insertWASMSectionBefore(t, fixture, 10, 8, []byte{0x06})
	assertRejected(t, withStart, "start functions are not allowed")
}

func TestPolicyV1ResourceBudgetBoundaries(t *testing.T) {
	minimum := pluginsdk.PolicyV1ResourceBudget{
		TimeoutMilliseconds: 1,
		MemoryBytes:         pluginsdk.PolicyV1MinMemoryBytes,
		Concurrency:         1,
		InputFrameBytes:     pluginsdk.PolicyV1MinInputFrameBytes,
		OutputFrameBytes:    pluginsdk.PolicyV1MinOutputFrameBytes,
	}
	maximum := pluginsdk.PolicyV1ResourceBudget{
		TimeoutMilliseconds: pluginsdk.PolicyV1MaxTimeoutMilliseconds,
		MemoryBytes:         pluginsdk.PolicyV1MaxMemoryBytes,
		Concurrency:         pluginsdk.PolicyV1MaxConcurrency,
		InputFrameBytes:     pluginsdk.PolicyV1MaxInputFrameBytes,
		OutputFrameBytes:    pluginsdk.PolicyV1MaxOutputFrameBytes,
	}
	if err := minimum.Validate(); err != nil {
		t.Fatalf("minimum budget rejected: %v", err)
	}
	if err := maximum.Validate(); err != nil {
		t.Fatalf("maximum budget rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*pluginsdk.PolicyV1ResourceBudget)
	}{
		{"timeout ceiling", func(b *pluginsdk.PolicyV1ResourceBudget) { b.TimeoutMilliseconds++ }},
		{"memory ceiling", func(b *pluginsdk.PolicyV1ResourceBudget) { b.MemoryBytes++ }},
		{"concurrency ceiling", func(b *pluginsdk.PolicyV1ResourceBudget) { b.Concurrency++ }},
		{"input frame ceiling", func(b *pluginsdk.PolicyV1ResourceBudget) { b.InputFrameBytes++ }},
		{"output frame ceiling", func(b *pluginsdk.PolicyV1ResourceBudget) { b.OutputFrameBytes++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invalid := maximum
			test.mutate(&invalid)
			if err := invalid.Validate(); err == nil {
				t.Fatal("over-ceiling policy budget accepted")
			}
		})
	}
	minimum.InputFrameBytes--
	if err := minimum.Validate(); err == nil || !strings.Contains(err.Error(), "complete protobuf wire-frame budget") {
		t.Fatalf("undersized input wire-frame budget error = %v", err)
	}
}

func TestPolicyV1OptionalImportsRequireActualHostCapabilities(t *testing.T) {
	available := make([]string, 0)
	for name := range pluginsdk.PolicyV1HostFunctions() {
		available = append(available, name)
	}
	legacy := compatfixture.PolicyV1GuestWASM()
	if err := pluginsdk.ValidatePolicyV1WASMForHost(legacy, 1<<20, nil, nil, available); err != nil {
		t.Fatalf("legacy guest rejected: %v", err)
	}
	module, err := compatfixture.PolicyV1GuestWASMWithOptionalImports(pluginsdk.PolicyHostReadTrustedSource, pluginsdk.PolicyHostDatasetQuery)
	if err != nil {
		t.Fatal(err)
	}
	scopes := []string{string(pluginsdk.CapabilityPolicyTrustedSource), string(pluginsdk.CapabilityDatasetQuery)}
	if err := pluginsdk.ValidatePolicyV1WASMForHost(module, 1<<20, scopes, scopes, available); err != nil {
		t.Fatalf("extended guest rejected: %v", err)
	}
	if err := pluginsdk.ValidatePolicyV1WASMForHost(module, 1<<20, scopes, scopes[:1], available); err == nil {
		t.Fatal("dataset import without grant accepted")
	}
	if err := pluginsdk.ValidatePolicyV1WASMForHost(module, 1<<20, scopes[:1], scopes, available); err == nil {
		t.Fatal("dataset import without signed declaration accepted")
	}
	legacyImports := make([]string, 0)
	for name := range pluginsdk.PolicyV1RequiredHostFunctions() {
		legacyImports = append(legacyImports, name)
	}
	if err := pluginsdk.ValidatePolicyV1WASMForHost(module, 1<<20, scopes, scopes, legacyImports); err == nil {
		t.Fatal("Host lacking actual additive imports accepted extended guest")
	}
}

func TestPolicyDatasetResolveImportIsOptionalAndRequiresActualHostSupport(t *testing.T) {
	module, err := compatfixture.PolicyV1GuestWASMWithOptionalImports(pluginsdk.PolicyHostDatasetResolve)
	if err != nil {
		t.Fatal(err)
	}
	available := make([]string, 0)
	for name := range pluginsdk.PolicyV1HostFunctions() {
		available = append(available, name)
	}
	scopes := []string{string(pluginsdk.CapabilityDatasetQuery), string(pluginsdk.CapabilityDatasetResolve)}
	if err := pluginsdk.ValidatePolicyV1WASMForHost(module, 1<<20, scopes, scopes, available); err != nil {
		t.Fatal("supported resolver module rejected", err)
	}
	legacy := make([]string, 0)
	for name := range pluginsdk.PolicyV1RequiredHostFunctions() {
		legacy = append(legacy, name)
	}
	if err := pluginsdk.ValidatePolicyV1WASMForHost(module, 1<<20, scopes, scopes, legacy); err == nil {
		t.Fatal("old Host admitted an unavailable resolver import")
	}
	for _, missing := range [][]string{nil, scopes[:1], scopes[1:]} {
		if pluginsdk.ValidatePolicyV1WASMForHost(module, 1<<20, scopes, missing, available) == nil || pluginsdk.ValidatePolicyV1WASMForHost(module, 1<<20, missing, scopes, available) == nil {
			t.Fatal("resolver import accepted missing grant/declaration")
		}
	}
	if err := pluginsdk.ValidatePolicyV1WASMForHost(compatfixture.PolicyV1GuestWASM(), 1<<20, nil, nil, legacy); err != nil {
		t.Fatal("original six-import guest regressed", err)
	}
}

func insertWASMSectionBefore(t *testing.T, module []byte, beforeID, sectionID byte, payload []byte) []byte {
	t.Helper()
	for offset := pluginsdk.WASMModuleV1HeaderSize; offset < len(module); {
		start := offset
		currentID := module[offset]
		offset++
		size, payloadStart := decodeULEB128(t, module, offset)
		end := payloadStart + int(size)
		if end > len(module) {
			t.Fatal("fixture section exceeds module")
		}
		if currentID == beforeID {
			section := []byte{sectionID}
			section = append(section, encodeULEB128(uint32(len(payload)))...)
			section = append(section, payload...)
			result := append([]byte(nil), module[:start]...)
			result = append(result, section...)
			return append(result, module[start:]...)
		}
		offset = end
	}
	t.Fatalf("fixture section %d not found", beforeID)
	return nil
}

func decodeULEB128(t *testing.T, data []byte, offset int) (uint32, int) {
	t.Helper()
	var value uint32
	for shift := uint(0); shift < 35; shift += 7 {
		if offset >= len(data) {
			t.Fatal("truncated ULEB128")
		}
		current := data[offset]
		offset++
		value |= uint32(current&0x7f) << shift
		if current&0x80 == 0 {
			return value, offset
		}
	}
	t.Fatal("ULEB128 is too long")
	return 0, 0
}

func encodeULEB128(value uint32) []byte {
	var result []byte
	for {
		current := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			current |= 0x80
		}
		result = append(result, current)
		if value == 0 {
			return result
		}
	}
}
