package channel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

func testManagerConfig(agentID string, credentials relay.TunnelCredentialProvider) Config {
	return Config{
		AgentID:        agentID,
		Credentials:    credentials,
		BackoffBase:    20 * time.Millisecond,
		BackoffLimit:   200 * time.Millisecond,
		ConnectTimeout: 5 * time.Second,
		UDPIdleTimeout: 2 * time.Second,
	}
}

func startTCPEcho(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen backend: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return listener.Addr().String()
}

func startUDPEcho(t *testing.T) string {
	t.Helper()
	address, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve backend: %v", err)
	}
	socket, err := net.ListenUDP("udp", address)
	if err != nil {
		t.Fatalf("listen backend: %v", err)
	}
	t.Cleanup(func() { _ = socket.Close() })
	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, peer, err := socket.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = socket.WriteToUDP(buf[:n], peer)
		}
	}()
	return socket.LocalAddr().String()
}

type channelTestRig struct {
	t       *testing.T
	entry   *Manager
	exit    *Manager
	ingress string
	bridge  string
	spec    SessionSpec
}

func newChannelTestRig(t *testing.T, ca *testCA, protocol, backend string) *channelTestRig {
	t.Helper()
	entryPKI := newTestTunnelPKI(t, ca)
	entryPKI.issueAgent("entry-agent")
	exitPKI := newTestTunnelPKI(t, ca)
	exitPKI.issueAgent("exit-agent")

	entry, err := NewManager(testManagerConfig("entry-agent", entryPKI))
	if err != nil {
		t.Fatalf("entry manager: %v", err)
	}
	t.Cleanup(func() { _ = entry.Close() })
	exit, err := NewManager(testManagerConfig("exit-agent", exitPKI))
	if err != nil {
		t.Fatalf("exit manager: %v", err)
	}
	t.Cleanup(func() { _ = exit.Close() })

	entryStatus, err := entry.Ensure(context.Background(), SessionSpec{
		SessionID:    "session-1",
		Role:         RoleEntry,
		Protocol:     protocol,
		EntryAgentID: "entry-agent",
		ExitAgentID:  "exit-agent",
	})
	if err != nil {
		t.Fatalf("entry ensure: %v", err)
	}
	if entryStatus.IngressAddress == "" || entryStatus.BridgeAddress == "" {
		t.Fatalf("entry status lacks bound addresses: %+v", entryStatus)
	}
	_, ingressPort, _ := net.SplitHostPort(entryStatus.IngressAddress)

	return &channelTestRig{
		t:       t,
		entry:   entry,
		exit:    exit,
		ingress: net.JoinHostPort("127.0.0.1", ingressPort),
		bridge:  entryStatus.BridgeAddress,
		spec: SessionSpec{
			SessionID:      "session-1",
			Role:           RoleExit,
			Protocol:       protocol,
			EntryAgentID:   "entry-agent",
			ExitAgentID:    "exit-agent",
			DialAddress:    net.JoinHostPort("127.0.0.1", ingressPort),
			BackendAddress: backend,
		},
	}
}

func (rig *channelTestRig) ensureExit(t *testing.T) SessionStatus {
	t.Helper()
	status, err := rig.exit.Ensure(context.Background(), rig.spec)
	if err != nil {
		t.Fatalf("exit ensure: %v", err)
	}
	return status
}

func waitForStatus(t *testing.T, manager *Manager, sessionID, want string) SessionStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err := manager.Status(context.Background(), sessionID)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if status.State == want {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	status, _ := manager.Status(context.Background(), sessionID)
	t.Fatalf("session %s did not reach state %q, last %+v", sessionID, want, status)
	return SessionStatus{}
}

func assertBridgeEcho(t *testing.T, bridge string, message string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", bridge, 2*time.Second)
	if err != nil {
		t.Fatalf("dial bridge: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte(message)); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len(message))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != message {
		t.Fatalf("echo = %q, want %q", buf, message)
	}
}

func TestChannelTCPEndToEndDirect(t *testing.T) {
	rig := newChannelTestRig(t, newTestCA(t, "domain-1"), ProtocolTCP, startTCPEcho(t))
	status := rig.ensureExit(t)
	if status.State != StateOnline {
		t.Fatalf("exit status = %+v, want online", status)
	}
	waitForStatus(t, rig.entry, "session-1", StateOnline)
	assertBridgeEcho(t, rig.bridge, "hello reverse channel")
}

func TestChannelUDPEndToEndDirect(t *testing.T) {
	rig := newChannelTestRig(t, newTestCA(t, "domain-1"), ProtocolUDP, startUDPEcho(t))
	status := rig.ensureExit(t)
	if status.State != StateOnline {
		t.Fatalf("exit status = %+v, want online", status)
	}
	waitForStatus(t, rig.entry, "session-1", StateOnline)

	bridgeAddress, err := net.ResolveUDPAddr("udp", rig.bridge)
	if err != nil {
		t.Fatalf("resolve bridge: %v", err)
	}
	conn, err := net.DialUDP("udp", nil, bridgeAddress)
	if err != nil {
		t.Fatalf("dial bridge: %v", err)
	}
	defer conn.Close()
	message := []byte("udp datagram")
	if _, err := conn.Write(message); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64*1024)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf[:n]) != string(message) {
		t.Fatalf("echo = %q, want %q", buf[:n], message)
	}
}

// startHalfCloseResponser serves one response per connection, but only after
// the peer half-closes: the response bytes are generated strictly after the
// channel propagated the FIN, so a stream aborted on first EOF loses them.
func startHalfCloseResponser(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen backend: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				request, err := io.ReadAll(conn)
				if err != nil {
					return
				}
				response := append([]byte("response: "), request...)
				response = append(response, " (after fin)"...)
				_, _ = conn.Write(response)
			}()
		}
	}()
	return listener.Addr().String()
}

func TestChannelTCPHalfCloseDeliversTailResponse(t *testing.T) {
	rig := newChannelTestRig(t, newTestCA(t, "domain-1"), ProtocolTCP, startHalfCloseResponser(t))
	if status := rig.ensureExit(t); status.State != StateOnline {
		t.Fatalf("exit status = %+v, want online", status)
	}
	waitForStatus(t, rig.entry, "session-1", StateOnline)

	conn, err := net.DialTimeout("tcp", rig.bridge, 2*time.Second)
	if err != nil {
		t.Fatalf("dial bridge: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("half-close payload")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		if err := tcp.CloseWrite(); err != nil {
			t.Fatalf("client half-close: %v", err)
		}
	}
	response, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read response after half-close: %v", err)
	}
	if want := "response: half-close payload (after fin)"; string(response) != want {
		t.Fatalf("response = %q, want %q", response, want)
	}
}

func TestChannelConcurrentEnsureKeepsSingleRuntime(t *testing.T) {
	rig := newChannelTestRig(t, newTestCA(t, "domain-1"), ProtocolTCP, startTCPEcho(t))

	const concurrency = 4
	results := make(chan SessionStatus, concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			status, err := rig.exit.Ensure(context.Background(), rig.spec)
			if err != nil {
				t.Errorf("concurrent ensure: %v", err)
				return
			}
			results <- status
		}()
	}
	online := false
	for i := 0; i < concurrency; i++ {
		select {
		case status := <-results:
			if status.State == StateOnline {
				online = true
			}
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent ensures did not return")
		}
	}
	if !online {
		waitForStatus(t, rig.exit, "session-1", StateOnline)
	}
	waitForStatus(t, rig.entry, "session-1", StateOnline)

	// Exactly one runtime may survive the concurrent ensures: a leaked loser
	// would reconnect after teardown and flip the entry back online.
	if err := rig.exit.Teardown(context.Background(), "session-1"); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	waitForStatus(t, rig.entry, "session-1", StateOffline)
	time.Sleep(500 * time.Millisecond)
	status, err := rig.entry.Status(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("entry status: %v", err)
	}
	if status.State != StateOffline {
		t.Fatalf("a leaked runtime reconnected after teardown: %+v", status)
	}
}

func TestChannelReconnectsAfterChannelKilled(t *testing.T) {
	rig := newChannelTestRig(t, newTestCA(t, "domain-1"), ProtocolTCP, startTCPEcho(t))
	if status := rig.ensureExit(t); status.State != StateOnline {
		t.Fatalf("exit status = %+v, want online", status)
	}
	waitForStatus(t, rig.entry, "session-1", StateOnline)

	// Kill the established channel at the entry side without touching the
	// ingress listener: the exit must observe the drop and reconnect.
	rig.entry.mu.Lock()
	current := rig.entry.sessions["session-1"]
	rig.entry.mu.Unlock()
	if current == nil {
		t.Fatal("entry session is missing")
	}
	entry, ok := current.runtime.(*entryRuntime)
	if !ok {
		t.Fatalf("entry runtime type %T", current.runtime)
	}
	entry.mu.Lock()
	active := entry.active
	entry.mu.Unlock()
	if active == nil {
		t.Fatal("entry has no active channel")
	}
	_ = active.Close()

	// Wait until both peers observed the drop before waiting for the
	// reconnect, so the online states below cannot be stale.
	waitForStatus(t, rig.entry, "session-1", StateOffline)
	waitForStatus(t, rig.exit, "session-1", StateOffline)
	waitForStatus(t, rig.exit, "session-1", StateOnline)
	waitForStatus(t, rig.entry, "session-1", StateOnline)
	assertBridgeEcho(t, rig.bridge, "after reconnect")
}

// expectTLSAlert asserts the peer tears the connection down. TLS 1.3 clients
// finish their handshake before the server validates the client certificate,
// so a rejected peer is only observable on the first record exchange.
func expectTLSAlert(t *testing.T, conn *tls.Conn, what string) {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte{1}); err != nil {
		return
	}
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		_ = conn.Close()
		t.Fatalf("expected %s to be rejected", what)
	}
}

func TestChannelRejectsNonMTLSHandshake(t *testing.T) {
	ca := newTestCA(t, "domain-1")
	rig := newChannelTestRig(t, ca, ProtocolTCP, startTCPEcho(t))

	// A peer without any client credential must be rejected. The server may
	// only signal the rejection after the TLS 1.3 client finished, so probe
	// with a record exchange when the client-side handshake returns nil.
	plain := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13} //nolint:gosec // test probes rejection
	conn, err := tls.Dial("tcp", rig.ingress, plain)
	if err == nil {
		expectTLSAlert(t, conn, "handshake without client certificate")
	}

	// A peer with a credential from a different authority must be rejected.
	foreignCA := newTestCA(t, "domain-1")
	foreign := newTestTunnelPKI(t, foreignCA)
	foreign.issueAgent("exit-agent")
	foreignConfig, err := relay.AgentTunnelClientTLSConfig(context.Background(), foreign, "entry-agent")
	if err != nil {
		t.Fatalf("foreign client config: %v", err)
	}
	rawConn, err := net.DialTimeout("tcp", rig.ingress, 2*time.Second)
	if err != nil {
		t.Fatalf("dial ingress: %v", err)
	}
	foreignConn := tls.Client(rawConn, foreignConfig)
	if err := foreignConn.Handshake(); err == nil {
		expectTLSAlert(t, foreignConn, "handshake with foreign credential")
	}

	status, err := rig.entry.Status(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("entry status: %v", err)
	}
	if status.State != StateOffline {
		t.Fatalf("entry status = %+v, want offline after rejected handshakes", status)
	}
}

func TestChannelRejectsWrongAgentIdentity(t *testing.T) {
	ca := newTestCA(t, "domain-1")
	rig := newChannelTestRig(t, ca, ProtocolTCP, startTCPEcho(t))

	// A valid credential owned by a different agent must be rejected: the
	// session pins the expected exit agent identity.
	impostor := newTestTunnelPKI(t, ca)
	impostor.issueAgent("impostor-agent")
	config, err := relay.AgentTunnelClientTLSConfig(context.Background(), impostor, "entry-agent")
	if err != nil {
		t.Fatalf("impostor client config: %v", err)
	}
	rawConn, err := net.DialTimeout("tcp", rig.ingress, 2*time.Second)
	if err != nil {
		t.Fatalf("dial ingress: %v", err)
	}
	conn := tls.Client(rawConn, config)
	if err := conn.Handshake(); err == nil {
		expectTLSAlert(t, conn, "handshake from the wrong agent identity")
	}
}

func TestChannelExitReportsOfflineWhenEntryUnreachable(t *testing.T) {
	rig := newChannelTestRig(t, newTestCA(t, "domain-1"), ProtocolTCP, startTCPEcho(t))
	rig.spec.DialAddress = "127.0.0.1:1" // unreachable
	status := rig.ensureExit(t)
	if status.State != StateOffline {
		t.Fatalf("exit status = %+v, want offline", status)
	}
	if status.LastError == "" {
		t.Fatal("exit status should report the dial failure")
	}
}

func TestChannelEnsureIsIdempotentAndTeardownReleases(t *testing.T) {
	rig := newChannelTestRig(t, newTestCA(t, "domain-1"), ProtocolTCP, startTCPEcho(t))
	first := rig.ensureExit(t)
	second := rig.ensureExit(t)
	if first.State != StateOnline || second.State != StateOnline {
		t.Fatalf("idempotent ensure states = %+v / %+v", first, second)
	}

	entryReensure, err := rig.entry.Ensure(context.Background(), SessionSpec{
		SessionID: "session-1", Role: RoleEntry, Protocol: ProtocolTCP,
		EntryAgentID: "entry-agent", ExitAgentID: "exit-agent",
	})
	if err != nil {
		t.Fatalf("entry re-ensure: %v", err)
	}
	if entryReensure.IngressAddress != rig.ingress && entryReensure.IngressAddress == "" {
		t.Fatalf("entry re-ensure lost ingress address: %+v", entryReensure)
	}

	if err := rig.exit.Teardown(context.Background(), "session-1"); err != nil {
		t.Fatalf("exit teardown: %v", err)
	}
	status, err := rig.exit.Status(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("exit status: %v", err)
	}
	if status.State != StateOffline {
		t.Fatalf("status after teardown = %+v, want offline", status)
	}
	// Teardown is idempotent and rejects malformed identities.
	if err := rig.exit.Teardown(context.Background(), "session-1"); err != nil {
		t.Fatalf("repeat teardown: %v", err)
	}
	if err := rig.exit.Teardown(context.Background(), "bad\nid"); err == nil {
		t.Fatal("teardown accepted an invalid session id")
	}
}

func TestChannelDialThroughRelayChain(t *testing.T) {
	ca := newTestCA(t, "domain-1")
	backend := startTCPEcho(t)

	// The relay hop runs on a third agent and terminates pki_mtls itself.
	relayPKI := newTestTunnelPKI(t, ca)
	relayPKI.issueAgent("relay-agent")
	relayPKI.issueListener("relay-agent", 71, "relay.test")

	listenPort := freeTCPPort(t)
	listener := model.RelayListener{
		ID:                     71,
		AgentID:                "relay-agent",
		ListenHost:             "127.0.0.1",
		BindHosts:              []string{"127.0.0.1"},
		ListenPort:             listenPort,
		PublicHost:             "relay.test",
		PublicPort:             listenPort,
		Enabled:                true,
		TLSMode:                relay.TLSModePKIMTLS,
		PKIIdentityID:          relayPKI.listenerIdentityID(71),
		AllowTransportFallback: true,
	}
	server, err := relay.Start(context.Background(), []model.RelayListener{listener}, tunnelOnlyMaterialProvider{tunnel: relayPKI})
	if err != nil {
		t.Fatalf("start relay: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	rig := newChannelTestRig(t, ca, ProtocolTCP, backend)
	rig.spec.SessionID = "session-1"
	rig.spec.RelayChain = []relay.Hop{{
		Address:    net.JoinHostPort("127.0.0.1", fmt.Sprint(listenPort)),
		ServerName: "relay.test",
		Listener:   listener,
	}}
	status := rig.ensureExit(t)
	if status.State != StateOnline {
		t.Fatalf("exit status through relay = %+v, want online", status)
	}
	waitForStatus(t, rig.entry, "session-1", StateOnline)
	assertBridgeEcho(t, rig.bridge, "through the relay chain")
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func TestChannelStatusObservesCallerCancel(t *testing.T) {
	ca := newTestCA(t, "domain-1")
	pki := newTestTunnelPKI(t, ca)
	pki.issueAgent("entry-agent")
	manager, err := NewManager(testManagerConfig("entry-agent", pki))
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.Status(canceled, "session-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled status error = %v", err)
	}

	deadline, cancelDeadline := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancelDeadline()
	<-deadline.Done()
	if _, err := manager.Status(deadline, "session-1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline status error = %v", err)
	}

	if _, err := manager.Ensure(context.Background(), SessionSpec{
		SessionID: "session-1", Role: RoleEntry, Protocol: ProtocolTCP,
		EntryAgentID: "entry-agent", ExitAgentID: "exit-agent",
	}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	status, err := manager.Status(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("live status: %v", err)
	}
	if status.SessionID != "session-1" || status.State == "" {
		t.Fatalf("live status = %+v", status)
	}
	if _, err := manager.Status(canceled, "session-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled live status error = %v", err)
	}
}
