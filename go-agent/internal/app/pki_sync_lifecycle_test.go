//go:build !integration

package app

import (
	"context"
	"io"
	"net"
	"reflect"
	stdruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	agentmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	modulerelay "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

func TestRunRestoresPKIMTLSRelayAfterRestartCredentialBinding(t *testing.T) {
	fixture := newLifecycleRelayMTLSFixture(t)
	listenerPort := lifecyclePickFreeTCPPort(t)
	listener := fixture.listener(listenerPort)

	registry := agentmodule.NewRegistry()
	if err := registry.Register(appProviderModule{
		name: "relay-mtls-material", provides: agentmodule.ProviderTLSMaterial, provider: fixture.provider,
	}); err != nil {
		t.Fatal(err)
	}
	relayModule := modulerelay.NewModule(modulerelay.Config{AgentID: fixture.agentID, AgentName: fixture.agentID})
	if err := registry.Register(relayModule); err != nil {
		t.Fatal(err)
	}
	modulerelay.SetProcessTunnelCredentialProvider(nil)
	t.Cleanup(func() { modulerelay.SetProcessTunnelCredentialProvider(nil) })

	applied := Snapshot{
		DesiredVersion: "1.0.0", Revision: 7,
		Rules:               []model.HTTPRule{},
		L4Rules:             []model.L4Rule{},
		RelayListeners:      []model.RelayListener{listener},
		EgressProfiles:      []model.EgressProfile{},
		Certificates:        []model.ManagedCertificateBundle{},
		CertificatePolicies: []model.ManagedCertificatePolicy{},
	}
	store := core.NewInMemory()
	if err := store.SaveAppliedSnapshot(applied); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDesiredSnapshot(applied); err != nil {
		t.Fatal(err)
	}
	application := &App{
		cfg: Config{
			AgentID: fixture.agentID, AgentName: fixture.agentID,
			CurrentVersion: "1.0.0", HeartbeatInterval: time.Hour,
		},
		syncClient: syncClientFunc(func(_ context.Context, request SyncRequest) (Snapshot, error) {
			return Snapshot{Revision: int64(request.CurrentRevision)}, nil
		}),
		store: store, runtime: core.NewRuntimeWithActivator(appSnapshotActivator(registry)),
		moduleRegistry: registry, relayTunnelCredentials: fixture.provider,
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- application.Run(runCtx) }()
	t.Cleanup(cancelRun)

	backendAddress, stopBackend := lifecycleStartTCPEchoServer(t)
	t.Cleanup(stopBackend)
	chain := []modulerelay.Hop{{
		Address: net.JoinHostPort("127.0.0.1", lifecyclePortString(listenerPort)), Listener: listener,
	}}
	deadline := time.Now().Add(5 * time.Second)
	var connection net.Conn
	var dialErr error
	for time.Now().Before(deadline) {
		dialCtx, cancelDial := context.WithTimeout(t.Context(), 250*time.Millisecond)
		connection, dialErr = modulerelay.Dial(dialCtx, "tcp", backendAddress, chain, fixture.provider)
		cancelDial()
		if dialErr == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if dialErr != nil {
		cancelRun()
		<-runDone
		t.Fatalf("restart hydration did not restore the pki_mtls relay: %v", dialErr)
	}
	payload := []byte("restored-after-restart")
	if _, err := connection.Write(payload); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(connection, reply); err != nil || !reflect.DeepEqual(reply, payload) {
		t.Fatalf("restored relay round trip = %q, error = %v", reply, err)
	}
	_ = connection.Close()

	cancelRun()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}
}

func TestRunHotRestartChildBindsTunnelCredentialBeforeRestoringPKIMTLSRuntime(t *testing.T) {
	if stdruntime.GOOS != "linux" {
		t.Skip("hot restart descriptor handoff is supported on Linux")
	}
	fixture := newLifecycleRelayMTLSFixture(t)
	listener := fixture.listener(lifecyclePickFreeTCPPort(t))
	desired := Snapshot{
		Rules:               []model.HTTPRule{},
		L4Rules:             []model.L4Rule{},
		RelayListeners:      []model.RelayListener{listener},
		EgressProfiles:      []model.EgressProfile{},
		Certificates:        []model.ManagedCertificateBundle{},
		CertificatePolicies: []model.ManagedCertificatePolicy{},
	}

	parentConfig := Config{
		AgentID: fixture.agentID, AgentName: fixture.agentID,
		CurrentVersion: "1.0.0", DataDir: t.TempDir(),
	}
	parentModules, err := newConfiguredModules(parentConfig)
	if err != nil {
		t.Fatal(err)
	}
	parent := &App{cfg: parentConfig, relayTunnelCredentials: fixture.provider}
	parent.setConfiguredModules(parentModules)
	parent.bindRelayTunnelCredentialProvider()
	t.Cleanup(func() { _ = parent.Close() })
	if err := parent.runtime.Apply(t.Context(), Snapshot{}, desired); err != nil {
		t.Fatalf("prepare parent pki_mtls runtime: %v", err)
	}
	streamBundle, err := parent.processStreams.Export()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = streamBundle.Close() })

	unclaimedPacket, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unclaimedPacket.Close() })
	packetBundle, err := hotrestart.ExportPacketConns(map[string]net.PacketConn{
		"unclaimed-packet": unclaimedPacket,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = packetBundle.Close() })

	childConfig := parentConfig
	childConfig.DataDir = t.TempDir()
	childModules, err := newConfiguredModules(childConfig)
	if err != nil {
		t.Fatal(err)
	}
	store := core.NewInMemory()
	if err := store.SaveDesiredSnapshot(desired); err != nil {
		t.Fatal(err)
	}
	childApp := &App{
		cfg: childConfig, store: store,
		relayTunnelCredentials: fixture.provider,
	}
	childApp.setConfiguredModules(childModules)
	candidate, managed, err := childApp.runtime.CandidateGenerationIdentity(Snapshot{}, desired)
	if err != nil {
		t.Fatal(err)
	}
	if !managed || candidate.ID == "" {
		t.Fatalf("bootstrap hot restart candidate = %+v, managed = %t", candidate, managed)
	}
	runtimeDigest := mustHotRestartSnapshotDigest(t, desired)
	identity := hotrestart.Identity{
		Revision: 0, SnapshotDigest: runtimeDigest, GenerationID: candidate.ID,
		LeaseID: bootstrapHotRestartLeaseID(runtimeDigest), LaunchEpoch: "pki-mtls-child",
	}

	// Model a fresh child process: the package-level provider starts empty and
	// only RunHotRestartChild may bind the App-owned execution-plane provider.
	modulerelay.SetProcessTunnelCredentialProvider(nil)
	t.Cleanup(func() { modulerelay.SetProcessTunnelCredentialProvider(nil) })
	err = childApp.RunHotRestartChild(t.Context(), &hotrestart.ChildSession{
		Identity:          identity,
		StreamDescriptors: streamBundle.Descriptors,
		StreamFiles:       streamBundle.Files,
		PacketDescriptors: packetBundle.Descriptors,
		PacketFiles:       packetBundle.Files,
	})
	if err == nil || !strings.Contains(err.Error(), "inherited packet descriptors were not consumed: unclaimed-packet") {
		t.Fatalf("RunHotRestartChild() error = %v, want post-apply packet validation sentinel", err)
	}
	if strings.Contains(err.Error(), "tunnel credential provider") {
		t.Fatalf("RunHotRestartChild() applied pki_mtls runtime before credential binding: %v", err)
	}
}

func TestHotRestartRelayListenerSurvivesParentDrain(t *testing.T) {
	if stdruntime.GOOS != "linux" {
		t.Skip("hot restart descriptor handoff is supported on Linux")
	}
	fixture := newLifecycleRelayMTLSFixture(t)
	listener := fixture.listener(lifecyclePickFreeTCPPort(t))
	desired := Snapshot{
		Rules:               []model.HTTPRule{},
		L4Rules:             []model.L4Rule{},
		RelayListeners:      []model.RelayListener{listener},
		EgressProfiles:      []model.EgressProfile{},
		Certificates:        []model.ManagedCertificateBundle{},
		CertificatePolicies: []model.ManagedCertificatePolicy{},
	}

	parentConfig := Config{
		AgentID: fixture.agentID, AgentName: fixture.agentID,
		CurrentVersion: "1.0.0", DataDir: t.TempDir(),
	}
	parentModules, err := newConfiguredModules(parentConfig)
	if err != nil {
		t.Fatal(err)
	}
	parent := &App{cfg: parentConfig, relayTunnelCredentials: fixture.provider}
	parent.setConfiguredModules(parentModules)
	parent.bindRelayTunnelCredentialProvider()
	parentClosed := false
	t.Cleanup(func() {
		if !parentClosed {
			_ = parent.Close()
		}
	})
	if err := parent.runtime.Apply(t.Context(), Snapshot{}, desired); err != nil {
		t.Fatalf("prepare hot restart parent relay: %v", err)
	}
	streamBundle, err := parent.processStreams.Export()
	if err != nil {
		t.Fatal(err)
	}

	childConfig := parentConfig
	childConfig.DataDir = t.TempDir()
	childModules, err := newConfiguredModules(childConfig)
	if err != nil {
		t.Fatal(err)
	}
	child := &App{cfg: childConfig, relayTunnelCredentials: fixture.provider}
	child.setConfiguredModules(childModules)
	child.bindRelayTunnelCredentialProvider()
	t.Cleanup(func() { _ = child.Close() })
	streamSet, err := child.processStreams.Import(streamBundle.Descriptors, streamBundle.Files)
	if err != nil {
		t.Fatal(err)
	}
	streamBundle.Files = nil
	_ = streamBundle.Close()
	t.Cleanup(func() { _ = streamSet.Close() })
	if err := child.runtime.Apply(t.Context(), Snapshot{}, desired); err != nil {
		t.Fatalf("prepare hot restart child relay: %v", err)
	}
	if err := child.processStreams.ValidateImported(); err != nil {
		t.Fatalf("validate inherited relay listener: %v", err)
	}
	if err := parent.processStreams.Pause(); err != nil {
		t.Fatalf("pause hot restart parent relay: %v", err)
	}
	if err := child.processStreams.ActivateImported(); err != nil {
		t.Fatalf("activate hot restart child relay: %v", err)
	}
	if err := parent.Close(); err != nil {
		t.Fatalf("drain hot restart parent relay: %v", err)
	}
	parentClosed = true

	connection, err := net.DialTimeout(
		"tcp",
		net.JoinHostPort("127.0.0.1", lifecyclePortString(listener.ListenPort)),
		250*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("hot restart parent drain closed the child relay listener: %v", err)
	}
	_ = connection.Close()
}

func TestRelayPortMovesKeepHotRestartDescriptorsBounded(t *testing.T) {
	fixture := newLifecycleRelayMTLSFixture(t)
	cfg := Config{
		AgentID: fixture.agentID, AgentName: fixture.agentID,
		CurrentVersion: "1.0.0", DataDir: t.TempDir(),
	}
	modules, err := newConfiguredModules(cfg)
	if err != nil {
		t.Fatal(err)
	}
	application := &App{cfg: cfg, relayTunnelCredentials: fixture.provider}
	application.setConfiguredModules(modules)
	application.bindRelayTunnelCredentialProvider()
	t.Cleanup(func() { _ = application.Close() })

	usedPorts := make(map[int]struct{})
	previous := Snapshot{}
	previousPort := 0
	for revision := int64(1); revision <= 70; revision++ {
		port := 0
		for port == 0 {
			candidate := lifecyclePickFreeTCPPort(t)
			if _, used := usedPorts[candidate]; !used {
				port = candidate
				usedPorts[port] = struct{}{}
			}
		}
		next := Snapshot{Revision: revision, RelayListeners: []model.RelayListener{fixture.listener(port)}}
		if err := application.runtime.Apply(t.Context(), previous, next); err != nil {
			t.Fatalf("apply relay port move %d: %v", revision, err)
		}
		connection, err := net.DialTimeout(
			"tcp", net.JoinHostPort("127.0.0.1", lifecyclePortString(port)), 250*time.Millisecond,
		)
		if err != nil {
			t.Fatalf("active relay port %d is unavailable after move %d: %v", port, revision, err)
		}
		_ = connection.Close()
		if previousPort != 0 {
			oldAddress := net.JoinHostPort("127.0.0.1", lifecyclePortString(previousPort))
			replacement, err := net.Listen("tcp", oldAddress)
			if err != nil {
				t.Fatalf("retired relay port %d still owns an FD after move %d: %v", previousPort, revision, err)
			}
			_ = replacement.Close()
		}
		previous = next
		previousPort = port
	}

	bundle, err := application.processStreams.Export()
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	if len(bundle.Descriptors) != 1 {
		t.Fatalf("relay port moves exported %d listener FDs, want 1", len(bundle.Descriptors))
	}
}
