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

func TestIntegrationOptedExecutionScopeChild(t *testing.T) {
	if os.Getenv(sdk.EnvPluginEndpoint) == "" {
		return
	}
	if os.Getenv(sdk.EnvPluginExecutionScope) != sdk.HostScopeAgent {
		t.Fatalf("signed opt-in scope = %q, want agent", os.Getenv(sdk.EnvPluginExecutionScope))
	}
	if strings.TrimSpace(os.Getenv(sdk.EnvPluginHostEndpoint)) == "" {
		t.Fatal("opted managed package did not receive its HostRuntime endpoint")
	}
	features := sdk.RPCFeaturesWithExecutionScope(sdk.RequiredRPCFeatures([]string{sdk.PermissionManagedNetworkListen}))
	adapter, err := rpcplugin.NewAdapter(rpcplugin.Config{PluginID: "opted.scope", PluginVersion: "1.0.0", RequiredGrants: []string{sdk.PermissionManagedNetworkListen}, SupportedFeatures: features, Timeouts: rpcplugin.UniformTimeouts(5 * time.Second)}, rpcplugin.HookFuncs{})
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
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(nil, nil, os.Stderr), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	fromGeneration := func(instanceID, pluginID, generationID string, requiredFeatures []string, child string) HostCandidate {
		t.Helper()
		generation := model.PluginGeneration{
			ID: generationID, InstanceID: instanceID, OperationID: "operation", Revision: 1,
			PluginID: pluginID, PluginVersion: "1.0.0", PackageDigest: digest,
			Runtime:          model.PluginRuntimeDescriptor{Kind: model.PluginRuntimeRPCService, ABI: model.PluginRPCABIV1, HostScope: sdk.HostScopeAgent, Entry: "plugin"},
			Artifact:         model.PluginArtifactDescriptor{ArtifactID: "artifact", PackageIdentity: digest, RelativePath: "plugin", SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(value)), Mode: "executable", GOOS: "linux", GOARCH: "amd64", LocalPath: cache, SignatureVerified: true, SignerKeyID: "fixture", SignerFingerprint: strings.Repeat("b", 64)},
			RequiredFeatures: append([]string(nil), requiredFeatures...), ConfigVersion: 1, Config: []byte(`{}`),
			Grants:         []model.PluginGrantProjection{{Name: sdk.PermissionManagedNetworkListen}},
			ResourceBudget: model.PluginResourceBudget{TimeoutMS: 5000, MemoryBytes: 256 << 20, Concurrency: 4, InputBytes: 4096, OutputBytes: 4096, CPUMillis: 1000},
			Target:         model.PluginTargetBinding{Kind: "agent", ID: "edge", ResourceGroupID: "default", Version: 1},
			FailurePolicy:  model.PluginFailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "never", CoreFallback: "preserve"},
		}
		candidate, err := hostCandidateFromGeneration(generation, generationID+"-runtime")
		if err != nil {
			t.Fatal(err)
		}
		candidate.Process.Args = []string{"-test.run=^" + child + "$"}
		candidate.Process.GracePeriod = time.Second
		candidate.Dial = DialConfig{Network: "unix", Deadline: 5 * time.Second}
		candidate.services = &runtimeServices{}
		return candidate
	}
	candidate := fromGeneration("legacy", "legacy.scope", "legacy-provider", sdk.RequiredRPCFeatures([]string{sdk.PermissionManagedNetworkListen}), "TestIntegrationLegacyExecutionScopeChild")
	running, err := host.Activate(t.Context(), candidate)
	if err != nil {
		t.Fatal("legacy package startup changed", err)
	}
	if running.Status().PID <= 0 {
		t.Fatal("legacy process not running")
	}
	optedFeatures, err := sdk.RequiredRPCFeaturesForExecutionScope([]string{sdk.PermissionManagedNetworkListen}, nil, sdk.HostScopeAgent)
	if err != nil {
		t.Fatal(err)
	}
	opted := fromGeneration("opted", "opted.scope", "opted-provider", optedFeatures, "TestIntegrationOptedExecutionScopeChild")
	optedRunning, err := host.Activate(t.Context(), opted)
	if err != nil {
		t.Fatal("signed opt-in package startup", err)
	}
	if optedRunning.Status().PID <= 0 {
		t.Fatal("opted process not running")
	}
	opted.InstanceID = "override"
	opted.Generation = "override-generation"
	opted.Process.Environment = []string{sdk.EnvPluginExecutionScope + "=" + sdk.HostScopeControlPlane}
	if _, err := host.Activate(t.Context(), opted); err == nil {
		t.Fatal("user environment overrode Host execution scope")
	}
}
