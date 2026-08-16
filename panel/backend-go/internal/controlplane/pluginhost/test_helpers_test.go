//go:build !integration

package pluginhost

import (
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func validatedSandboxPackage(digest string, permissions []string, extensions []string) plugins.ValidatedPackage {
	manifestPermissions := make([]plugins.Permission, 0, len(permissions))
	for _, permission := range permissions {
		manifestPermissions = append(manifestPermissions, plugins.Permission{Name: permission})
	}
	return plugins.ValidatedPackage{Digest: digest, Manifest: plugins.Manifest{
		Runtime:         plugins.Runtime{Kind: pluginsdk.RuntimeRPCService, ABI: pluginsdk.RPCABIV1, HostScope: "control-plane", Entry: "plugin"},
		Permissions:     manifestPermissions,
		ExtensionPoints: append([]string(nil), extensions...),
		ResourceBudget:  plugins.ResourceBudget{TimeoutMS: 1000, MemoryBytes: 256 << 20, Concurrency: 2, InputBytes: 4096, OutputBytes: 4096, CPUMillis: 1000, Restarts: 2},
	}}
}

func mustValidatedSandboxRequirement(t *testing.T, digest string) SandboxRequirement {
	t.Helper()
	requirement, err := SandboxRequirementFromValidatedPackage(validatedSandboxPackage(digest, []string{"agent.read"}, []string{"http.request"}))
	if err != nil {
		t.Fatal(err)
	}
	return requirement
}
