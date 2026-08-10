package app

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
	pluginrpc "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/rpc"
)

type failingLoadStore struct {
	*core.InMemory
	appliedErr error
	desiredErr error
}

func (s *failingLoadStore) LoadAppliedSnapshot() (core.Snapshot, error) {
	if s.appliedErr != nil {
		return core.Snapshot{}, s.appliedErr
	}
	return s.InMemory.LoadAppliedSnapshot()
}

func (s *failingLoadStore) LoadDesiredSnapshot() (core.Snapshot, error) {
	if s.desiredErr != nil {
		return core.Snapshot{}, s.desiredErr
	}
	return s.InMemory.LoadDesiredSnapshot()
}

func TestRPCProcessSupervisorIsAppOwnedAndDrained(t *testing.T) {
	app := newAppWithAllDeps(Config{}, nil, nil, nil, nil)
	if app.PluginProcessSupervisor() == nil {
		t.Fatal("app did not create the RPC process supervisor")
	}
	if app.PluginRPCHost() == nil {
		t.Fatal("app did not create the RPC orchestration host")
	}
	app.closeLocalRuntimes()
	if app.PluginProcessSupervisor() != nil {
		t.Fatal("app did not drain the RPC process supervisor")
	}
}

func TestAppCloseReconcilesHostAfterSupervisorTermination(t *testing.T) {
	host, supervisor := testAppRPCRuntimes(t)
	hostCalls, supervisorCalls := 0, 0
	application := &App{rpcHost: host, rpcProcesses: supervisor}
	application.rpcHostClose = func(context.Context) error {
		hostCalls++
		if hostCalls <= 3 {
			return errors.New("credentials still owned")
		}
		return nil
	}
	application.rpcProcessesClose = func(context.Context) error {
		supervisorCalls++
		return nil
	}
	if err := application.Close(); err != nil {
		t.Fatal(err)
	}
	if hostCalls != 4 || supervisorCalls != 1 || application.rpcHost != nil || application.rpcProcesses != nil {
		t.Fatalf("shutdown reconciliation: host=%d supervisor=%d hostRef=%v processRef=%v", hostCalls, supervisorCalls, application.rpcHost != nil, application.rpcProcesses != nil)
	}
}

func TestAppCloseCanRetryAfterFailure(t *testing.T) {
	host, supervisor := testAppRPCRuntimes(t)
	allowHostClose := false
	application := &App{rpcHost: host, rpcProcesses: supervisor}
	application.rpcHostClose = func(context.Context) error {
		if !allowHostClose {
			return errors.New("host cleanup failed")
		}
		return nil
	}
	application.rpcProcessesClose = func(context.Context) error { return nil }
	if err := application.Close(); err == nil || application.rpcHost == nil || application.rpcProcesses == nil {
		t.Fatalf("first Close did not preserve failed owners: err=%v", err)
	}
	allowHostClose = true
	if err := application.Close(); err != nil {
		t.Fatalf("second Close did not retry: %v", err)
	}
	if application.rpcHost != nil || application.rpcProcesses != nil {
		t.Fatal("successful retry retained runtime references")
	}
}

func TestAppRunJoinsExhaustedRuntimeCloseError(t *testing.T) {
	loadErr := errors.New("load applied failed")
	closeErr := errors.New("runtime close retries exhausted")
	host, supervisor := testAppRPCRuntimes(t)
	application := &App{
		store:             &failingLoadStore{InMemory: core.NewInMemory(), appliedErr: loadErr},
		runtime:           core.NewRuntime(),
		rpcHost:           host,
		rpcProcesses:      supervisor,
		rpcHostClose:      func(context.Context) error { return closeErr },
		rpcProcessesClose: func(context.Context) error { return nil },
	}
	err := application.Run(t.Context())
	if !errors.Is(err, loadErr) || !errors.Is(err, closeErr) {
		t.Fatalf("Run error = %v, want load and exhausted Close errors", err)
	}
}

func TestAppRunHotRestartChildJoinsExhaustedRuntimeCloseError(t *testing.T) {
	loadErr := errors.New("load desired failed")
	closeErr := errors.New("runtime close retries exhausted")
	host, supervisor := testAppRPCRuntimes(t)
	application := &App{
		store:             &failingLoadStore{InMemory: core.NewInMemory(), desiredErr: loadErr},
		runtime:           core.NewRuntime(),
		rpcHost:           host,
		rpcProcesses:      supervisor,
		rpcHostClose:      func(context.Context) error { return closeErr },
		rpcProcessesClose: func(context.Context) error { return nil },
	}
	err := application.RunHotRestartChild(t.Context(), &hotrestart.ChildSession{})
	if !errors.Is(err, loadErr) || !errors.Is(err, closeErr) {
		t.Fatalf("RunHotRestartChild error = %v, want load and exhausted Close errors", err)
	}
}

func testAppRPCRuntimes(t *testing.T) (*pluginrpc.Host, *pluginprocess.Supervisor) {
	t.Helper()
	supervisor := pluginprocess.NewSupervisor(nil, nil, io.Discard)
	host, err := pluginrpc.NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(t.TempDir(), "runtime")}, supervisor, nil)
	if err != nil {
		t.Fatal(err)
	}
	return host, supervisor
}
