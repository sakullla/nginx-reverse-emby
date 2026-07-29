//go:build integration

package l4

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestIntegrationL4GenerationRemainsUsableAfterApplyContextCancellation(t *testing.T) {
	t.Parallel()
	backend := startL4GenerationTCPBackend(t, "backend")
	frontendPort := pickFreeTCPPort(t)
	next := l4GenerationSnapshot(1, "tcp", frontendPort, backend)
	registry := module.NewRegistry()
	mod := NewModule(Config{
		GenerationSelector:     registry,
		SessionRegistrar:       l4GenerationNoopRegistrar{},
		ExternalDrainLifecycle: true,
	})
	defer mod.Close()
	if err := registry.Register(mod); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	generationContext, err := module.NewGenerationContext(model.Snapshot{}, next)
	if err != nil {
		t.Fatalf("NewGenerationContext() error = %v", err)
	}
	applyCtx, cancelApply := context.WithCancel(t.Context())
	candidate, err := registry.PrepareGeneration(applyCtx, generationContext)
	if err != nil {
		t.Fatalf("PrepareGeneration() error = %v", err)
	}
	if err := candidate.Ready(applyCtx); err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	view, _ := candidate.Publish()
	defer view.Destroy(context.Background())
	cancelApply()

	if got, err := l4GenerationTCPExchange(frontendPort, "request"); err != nil || got != "backend:request" {
		t.Fatalf("exchange after apply context cancellation = %q, %v", got, err)
	}
}

func TestIntegrationL4GenerationTCPPublishPinsExistingConnection(t *testing.T) {
	t.Parallel()
	oldBackend := startL4GenerationTCPBackend(t, "old")
	newBackend := startL4GenerationTCPBackend(t, "new")
	frontendPort := pickFreeTCPPort(t)
	first := l4GenerationSnapshot(1, "tcp", frontendPort, oldBackend)
	second := l4GenerationSnapshot(2, "tcp", frontendPort, newBackend)

	registry := module.NewRegistry()
	mod := NewModule(Config{
		GenerationSelector:     registry,
		SessionRegistrar:       l4GenerationNoopRegistrar{},
		ExternalDrainLifecycle: true,
	})
	if err := registry.Register(mod); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	firstCandidate := prepareL4GenerationCandidate(t, registry, model.Snapshot{}, first)
	if _, err := l4GenerationTCPExchange(frontendPort, "before-publish"); err == nil {
		t.Fatal("candidate accepted TCP traffic before publication")
	}
	firstView, _ := firstCandidate.Publish()
	defer firstView.Destroy(context.Background())

	oldConn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(frontendPort)), time.Second)
	if err != nil {
		t.Fatalf("dial first generation: %v", err)
	}
	defer oldConn.Close()
	if got, err := l4GenerationTCPExchangeConn(oldConn, "one"); err != nil || got != "old:one" {
		t.Fatalf("first exchange = %q, %v", got, err)
	}

	secondCandidate := prepareL4GenerationCandidate(t, registry, first, second)
	if got, err := l4GenerationTCPExchange(frontendPort, "candidate"); err != nil || got != "old:candidate" {
		t.Fatalf("unpublished candidate changed active TCP route: %q, %v", got, err)
	}
	secondView, retired := secondCandidate.Publish()
	defer secondView.Destroy(context.Background())
	if retired != firstView {
		t.Fatal("second publication did not retire the first generation")
	}

	if got, err := l4GenerationTCPExchangeConn(oldConn, "two"); err != nil || got != "old:two" {
		t.Fatalf("existing connection moved generations: %q, %v", got, err)
	}
	if got, err := l4GenerationTCPExchange(frontendPort, "fresh"); err != nil || got != "new:fresh" {
		t.Fatalf("new connection did not use active generation: %q, %v", got, err)
	}
}

func TestIntegrationL4GenerationCandidateDestroyAndPrepareFailurePreserveActive(t *testing.T) {
	t.Parallel()
	oldBackend := startL4GenerationTCPBackend(t, "old")
	newBackend := startL4GenerationTCPBackend(t, "new")
	frontendPort := pickFreeTCPPort(t)
	first := l4GenerationSnapshot(1, "tcp", frontendPort, oldBackend)
	second := l4GenerationSnapshot(2, "tcp", frontendPort, newBackend)

	registry := module.NewRegistry()
	mod := NewModule(Config{
		GenerationSelector:     registry,
		SessionRegistrar:       l4GenerationNoopRegistrar{},
		ExternalDrainLifecycle: true,
	})
	if err := registry.Register(mod); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	firstCandidate := prepareL4GenerationCandidate(t, registry, model.Snapshot{}, first)
	firstView, _ := firstCandidate.Publish()
	defer firstView.Destroy(context.Background())

	discarded := prepareL4GenerationCandidate(t, registry, first, second)
	if err := discarded.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	if got, err := l4GenerationTCPExchange(frontendPort, "after-destroy"); err != nil || got != "old:after-destroy" {
		t.Fatalf("destroyed candidate changed active route: %q, %v", got, err)
	}

	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy TCP port: %v", err)
	}
	defer occupied.Close()
	failed := second
	failed.L4Rules = append(failed.L4Rules, model.L4Rule{
		ID:         2,
		Enabled:    true,
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: occupied.Addr().(*net.TCPAddr).Port,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: newBackend}},
	})
	generationContext, err := module.NewGenerationContext(first, failed)
	if err != nil {
		t.Fatalf("NewGenerationContext() error = %v", err)
	}
	if _, err := registry.PrepareGeneration(context.Background(), generationContext); err == nil {
		t.Fatal("PrepareGeneration() succeeded with an occupied candidate binding")
	}
	if got, err := l4GenerationTCPExchange(frontendPort, "after-failure"); err != nil || got != "old:after-failure" {
		t.Fatalf("failed candidate changed active route: %q, %v", got, err)
	}
}

func TestIntegrationL4GenerationUDPTuplePinsAndReselectsAfterIdle(t *testing.T) {
	t.Parallel()
	oldBackend := startL4GenerationUDPBackend(t, "old")
	newBackend := startL4GenerationUDPBackend(t, "new")
	frontendPort := pickFreeUDPPort(t)
	first := l4GenerationSnapshot(1, "udp", frontendPort, oldBackend)
	second := l4GenerationSnapshot(2, "udp", frontendPort, newBackend)

	registry := module.NewRegistry()
	mod := NewModule(Config{
		GenerationSelector:     registry,
		SessionRegistrar:       l4GenerationNoopRegistrar{},
		ExternalDrainLifecycle: true,
	})
	if err := registry.Register(mod); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	firstCandidate := prepareL4GenerationCandidate(t, registry, model.Snapshot{}, first)
	firstView, _ := firstCandidate.Publish()
	defer firstView.Destroy(context.Background())
	firstSource := l4GenerationSource(t, firstView)
	firstSource.server.setUDPTimeoutsForTest(0, 15*time.Millisecond)

	frontend := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: frontendPort}
	oldTuple, err := net.DialUDP("udp", nil, frontend)
	if err != nil {
		t.Fatalf("DialUDP(old tuple): %v", err)
	}
	defer oldTuple.Close()
	if got, err := l4GenerationUDPExchange(oldTuple, "one"); err != nil || got != "old:one" {
		t.Fatalf("first UDP exchange = %q, %v", got, err)
	}

	secondCandidate := prepareL4GenerationCandidate(t, registry, first, second)
	secondView, retired := secondCandidate.Publish()
	defer secondView.Destroy(context.Background())
	if retired != firstView {
		t.Fatal("second publication did not retire the first generation")
	}
	if got, err := l4GenerationUDPExchange(oldTuple, "two"); err != nil || got != "old:two" {
		t.Fatalf("existing UDP tuple moved generations: %q, %v", got, err)
	}

	newTuple, err := net.DialUDP("udp", nil, frontend)
	if err != nil {
		t.Fatalf("DialUDP(new tuple): %v", err)
	}
	defer newTuple.Close()
	if got, err := l4GenerationUDPExchange(newTuple, "fresh"); err != nil || got != "new:fresh" {
		t.Fatalf("new UDP tuple did not use active generation: %q, %v", got, err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		remaining := firstSource.server.udpSessionCount()
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("old UDP generation retained %d idle sessions", remaining)
		}
		time.Sleep(time.Millisecond)
	}
	if got, err := l4GenerationUDPExchange(oldTuple, "reselected"); err != nil || got != "new:reselected" {
		t.Fatalf("released UDP tuple did not reselect active generation: %q, %v", got, err)
	}
}

func TestIntegrationL4GenerationDrainRevokesTargetAndForcesOldestGeneration(t *testing.T) {
	t.Parallel()
	owner := NewModule(Config{})
	defer owner.Close()
	controller := owner.SessionController()
	if controller == nil {
		t.Fatal("production L4 module has no drain controller")
	}

	firstServer := newBareL4GenerationServer("g1", controller)
	firstTarget, firstPeer := net.Pipe()
	secondTarget, secondPeer := net.Pipe()
	defer firstPeer.Close()
	defer secondPeer.Close()
	firstServer.trackTCPConn(firstTarget, 1)
	firstServer.trackTCPConn(secondTarget, 2)
	firstHandle, _ := firstServer.registerSession(1, "tcp", l4ConnectionSession{conn: firstTarget})
	secondHandle, _ := firstServer.registerSession(2, "tcp", l4ConnectionSession{conn: secondTarget})
	firstTx := &l4GenerationTransaction{
		server: firstServer, generationID: "g1", generationRevision: 1,
		drainController: controller, drainTimeout: time.Minute, manageDrain: true, published: true,
	}
	firstTx.FinalizeCommitSuccess()
	if got := controller.Registry().GenerationCount("g1"); got != 2 {
		t.Fatalf("g1 session count = %d, want 2", got)
	}

	secondServer := newBareL4GenerationServer("g2", controller)
	thirdTarget, thirdPeer := net.Pipe()
	defer thirdPeer.Close()
	secondServer.trackTCPConn(thirdTarget, 3)
	thirdHandle, _ := secondServer.registerSession(3, "tcp", l4ConnectionSession{conn: thirdTarget})
	secondTx := &l4GenerationTransaction{
		server: secondServer, previousServer: firstServer,
		generationID: "g2", generationRevision: 2,
		drainController: controller, drainTimeout: time.Minute, manageDrain: true, published: true,
		revokedEntities: map[string]struct{}{"1": {}},
		entityChanges: []generation.EntityChange{{
			Entity: generation.EntityKey{Module: "l4", ID: "1"}, Action: generation.EntityDeleted,
		}},
	}
	secondTx.FinalizeCommitSuccess()
	assertL4GenerationPipeClosed(t, firstPeer, "deleted entity")
	assertL4GenerationPipeOpen(t, secondPeer, "unrelated entity")
	if got := controller.Registry().GenerationCount("g1"); got != 1 {
		t.Fatalf("g1 session count after revoke = %d, want 1", got)
	}

	thirdServer := newBareL4GenerationServer("g3", controller)
	thirdTx := &l4GenerationTransaction{
		server: thirdServer, previousServer: secondServer,
		generationID: "g3", generationRevision: 3,
		drainController: controller, drainTimeout: time.Minute, manageDrain: true, published: true,
	}
	thirdTx.FinalizeCommitSuccess()
	assertL4GenerationPipeClosed(t, secondPeer, "oldest generation")
	status := l4GenerationDrainStatus(t, controller.Snapshot(), "g1")
	if status.State != model.GenerationDrainStateForced || status.ForceReason != model.GenerationForceReasonGenerationLimit {
		t.Fatalf("g1 drain status = %+v", status)
	}
	if status.ForcedSessionCount != 1 || status.SessionCount != 0 {
		t.Fatalf("g1 forced counts = %+v", status)
	}

	firstHandle.Finish()
	secondHandle.Finish()
	thirdHandle.Finish()
	_ = firstServer.Close()
	_ = secondServer.Close()
	_ = thirdServer.Close()
}

func TestIntegrationL4GenerationNaturalFinishDoesNotWaitOnItsOwnServer(t *testing.T) {
	t.Parallel()
	controller := generation.NewDrainController(nil)
	firstServer := newBareL4GenerationServer("g1", controller)
	firstTarget, firstPeer := net.Pipe()
	defer firstPeer.Close()
	firstServer.trackTCPConn(firstTarget, 1)
	handle, _ := firstServer.registerSession(1, "tcp", l4ConnectionSession{conn: firstTarget})
	firstTx := &l4GenerationTransaction{
		server: firstServer, generationID: "g1", generationRevision: 1,
		drainController: controller, drainTimeout: time.Minute, manageDrain: true, published: true,
	}
	firstTx.FinalizeCommitSuccess()

	secondServer := newBareL4GenerationServer("g2", controller)
	secondTx := &l4GenerationTransaction{
		server: secondServer, previousServer: firstServer,
		generationID: "g2", generationRevision: 2,
		drainController: controller, drainTimeout: time.Minute, manageDrain: true, published: true,
	}
	secondTx.FinalizeCommitSuccess()

	finished := make(chan struct{})
	firstServer.wg.Add(1)
	go func() {
		defer firstServer.wg.Done()
		handle.Finish()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("natural session finish deadlocked on Server.Close")
	}
	deadline := time.Now().Add(time.Second)
	for {
		status := l4GenerationDrainStatus(t, controller.Snapshot(), "g1")
		if status.State == model.GenerationDrainStateDrained {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("g1 drain status = %+v, want drained", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = firstTarget.Close()
	_ = firstServer.Close()
	_ = secondServer.Close()
}

func TestIntegrationL4RuleEntityChangesRevokeOnlyDeleteAndDisable(t *testing.T) {
	t.Parallel()
	previous := []model.L4Rule{
		{ID: 1, Enabled: true, Protocol: "tcp"},
		{ID: 2, Enabled: true, Protocol: "tcp"},
		{ID: 3, Enabled: true, Protocol: "tcp", Name: "before"},
	}
	next := []model.L4Rule{
		{ID: 2, Enabled: false, Protocol: "tcp"},
		{ID: 3, Enabled: true, Protocol: "tcp", Name: "after"},
	}
	revoked := revokedL4RuleEntities(previous, next)
	if _, ok := revoked["1"]; !ok {
		t.Fatal("deleted rule was not revoked")
	}
	if _, ok := revoked["2"]; !ok {
		t.Fatal("disabled rule was not revoked")
	}
	if _, ok := revoked["3"]; ok {
		t.Fatal("modified rule was revoked")
	}
	changes := l4RuleEntityChanges(previous, next)
	if len(changes) != 3 || changes[0].Action != generation.EntityDeleted || changes[1].Action != generation.EntityDisabled || changes[2].Action != generation.EntityModified {
		t.Fatalf("entity changes = %+v", changes)
	}
	if active := generationL4Rules(next); len(active) != 1 || active[0].ID != 3 {
		t.Fatalf("active generation rules = %+v", active)
	}
}

func TestIntegrationL4UDPInitializationCannotOutliveRevocationOrClose(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name   string
		cancel func(*Server)
	}{
		{name: "revoke", cancel: func(server *Server) { server.revokeRules(map[string]struct{}{"7": {}}) }},
		{name: "close", cancel: func(server *Server) { _ = server.Close() }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := newBareL4GenerationServer("g1", nil)
			defer server.Close()
			listenerConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
			if err != nil {
				t.Fatalf("ListenUDP() error = %v", err)
			}
			defer listenerConn.Close()
			listener := packetUDPListener{PacketConn: listenerConn}

			dialStarted := make(chan struct{})
			releaseDial := make(chan struct{})
			upstream, upstreamPeer := net.Pipe()
			defer upstreamPeer.Close()
			server.udpDialer = func(model.L4Rule, string) (udpUpstream, l4Candidate, error) {
				close(dialStarted)
				<-releaseDial
				return &connUDPUpstream{conn: upstream}, l4Candidate{address: "127.0.0.1:9000"}, nil
			}
			rule := model.L4Rule{ID: 7, Enabled: true, Protocol: "udp"}
			type result struct {
				session *udpSession
				err     error
			}
			resultCh := make(chan result, 1)
			go func() {
				session, err := server.sessionForUDPFlow(rule, listener, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 32000}, "")
				resultCh <- result{session: session, err: err}
			}()
			select {
			case <-dialStarted:
			case <-time.After(time.Second):
				t.Fatal("UDP dial did not reach the controlled blocker")
			}
			testCase.cancel(server)
			close(releaseDial)
			select {
			case result := <-resultCh:
				if result.session != nil || result.err == nil {
					t.Fatalf("sessionForUDPFlow() = %+v, want canceled initialization", result)
				}
			case <-time.After(time.Second):
				t.Fatal("UDP initialization did not converge after cancellation")
			}
			if got := server.udpSessionCount(); got != 0 {
				t.Fatalf("UDP session count = %d, want 0", got)
			}
			_ = upstreamPeer.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := upstreamPeer.Read(make([]byte, 1)); err == nil {
				t.Fatal("canceled UDP upstream remained open")
			} else if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				t.Fatal("canceled UDP upstream remained open until timeout")
			}
		})
	}
}

func TestIntegrationL4SessionRegistrationFailureClosesImmediateAndDeferredSessions(t *testing.T) {
	t.Parallel()
	registerErr := errors.New("register failed")
	for _, registrationReady := range []bool{true, false} {
		t.Run(fmt.Sprintf("ready=%t", registrationReady), func(t *testing.T) {
			registrar := l4GenerationFailingRegistrar{err: registerErr}
			server := newBareL4GenerationServer("g1", registrar)
			server.sessions = newL4SessionTracker("g1", registrar, registrationReady)
			defer server.Close()
			target, peer := net.Pipe()
			defer peer.Close()
			handle, err := server.registerSession(9, "tcp", l4ConnectionSession{conn: target})
			if registrationReady {
				if !errors.Is(err, registerErr) || handle != nil {
					t.Fatalf("registerSession() = %v, %v", handle, err)
				}
			} else {
				if err != nil || handle == nil {
					t.Fatalf("deferred registerSession() = %v, %v", handle, err)
				}
				server.sessions.enableRegistration()
			}
			assertL4GenerationPipeClosed(t, peer, "registration failure")
			server.sessions.mu.Lock()
			remaining := len(server.sessions.sessions)
			server.sessions.mu.Unlock()
			if remaining != 0 {
				t.Fatalf("local session entities = %d, want 0", remaining)
			}
		})
	}
}

type l4GenerationNoopRegistrar struct{}

func (l4GenerationNoopRegistrar) RegisterSession(string, generation.EntityKey, string, generation.Session) (*generation.SessionHandle, error) {
	return nil, nil
}

type l4GenerationFailingRegistrar struct{ err error }

func (r l4GenerationFailingRegistrar) RegisterSession(string, generation.EntityKey, string, generation.Session) (*generation.SessionHandle, error) {
	return nil, r.err
}

func prepareL4GenerationCandidate(t *testing.T, registry *module.Registry, previous, next model.Snapshot) module.PreparedGeneration {
	t.Helper()
	generationContext, err := module.NewGenerationContext(previous, next)
	if err != nil {
		t.Fatalf("NewGenerationContext() error = %v", err)
	}
	candidate, err := registry.PrepareGeneration(context.Background(), generationContext)
	if err != nil {
		t.Fatalf("PrepareGeneration() error = %v", err)
	}
	if err := candidate.Ready(context.Background()); err != nil {
		_ = candidate.Destroy(context.Background())
		t.Fatalf("Ready() error = %v", err)
	}
	return candidate
}

func l4GenerationSnapshot(revision int64, protocol string, frontendPort, backendPort int) model.Snapshot {
	return model.Snapshot{Revision: revision, L4Rules: []model.L4Rule{{
		ID:         1,
		Enabled:    true,
		Protocol:   protocol,
		ListenHost: "127.0.0.1",
		ListenPort: frontendPort,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: backendPort}},
	}}}
}

func startL4GenerationTCPBackend(t *testing.T, label string) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen(TCP backend): %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(connection net.Conn) {
				defer connection.Close()
				reader := bufio.NewReader(connection)
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					_, _ = fmt.Fprintf(connection, "%s:%s", label, line)
				}
			}(conn)
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port
}

func startL4GenerationUDPBackend(t *testing.T, label string) int {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP(backend): %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buffer := make([]byte, 64*1024)
		for {
			n, peer, err := conn.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			payload := append([]byte(label+":"), buffer[:n]...)
			_, _ = conn.WriteToUDP(payload, peer)
		}
	}()
	return conn.LocalAddr().(*net.UDPAddr).Port
}

func l4GenerationTCPExchange(port int, payload string) (string, error) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 500*time.Millisecond)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	return l4GenerationTCPExchangeConn(conn, payload)
}

func l4GenerationTCPExchangeConn(conn net.Conn, payload string) (string, error) {
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		return "", err
	}
	if _, err := io.WriteString(conn, payload+"\n"); err != nil {
		return "", err
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return "", err
	}
	return line[:len(line)-1], nil
}

func l4GenerationUDPExchange(conn *net.UDPConn, payload string) (string, error) {
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		return "", err
	}
	if _, err := conn.Write([]byte(payload)); err != nil {
		return "", err
	}
	buffer := make([]byte, 1024)
	n, err := conn.Read(buffer)
	if err != nil {
		return "", err
	}
	return string(buffer[:n]), nil
}

func l4GenerationSource(t *testing.T, view *module.GenerationView) l4DiagnosticsSource {
	t.Helper()
	provider, ok := view.Resolve(module.ProviderDiagnosticsL4Source)
	if !ok {
		t.Fatal("generation view has no L4 diagnostics provider")
	}
	source, ok := provider.(l4DiagnosticsSource)
	if !ok || source.server == nil {
		t.Fatalf("L4 diagnostics provider = %T", provider)
	}
	return source
}

func newBareL4GenerationServer(generationID string, registrar L4SessionRegistrar) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		ctx:             ctx,
		cancel:          cancel,
		tcpConns:        make(map[net.Conn]int),
		udpSessions:     make(map[string]*udpSession),
		udpAssociations: make(map[string]udpProxyAssociation),
		generationID:    generationID,
		sessions:        newL4SessionTracker(generationID, registrar, false),
	}
}

func assertL4GenerationPipeClosed(t *testing.T, peer net.Conn, name string) {
	t.Helper()
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, 1)
	if _, err := peer.Read(buffer); err == nil {
		t.Fatalf("%s connection remained open", name)
	} else if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		t.Fatalf("%s connection remained open until timeout", name)
	}
}

func assertL4GenerationPipeOpen(t *testing.T, peer net.Conn, name string) {
	t.Helper()
	_ = peer.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	buffer := make([]byte, 1)
	_, err := peer.Read(buffer)
	if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("%s connection read error = %v, want timeout", name, err)
	}
	_ = peer.SetReadDeadline(time.Time{})
}

func l4GenerationDrainStatus(t *testing.T, snapshot model.GenerationDrainSnapshot, generationID string) model.GenerationDrainStatus {
	t.Helper()
	for _, status := range snapshot.Generations {
		if status.GenerationID == generationID {
			return status
		}
	}
	t.Fatalf("generation %s not found in %+v", generationID, snapshot)
	return model.GenerationDrainStatus{}
}
