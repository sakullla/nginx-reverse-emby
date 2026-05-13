package wireguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestManagerReusesSameFingerprintRuntime(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{Factory: factory.Create})
	defer manager.Close()

	profile := validProfile()
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	first := factory.created[0]
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(second) error = %v", err)
	}
	if len(factory.created) != 1 {
		t.Fatalf("created runtimes = %d, want 1", len(factory.created))
	}
	if first.closed {
		t.Fatal("runtime was closed despite matching fingerprint")
	}
}

func TestManagerReplacesChangedConfigRuntime(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{Factory: factory.Create})
	defer manager.Close()

	profile := validProfile()
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	first := factory.created[0]

	profile.Peers[0].Endpoint = "peer.example.com:51821"
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(changed) error = %v", err)
	}
	if len(factory.created) != 2 {
		t.Fatalf("created runtimes = %d, want 2", len(factory.created))
	}
	if !first.closed {
		t.Fatal("stale runtime was not closed")
	}
}

func TestManagerCreatesReplacementBeforeClosingExistingRuntime(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{Factory: factory.Create})
	defer manager.Close()

	profile := validProfile()
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	first := factory.created[0]

	profile.Peers[0].Endpoint = "peer.example.com:51821"
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(changed) error = %v", err)
	}
	if len(factory.events) < 3 {
		t.Fatalf("events = %v, want at least initial create, replacement create, close", factory.events)
	}
	if factory.events[1] != "create:7" || factory.events[2] != "close:7" {
		t.Fatalf("events = %v, want replacement create before close", factory.events)
	}
	if !first.closed {
		t.Fatal("stale runtime was not closed")
	}
}

func TestManagerPreservesExistingRuntimeAfterReplacementCreateFails(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{Factory: factory.Create})
	defer manager.Close()

	profile := validProfile()
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	first := factory.created[0]

	factory.createErr = errors.New("bind failed")
	profile.Peers[0].Endpoint = "peer.example.com:51821"
	err := manager.Apply(context.Background(), []model.WireGuardProfile{profile})
	if err == nil || !strings.Contains(err.Error(), "bind failed") {
		t.Fatalf("Apply(changed) error = %v, want bind failed", err)
	}
	if first.closed {
		t.Fatal("existing runtime was closed after replacement creation failed")
	}
	got, ok := manager.Runtime(profile.ID)
	if !ok {
		t.Fatal("existing runtime was unregistered after replacement creation failed")
	}
	if got != first {
		t.Fatal("manager did not preserve the original runtime after replacement creation failed")
	}
}

func TestManagerPreflightsAndRollsBackSamePortReplacementFailure(t *testing.T) {
	t.Parallel()

	var preflightCalls int
	var replacementAttempts int
	var rollback *fakeRuntime
	factory := &recordingFactory{}
	factory.createFunc = func(_ context.Context, cfg Config) (Runtime, error) {
		switch cfg.Peers[0].Endpoint {
		case "peer.example.com:51820":
			return factory.newRuntime(cfg), nil
		case "peer.example.com:51821":
			replacementAttempts++
			if replacementAttempts == 1 {
				return nil, errors.New("address already in use")
			}
			return nil, errors.New("device setup failed")
		default:
			return nil, fmt.Errorf("unexpected endpoint %q", cfg.Peers[0].Endpoint)
		}
	}
	manager := NewManager(ManagerOptions{
		Factory: factory.Create,
		Preflight: func(context.Context, Config) error {
			preflightCalls++
			return nil
		},
	})
	defer manager.Close()

	profile := validProfile()
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	first := factory.created[0]

	factory.createFunc = func(_ context.Context, cfg Config) (Runtime, error) {
		switch cfg.Peers[0].Endpoint {
		case "peer.example.com:51821":
			replacementAttempts++
			if replacementAttempts == 1 {
				return nil, errors.New("address already in use")
			}
			return nil, errors.New("device setup failed")
		case "peer.example.com:51820":
			rollback = factory.newRuntime(cfg)
			return rollback, nil
		default:
			return nil, fmt.Errorf("unexpected endpoint %q", cfg.Peers[0].Endpoint)
		}
	}
	profile.Peers[0].Endpoint = "peer.example.com:51821"
	err := manager.Apply(context.Background(), []model.WireGuardProfile{profile})
	if err == nil || !strings.Contains(err.Error(), "device setup failed") {
		t.Fatalf("Apply(changed) error = %v, want device setup failed", err)
	}
	if preflightCalls != 1 {
		t.Fatalf("preflight calls = %d, want 1", preflightCalls)
	}
	if !first.closed {
		t.Fatal("existing same-port runtime was not closed before replacement retry")
	}
	if rollback == nil || rollback.closed {
		t.Fatalf("rollback runtime = %+v, want active rollback", rollback)
	}
	got, ok := manager.Runtime(profile.ID)
	if !ok {
		t.Fatal("manager has no runtime after same-port replacement failure")
	}
	if got != rollback {
		t.Fatal("manager did not restore the previous runtime after same-port replacement failure")
	}
}

func TestManagerPrepareRetriesSamePortReplacementAfterClosingExistingRuntime(t *testing.T) {
	t.Parallel()

	var preflightCalls int
	var replacementAttempts int
	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{
		Factory: factory.Create,
		Preflight: func(context.Context, Config) error {
			preflightCalls++
			return nil
		},
	})
	defer manager.Close()

	profile := validProfile()
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	first := factory.created[0]

	factory.createFunc = func(_ context.Context, cfg Config) (Runtime, error) {
		if cfg.Peers[0].Endpoint != "peer.example.com:51821" {
			return nil, fmt.Errorf("unexpected endpoint %q", cfg.Peers[0].Endpoint)
		}
		replacementAttempts++
		if replacementAttempts == 1 {
			return nil, errors.New("address already in use")
		}
		return factory.newRuntime(cfg), nil
	}
	profile.Peers[0].Endpoint = "peer.example.com:51821"
	transaction, err := manager.Prepare(context.Background(), []model.WireGuardProfile{profile})
	if err != nil {
		t.Fatalf("Prepare(changed) error = %v", err)
	}
	defer transaction.Rollback()

	if preflightCalls != 1 {
		t.Fatalf("preflight calls = %d, want 1", preflightCalls)
	}
	if replacementAttempts != 2 {
		t.Fatalf("replacement attempts = %d, want 2", replacementAttempts)
	}
	if !first.closed {
		t.Fatal("existing same-port runtime was not closed before replacement retry")
	}
	prepared, ok := transaction.Runtime(profile.ID)
	if !ok {
		t.Fatal("prepared transaction has no runtime")
	}
	if prepared == first {
		t.Fatal("prepared transaction reused the closed runtime")
	}
	if got, ok := manager.Runtime(profile.ID); !ok || got != prepared {
		t.Fatal("manager did not expose prepared same-port runtime before commit")
	}
}

func TestManagerPrepareRollbackRestoresSamePortReplacement(t *testing.T) {
	t.Parallel()

	var replacementAttempts int
	var rollback *fakeRuntime
	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{
		Factory:   factory.Create,
		Preflight: func(context.Context, Config) error { return nil },
	})
	defer manager.Close()

	profile := validProfile()
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	first := factory.created[0]

	factory.createFunc = func(_ context.Context, cfg Config) (Runtime, error) {
		switch cfg.Peers[0].Endpoint {
		case "peer.example.com:51821":
			replacementAttempts++
			if replacementAttempts == 1 {
				return nil, errors.New("address already in use")
			}
			return factory.newRuntime(cfg), nil
		case "peer.example.com:51820":
			rollback = factory.newRuntime(cfg)
			return rollback, nil
		default:
			return nil, fmt.Errorf("unexpected endpoint %q", cfg.Peers[0].Endpoint)
		}
	}
	profile.Peers[0].Endpoint = "peer.example.com:51821"
	transaction, err := manager.Prepare(context.Background(), []model.WireGuardProfile{profile})
	if err != nil {
		t.Fatalf("Prepare(changed) error = %v", err)
	}
	prepared, ok := transaction.Runtime(profile.ID)
	if !ok {
		t.Fatal("prepared transaction has no runtime")
	}

	transaction.Rollback()

	if !first.closed {
		t.Fatal("original runtime was not closed during same-port replacement")
	}
	if preparedRuntime, ok := prepared.(*fakeRuntime); !ok || !preparedRuntime.closed {
		t.Fatalf("prepared runtime closed = %v, want true", ok && preparedRuntime.closed)
	}
	if rollback == nil || rollback.closed {
		t.Fatalf("rollback runtime = %+v, want active rollback", rollback)
	}
	got, ok := manager.Runtime(profile.ID)
	if !ok {
		t.Fatal("manager has no runtime after prepared transaction rollback")
	}
	if got != rollback {
		t.Fatal("manager did not restore previous runtime after prepared transaction rollback")
	}
}

func TestManagerPrepareFailureAfterSamePortReplacementRestoresOldRuntime(t *testing.T) {
	t.Parallel()

	var replacementAttempts int
	var rollback *fakeRuntime
	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{
		Factory:   factory.Create,
		Preflight: func(context.Context, Config) error { return nil },
	})
	defer manager.Close()

	firstProfile := validProfile()
	secondProfile := validProfile()
	secondProfile.ID = 8
	secondProfile.ListenPort = 51822
	secondProfile.Peers[0].Endpoint = "peer.example.com:51822"
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{firstProfile}); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	first := factory.created[0]

	factory.createFunc = func(_ context.Context, cfg Config) (Runtime, error) {
		switch cfg.ID {
		case firstProfile.ID:
			switch cfg.Peers[0].Endpoint {
			case "peer.example.com:51821":
				replacementAttempts++
				if replacementAttempts == 1 {
					return nil, errors.New("address already in use")
				}
				return factory.newRuntime(cfg), nil
			case "peer.example.com:51820":
				rollback = factory.newRuntime(cfg)
				return rollback, nil
			default:
				return nil, fmt.Errorf("unexpected endpoint %q", cfg.Peers[0].Endpoint)
			}
		case secondProfile.ID:
			return nil, errors.New("second profile failed")
		default:
			return nil, fmt.Errorf("unexpected profile %d", cfg.ID)
		}
	}
	firstProfile.Peers[0].Endpoint = "peer.example.com:51821"
	_, err := manager.Prepare(context.Background(), []model.WireGuardProfile{firstProfile, secondProfile})
	if err == nil || !strings.Contains(err.Error(), "second profile failed") {
		t.Fatalf("Prepare() error = %v, want second profile failed", err)
	}

	if !first.closed {
		t.Fatal("original runtime was not closed during same-port replacement")
	}
	if rollback == nil || rollback.closed {
		t.Fatalf("rollback runtime = %+v, want active rollback", rollback)
	}
	got, ok := manager.Runtime(firstProfile.ID)
	if !ok {
		t.Fatal("manager has no runtime after later profile prepare failure")
	}
	if got != rollback {
		t.Fatal("manager did not restore previous runtime after later profile prepare failure")
	}
}

func TestManagerClosesUnusedRuntime(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{Factory: factory.Create})
	defer manager.Close()

	firstProfile := validProfile()
	secondProfile := validProfile()
	secondProfile.ID = 8

	if err := manager.Apply(context.Background(), []model.WireGuardProfile{firstProfile, secondProfile}); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	firstRuntime := factory.runtimeByProfileID[7]
	secondRuntime := factory.runtimeByProfileID[8]
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{secondProfile}); err != nil {
		t.Fatalf("Apply(remove) error = %v", err)
	}
	if !firstRuntime.closed {
		t.Fatal("unused runtime was not closed")
	}
	if secondRuntime.closed {
		t.Fatal("active runtime was closed")
	}
}

func TestManagerDisablesProfile(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	manager := NewManager(ManagerOptions{Factory: factory.Create})
	defer manager.Close()

	profile := validProfile()
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(initial) error = %v", err)
	}
	profile.Enabled = false
	if err := manager.Apply(context.Background(), []model.WireGuardProfile{profile}); err != nil {
		t.Fatalf("Apply(disabled) error = %v", err)
	}
	if len(factory.created) != 1 {
		t.Fatalf("created runtimes = %d, want 1", len(factory.created))
	}
	if !factory.created[0].closed {
		t.Fatal("runtime was not closed after disable")
	}
}

func TestIPCConfigResolvesDNSEndpoint(t *testing.T) {
	t.Parallel()

	cfg, err := NormalizeConfig(validProfile())
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	resolve := func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("2001:db8::7"), net.ParseIP("203.0.113.7")}, nil
	}

	ipc, err := ipcConfig(context.Background(), cfg, resolve)
	if err != nil {
		t.Fatalf("ipcConfig() error = %v", err)
	}
	if !strings.Contains(ipc, "endpoint=[2001:db8::7]:51820\n") {
		t.Fatalf("ipc endpoint was not resolved to first IP: %q", ipc)
	}
	if strings.Contains(ipc, "peer.example.com") {
		t.Fatalf("ipc endpoint still contains DNS host: %q", ipc)
	}
}

func TestIPCConfigKeepsIPEndpointWithoutResolver(t *testing.T) {
	t.Parallel()

	profile := validProfile()
	profile.Peers[0].Endpoint = "203.0.113.20:51820"
	cfg, err := NormalizeConfig(profile)
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	resolveCalls := 0

	ipc, err := ipcConfig(context.Background(), cfg, func(context.Context, string) ([]net.IP, error) {
		resolveCalls++
		return nil, errors.New("unexpected resolver call")
	})
	if err != nil {
		t.Fatalf("ipcConfig() error = %v", err)
	}
	if resolveCalls != 0 {
		t.Fatalf("resolver calls = %d, want 0", resolveCalls)
	}
	if !strings.Contains(ipc, "endpoint=203.0.113.20:51820\n") {
		t.Fatalf("ipc endpoint = %q, want IP endpoint", ipc)
	}
}

func TestIPCConfigUnmapsResolvedIPv4Endpoint(t *testing.T) {
	t.Parallel()

	cfg, err := NormalizeConfig(validProfile())
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}

	ipc, err := ipcConfig(context.Background(), cfg, func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.7")}, nil
	})
	if err != nil {
		t.Fatalf("ipcConfig() error = %v", err)
	}
	if !strings.Contains(ipc, "endpoint=203.0.113.7:51820\n") {
		t.Fatalf("ipc endpoint = %q, want unmapped IPv4 endpoint", ipc)
	}
}

func TestIPCConfigReturnsResolverErrorForDNSEndpoint(t *testing.T) {
	t.Parallel()

	cfg, err := NormalizeConfig(validProfile())
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	resolveErr := errors.New("no such host")

	_, err = ipcConfig(context.Background(), cfg, func(context.Context, string) ([]net.IP, error) {
		return nil, resolveErr
	})
	if err == nil || !strings.Contains(err.Error(), "resolve endpoint peer.example.com") {
		t.Fatalf("ipcConfig() error = %v, want resolver context", err)
	}
}

func TestNetstackRuntimeCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	closer := &countingCloser{}
	runtime := &netstackRuntime{tun: closer}

	if err := runtime.Close(); err != nil {
		t.Fatalf("Close(first) error = %v", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close(second) error = %v", err)
	}
	if closer.count != 1 {
		t.Fatalf("tun close count = %d, want 1", closer.count)
	}
}

type recordingFactory struct {
	created            []*fakeRuntime
	runtimeByProfileID map[int]*fakeRuntime
	events             []string
	createErr          error
	createFunc         func(context.Context, Config) (Runtime, error)
}

func (f *recordingFactory) Create(ctx context.Context, cfg Config) (Runtime, error) {
	f.events = append(f.events, "create:"+strconv.Itoa(cfg.ID))
	if f.createFunc != nil {
		return f.createFunc(ctx, cfg)
	}
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.newRuntime(cfg), nil
}

func (f *recordingFactory) newRuntime(cfg Config) *fakeRuntime {
	if f.runtimeByProfileID == nil {
		f.runtimeByProfileID = make(map[int]*fakeRuntime)
	}
	runtime := &fakeRuntime{profileID: cfg.ID, endpoint: cfg.Peers[0].Endpoint, onClose: func(profileID int) {
		f.events = append(f.events, "close:"+strconv.Itoa(profileID))
	}}
	f.created = append(f.created, runtime)
	f.runtimeByProfileID[cfg.ID] = runtime
	return runtime
}

type fakeRuntime struct {
	profileID int
	endpoint  string
	closed    bool
	onClose   func(int)
}

func (r *fakeRuntime) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errFakeRuntime
}

func (r *fakeRuntime) ListenTCP(context.Context, string) (net.Listener, error) {
	return nil, errFakeRuntime
}

func (r *fakeRuntime) ListenUDP(context.Context, string) (PacketConn, error) {
	return nil, errFakeRuntime
}

func (r *fakeRuntime) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	if r.onClose != nil {
		r.onClose(r.profileID)
	}
	return nil
}

type countingCloser struct {
	count int
}

func (c *countingCloser) Close() error {
	c.count++
	return nil
}

var errFakeRuntime = &net.OpError{Op: "fake"}
