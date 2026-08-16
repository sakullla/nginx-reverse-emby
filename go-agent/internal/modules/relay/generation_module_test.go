//go:build !integration

package relay_test

import (
	"context"
	"crypto/tls"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	relaymodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

func TestRelayGenerationRemainsUsableAfterApplyContextCancellation(t *testing.T) {
	certificateID := 1
	certificate := mustIssueTestTLSCertificate(t)
	provider := &fakeTLSMaterialProvider{certificates: map[int]tls.Certificate{certificateID: certificate}}
	registry := module.NewRegistry()
	relayModule := relaymodule.NewModule(relaymodule.Config{
		AgentID: "agent-a", AgentName: "node-a", GenerationSelector: registry, ExternalDrainLifecycle: true,
	})
	defer relayModule.Close()
	mustRegister(t, registry, generationProviderModule{name: "certs", ref: module.ProviderTLSMaterial, provider: provider})
	mustRegister(t, registry, relayModule)
	port := pickFreeTCPPort(t)
	next := model.Snapshot{Revision: 1, RelayListeners: []model.RelayListener{
		testRelayListener(71, "agent-a", "node-a", port, certificateID),
	}}
	generationContext, err := module.NewGenerationContext(model.Snapshot{}, next)
	if err != nil {
		t.Fatal(err)
	}
	applyCtx, cancelApply := context.WithCancel(t.Context())
	candidate, err := registry.PrepareGeneration(applyCtx, generationContext)
	if err != nil {
		t.Fatalf("prepare relay generation: %v", err)
	}
	if err := candidate.Ready(applyCtx); err != nil {
		t.Fatalf("ready relay generation: %v", err)
	}
	view, _ := candidate.Publish()
	defer view.Destroy(context.Background())
	cancelApply()

	if got := dialServedCertificate(t, port); !certificateDEREqual(got, certificate) {
		t.Fatal("relay generation served the wrong certificate after apply context cancellation")
	}
}

func TestRelayGenerationCandidateKeepsSameBindingAndTLSInvisibleUntilPublish(t *testing.T) {
	t.Parallel()
	firstCertificateID := 1
	secondCertificateID := 2
	firstCertificate := mustIssueTestTLSCertificate(t)
	secondCertificate := mustIssueTestTLSCertificate(t)
	provider := &fakeTLSMaterialProvider{certificates: map[int]tls.Certificate{
		firstCertificateID: firstCertificate, secondCertificateID: secondCertificate,
	}}
	registry := module.NewRegistry()
	relayModule := relaymodule.NewModule(relaymodule.Config{
		AgentID: "agent-a", AgentName: "node-a", GenerationSelector: registry, ExternalDrainLifecycle: true,
	})
	mustRegister(t, registry, generationProviderModule{name: "certs", ref: module.ProviderTLSMaterial, provider: provider})
	mustRegister(t, registry, relayModule)
	port := pickFreeTCPPort(t)

	firstSnapshot := model.Snapshot{Revision: 1, RelayListeners: []model.RelayListener{
		testRelayListener(71, "agent-a", "node-a", port, firstCertificateID),
	}}
	firstContext, err := module.NewGenerationContext(model.Snapshot{}, firstSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	firstCandidate, err := registry.PrepareGeneration(context.Background(), firstContext)
	if err != nil {
		t.Fatalf("prepare first relay generation: %v", err)
	}
	if err := firstCandidate.Ready(context.Background()); err != nil {
		t.Fatalf("ready first relay generation: %v", err)
	}
	assertRelayTLSDialFails(t, port)
	firstView, _ := firstCandidate.Publish()
	if got := dialServedCertificate(t, port); !certificateDEREqual(got, firstCertificate) {
		t.Fatal("first published relay generation served the wrong certificate")
	}

	secondListener := testRelayListener(71, "agent-a", "node-a", port, secondCertificateID)
	secondListener.Revision = 2
	secondSnapshot := model.Snapshot{Revision: 2, RelayListeners: []model.RelayListener{secondListener}}
	secondContext, err := module.NewGenerationContext(firstSnapshot, secondSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	secondCandidate, err := registry.PrepareGeneration(context.Background(), secondContext)
	if err != nil {
		t.Fatalf("prepare second relay generation on stable binding: %v", err)
	}
	if err := secondCandidate.Ready(context.Background()); err != nil {
		t.Fatalf("ready second relay generation: %v", err)
	}
	if got := dialServedCertificate(t, port); !certificateDEREqual(got, firstCertificate) {
		t.Fatal("unpublished relay candidate replaced active TLS material")
	}
	secondView, previous := secondCandidate.Publish()
	if previous != firstView {
		t.Fatal("second relay publish did not retire the first generation")
	}
	if got := dialServedCertificate(t, port); !certificateDEREqual(got, secondCertificate) {
		t.Fatal("second published relay generation did not serve new TLS material")
	}

	if err := firstView.Destroy(context.Background()); err != nil {
		t.Fatalf("destroy first relay generation: %v", err)
	}
	if got := dialServedCertificate(t, port); !certificateDEREqual(got, secondCertificate) {
		t.Fatal("destroying retired relay generation disrupted active binding")
	}
	_ = secondView.Destroy(context.Background())
	_ = relayModule.Close()
}

func assertRelayTLSDialFails(t *testing.T, port int) {
	t.Helper()
	assertRelayTLSDialFailsAt(t, "127.0.0.1", port)
}

func assertRelayTLSDialFailsAt(t *testing.T, host string, port int) {
	t.Helper()
	address := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 100 * time.Millisecond}, "tcp", address, &tls.Config{InsecureSkipVerify: true})
	if err == nil {
		_ = conn.Close()
		t.Fatal("unpublished relay endpoint accepted a TLS connection")
	}
}

type generationProviderModule struct {
	name     string
	ref      module.ProviderRef
	provider any
}

func (m generationProviderModule) Name() string { return m.name }
func (m generationProviderModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name, Provides: []module.ProviderRef{m.ref}}
}
func (m generationProviderModule) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(m.ref, m.provider)
}
func (generationProviderModule) Capabilities(module.SnapshotView) []module.Capability { return nil }
func (generationProviderModule) Apply(context.Context, module.ApplyRequest) error     { return nil }
func (generationProviderModule) Stop(context.Context) error                           { return nil }
func (m generationProviderModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	return generationProviderTransaction{ref: m.ref, provider: m.provider}, nil
}

type generationProviderTransaction struct {
	ref      module.ProviderRef
	provider any
}

func (t generationProviderTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(t.ref, t.provider)
}
func (generationProviderTransaction) Ready(context.Context) error   { return nil }
func (generationProviderTransaction) Destroy(context.Context) error { return nil }
func (generationProviderTransaction) Commit() error                 { return nil }
func (generationProviderTransaction) Rollback() error               { return nil }

var _ module.GenerationTransaction = generationProviderTransaction{}
