//go:build linux && !fast && !integration

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/rpcplugin"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestPolicyConsumptionProcessChild(t *testing.T) {
	mode := os.Getenv("NRE_CONSUMPTION_TEST_MODE")
	if mode == "" {
		return
	}
	scopes := []string{}
	features := sdk.RequiredRPCFeatures(nil)
	if mode == "typed" {
		scopes = []string{"dataset.bind", "policy.control", "storage.write"}
		var err error
		features, err = sdk.RequiredRPCFeaturesForExecutionScope(scopes, nil, sdk.HostScopeControlPlane)
		if err != nil {
			t.Fatal(err)
		}
	}
	adapter, err := rpcplugin.NewAdapter(rpcplugin.Config{PluginID: "ip-policy", PluginVersion: "1.0.0", RequiredGrants: scopes, SupportedFeatures: features, Timeouts: rpcplugin.UniformTimeouts(3 * time.Second)}, rpcplugin.HookFuncs{PrepareFunc: func(ctx context.Context, _ *rpcplugin.Generation, _ []byte) error {
		scope, present := os.LookupEnv(sdk.EnvPluginExecutionScope)
		if sdk.AgentExecutionFace() {
			return errors.New("control-plane process selected Agent face")
		}
		if mode == "legacy" {
			if present {
				return errors.New("legacy guest was forced to new scope contract")
			}
			return nil
		}
		if !present || scope != sdk.HostScopeControlPlane {
			return errors.New("Host scope missing")
		}
		client, err := sdk.NewHostRuntimeClientFromEnvironment()
		if err != nil {
			return err
		}
		response, err := client.ControlPolicy(ctx, sdk.PolicyControlRequest{Action: sdk.PolicyControlInspect, InstanceID: "ip-default", Stage: sdk.PolicyStageIdentity{Kind: "ip", PolicyID: "ip-default"}})
		if err != nil {
			return err
		}
		if response.Desired.Settings.DefaultMode == nil || *response.Desired.Settings.DefaultMode != sdk.PolicyModeObserve {
			return errors.New("real dispatcher returned incorrect defaults")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := sdk.ServeRPCPlugin(t.Context(), adapter); err != nil {
		t.Fatal(err)
	}
}
func TestPolicyConsumptionRealControlPlaneScopeAndDispatcher(t *testing.T) {
	for _, mode := range []string{"typed", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			manager, candidate := newPolicyConsumptionFixture(t)
			root := t.TempDir()
			host, err := pluginhost.New(filepath.Join(root, "runtime"), nil, pluginhost.GRPCDialer{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			host.SetHostResourceDispatcher(manager)
			defer host.Close(context.Background())
			executable, _ := os.Executable()
			binary, err := os.ReadFile(executable)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(binary)
			cache := filepath.Join(root, "binary")
			if err := os.WriteFile(cache, binary, 0600); err != nil {
				t.Fatal(err)
			}
			if mode == "legacy" {
				candidate.Grants = nil
				candidate.Identity.Scopes = nil
			}
			permissions := []plugins.Permission{}
			for _, scope := range candidate.Grants {
				permissions = append(permissions, plugins.Permission{Name: scope})
			}
			requirement, err := pluginhost.SandboxRequirementFromValidatedPackage(plugins.ValidatedPackage{Digest: candidate.Identity.PackageDigest, Manifest: plugins.Manifest{Runtime: plugins.Runtime{Kind: sdk.RuntimeRPCService, ABI: sdk.RPCABIV1, HostScope: sdk.HostScopeControlPlane, Entry: "plugin"}, Permissions: permissions, ResourceBudget: plugins.ResourceBudget{TimeoutMS: 3000, MemoryBytes: 256 << 20, Concurrency: 2, InputBytes: 1 << 20, OutputBytes: 1 << 20, CPUMillis: 1000, Restarts: 0}}})
			if err != nil {
				t.Fatal(err)
			}
			candidate.Identity.Version = "1.0.0"
			candidate.Artifact = pluginhost.Artifact{CachePath: cache, SHA256: hex.EncodeToString(sum[:]), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
			candidate.Requirement = requirement
			candidate.Config = []byte(`{}`)
			candidate.Args = []string{"-test.run=^TestPolicyConsumptionProcessChild$"}
			candidate.Environment = []string{"NRE_CONSUMPTION_TEST_MODE=" + mode}
			candidate.Endpoint = pluginhost.Endpoint{Network: "unix"}
			candidate.Deadline = 5 * time.Second
			candidate.GracePeriod = time.Second
			candidate.Declaration = pluginhost.Declaration{PluginID: candidate.Identity.PluginID}
			if _, err := host.Activate(t.Context(), candidate); err != nil {
				t.Fatal("real sandbox Prepare and public dispatcher", err)
			}
			candidate.Identity.Generation = "spoofed-generation"
			candidate.Environment = append(candidate.Environment, sdk.EnvPluginExecutionScope+"="+sdk.HostScopeAgent)
			if _, err := host.Activate(t.Context(), candidate); err == nil {
				t.Fatal("guest configuration overrode Host scope")
			}
		})
	}
}
