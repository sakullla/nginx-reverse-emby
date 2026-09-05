//go:build linux && integration

package rpc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/rpcplugin"
)

const scopedErrorCanary = "scoped-lifecycle-error-canary-79c3"

func TestIntegrationScopedErrorChild(t *testing.T) {
	if os.Getenv(sdk.EnvPluginEndpoint) == "" {
		return
	}
	client, err := sdk.NewHostRuntimeClientFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	var phase, retained string
	adapter, err := rpcplugin.NewAdapter(rpcplugin.Config{PluginID: "scoped.errors", PluginVersion: "1.0.0", RequiredGrants: []string{sdk.PermissionScopedSecretRead}, SupportedFeatures: sdk.RequiredRPCFeatures([]string{sdk.PermissionScopedSecretRead}), Timeouts: rpcplugin.UniformTimeouts(5 * time.Second)}, rpcplugin.HookFuncs{
		PrepareFunc: func(ctx context.Context, generation *rpcplugin.Generation, config []byte) error {
			var settings struct{ Phase string }
			if err := json.Unmarshal(config, &settings); err != nil {
				return err
			}
			phase = settings.Phase
			request := sdk.ScopedSecretRequest{Action: sdk.ScopedSecretRead, Binding: sdk.ManagedBinding{InstanceID: "instance", Generation: generation.ID(), EntryID: "instance"}, Reference: sdk.ScopedSecretReference{InstanceID: "instance", ID: "credential", Scope: "relay", Version: strings.Repeat("a", 32)}}
			response, err := client.ScopedSecret(ctx, request)
			if err != nil {
				return err
			}
			defer response.Material.Close()
			if err := response.Material.WithBytes(func(value []byte) error { retained = string(value); return nil }); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "guest stderr material:", retained)
			if phase == "prepare" {
				return errors.New("Prepare includes " + retained)
			}
			return nil
		}, ActivateFunc: func(context.Context, *rpcplugin.Generation) error {
			if phase == "activate" {
				return errors.New("Activate includes " + retained)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sdk.ServeRPCPlugin(context.Background(), adapter); err != nil {
		t.Fatal(err)
	}
}

type scopedErrorRunner struct{}

func (scopedErrorRunner) Start(ctx context.Context, spec pluginprocess.InstanceSpec, sandbox pluginprocess.Sandbox, output io.Writer) (pluginprocess.ManagedProcess, func() error, error) {
	spec.Args = []string{"-test.run=^TestIntegrationScopedErrorChild$"}
	return (pluginprocess.ExecRunner{}).Start(ctx, spec, sandbox, output)
}
func (scopedErrorRunner) StartWithStreams(ctx context.Context, spec pluginprocess.InstanceSpec, sandbox pluginprocess.Sandbox, stdout, stderr io.Writer) (pluginprocess.ManagedProcess, func() error, error) {
	spec.Args = []string{"-test.run=^TestIntegrationScopedErrorChild$"}
	return (pluginprocess.ExecRunner{}).StartWithStreams(ctx, spec, sandbox, stdout, stderr)
}

type lockedErrorLog struct {
	sync.Mutex
	value bytes.Buffer
}

func (buffer *lockedErrorLog) Write(value []byte) (int, error) {
	buffer.Lock()
	defer buffer.Unlock()
	return buffer.value.Write(value)
}
func (buffer *lockedErrorLog) String() string {
	buffer.Lock()
	defer buffer.Unlock()
	return buffer.value.String()
}

func TestIntegrationScopedLifecycleErrorsNeverReachHostDiagnostics(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(binary)
	for index, phase := range []string{"prepare", "activate"} {
		t.Run(phase, func(t *testing.T) {
			directory := t.TempDir()
			cache := filepath.Join(directory, "cache.bin")
			if err := os.WriteFile(cache, binary, 0o600); err != nil {
				t.Fatal(err)
			}
			configuration, _ := json.Marshal(map[string]string{"Phase": phase})
			generation := model.PluginGeneration{ID: "provider-" + phase, InstanceID: "instance", OperationID: "operation-" + phase, Revision: int64(index + 1), PluginID: "scoped.errors", PluginVersion: "1.0.0", PackageDigest: strings.Repeat("a", 64),
				Runtime:         model.PluginRuntimeDescriptor{Kind: model.PluginRuntimeRPCService, ABI: model.PluginRPCABIV1, HostScope: "agent", Entry: "artifacts/plugin"},
				Artifact:        model.PluginArtifactDescriptor{ArtifactID: "artifact", PackageIdentity: "scoped.errors@1.0.0", RelativePath: "artifacts/plugin", SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(binary)), Mode: "executable", GOOS: "linux", GOARCH: "amd64", SignatureVerified: true, SignerKeyID: "key", SignerFingerprint: strings.Repeat("b", 64), LocalPath: cache},
				ExtensionPoints: []string{"l4.accept"}, RequiredFeatures: sdk.RequiredRPCFeatures([]string{sdk.PermissionScopedSecretRead}), ConfigVersion: 1, Config: configuration, Grants: []model.PluginGrantProjection{{Name: sdk.PermissionScopedSecretRead, ResourceKind: "secret-scope", ResourceID: "relay"}},
				ResourceBudget: model.PluginResourceBudget{TimeoutMS: 5000, MemoryBytes: 256 << 20, Concurrency: 4, InputBytes: 1 << 20, OutputBytes: 1 << 20, CPUMillis: 1000}, Target: model.PluginTargetBinding{Kind: "agent", ID: "edge", ResourceGroupID: "default", Version: 1}, FailurePolicy: model.PluginFailurePolicy{OnError: "degraded", OnBudget: "fail-closed", Restart: "never", CoreFallback: "preserve"}}
			snapshot := model.Snapshot{Revision: generation.Revision, PluginGenerations: []model.PluginGeneration{generation}}
			identity, err := module.NewGenerationContext(model.Snapshot{}, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			var reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request model.PluginSecretRedemptionRequest
				if r.Header.Get("X-Agent-Token") != "agent-token" || json.NewDecoder(r.Body).Decode(&request) != nil || request.Validate() != nil || request.RuntimeGenerationID != identity.ID() || request.GenerationID != generation.ID {
					http.Error(w, "denied", 403)
					return
				}
				scoped, err := sdk.DecodeScopedSecretRequest(request.Scoped)
				if err != nil {
					http.Error(w, "invalid", 400)
					return
				}
				defer scoped.Material.Close()
				material, _ := sdk.NewManagedSecretMaterial([]byte(scopedErrorCanary))
				defer material.Close()
				payload, err := sdk.EncodeScopedSecretResponse(scoped, sdk.ScopedSecretResponse{Reference: scoped.Reference, Material: material})
				if err != nil {
					http.Error(w, "invalid", 400)
					return
				}
				defer clear(payload)
				reads.Add(1)
				json.NewEncoder(w).Encode(model.PluginSecretRedemptionResponse{Scoped: payload})
			}))
			defer server.Close()
			output := &lockedErrorLog{}
			previousWriter := log.Writer()
			log.SetOutput(output)
			defer log.SetOutput(previousWriter)
			host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(directory, "runtime")}, pluginprocess.NewSupervisor(scopedErrorRunner{}, nil, output), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer host.Close(context.Background())
			host.SetSecretRedeemer(control.NewSyncClient(control.SyncClientConfig{MasterURL: server.URL, AgentToken: "agent-token"}, server.Client()))
			owner := NewGenerationModule(host)
			prepared, err := owner.Prepare(t.Context(), module.ApplyRequest{Next: snapshot, Generation: identity})
			if err != nil {
				t.Fatal(err)
			}
			transaction := prepared.(*generationTransaction)
			if err := transaction.Ready(t.Context()); err != nil {
				t.Fatal(err)
			}

			publicationErr := transaction.PrepareGenerationPublication(t.Context())
			reportStatus, errorMessage := "applied", ""
			if phase == "prepare" {
				if publicationErr != nil {
					t.Fatal("optional failed Prepare could not publish remaining generation")
				}
				transaction.FinalizeGenerationPublication()
			} else {
				// SDK Activate failure has already terminated its lifecycle; Stop then rejects
				// the transition. The Host must report this cleanup failure, not publish it.
				if publicationErr == nil {
					t.Fatal("failed activation cleanup incorrectly published")
				}
				if strings.Contains(publicationErr.Error(), scopedErrorCanary) {
					t.Fatal("activation cleanup error retained material")
				}
				reportStatus, errorMessage = "failed", publicationErr.Error()
				if _, active := host.Active(generation.InstanceID); active {
					t.Fatal("failed activation became active")
				}
			}
			statuses := transaction.PluginRuntimeStatuses()
			if len(statuses) != 1 {
				t.Fatalf("expected one status, got %d", len(statuses))
			}
			if phase == "prepare" && statuses[0].ErrorCode != "rpc_prepare_failed" {
				t.Fatal("optional failed Prepare lost error classification")
			}
			report, err := json.Marshal(model.RevisionReport{Revision: generation.Revision, GenerationID: "attempt", Status: reportStatus, ErrorMessage: errorMessage, PluginStatuses: statuses})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(output.String(), scopedErrorCanary) || bytes.Contains(report, []byte(scopedErrorCanary)) {
				t.Fatal("scoped material escaped to Host log/status/report")
			}
			if !strings.Contains(output.String(), "[REDACTED]") {
				t.Fatal("actual guest stderr redaction was not exercised")
			}
			if phase == "prepare" && !strings.Contains(output.String(), "optional candidate excluded") {
				t.Fatal("actual optional candidate Host log was not exercised")
			}
			if destroyErr := transaction.Destroy(t.Context()); destroyErr != nil && strings.Contains(destroyErr.Error(), scopedErrorCanary) {
				t.Fatal("cleanup error exposed material")
			}

			candidate, err := hostCandidateFromGeneration(generation, identity.ID())
			if err != nil {
				t.Fatal(err)
			}
			candidate.services = &runtimeServices{}
			_, returned := host.Activate(t.Context(), candidate)
			if returned == nil || strings.Contains(returned.Error(), scopedErrorCanary) {
				t.Fatal("direct lifecycle error leaked material or unexpectedly succeeded")
			}
			if reads.Load() != 2 {
				t.Fatalf("actual scoped reads=%d, want two real failed attempts", reads.Load())
			}
		})
	}
}
