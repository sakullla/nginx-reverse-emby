package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/localagent"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type localAgentRuntimeStub struct {
	start func(context.Context) error
}

func (s localAgentRuntimeStub) Start(ctx context.Context) error {
	if s.start != nil {
		return s.start(ctx)
	}
	return nil
}

func TestNewLocalAgentStarterBuildsSQLiteStoreAndInvokesRuntime(t *testing.T) {
	cfg := config.Default()
	cfg.EnableLocalAgent = true
	cfg.DataDir = t.TempDir()
	cfg.LocalAgentID = "local-test"
	cfg.LocalAgentName = "local-test"

	started := false
	previousNewLocalAgentRuntime := newLocalAgentRuntime
	t.Cleanup(func() {
		newLocalAgentRuntime = previousNewLocalAgentRuntime
	})

	newLocalAgentRuntime = func(gotCfg config.Config, store localagent.Store) (localAgentRuntime, error) {
		if gotCfg.LocalAgentID != "local-test" {
			t.Fatalf("LocalAgentID = %q", gotCfg.LocalAgentID)
		}
		sqliteStore, ok := store.(*storage.SQLiteStore)
		if !ok {
			t.Fatalf("store type = %T, want *storage.SQLiteStore", store)
		}
		if _, err := sqliteStore.LoadLocalSnapshot(t.Context(), gotCfg.LocalAgentID); err != nil {
			t.Fatalf("LoadLocalSnapshot() error = %v", err)
		}
		t.Cleanup(func() {
			_ = sqliteStore.Close()
		})
		return localAgentRuntimeStub{
			start: func(context.Context) error {
				started = true
				return nil
			},
		}, nil
	}

	starter, err := newLocalAgentStarter(cfg)
	if err != nil {
		t.Fatalf("newLocalAgentStarter() error = %v", err)
	}
	if starter == nil {
		t.Fatal("newLocalAgentStarter() returned nil starter")
	}
	if err := starter(t.Context()); err != nil {
		t.Fatalf("starter() error = %v", err)
	}
	if !started {
		t.Fatal("starter did not invoke runtime Start")
	}
}

func TestNewControlPlaneAppStartsEmbeddedLocalAgentWhenEnabled(t *testing.T) {
	cfg := config.Default()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.EnableLocalAgent = true
	cfg.DataDir = t.TempDir()

	started := make(chan struct{}, 1)

	previousNewHandler := newHandler
	previousNewLocalAgentRuntime := newLocalAgentRuntime
	t.Cleanup(func() {
		newHandler = previousNewHandler
		newLocalAgentRuntime = previousNewLocalAgentRuntime
	})

	newHandler = func(config.Config) (http.Handler, error) {
		return http.NewServeMux(), nil
	}
	newLocalAgentRuntime = func(_ config.Config, store localagent.Store) (localAgentRuntime, error) {
		if sqliteStore, ok := store.(*storage.SQLiteStore); ok {
			t.Cleanup(func() {
				_ = sqliteStore.Close()
			})
		}
		return localAgentRuntimeStub{
			start: func(context.Context) error {
				started <- struct{}{}
				return nil
			},
		}, nil
	}

	application, err := newControlPlaneApp(cfg, nil)
	if err != nil {
		t.Fatalf("newControlPlaneApp() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := application.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	select {
	case <-started:
	default:
		t.Fatal("embedded local agent starter was not invoked")
	}
}
