//go:build linux && integration

package rpc

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/rpcplugin"
)

func TestIntegrationRetiredRestartChild(t *testing.T) {
	if os.Getenv(sdk.EnvPluginEndpoint) == "" {
		return
	}
	client, err := sdk.NewHostRuntimeClientFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	var listener *sdk.ManagedNetworkHandle
	adapter, err := rpcplugin.NewAdapter(rpcplugin.Config{PluginID: "restart.test", PluginVersion: "1.0.0", RequiredGrants: []string{sdk.PermissionManagedNetworkListen}, SupportedFeatures: sdk.RequiredRPCFeatures([]string{sdk.PermissionManagedNetworkListen}), Timeouts: rpcplugin.UniformTimeouts(5 * time.Second)}, rpcplugin.HookFuncs{
		PrepareFunc: func(ctx context.Context, generation *rpcplugin.Generation, config []byte) error {
			var endpoint sdk.ManagedNetworkEndpoint
			if err := json.Unmarshal(config, &endpoint); err != nil {
				return err
			}
			response, err := client.ManagedNetwork(ctx, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkListen, Binding: sdk.ManagedBinding{InstanceID: "instance", EntryID: "instance", Generation: generation.ID()}, RequestID: "listen", Endpoint: &endpoint, Protocol: "tcp", MaxFlows: 16, IdleMS: 30000})
			listener = response.Handle
			return err
		},
		ActivateFunc: func(_ context.Context, generation *rpcplugin.Generation) error {
			go func() {
				for {
					response, err := client.ManagedNetwork(context.Background(), sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkAccept, Binding: listener.Binding, RequestID: "accept", Handle: listener, WaitMS: 30000})
					if err != nil {
						return
					}
					go func(handle sdk.ManagedNetworkHandle) {
						stream, err := sdk.NewManagedTCPStream(context.Background(), client, handle)
						if err != nil {
							return
						}
						defer stream.Close()
						for {
							buffer := make([]byte, 1)
							if _, err := io.ReadFull(stream, buffer); err != nil {
								return
							}
							if _, err := stream.Write([]byte(generation.ID() + "\n")); err != nil {
								return
							}
						}
					}(*response.Handle)
				}
			}()
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

type retainedRestartView struct {
	host      *Host
	instance  *HostedInstance
	view      *module.GenerationView
	destroyed atomic.Bool
	done      chan struct{}
}

func (resource *retainedRestartView) Destroy(ctx context.Context) error {
	if err := resource.host.DestroyCandidate(resource.instance); err != nil {
		return err
	}
	if err := resource.view.Destroy(ctx); err != nil {
		return err
	}
	if resource.destroyed.CompareAndSwap(false, true) && resource.done != nil {
		close(resource.done)
	}
	return nil
}

type unrelatedRestartSession struct{ net.Conn }

func (session unrelatedRestartSession) ForceClose(context.Context, string) error {
	return session.Close()
}
func restartView(t *testing.T, revision int64) *module.GenerationView {
	t.Helper()
	registry := module.NewRegistry()
	identity, err := module.NewGenerationContext(model.Snapshot{}, model.Snapshot{Revision: revision})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := registry.PrepareGeneration(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	view, _ := candidate.Publish()
	return view
}
func assertRestartPeer(t *testing.T, peer net.Conn, want string) {
	t.Helper()
	peer.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := peer.Write([]byte("?")); err != nil {
		t.Fatal(err)
	}
	value, err := bufio.NewReader(peer).ReadString('\n')
	if err != nil || strings.TrimSpace(value) != want {
		t.Fatalf("connection reached %q, want %q: %v", value, want, err)
	}
}

func TestIntegrationRetiredGenerationCannotReclaimManagedListener(t *testing.T) {
	for _, race := range []bool{false, true} {
		name := "crash-after-publication"
		if race {
			name = "completed-restart-races-publication"
		}
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			binary, err := os.ReadFile(executable)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(binary)
			cache := filepath.Join(directory, "cache.bin")
			if err := os.WriteFile(cache, binary, 0o600); err != nil {
				t.Fatal(err)
			}
			digest := strings.Repeat("a", 64)
			requirement, err := pluginprocess.NewSandboxRequirement(pluginprocess.SandboxRequirementProjection{PackageDigest: digest, Permissions: []pluginprocess.SandboxPermission{pluginprocess.SandboxPermission(sdk.PermissionManagedNetworkListen)}, ResourceBudget: pluginprocess.ManifestResourceBudget{TimeoutMS: 5000, MemoryBytes: 256 << 20, Concurrency: 4, InputBytes: 1 << 20, OutputBytes: 1 << 20, CPUMillis: 1000, Restarts: 3}})
			if err != nil {
				t.Fatal(err)
			}
			host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(directory, "runtime")}, pluginprocess.NewSupervisor(nil, nil, os.Stderr), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer host.Close(context.Background())
			ready, released := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(released) }) }
			defer release()
			if race {
				host.beforeRestartActivation = func() { close(ready); <-released }
			}
			reservation, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			endpoint := sdk.ManagedNetworkEndpoint{Host: "127.0.0.1", Port: reservation.Addr().(*net.TCPAddr).Port}
			address := reservation.Addr().String()
			reservation.Close()
			config, _ := json.Marshal(endpoint)
			oldView, newView := restartView(t, 1), restartView(t, 2)
			oldCandidate := HostCandidate{InstanceID: "instance", PluginID: "restart.test", PluginVersion: "1.0.0", PackageDigest: digest, Generation: oldView.ID(), ProviderGenerationID: "provider-old", OperationID: "old", Revision: 1, AgentID: "edge", Artifact: pluginprocess.Artifact{CachePath: cache, SHA256: hex.EncodeToString(sum[:]), GOOS: "linux", GOARCH: "amd64"}, Requirement: requirement, Scopes: []string{sdk.PermissionManagedNetworkListen}, Grants: []model.PluginGrantProjection{{Name: sdk.PermissionManagedNetworkListen}}, Restart: "on-failure", Config: config, Process: pluginprocess.InstanceSpec{Args: []string{"-test.run=^TestIntegrationRetiredRestartChild$"}, GracePeriod: time.Second, InitialBackoff: time.Millisecond, MaximumBackoff: time.Millisecond, RestartLimit: 3}, Dial: DialConfig{Network: "unix", Deadline: 5 * time.Second}, services: &runtimeServices{}}
			old, err := host.Activate(t.Context(), oldCandidate)
			if err != nil {
				t.Fatal(err)
			}
			controller := generation.NewDrainController(nil)
			defer controller.Close(context.Background())
			retained := &retainedRestartView{host: host, instance: old, view: oldView, done: make(chan struct{})}
			if err := controller.Activate(t.Context(), generation.Generation{ID: oldView.ID(), Revision: 1, Resource: retained}, nil, time.Minute); err != nil {
				t.Fatal(err)
			}
			other, otherPeer := net.Pipe()
			defer other.Close()
			defer otherPeer.Close()
			session, err := controller.RegisterSession(oldView.ID(), generation.EntityKey{Module: "http", ID: "unrelated-entry"}, "unrelated-session", unrelatedRestartSession{other})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Finish()
			oldPeer, err := net.Dial("tcp", address)
			if err != nil {
				t.Fatal(err)
			}
			defer oldPeer.Close()
			assertRestartPeer(t, oldPeer, oldView.ID())
			nextCandidate := oldCandidate
			nextCandidate.Generation = newView.ID()
			nextCandidate.ProviderGenerationID = "provider-new"
			nextCandidate.Revision = 2
			nextCandidate.OperationID = "new"
			next, err := host.PrepareCandidate(t.Context(), nextCandidate)
			if err != nil {
				t.Fatal(err)
			}
			if err := host.ReadyCandidate(next); err != nil {
				t.Fatal(err)
			}
			if err := host.ActivatePreparedCandidate(t.Context(), next); err != nil {
				t.Fatal(err)
			}
			oldPID := old.Status().PID
			if race {
				if err := syscall.Kill(oldPID, syscall.SIGKILL); err != nil {
					t.Fatal(err)
				}
				select {
				case <-ready:
				case <-time.After(10 * time.Second):
					t.Fatal("real replacement did not reach activation barrier")
				}
			}
			if err := host.PublishPreparedGeneration(newView.ID(), []*HostedInstance{next}); err != nil {
				t.Fatal(err)
			}
			if err := controller.Activate(t.Context(), generation.Generation{ID: newView.ID(), Revision: 2, Resource: &retainedRestartView{host: host, instance: next, view: newView}}, nil, time.Minute); err != nil {
				t.Fatal(err)
			}
			if retained.destroyed.Load() || controller.Registry().GenerationCount(oldView.ID()) != 1 {
				t.Fatal("unrelated session did not retain actual old view")
			}
			if !race {
				assertRestartPeer(t, oldPeer, oldView.ID())
				if err := syscall.Kill(oldPID, syscall.SIGKILL); err != nil {
					t.Fatal(err)
				}
			}
			peer, err := net.Dial("tcp", address)
			if err != nil {
				t.Fatal(err)
			}
			assertRestartPeer(t, peer, newView.ID())
			peer.Close()
			release()
			select {
			case <-old.done:
			case <-time.After(10 * time.Second):
				t.Fatal("retired process retained a live restart loop")
			}
			if retained.destroyed.Load() {
				t.Fatal("plugin crash destroyed view still held by unrelated session")
			}
			for range 3 {
				peer, err := net.Dial("tcp", address)
				if err != nil {
					t.Fatal(err)
				}
				assertRestartPeer(t, peer, newView.ID())
				peer.Close()
			}
			if !race && old.Status().RestartCount != 0 {
				t.Fatal("retired generation attempted restart")
			}
			session.Finish()
			select {
			case <-retained.done:
			case <-time.After(5 * time.Second):
				t.Fatal("old view did not release after unrelated session ended")
			}
		})
	}
}
