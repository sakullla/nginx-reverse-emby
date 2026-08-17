package pluginsdk

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tetratelabs/wazero"
)

func TestValidatePolicyV1WASMCompilerCapabilityFailuresFailClosed(t *testing.T) {
	encodedFixture, err := os.ReadFile(filepath.Join("..", "policy", "v1", "testdata", "compatible_guest.wasm.hex"))
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := hex.DecodeString(strings.TrimSpace(string(encodedFixture)))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name                  string
		compilerConfigFactory func() wazero.RuntimeConfig
		runtimeFactory        func(context.Context, wazero.RuntimeConfig) wazero.Runtime
	}{
		{
			name: "unsupported compiler platform",
			compilerConfigFactory: func() wazero.RuntimeConfig {
				panic("unsupported GOOS/GOARCH/CPU")
			},
			runtimeFactory: wazero.NewRuntimeWithConfig,
		},
		{
			name:                  "compiler configuration unavailable",
			compilerConfigFactory: func() wazero.RuntimeConfig { return nil },
			runtimeFactory:        wazero.NewRuntimeWithConfig,
		},
		{
			name:                  "executable memory denied",
			compilerConfigFactory: wazero.NewRuntimeConfigCompiler,
			runtimeFactory: func(context.Context, wazero.RuntimeConfig) wazero.Runtime {
				panic("executable memory policy denied compiler runtime")
			},
		},
		{
			name:                  "compiler runtime unavailable",
			compilerConfigFactory: wazero.NewRuntimeConfigCompiler,
			runtimeFactory:        func(context.Context, wazero.RuntimeConfig) wazero.Runtime { return nil },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validatePolicyV1WASMWithCompiler(
				fixture,
				1<<20,
				test.compilerConfigFactory,
				test.runtimeFactory,
			)
			if err == nil || !strings.Contains(err.Error(), "WebAssembly compiler is unavailable") {
				t.Fatalf("validation error = %v, want compiler-unavailable failure", err)
			}
		})
	}
}
