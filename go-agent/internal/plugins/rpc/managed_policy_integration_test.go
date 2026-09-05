//go:build linux && integration

package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/wasm"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/wasm/testfixture"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/rpcplugin"
)

type managedPolicyFactory struct {
	wasm.GenerationFactory
	mu      sync.Mutex
	payload []byte
}

func (f *managedPolicyFactory) PrepareGeneration(ctx context.Context, spec policy.GenerationSpec) (policy.GenerationRuntime, error) {
	runtime, err := f.GenerationFactory.PrepareGeneration(ctx, spec)
	if err != nil {
		return nil, err
	}
	return &managedPolicyRuntime{GenerationRuntime: runtime, factory: f}, nil
}

type managedPolicyRuntime struct {
	policy.GenerationRuntime
	factory *managedPolicyFactory
}

func (r *managedPolicyRuntime) Evaluate(ctx context.Context, request policy.ModuleRequest) (policy.ModuleResponse, error) {
	response, err := r.GenerationRuntime.Evaluate(ctx, request)
	r.factory.mu.Lock()
	r.factory.payload = append([]byte(nil), response.Payload...)
	r.factory.mu.Unlock()
	return response, err
}

type managedPolicyProbe struct {
	*rpcplugin.Adapter
	tcp, udp atomic.Int32
}

func (probe *managedPolicyProbe) Call(context.Context, string, string, []byte) ([]byte, error) {
	return json.Marshal(map[string]int32{"tcp": probe.tcp.Load(), "udp": probe.udp.Load()})
}
func TestIntegrationManagedPolicyChild(t *testing.T) {
	if os.Getenv(sdk.EnvPluginEndpoint) == "" {
		return
	}
	if !sdk.AgentExecutionFace() {
		t.Fatal("Agent opened management face")
	}
	client, err := sdk.NewHostRuntimeClientFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	probe := &managedPolicyProbe{}
	listeners := map[string]*sdk.ManagedNetworkHandle{}
	probe.Adapter, err = rpcplugin.NewAdapter(rpcplugin.Config{PluginID: "managed.policy", PluginVersion: "1.0.0", RequiredGrants: []string{sdk.PermissionManagedNetworkListen}, SupportedFeatures: sdk.RPCFeaturesWithExecutionScope(sdk.RequiredRPCFeatures([]string{sdk.PermissionManagedNetworkListen})), Timeouts: rpcplugin.UniformTimeouts(5 * time.Second)}, rpcplugin.HookFuncs{
		PrepareFunc: func(ctx context.Context, generation *rpcplugin.Generation, config []byte) error {
			var endpoints map[string]sdk.ManagedNetworkEndpoint
			if err := json.Unmarshal(config, &endpoints); err != nil {
				return err
			}
			for _, protocol := range []string{"tcp", "udp"} {
				endpoint := endpoints[protocol]
				response, err := client.ManagedNetwork(ctx, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkListen, Binding: sdk.ManagedBinding{InstanceID: "managed", Generation: generation.ID(), EntryID: "managed"}, RequestID: "listen-" + protocol, Endpoint: &endpoint, Protocol: protocol, MaxFlows: 8, IdleMS: 30000})
				if err != nil {
					return err
				}
				listeners[protocol] = response.Handle
			}
			return nil
		},
		ActivateFunc: func(context.Context, *rpcplugin.Generation) error {
			for _, protocol := range []string{"tcp", "udp"} {
				go func(protocol string) {
					listener := listeners[protocol]
					response, err := client.ManagedNetwork(context.Background(), sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkAccept, Binding: listener.Binding, RequestID: "accept-" + protocol, Handle: listener, WaitMS: 30000})
					if err != nil {
						return
					}
					if protocol == "tcp" {
						probe.tcp.Add(1)
						stream, err := sdk.NewManagedTCPStream(context.Background(), client, *response.Handle)
						if err == nil {
							defer stream.Close()
							io.Copy(stream, stream)
						}
					} else {
						packet, err := client.ManagedNetwork(context.Background(), sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkReceive, Binding: listener.Binding, RequestID: "receive", Handle: response.Handle, MaxBytes: sdk.ManagedNetworkMaxDatagramBytes, WaitMS: 1000})
						if err != nil {
							return
						}
						probe.udp.Add(1)
						client.ManagedNetwork(context.Background(), sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkSend, Binding: listener.Binding, RequestID: "send", Handle: response.Handle, Data: packet.Data, WaitMS: 1000})
					}
				}(protocol)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sdk.ServeRPCPlugin(context.Background(), probe); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationManagedProtocolPoliciesUseRealEntryAndPreDeliveryAdmission(t *testing.T) {
	runtime, err := wasm.NewRuntime(t.Context(), wasm.RuntimeOptions{MaxMemoryPages: 16})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	snapshot, err := testfixture.Snapshot(t.Context(), t.TempDir(), 1, "127.0.0.0/8", []testfixture.Stage{{Kind: model.PolicyKindIP, Mode: sdk.PolicyModeObserve, Action: sdk.PolicyActionDeny}})
	if err != nil {
		t.Fatal(err)
	}
	base := snapshot.PluginPolicies[0].Stages[0].PolicySettings
	base.Version.InstanceVersion = 2
	entry := *model.ClonePolicySettings(base)
	enforce := sdk.PolicyModeEnforce
	entry.Settings.EntryMode = &enforce
	tcpRef := &model.PolicyRef{ID: "effective", StageModes: []model.PolicyModeBinding{{Stage: sdk.PolicyStageIdentity{Kind: "ip", PolicyID: "fixture-ip"}, Snapshot: entry}}}
	udpRef := &model.PolicyRef{ID: "effective"}
	snapshot.PluginGenerations = []model.PluginGeneration{{ID: "managed-provider", InstanceID: "managed", ManagedNetworkPolicies: map[string]*model.PolicyRef{"tcp": tcpRef, "udp": udpRef}}}
	factory := &managedPolicyFactory{GenerationFactory: wasm.GenerationFactory{Runtime: runtime}}
	registry := module.NewRegistry()
	if err := registry.Register(policy.NewModule(factory, nil)); err != nil {
		t.Fatal(err)
	}
	identity, err := module.NewGenerationContext(model.Snapshot{}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := registry.PrepareGeneration(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepared.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	view, _ := prepared.Publish()
	defer view.Destroy(context.Background())
	value, _ := view.Resolve(policy.ProviderEvaluator)
	tcpReserve, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcpEndpoint := sdk.ManagedNetworkEndpoint{Host: "127.0.0.1", Port: tcpReserve.Addr().(*net.TCPAddr).Port}
	tcpReserve.Close()
	udpReserve, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udpEndpoint := sdk.ManagedNetworkEndpoint{Host: "127.0.0.1", Port: udpReserve.LocalAddr().(*net.UDPAddr).Port}
	udpReserve.Close()
	config, _ := json.Marshal(map[string]sdk.ManagedNetworkEndpoint{"tcp": tcpEndpoint, "udp": udpEndpoint})
	root := t.TempDir()
	executable, _ := os.Executable()
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(binary)
	cache := filepath.Join(root, "cache")
	if err := os.WriteFile(cache, binary, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	requirement, err := pluginprocess.NewSandboxRequirement(pluginprocess.SandboxRequirementProjection{PackageDigest: digest, Permissions: []pluginprocess.SandboxPermission{pluginprocess.SandboxPermission(sdk.PermissionManagedNetworkListen)}, ResourceBudget: pluginprocess.ManifestResourceBudget{TimeoutMS: 5000, MemoryBytes: 256 << 20, Concurrency: 4, InputBytes: 1 << 20, OutputBytes: 1 << 20, CPUMillis: 1000}})
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(nil, nil, os.Stderr), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	candidate := HostCandidate{InstanceID: "managed", PluginID: "managed.policy", PluginVersion: "1.0.0", Generation: view.ID(), ProviderGenerationID: "provider", OperationID: "operation", Revision: 1, AgentID: "edge", PackageDigest: digest, Artifact: pluginprocess.Artifact{CachePath: cache, SHA256: hex.EncodeToString(sum[:]), GOOS: "linux", GOARCH: "amd64"}, Requirement: requirement, Scopes: []string{sdk.PermissionManagedNetworkListen}, Grants: []model.PluginGrantProjection{{Name: sdk.PermissionManagedNetworkListen}}, Config: config, Process: pluginprocess.InstanceSpec{Args: []string{"-test.run=^TestIntegrationManagedPolicyChild$"}, GracePeriod: time.Second}, Dial: DialConfig{Network: "unix", Deadline: 5 * time.Second}, services: &runtimeServices{evaluator: value.(policy.Evaluator), policies: map[string]*model.PolicyRef{"tcp": tcpRef, "udp": udpRef}}}
	if _, err := host.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	tcp, err := net.Dial("tcp", net.JoinHostPort(tcpEndpoint.Host, strconv.Itoa(tcpEndpoint.Port)))
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	tcp.SetDeadline(time.Now().Add(time.Second))
	tcp.Write([]byte("must-not-reach-plugin"))
	if _, err := tcp.Read(make([]byte, 1)); err == nil {
		t.Fatal("enforced TCP source reached plugin")
	}
	udp, err := net.Dial("udp", net.JoinHostPort(udpEndpoint.Host, strconv.Itoa(udpEndpoint.Port)))
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	udp.SetDeadline(time.Now().Add(2 * time.Second))
	udp.Write([]byte("observed"))
	buffer := make([]byte, 32)
	n, err := udp.Read(buffer)
	if err != nil || string(buffer[:n]) != "observed" {
		t.Fatal("TCP override leaked into observed UDP entry", err)
	}
	factory.mu.Lock()
	payload := append([]byte(nil), factory.payload...)
	factory.mu.Unlock()
	status, frame, err := testfixture.Slot(payload, 2)
	source, decodeErr := sdk.UnmarshalPolicyTrustedSourceResponse(frame, 4096)
	if err != nil || decodeErr != nil || status != sdk.PolicyStatusOK || source.Source == nil || source.Source.ValidateFor("fixture-ip", view.ID(), "managed") != nil || source.Source.SourceAddress.String() != "127.0.0.1" || source.Source.Authority != sdk.PolicySourceSocket {
		t.Fatal("managed source import missing real entry authority", err, decodeErr)
	}
	raw, err := host.Call(t.Context(), candidate.PluginID, "status", nil)
	if err != nil {
		t.Fatal(err)
	}
	var calls map[string]int
	if err := json.Unmarshal(raw, &calls); err != nil {
		t.Fatal(err)
	}
	if calls["tcp"] != 0 || calls["udp"] != 1 {
		t.Fatal("pre-delivery policy failed", calls)
	}
}
