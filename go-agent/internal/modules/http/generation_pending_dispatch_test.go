//go:build integration

package http

import (
	"context"
	"fmt"
	"io"
	"net"
	stdhttp "net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestIntegrationHTTPPendingDispatchDoesNotBlockGenerationPublication(t *testing.T) {
	backend := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		_, _ = io.WriteString(w, r.URL.Path)
	}))
	defer backend.Close()
	registry := module.NewRegistry()
	if err := registry.Register(&generationTestTLSModule{provider: &testTLSProvider{}}); err != nil {
		t.Fatal(err)
	}
	drain := generation.NewDrainController(nil)
	mod := NewModule(Config{GenerationSelector: registry, SessionRegistrar: drain})
	defer mod.Close()
	if err := registry.Register(mod); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := drain.Close(ctx); err != nil {
			t.Errorf("close generations: %v", err)
		}
	})

	address := fmt.Sprintf("127.0.0.1:%d", pickFreeTCPUDPPort(t))
	frontend := "http://" + address
	first := model.Snapshot{Revision: 1, Rules: []model.HTTPRule{{
		ID: 1, Enabled: true, FrontendURL: frontend, Backends: []model.HTTPBackend{{URL: backend.URL + "/old"}},
	}}}
	second := model.Snapshot{Revision: 2, Rules: []model.HTTPRule{{
		ID: 1, Enabled: true, FrontendURL: frontend, Backends: []model.HTTPBackend{{URL: backend.URL + "/new"}},
	}}}
	firstCandidate := prepareRegistryGenerationForTest(t, registry, model.Snapshot{}, first)
	// Observe physical acceptance without sending a request. This leaves a
	// pending ingress dispatch but no registered HTTP request session.
	accepted := make(chan struct{})
	var once sync.Once
	firstView, _ := firstCandidate.Publish()
	for _, server := range mod.ingress.currentRuntime().servers {
		previous := server.ConnState
		server.ConnState = func(conn net.Conn, state stdhttp.ConnState) {
			if previous != nil {
				previous(conn, state)
			}
			if state == stdhttp.StateNew {
				once.Do(func() { close(accepted) })
			}
		}
	}
	if err := drain.Activate(t.Context(), generation.Generation{ID: firstView.ID(), Revision: 1, Resource: firstView}, nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("idle connection was not accepted")
	}
	if count := drain.Registry().GenerationCount(firstView.ID()); count != 0 {
		t.Fatalf("idle connection owns %d request sessions", count)
	}

	secondCandidate := prepareRegistryGenerationForTest(t, registry, first, second)
	secondView, _ := secondCandidate.Publish()
	done := make(chan error, 1)
	go func() {
		done <- drain.Activate(t.Context(), generation.Generation{ID: secondView.ID(), Revision: 2, Resource: secondView}, nil, time.Minute)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("publication blocked on the old idle HTTP connection")
	}
	if got := generationTestGET(t, frontend); got != "/new" {
		t.Fatalf("new generation response = %q, want /new", got)
	}
	for _, status := range drain.Snapshot().Generations {
		if status.GenerationID == firstView.ID() && !status.CompletedAt.IsZero() {
			t.Fatal("old generation reported drained while dispatch was pending")
		}
	}
	_ = conn.Close()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		for _, status := range drain.Snapshot().Generations {
			if status.GenerationID == firstView.ID() && !status.CompletedAt.IsZero() {
				if status.State != model.GenerationDrainStateDrained {
					t.Fatalf("old generation cleanup = %+v", status)
				}
				return
			}
		}
		select {
		case <-poll.C:
		case <-deadline.C:
			t.Fatal("old generation did not finish cleanup after the idle connection closed")
		}
	}
}
