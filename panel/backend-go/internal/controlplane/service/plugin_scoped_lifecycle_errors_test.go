//go:build linux && !fast && !integration

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/rpcplugin"
)

type scopedLifecycleErrorGuest struct {
	*rpcplugin.Adapter
	client    *sdk.HostRuntimeClient
	phase     string
	reference sdk.ScopedSecretReference
	value     string
}

func (g *scopedLifecycleErrorGuest) Handshake(ctx context.Context, request sdk.RPCHandshakeRequest) (sdk.RPCHandshakeResponse, error) {
	response, err := g.Adapter.Handshake(ctx, request)
	if err != nil {
		return response, err
	}
	delivery, err := g.client.ScopedSecret(ctx, sdk.ScopedSecretRequest{Action: sdk.ScopedSecretRead, Binding: sdk.ManagedBinding{InstanceID: g.reference.InstanceID, EntryID: g.reference.InstanceID, Generation: request.Generation}, Reference: g.reference})
	if err != nil {
		return sdk.RPCHandshakeResponse{}, err
	}
	defer delivery.Material.Close()
	if err := delivery.Material.WithBytes(func(value []byte) error { g.value = string(value); return nil }); err != nil {
		return sdk.RPCHandshakeResponse{}, err
	}
	if g.phase == "handshake" {
		return sdk.RPCHandshakeResponse{}, errors.New(g.value)
	}
	return response, nil
}

func TestControlPlaneScopedLifecycleErrorChild(t *testing.T) {
	phase := os.Getenv("NRE_SCOPED_FAILURE_PHASE")
	if phase == "" {
		return
	}
	client, err := sdk.NewHostRuntimeClientFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	guest := &scopedLifecycleErrorGuest{client: client, phase: phase}
	if json.Unmarshal([]byte(os.Getenv("NRE_SCOPED_FAILURE_REFERENCE")), &guest.reference) != nil {
		t.Fatal("invalid fixture reference")
	}
	adapter, err := rpcplugin.NewAdapter(rpcplugin.Config{PluginID: "plugin-a", PluginVersion: "1.0.0", RequiredGrants: []string{sdk.PermissionScopedSecretRead}, SupportedFeatures: sdk.RequiredRPCFeatures([]string{sdk.PermissionScopedSecretRead}), Timeouts: rpcplugin.UniformTimeouts(3 * time.Second)}, rpcplugin.HookFuncs{
		PrepareFunc: func(context.Context, *rpcplugin.Generation, []byte) error {
			if phase == "prepare" {
				return errors.New(guest.value)
			}
			return nil
		},
		ActivateFunc: func(context.Context, *rpcplugin.Generation) error {
			if phase == "activate" {
				return errors.New(guest.value)
			}
			return nil
		},
		StopFunc: func(context.Context, *rpcplugin.Generation) error {
			if phase == "stop" || phase == "activate" {
				return errors.New(guest.value)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	guest.Adapter = adapter
	if err := sdk.ServeRPCPlugin(t.Context(), guest); err != nil {
		t.Fatal(err)
	}
}

func TestControlPlaneScopedLifecycleFailureDoesNotPersistMaterial(t *testing.T) {
	for _, phase := range []string{"handshake", "prepare", "activate", "stop"} {
		t.Run(phase, func(t *testing.T) {
			manager, store, candidate, create := scopedSecretFixture(t)
			created, failure := callScopedSecret(t, manager, candidate, create)
			if failure != nil {
				t.Fatal(failure)
			}
			root := t.TempDir()
			processHost, err := pluginhost.New(filepath.Join(root, "runtime"), nil, pluginhost.GRPCDialer{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			processHost.SetHostResourceDispatcher(manager)
			runtimeHost, err := NewPluginRuntimeHost(processHost, store)
			if err != nil {
				t.Fatal(err)
			}
			defer runtimeHost.Close(context.Background())
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			binary, err := os.ReadFile(executable)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(binary)
			digest := hex.EncodeToString(sum[:])
			cache := filepath.Join(root, "cache.bin")
			if err := os.WriteFile(cache, binary, 0o600); err != nil {
				t.Fatal(err)
			}
			packageDigest := strings.Repeat("a", 64)
			requirement, err := pluginhost.SandboxRequirementFromValidatedPackage(plugins.ValidatedPackage{Digest: packageDigest, Manifest: plugins.Manifest{
				Runtime: plugins.Runtime{Kind: sdk.RuntimeRPCService, ABI: sdk.RPCABIV1, HostScope: "control-plane", Entry: "plugin"}, Permissions: []plugins.Permission{{Name: sdk.PermissionScopedSecretRead}}, ExtensionPoints: []string{"http.request"}, ResourceBudget: plugins.ResourceBudget{TimeoutMS: 3000, MemoryBytes: 256 << 20, Concurrency: 2, InputBytes: 1 << 20, OutputBytes: 1 << 20, CPUMillis: 1000, Restarts: 0},
			}})
			if err != nil {
				t.Fatal(err)
			}
			reference, _ := json.Marshal(created.Reference)
			candidate.Identity.Version = "1.0.0"
			candidate.Identity.PackageDigest = packageDigest
			candidate.OperationID = "lifecycle-" + phase
			candidate.Revision = 1
			candidate.Artifact = pluginhost.Artifact{CachePath: cache, SHA256: digest, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
			candidate.Requirement = requirement
			candidate.Grants = []string{sdk.PermissionScopedSecretRead}
			candidate.Config = []byte(`{}`)
			candidate.Args = []string{"-test.run=^TestControlPlaneScopedLifecycleErrorChild$"}
			candidate.Environment = []string{"NRE_SCOPED_FAILURE_PHASE=" + phase, "NRE_SCOPED_FAILURE_REFERENCE=" + string(reference)}
			candidate.Endpoint = pluginhost.Endpoint{Network: "unix"}
			candidate.Deadline = 3 * time.Second
			candidate.GracePeriod = time.Second
			candidate.Declaration = pluginhost.Declaration{PluginID: candidate.Identity.PluginID, ExtensionPoints: []string{"http.request"}}
			_, err = runtimeHost.Activate(t.Context(), candidate)
			if phase == "stop" {
				if err != nil {
					t.Fatal("healthy pre-stop activation", err)
				}
				err = runtimeHost.Stop(t.Context(), candidate.InstanceID)
			}
			if err == nil {
				t.Fatal("material-bearing lifecycle failure was accepted")
			}
			if strings.Contains(err.Error(), "sensitive-test-material") {
				t.Fatal("returned lifecycle error contains material")
			}
			if !strings.Contains(err.Error(), phase) {
				t.Fatal("safe failure lost lifecycle stage", err)
			}
			row, found, lookupErr := store.GetPluginRuntime(t.Context(), candidate.InstanceID)
			if lookupErr != nil || !found {
				t.Fatal(lookupErr)
			}
			if strings.Contains(row.LastError+row.CandidateLastError, "sensitive-test-material") {
				t.Fatal("persisted runtime failure contains material")
			}
			if phase != "stop" && row.CandidateLastError == "" {
				t.Fatal("candidate failure was not persisted")
			}
			var deliveries []storage.PluginScopedSecretDeliveryRow
			name := pluginScopedSecretName(candidate, created.Reference)
			deliveries, lookupErr = store.PendingScopedSecretDeliveries(t.Context(), name, created.Reference.Version)
			if lookupErr != nil || len(deliveries) != 1 {
				t.Fatal("test did not perform real public scoped read before failing", lookupErr)
			}
		})
	}
}
