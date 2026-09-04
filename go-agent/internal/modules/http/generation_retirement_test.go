//go:build !integration

package http

import (
	"context"
	"errors"
	stdhttp "net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestHTTPRetirementUsesActiveLeaseIdentityAcrossInheritedAlias(t *testing.T) {
	active := &httpIngressBinding{key: "tcp:inherited-address"}
	stale := &httpIngressBinding{key: "tcp:retired-address"}
	manager := &httpIngressManager{bindings: map[string]*httpIngressBinding{
		active.key: active,
		stale.key:  stale,
	}}
	runtime := &Runtime{
		bindings: []string{"tcp:requested-address"},
		ingressLeases: []*httpIngressLease{{
			binding: active,
		}},
	}
	transaction := &httpGenerationTransaction{
		module: &Module{ingress: manager}, runtime: runtime,
	}

	transaction.retireInactiveIngressBindings()

	if manager.bindings[active.key] != active {
		t.Fatal("retirement removed the active inherited binding after its key alias changed")
	}
	if manager.bindings[stale.key] != nil {
		t.Fatal("retirement kept an inactive binding")
	}
}

type rejectingHTTPSessionRegistrar struct{}

func (rejectingHTTPSessionRegistrar) RegisterSession(
	string,
	generation.EntityKey,
	string,
	generation.Session,
) (*generation.SessionHandle, error) {
	return nil, errors.New("generation admission is closed")
}

func TestHTTPRegistrationFailureRejectsStaleGenerationRequest(t *testing.T) {
	var backendReached atomic.Bool
	backend := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		backendReached.Store(true)
		w.WriteHeader(stdhttp.StatusNoContent)
	}))
	defer backend.Close()

	server := NewServer(model.HTTPListener{Rules: []model.HTTPRule{{
		ID: 1, Enabled: true, FrontendURL: "http://frontend.example",
		Backends: []model.HTTPBackend{{URL: backend.URL}},
	}}})
	handler := &generationHTTPHandler{
		server: server,
		tracker: newHTTPSessionTracker(
			"generation-1",
			rejectingHTTPSessionRegistrar{},
			true,
		),
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(stdhttp.MethodGet, "http://frontend.example/library", nil)

	handler.ServeHTTP(recorder, request.WithContext(context.Background()))

	if recorder.Code != stdhttp.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, stdhttp.StatusServiceUnavailable)
	}
	if backendReached.Load() {
		t.Fatal("request reached the backend after generation session registration was rejected")
	}
}
