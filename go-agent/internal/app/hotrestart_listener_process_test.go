//go:build integration && !windows

package app

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

const hotRestartListenerHelperEnv = "NRE_TEST_HOT_RESTART_LISTENER_CHILD"

func TestHotRestartListenerProcessHelper(t *testing.T) {
	if os.Getenv(hotRestartListenerHelperEnv) != "1" {
		return
	}
	childSession, isChild, err := hotrestart.OpenChildSessionFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if !isChild {
		t.Fatal("helper process did not receive a hot restart session")
	}
	defer childSession.Close()

	dataDir := os.Getenv("NRE_TEST_HOT_RESTART_DATA_DIR")
	fixture := newLifecycleRelayMTLSFixture(t)
	cfg := Config{
		DataDir: dataDir, AgentID: fixture.agentID, AgentName: fixture.agentID,
		CurrentVersion: "1.0.0", HeartbeatInterval: time.Hour,
	}
	store, err := core.NewFilesystem(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	modules, err := newConfiguredModules(cfg)
	if err != nil {
		t.Fatal(err)
	}
	child := &App{
		cfg: cfg, store: store,
		relayTunnelCredentials: fixture.provider,
		syncClient: syncClientFunc(func(context.Context, SyncRequest) (Snapshot, error) {
			if marker := os.Getenv("NRE_TEST_HOT_RESTART_SYNC_MARKER"); marker != "" {
				if err := os.WriteFile(marker, []byte("synced"), 0o600); err != nil {
					return Snapshot{}, err
				}
			}
			return store.LoadDesiredSnapshot()
		}),
	}
	child.setConfiguredModules(modules)
	if err := child.RunHotRestartChild(t.Context(), childSession); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestIntegrationHotRestartParentDrainPreservesChildRelayListener(t *testing.T) {
	if testing.Short() {
		t.Skip("real hot restart process coverage is not a short test")
	}
	fixture := newLifecycleRelayMTLSFixture(t)
	listenerPort := lifecyclePickFreeTCPPort(t)
	desired := Snapshot{
		Rules:               []model.HTTPRule{},
		L4Rules:             []model.L4Rule{},
		RelayListeners:      []model.RelayListener{fixture.listener(listenerPort)},
		EgressProfiles:      []model.EgressProfile{},
		Certificates:        []model.ManagedCertificateBundle{},
		CertificatePolicies: []model.ManagedCertificatePolicy{},
		PluginPolicies:      []model.PluginPolicy{},
		PluginGenerations:   []model.PluginGeneration{},
		PluginDependencies:  []model.PluginDependencyEdge{},
	}
	dataDir := t.TempDir()
	store, err := core.NewFilesystem(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDesiredSnapshot(desired); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAppliedSnapshot(desired); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		DataDir: dataDir, AgentID: fixture.agentID, AgentName: fixture.agentID,
		CurrentVersion: "1.0.0", HeartbeatInterval: time.Hour,
	}
	modules, err := newConfiguredModules(cfg)
	if err != nil {
		t.Fatal(err)
	}
	parent := &App{cfg: cfg, store: store, relayTunnelCredentials: fixture.provider}
	parent.setConfiguredModules(modules)
	parent.bindRelayTunnelCredentialProvider()
	parentClosed := false
	t.Cleanup(func() {
		if !parentClosed {
			_ = parent.Close()
		}
	})
	if err := parent.runtime.Apply(t.Context(), Snapshot{}, desired); err != nil {
		t.Fatalf("prepare parent relay generation: %v", err)
	}
	identity, err := parent.hotRestartLaunchIdentity()
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	syncMarker := filepath.Join(dataDir, "hot-restart", "child-synced")
	process, err := parent.startHotRestartWithResources(t.Context(), hotrestart.Launch{
		Binary: executable,
		Argv:   []string{executable, "-test.run=^TestHotRestartListenerProcessHelper$"},
		Env: append(os.Environ(),
			hotRestartListenerHelperEnv+"=1",
			"NRE_TEST_HOT_RESTART_DATA_DIR="+dataDir,
			"NRE_TEST_HOT_RESTART_SYNC_MARKER="+syncMarker,
		),
		Identity:         identity,
		AuthorityJournal: filepath.Join(dataDir, "hot-restart", "authority-test.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Abort() })
	activationCtx, cancelActivation := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancelActivation()
	if err := process.Activate(activationCtx); err != nil {
		t.Fatalf("activate hot restart child: %v", err)
	}
	if err := process.TransferAuthority(activationCtx); err != nil {
		t.Fatalf("transfer hot restart authority: %v", err)
	}
	for {
		if _, err := os.Stat(syncMarker); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		select {
		case <-activationCtx.Done():
			t.Fatal("hot restart child did not complete its post-authority sync")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := parent.Close(); err != nil {
		t.Fatalf("close hot restart parent: %v", err)
	}
	parentClosed = true

	connection, err := net.DialTimeout(
		"tcp",
		net.JoinHostPort("127.0.0.1", lifecyclePortString(listenerPort)),
		time.Second,
	)
	if err != nil {
		t.Fatalf("hot restart parent drain closed child relay listener: %v", err)
	}
	_ = connection.Close()
}
