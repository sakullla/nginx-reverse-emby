package cutover

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	controlplanehttp "github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/http"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/localagent"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type cutoverHarness struct {
	t       *testing.T
	cfg     config.Config
	fixture cutoverFixture

	httpClient *http.Client

	panelServer *httptest.Server
	httpBackend *httptest.Server
	stopTCP     func()

	apiStore     *storage.SQLiteStore
	runtimeStore *storage.SQLiteStore
	cancelRun    context.CancelFunc
	runDone      chan error
}

func newCutoverHarness(t *testing.T) *cutoverHarness {
	t.Helper()
	return newCutoverHarnessWithOptions(t, cutoverHarnessOptions{})
}

type cutoverHarnessOptions struct {
	enableRelayPath            bool
	disableL4Path              bool
	preferredHTTPFrontendPort  int
	preferredL4FrontendPort    int
	preferredRelayListenerPort int
	startupAttempts            int
}

func newCutoverHarnessWithOptions(t *testing.T, options cutoverHarnessOptions) *cutoverHarness {
	t.Helper()

	attempts := options.startupAttempts
	if attempts <= 0 {
		attempts = 3
	}

	attemptOptions := options
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		harness, err := tryStartCutoverHarness(t, attemptOptions)
		if err == nil {
			return harness
		}
		lastErr = err
		attemptOptions.preferredHTTPFrontendPort = 0
		attemptOptions.preferredL4FrontendPort = 0
		attemptOptions.preferredRelayListenerPort = 0
	}

	t.Fatalf("newCutoverHarnessWithOptions() failed after %d attempts: %v", attempts, lastErr)
	return nil
}

func tryStartCutoverHarness(t *testing.T, options cutoverHarnessOptions) (*cutoverHarness, error) {
	t.Helper()

	harness := &cutoverHarness{
		t:          t,
		httpClient: &http.Client{Timeout: 2 * time.Second},
	}
	cleanupOnFailure := true
	defer func() {
		if cleanupOnFailure {
			_ = harness.closeNoFail()
		}
	}()

	httpBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("backend:http"))
	}))
	harness.httpBackend = httpBackend

	tcpBackendAddr, stopTCP := startTCPEchoBackend(t)
	harness.stopTCP = stopTCP

	reservations, err := reserveHarnessPorts(options)
	if err != nil {
		return nil, err
	}
	defer reservations.release()

	fixture := buildCutoverFixture(t, cutoverFixtureInput{
		httpBackendURL:    httpBackend.URL,
		tcpBackendAddr:    tcpBackendAddr,
		enableRelayPath:   options.enableRelayPath,
		disableL4Path:     options.disableL4Path,
		httpFrontendPort:  reservations.httpFrontendPort,
		l4FrontendPort:    reservations.l4FrontendPort,
		relayListenerPort: reservations.relayListenerPort,
	})
	harness.fixture = fixture

	cfg := config.Default()
	cfg.DataDir = fixture.dataDir
	cfg.PanelToken = fixture.panelToken
	cfg.RegisterToken = fixture.registerToken
	cfg.EnableLocalAgent = true
	cfg.LocalAgentID = fixture.localAgentID
	cfg.LocalAgentName = "Local Agent"
	cfg.HeartbeatInterval = 25 * time.Millisecond
	harness.cfg = cfg

	apiStore, err := storage.NewSQLiteStore(cfg.DataDir, cfg.LocalAgentID)
	if err != nil {
		return nil, fmt.Errorf("NewSQLiteStore(api): %w", err)
	}
	harness.apiStore = apiStore

	relayService := service.NewRelayListenerService(cfg, apiStore)
	if err := relayService.Bootstrap(t.Context()); err != nil {
		return nil, fmt.Errorf("RelayListenerService.Bootstrap(): %w", err)
	}

	runtimeStore, err := storage.NewSQLiteStore(cfg.DataDir, cfg.LocalAgentID)
	if err != nil {
		return nil, fmt.Errorf("NewSQLiteStore(runtime): %w", err)
	}
	harness.runtimeStore = runtimeStore

	runtime, err := localagent.NewRuntime(cfg, runtimeStore)
	if err != nil {
		return nil, fmt.Errorf("localagent.NewRuntime(): %w", err)
	}

	agentService := service.NewAgentService(cfg, apiStore)
	certificateService := service.NewCertificateService(cfg, apiStore)
	agentService.SetLocalApplyTrigger(runtime.SyncNow)
	certificateService.SetLocalApplyTrigger(runtime.SyncNow)

	router, err := controlplanehttp.NewRouter(controlplanehttp.Dependencies{
		Config:               cfg,
		SystemService:        service.NewSystemService(cfg),
		AgentService:         agentService,
		RuleService:          service.NewRuleService(cfg, apiStore),
		L4RuleService:        service.NewL4RuleService(cfg, apiStore),
		VersionPolicyService: service.NewVersionPolicyService(apiStore),
		ClientPackageService: service.NewClientPackageService(apiStore),
		RelayListenerService: relayService,
		CertificateService:   certificateService,
	})
	if err != nil {
		return nil, fmt.Errorf("http.NewRouter(): %w", err)
	}
	panelServer := httptest.NewServer(router)
	harness.panelServer = panelServer

	// Release reservation listeners right before the runtime takes ownership of the ports.
	reservations.release()

	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	harness.cancelRun = cancelRun
	harness.runDone = runDone
	go func() {
		runDone <- runtime.Start(runCtx)
	}()

	if _, err := waitForStableLocalApplyState(apiStore, fixture.expectedRevision, 4*time.Second); err != nil {
		return nil, err
	}

	cleanupOnFailure = false
	return harness, nil
}

func (h *cutoverHarness) Close() {
	if h == nil {
		return
	}
	if err := h.closeNoFail(); err != nil {
		h.t.Fatalf("cutover harness close error: %v", err)
	}
}

func (h *cutoverHarness) closeNoFail() error {
	if h == nil {
		return nil
	}

	var closeErrs []string

	if h.cancelRun != nil {
		h.cancelRun()
		h.cancelRun = nil
	}
	if h.runDone != nil {
		select {
		case err := <-h.runDone:
			if err != nil && !errors.Is(err, context.Canceled) {
				closeErrs = append(closeErrs, fmt.Sprintf("embedded runtime exited with error: %v", err))
			}
		case <-time.After(3 * time.Second):
			closeErrs = append(closeErrs, "timed out waiting for embedded runtime shutdown")
		}
		h.runDone = nil
	}

	if h.panelServer != nil {
		h.panelServer.Close()
		h.panelServer = nil
	}
	if h.httpBackend != nil {
		h.httpBackend.Close()
		h.httpBackend = nil
	}
	if h.stopTCP != nil {
		h.stopTCP()
		h.stopTCP = nil
	}
	if h.runtimeStore != nil {
		if err := h.runtimeStore.Close(); err != nil {
			closeErrs = append(closeErrs, fmt.Sprintf("runtime store close: %v", err))
		}
		h.runtimeStore = nil
	}
	if h.apiStore != nil {
		if err := h.apiStore.Close(); err != nil {
			closeErrs = append(closeErrs, fmt.Sprintf("api store close: %v", err))
		}
		h.apiStore = nil
	}

	if len(closeErrs) > 0 {
		return errors.New(strings.Join(closeErrs, "; "))
	}
	return nil
}

func startTCPEchoBackend(t *testing.T) (string, func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() {
					_ = c.Close()
				}()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	return ln.Addr().String(), func() {
		_ = ln.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			// keep shutdown best-effort in constructor error paths
		}
	}
}

func frontendAddress(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d/", port)
}

type harnessPortReservations struct {
	httpListener  net.Listener
	l4Listener    net.Listener
	relayListener net.Listener

	httpFrontendPort  int
	l4FrontendPort    int
	relayListenerPort int
}

func reserveHarnessPorts(options cutoverHarnessOptions) (*harnessPortReservations, error) {
	httpListener, httpPort, err := reserveSingleHarnessPort(options.preferredHTTPFrontendPort)
	if err != nil {
		return nil, err
	}
	l4Listener, l4Port, err := reserveSingleHarnessPort(options.preferredL4FrontendPort)
	if err != nil {
		_ = httpListener.Close()
		return nil, err
	}
	relayListener, relayPort, err := reserveSingleHarnessPort(options.preferredRelayListenerPort)
	if err != nil {
		_ = l4Listener.Close()
		_ = httpListener.Close()
		return nil, err
	}
	return &harnessPortReservations{
		httpListener:      httpListener,
		l4Listener:        l4Listener,
		relayListener:     relayListener,
		httpFrontendPort:  httpPort,
		l4FrontendPort:    l4Port,
		relayListenerPort: relayPort,
	}, nil
}

func reserveSingleHarnessPort(preferred int) (net.Listener, int, error) {
	address := "127.0.0.1:0"
	if preferred > 0 {
		address = fmt.Sprintf("127.0.0.1:%d", preferred)
	}
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return nil, 0, err
	}
	return ln, ln.Addr().(*net.TCPAddr).Port, nil
}

func (r *harnessPortReservations) release() {
	if r == nil {
		return
	}
	if r.httpListener != nil {
		_ = r.httpListener.Close()
		r.httpListener = nil
	}
	if r.l4Listener != nil {
		_ = r.l4Listener.Close()
		r.l4Listener = nil
	}
	if r.relayListener != nil {
		_ = r.relayListener.Close()
		r.relayListener = nil
	}
}
