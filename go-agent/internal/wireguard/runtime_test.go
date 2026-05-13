package wireguard

import (
	"context"
	"net"
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

type recordingFactory struct {
	created            []*fakeRuntime
	runtimeByProfileID map[int]*fakeRuntime
}

func (f *recordingFactory) Create(_ context.Context, cfg Config) (Runtime, error) {
	if f.runtimeByProfileID == nil {
		f.runtimeByProfileID = make(map[int]*fakeRuntime)
	}
	runtime := &fakeRuntime{profileID: cfg.ID}
	f.created = append(f.created, runtime)
	f.runtimeByProfileID[cfg.ID] = runtime
	return runtime, nil
}

type fakeRuntime struct {
	profileID int
	closed    bool
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
	r.closed = true
	return nil
}

var errFakeRuntime = &net.OpError{Op: "fake"}
