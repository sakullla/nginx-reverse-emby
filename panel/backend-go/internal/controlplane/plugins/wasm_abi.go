package plugins

import (
	"os"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const wasmPageSizeBytes = pluginsdk.WASMPageSizeBytes

func validatePolicyWASMArtifact(name string, memoryBudgetBytes int64) error {
	data, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	return pluginsdk.ValidatePolicyV1WASM(data, memoryBudgetBytes)
}

func wasmPagesToBytes(pages uint64) (uint64, error) {
	return pluginsdk.WASMPagesToBytes(pages)
}
