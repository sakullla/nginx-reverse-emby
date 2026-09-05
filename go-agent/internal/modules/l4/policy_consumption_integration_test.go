//go:build integration

package l4

import (
	"context"
	"net"
	"strconv"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/wasm"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/wasm/testfixture"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type l4ConsumptionFactory struct {
	wasm.GenerationFactory
	mu        sync.Mutex
	responses map[string]policy.ModuleResponse
	calls     map[string]int
}

func (f *l4ConsumptionFactory) PrepareGeneration(ctx context.Context, spec policy.GenerationSpec) (policy.GenerationRuntime, error) {
	runtime, err := f.GenerationFactory.PrepareGeneration(ctx, spec)
	if err != nil {
		return nil, err
	}
	return &l4ConsumptionRuntime{GenerationRuntime: runtime, factory: f}, nil
}

type l4ConsumptionRuntime struct {
	policy.GenerationRuntime
	factory *l4ConsumptionFactory
}

func (r *l4ConsumptionRuntime) Evaluate(ctx context.Context, request policy.ModuleRequest) (policy.ModuleResponse, error) {
	response, err := r.GenerationRuntime.Evaluate(ctx, request)
	r.factory.mu.Lock()
	r.factory.responses[request.GenerationID] = response
	r.factory.calls[request.GenerationID]++
	r.factory.mu.Unlock()
	return response, err
}

func TestIntegrationL4WASMGenerationAdmissionAndExistingFlows(t *testing.T) {
	for _, protocol := range []string{"tcp", "udp"} {
		t.Run(protocol, func(t *testing.T) {
			runtime, err := wasm.NewRuntime(t.Context(), wasm.RuntimeOptions{MaxMemoryPages: 16})
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close(context.Background())
			factory := &l4ConsumptionFactory{GenerationFactory: wasm.GenerationFactory{Runtime: runtime}, responses: map[string]policy.ModuleResponse{}, calls: map[string]int{}}
			backend, port := 0, 0
			if protocol == "tcp" {
				backend = startL4GenerationTCPBackend(t, "business")
				port = pickFreeTCPPort(t)
			} else {
				backend = startL4GenerationUDPBackend(t, "business")
				port = pickFreeUDPPort(t)
			}
			snapshot := func(revision int64, mode sdk.PolicyMode, prefix string) model.Snapshot {
				value, err := testfixture.Snapshot(t.Context(), t.TempDir(), revision, prefix, []testfixture.Stage{{Kind: model.PolicyKindIP, Mode: mode, Action: sdk.PolicyActionDeny}})
				if err != nil {
					t.Fatal(err)
				}
				value.L4Rules = l4GenerationSnapshot(revision, protocol, port, backend).L4Rules
				value.L4Rules[0].PolicyRef = &model.PolicyRef{ID: "effective"}
				return value
			}
			first, second := snapshot(1, sdk.PolicyModeObserve, "127.0.0.0/8"), snapshot(2, sdk.PolicyModeEnforce, "198.51.100.0/24")
			registry := module.NewRegistry()
			if err := registry.Register(policy.NewModule(factory, nil)); err != nil {
				t.Fatal(err)
			}
			owner := NewModule(Config{GenerationSelector: registry, SessionRegistrar: l4GenerationNoopRegistrar{}, ExternalDrainLifecycle: true})
			if err := registry.Register(owner); err != nil {
				t.Fatal(err)
			}
			initial := prepareL4GenerationCandidate(t, registry, model.Snapshot{}, first)
			oldView, _ := initial.Publish()
			defer oldView.Destroy(context.Background())
			address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
			var oldTCP net.Conn
			var oldUDP *net.UDPConn
			exchangeOld := func(payload string) (string, error) {
				if protocol == "tcp" {
					return l4GenerationTCPExchangeConn(oldTCP, payload)
				}
				return l4GenerationUDPExchange(oldUDP, payload)
			}
			if protocol == "tcp" {
				oldTCP, err = net.Dial("tcp", address)
				if err != nil {
					t.Fatal(err)
				}
				defer oldTCP.Close()
			} else {
				peer, _ := net.ResolveUDPAddr("udp", address)
				oldUDP, err = net.DialUDP("udp", nil, peer)
				if err != nil {
					t.Fatal(err)
				}
				defer oldUDP.Close()
			}
			if value, err := exchangeOld("before"); err != nil || value != "business:before" {
				t.Fatal("observed real WASM deny blocked initial flow", value, err)
			}
			assertProbe := func(view *module.GenerationView, wantMatch bool) {
				t.Helper()
				factory.mu.Lock()
				payload := append([]byte(nil), factory.responses[view.ID()].Payload...)
				factory.mu.Unlock()
				status, frame, err := testfixture.Slot(payload, 0)
				resolved, decodeErr := sdk.UnmarshalPolicyDatasetResolveResponse(frame, testfixture.ResolveRequest("regions"))
				if err != nil || decodeErr != nil || status != sdk.PolicyStatusOK || resolved.Reference == nil {
					t.Fatal("actual guest init reference missing", err, decodeErr)
				}
				if resolved.Reference.Generation != view.ID() {
					t.Fatal("wrong runtime generation")
				}
				status, frame, err = testfixture.Slot(payload, 1)
				query, decodeErr := sdk.UnmarshalPolicyDatasetQueryResponse(frame, testfixture.QueryRequest(*resolved.Reference, "cn-44"))
				if err != nil || decodeErr != nil || status != sdk.PolicyStatusOK || len(query.Matches) != 1 || query.Matches[0].Matched != wantMatch {
					t.Fatal("actual source query used wrong generation", err, decodeErr)
				}
				status, frame, err = testfixture.Slot(payload, 2)
				source, decodeErr := sdk.UnmarshalPolicyTrustedSourceResponse(frame, 4096)
				if err != nil || decodeErr != nil || status != sdk.PolicyStatusOK || source.Source == nil || source.Source.ValidateFor("fixture-ip", view.ID(), "1") != nil || source.Source.SourceAddress.String() != "127.0.0.1" || source.Source.Authority != sdk.PolicySourceSocket {
					t.Fatal("actual socket source not authenticated", err, decodeErr)
				}
			}
			assertProbe(oldView, true)
			next := prepareL4GenerationCandidate(t, registry, first, second)
			newView, retired := next.Publish()
			defer newView.Destroy(context.Background())
			if retired != oldView {
				t.Fatal("old generation was not retained")
			}
			if value, err := exchangeOld("retained"); err != nil || value != "business:retained" {
				t.Fatal("ordinary publication broke old flow", value, err)
			}
			factory.mu.Lock()
			oldCalls := factory.calls[oldView.ID()]
			factory.mu.Unlock()
			if oldCalls != 1 {
				t.Fatal("established flow reran admission using mixed state")
			}
			if protocol == "tcp" {
				if _, err := l4GenerationTCPExchange(port, "denied"); err == nil {
					t.Fatal("new TCP flow bypassed new enforced policy")
				}
			} else {
				peer, _ := net.ResolveUDPAddr("udp", address)
				fresh, err := net.DialUDP("udp", nil, peer)
				if err != nil {
					t.Fatal(err)
				}
				defer fresh.Close()
				if _, err := l4GenerationUDPExchange(fresh, "denied"); err == nil {
					t.Fatal("new UDP flow bypassed policy")
				}
				if l4GenerationSource(t, newView).server.udpSessionCount() != 0 {
					t.Fatal("denied UDP flow opened upstream session")
				}
			}
			assertProbe(newView, false)
			if protocol == "udp" {
				l4GenerationSource(t, oldView).server.closeUDPSessions()
				if _, err := exchangeOld("readmitted"); err == nil {
					t.Fatal("released old UDP tuple bypassed new admission")
				}
				factory.mu.Lock()
				newCalls := factory.calls[newView.ID()]
				factory.mu.Unlock()
				if newCalls < 2 {
					t.Fatal("released tuple did not re-admit")
				}
			}
		})
	}
}
