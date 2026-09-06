//go:build integration

package l4

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/wasm"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/wasm/testfixture"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/datasets"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestIntegrationL4ProxySourcePolicyBeforeAuthenticatedRelay(t *testing.T) {
	backend, backendConnections := relayAdmissionTCPListener(t, "")
	certificate := mustIssueL4RelayCertificate(t, "relay.internal.test")
	provider := &relayAdmissionMaterial{certificate: certificate}
	certificateID := 51
	relayPort := pickFreeTCPPort(t)
	listener := model.RelayListener{ID: 51, AgentID: "relay-node", Name: "policy-relay", ListenHost: "127.0.0.1", ListenPort: relayPort,
		Enabled: true, CertificateID: &certificateID, TLSMode: "pin_only", PinSet: []model.RelayPin{{Type: "sha256", Value: mustL4RelaySPKIPin(t, certificate)}},
		ObfsMode: relay.RelayObfsModeOff, TransportMode: "tls_tcp", Revision: 1}
	relayServer, err := relay.Start(t.Context(), []relay.Listener{listener}, provider)
	if err != nil {
		t.Fatal(err)
	}
	defer relayServer.Close()
	// An opaque TCP forwarding listener observes every attempted Relay
	// connection. TLS and Relay framing still terminate in production Relay.
	relayAddress, relayConnections := relayAdmissionTCPListener(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(relayPort)))
	_, relayPublicPort, err := net.SplitHostPort(relayAddress)
	if err != nil {
		t.Fatal(err)
	}
	listener.PublicHost, listener.PublicPort = "127.0.0.1", mustRelayAdmissionPort(t, relayPublicPort)
	runtime, err := wasm.NewRuntime(t.Context(), wasm.RuntimeOptions{MaxMemoryPages: 16})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	factory := &l4ConsumptionFactory{GenerationFactory: wasm.GenerationFactory{Runtime: runtime}, responses: map[string]policy.ModuleResponse{}, calls: map[string]int{}}
	registry := module.NewRegistry()
	if err := registry.Register(policy.NewModule(factory, nil)); err != nil {
		t.Fatal(err)
	}
	snapshot, err := testfixture.Snapshot(t.Context(), t.TempDir(), 1, "198.51.100.0/24", []testfixture.Stage{{Kind: model.PolicyKindIP, Mode: sdk.PolicyModeEnforce, MatchDecision: true}},
		datasets.CIDRClassification{Name: "cn-11", Kind: sdk.DatasetClassificationRegion, CIDRs: []string{"203.0.113.0/24"}})
	if err != nil {
		t.Fatal(err)
	}
	frontPort := pickFreeTCPPort(t)
	_, backendPort, err := net.SplitHostPort(backend)
	if err != nil {
		t.Fatal(err)
	}
	rule := l4GenerationSnapshot(1, "tcp", frontPort, mustRelayAdmissionPort(t, backendPort)).L4Rules[0]
	rule.PolicyRef = &model.PolicyRef{ID: "effective"}
	rule.RelayLayers = [][]int{{51}}
	rule.Tuning.ProxyProtocol = model.L4ProxyProtocolTuning{Decode: true, TrustedPeers: []string{"127.0.0.1/32"}}
	snapshot.L4Rules = []model.L4Rule{rule}
	candidate := prepareL4GenerationCandidate(t, registry, model.Snapshot{}, snapshot)
	view, _ := candidate.Publish()
	defer view.Destroy(context.Background())
	evaluator, ok := view.Resolve(policy.ProviderEvaluator)
	if !ok {
		t.Fatal("policy generation provider missing")
	}
	front, err := newServerWithOptions(t.Context(), []model.L4Rule{rule}, []model.RelayListener{listener}, provider, serverOptions{generationID: view.ID(), policyEvaluator: evaluator.(policy.Evaluator)})
	if err != nil {
		t.Fatal(err)
	}
	defer front.Close()
	var initialReference *sdk.DatasetReference
	for index, test := range []struct {
		name, source string
		action       sdk.PolicyAction
		matched      bool
	}{
		{"denied", "198.51.100.10", sdk.PolicyActionDeny, true},
		{"allowed", "203.0.113.20", sdk.PolicyActionAllow, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(frontPort)), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if err := client.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			payload := "relay-business-payload"
			if _, err := fmt.Fprintf(client, "PROXY TCP4 %s 127.0.0.1 12345 %d\r\n%s", test.source, frontPort, payload); err != nil {
				t.Fatal(err)
			}
			response := make([]byte, len(payload))
			n, readErr := io.ReadFull(client, response)
			if test.action == sdk.PolicyActionDeny {
				if readErr == nil || n != 0 {
					t.Fatalf("denied source reached business: %q err=%v", response[:n], readErr)
				}
				if relayConnections.Load() != 0 || backendConnections.Load() != 0 {
					t.Fatalf("denied source opened downstream connections: Relay=%d backend=%d", relayConnections.Load(), backendConnections.Load())
				}
			} else {
				if readErr != nil || string(response) != payload {
					t.Fatalf("allowed source did not traverse authenticated Relay: %q err=%v", response[:n], readErr)
				}
				if relayConnections.Load() != 1 || backendConnections.Load() != 1 {
					t.Fatalf("allowed source bypassed Relay: Relay=%d backend=%d", relayConnections.Load(), backendConnections.Load())
				}
			}
			factory.mu.Lock()
			probe := append([]byte(nil), factory.responses[view.ID()].Payload...)
			action := factory.responses[view.ID()].Action
			calls := factory.calls[view.ID()]
			factory.mu.Unlock()
			if calls != index+1 {
				t.Fatalf("actual WASM admission calls=%d want=%d", calls, index+1)
			}
			wantAction := policy.ActionAllow
			if test.matched {
				wantAction = policy.ActionDeny
			}
			if action != wantAction {
				t.Fatalf("guest action=%s want=%s for actual dataset match=%v", action, wantAction, test.matched)
			}
			status, frame, err := testfixture.Slot(probe, 0)
			resolved, decodeErr := sdk.UnmarshalPolicyDatasetResolveResponse(frame, testfixture.ResolveRequest("regions"))
			if err != nil || decodeErr != nil || status != sdk.PolicyStatusOK || resolved.Reference == nil || resolved.Reference.Generation != view.ID() {
				t.Fatalf("guest init did not resolve local generation: %+v status=%v err=%v/%v", resolved, status, err, decodeErr)
			}
			if initialReference == nil {
				initialReference = resolved.Reference
			} else if *initialReference != *resolved.Reference {
				t.Fatal("source change replaced the same generation's initialized dataset reference")
			}
			status, frame, err = testfixture.Slot(probe, 1)
			query, decodeErr := sdk.UnmarshalPolicyDatasetQueryResponse(frame, testfixture.QueryRequest(*resolved.Reference, "cn-44"))
			if err != nil || decodeErr != nil || status != sdk.PolicyStatusOK || query.Status != sdk.DatasetQueryOK || len(query.Matches) != 1 || query.Matches[0].Matched != test.matched || query.Matches[0].Coverage != sdk.DatasetCovered {
				t.Fatalf("guest queried a different source: %+v status=%v err=%v/%v", query, status, err, decodeErr)
			}
			status, frame, err = testfixture.Slot(probe, 2)
			source, decodeErr := sdk.UnmarshalPolicyTrustedSourceResponse(frame, 4096)
			if err != nil || decodeErr != nil || status != sdk.PolicyStatusOK || source.Source == nil || source.Source.ValidateFor("fixture-ip", view.ID(), strconv.Itoa(rule.ID)) != nil || source.Source.SourceAddress.String() != test.source || source.Source.PeerAddress.String() != "127.0.0.1" || source.Source.Authority != sdk.PolicySourcePROXY {
				t.Fatalf("PROXY source provenance was fabricated or lost: %+v status=%v err=%v/%v", source, status, err, decodeErr)
			}
		})
	}
}

type relayAdmissionMaterial struct{ certificate tls.Certificate }

func (p *relayAdmissionMaterial) ServerCertificate(context.Context, int) (*tls.Certificate, error) {
	return &p.certificate, nil
}
func (*relayAdmissionMaterial) TrustedCAPool(context.Context, []int) (*x509.CertPool, error) {
	return x509.NewCertPool(), nil
}

func mustRelayAdmissionPort(t *testing.T, text string) int {
	t.Helper()
	port, err := strconv.Atoi(text)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func relayAdmissionTCPListener(t *testing.T, forwardTo string) (string, *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int32
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	var workers sync.WaitGroup
	acceptDone := make(chan struct{})
	workers.Add(1)
	go func() {
		defer workers.Done()
		defer close(acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			mu.Lock()
			connections[conn] = true
			mu.Unlock()
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer conn.Close()
				if forwardTo == "" {
					_, _ = io.Copy(conn, conn)
					return
				}
				upstream, err := net.DialTimeout("tcp", forwardTo, time.Second)
				if err != nil {
					return
				}
				defer upstream.Close()
				mu.Lock()
				connections[upstream] = true
				mu.Unlock()
				copied := make(chan struct{})
				go func() { _, _ = io.Copy(upstream, conn); _ = upstream.Close(); close(copied) }()
				_, _ = io.Copy(conn, upstream)
				_ = conn.Close()
				<-copied
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-acceptDone
		mu.Lock()
		for conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
		workers.Wait()
	})
	return listener.Addr().String(), &accepted
}
