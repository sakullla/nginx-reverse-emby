package app

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/wireguard"
)

func TestRelayRuntimeManagerAppliesWireGuardEgressProfilesFromInlineConfig(t *testing.T) {
	var created []wireguard.Config
	provider := &testRelayTLSProvider{
		certificates: map[int]tls.Certificate{
			1: mustIssueTestTLSCertificate(t),
		},
	}
	manager := newRelayRuntimeManager(provider)
	manager.egressWireGuard = newEgressWireGuardRuntime(func(_ context.Context, cfg wireguard.Config) (wireguard.Runtime, error) {
		created = append(created, cfg)
		return &testAppWireGuardRuntime{}, nil
	})
	defer manager.Close()

	profileID := 91
	if err := manager.ApplyWithWireGuardAndEgressProfiles(
		context.Background(),
		[]model.RelayListener{runtimeTestRelayListener(pickFreeTCPPort(t), 1)},
		nil,
		[]model.EgressProfile{validAppWireGuardEgressProfile(profileID)},
	); err != nil {
		t.Fatalf("ApplyWithWireGuardAndEgressProfiles() error = %v", err)
	}

	if len(created) != 1 {
		t.Fatalf("wireguard egress runtime creations = %d, want 1", len(created))
	}
	if got := created[0].ID; got != profileID {
		t.Fatalf("wireguard egress runtime profile ID = %d, want %d", got, profileID)
	}
	if got := created[0].PrivateKey; got != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" {
		t.Fatalf("wireguard egress runtime private key = %q, want inline egress config key", got)
	}
}

func TestRelayRuntimeManagerRollsBackEgressWireGuardBeforeRestoringPreviousServer(t *testing.T) {
	provider := &testRelayTLSProvider{
		certificates: map[int]tls.Certificate{
			1: mustIssueTestTLSCertificate(t),
		},
	}
	ctx := context.Background()
	listenErr := fmt.Errorf("wireguard listen failed after replacement")
	var candidateEgressRuntime *testAppWireGuardRuntime
	restoreObserved := false
	listenCalls := 0
	wireGuardRuntime := &testAppWireGuardRuntime{}
	wireGuardRuntime.onListenTCP = func(_ context.Context, address string) (net.Listener, error) {
		listenCalls++
		switch listenCalls {
		case 1:
			return net.Listen("tcp", address)
		case 2:
			return nil, fmt.Errorf("bind tcp %s: address already in use", address)
		case 3:
			return nil, listenErr
		default:
			restoreObserved = true
			if candidateEgressRuntime == nil || !candidateEgressRuntime.closed {
				return nil, fmt.Errorf("egress wireguard transaction was not rolled back before restore")
			}
			return net.Listen("tcp", address)
		}
	}

	manager := newRelayRuntimeManagerWithWireGuard(provider, newSharedWireGuardRuntimeWithFactory(func(context.Context, wireguard.Config) (wireguard.Runtime, error) {
		return wireGuardRuntime, nil
	}))
	egressRuntimeCreations := 0
	manager.egressWireGuard = newEgressWireGuardRuntime(func(context.Context, wireguard.Config) (wireguard.Runtime, error) {
		runtime := &testAppWireGuardRuntime{}
		egressRuntimeCreations++
		if egressRuntimeCreations == 2 {
			candidateEgressRuntime = runtime
		}
		return runtime, nil
	})
	defer manager.Close()

	profileID := 92
	listenPort := pickFreeTCPPort(t)
	listener := runtimeTestRelayListener(listenPort, 1)
	listener.TransportMode = relay.ListenerTransportModeWireGuard
	listener.WireGuardProfileID = &profileID
	wireGuardProfile := validAppWireGuardProfile(profileID)
	egressProfile := validAppWireGuardEgressProfile(193)
	if err := manager.ApplyWithWireGuardAndEgressProfiles(ctx, []model.RelayListener{listener}, []model.WireGuardProfile{wireGuardProfile}, []model.EgressProfile{egressProfile}); err != nil {
		t.Fatalf("failed to apply initial relay runtime: %v", err)
	}
	waitForPortState(t, listenPort, true)

	nextEgressProfile := egressProfile
	nextEgressProfile.Revision++
	nextEgressProfile.WireGuardConfig.Peers[0].Endpoint = "127.0.0.1:51821"
	reconfigured := listener
	reconfigured.Revision++
	err := manager.ApplyWithWireGuardAndEgressProfiles(ctx, []model.RelayListener{reconfigured}, []model.WireGuardProfile{wireGuardProfile}, []model.EgressProfile{nextEgressProfile})
	if err == nil || !strings.Contains(err.Error(), listenErr.Error()) {
		t.Fatalf("expected replacement listen error, got %v", err)
	}
	if strings.Contains(err.Error(), "restore failed") {
		t.Fatalf("restore failed because egress rollback happened too late: %v", err)
	}
	if !restoreObserved {
		t.Fatal("expected previous relay server to be restored")
	}
	if candidateEgressRuntime == nil {
		t.Fatal("expected replacement egress wireguard runtime to be prepared")
	}
	if !candidateEgressRuntime.closed {
		t.Fatal("expected replacement egress wireguard runtime to be rolled back")
	}
	waitForPortState(t, listenPort, true)
}
