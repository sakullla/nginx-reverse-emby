//go:build linux && integration

package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/rpcplugin"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIntegrationLegacyExecutionScopeChild(t *testing.T) {
	if os.Getenv(sdk.EnvPluginEndpoint) == "" {
		return
	}
	if _, present := os.LookupEnv(sdk.EnvPluginExecutionScope); present {
		t.Fatal("legacy package received a new execution protocol")
	}
	if strings.TrimSpace(os.Getenv(sdk.EnvPluginHostEndpoint)) == "" {
		t.Fatal("legacy managed package did not receive its HostRuntime endpoint")
	}
	adapter, err := rpcplugin.NewAdapter(rpcplugin.Config{PluginID: "legacy.scope", PluginVersion: "1.0.0", RequiredGrants: []string{sdk.PermissionManagedNetworkListen}, SupportedFeatures: sdk.RequiredRPCFeatures([]string{sdk.PermissionManagedNetworkListen}), Timeouts: rpcplugin.UniformTimeouts(5 * time.Second)}, rpcplugin.HookFuncs{})
	if err != nil {
		t.Fatal(err)
	}
	if err := sdk.ServeRPCPlugin(context.Background(), adapter); err != nil {
		t.Fatal(err)
	}
}
func TestIntegrationLegacyGuestScopeCompatibilityAndOverrideRefusal(t *testing.T) {
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(value)
	cache := filepath.Join(root, "cache")
	if err := os.WriteFile(cache, value, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	requirement, err := pluginprocess.NewSandboxRequirement(pluginprocess.SandboxRequirementProjection{PackageDigest: digest, Permissions: []pluginprocess.SandboxPermission{pluginprocess.SandboxPermission(sdk.PermissionManagedNetworkListen)}, ResourceBudget: pluginprocess.ManifestResourceBudget{TimeoutMS: 5000, MemoryBytes: 256 << 20, Concurrency: 4, InputBytes: 4096, OutputBytes: 4096, CPUMillis: 1000}})
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(nil, nil, os.Stderr), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	candidate := HostCandidate{InstanceID: "legacy", PluginID: "legacy.scope", PluginVersion: "1.0.0", Generation: "legacy-generation", ProviderGenerationID: "provider", OperationID: "operation", Revision: 1, AgentID: "edge", PackageDigest: digest, Scopes: []string{sdk.PermissionManagedNetworkListen}, RequiredFeatures: sdk.RequiredRPCFeatures([]string{sdk.PermissionManagedNetworkListen}), Grants: []model.PluginGrantProjection{{Name: sdk.PermissionManagedNetworkListen}}, Requirement: requirement, Artifact: pluginprocess.Artifact{CachePath: cache, SHA256: hex.EncodeToString(sum[:]), GOOS: "linux", GOARCH: "amd64"}, Config: []byte(`{}`), Process: pluginprocess.InstanceSpec{Args: []string{"-test.run=^TestIntegrationLegacyExecutionScopeChild$"}, GracePeriod: time.Second}, Dial: DialConfig{Network: "unix", Deadline: 5 * time.Second}, services: &runtimeServices{}}
	running, err := host.Activate(t.Context(), candidate)
	if err != nil {
		t.Fatal("legacy package startup changed", err)
	}
	if running.Status().PID <= 0 {
		t.Fatal("legacy process not running")
	}
	candidate.InstanceID = "override"
	candidate.Generation = "override-generation"
	candidate.RequiredFeatures = sdk.RPCFeaturesWithExecutionScope(sdk.RequiredRPCFeatures(candidate.Scopes))
	candidate.Process.Environment = []string{sdk.EnvPluginExecutionScope + "=" + sdk.HostScopeControlPlane}
	if _, err := host.Activate(t.Context(), candidate); err == nil {
		t.Fatal("user environment overrode Host execution scope")
	}
}
