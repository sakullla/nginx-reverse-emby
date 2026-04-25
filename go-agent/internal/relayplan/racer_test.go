package relayplan

import (
	"context"
	"errors"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
)

type fakePathDialer struct {
	mu       sync.Mutex
	results  map[string]fakeDialResult
	calls    [][]int
	canceled map[string]bool
}

type fakeDialResult struct {
	conn         net.Conn
	err          error
	delay        time.Duration
	ignoreCancel bool
}

func newFakePathDialer() *fakePathDialer {
	return &fakePathDialer{results: map[string]fakeDialResult{}, canceled: map[string]bool{}}
}

func (d *fakePathDialer) set(path []int, result fakeDialResult) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.results[pathKeyForTest(path)] = result
}

func (d *fakePathDialer) DialPath(ctx context.Context, _ Request, path Path) (net.Conn, relay.DialResult, error) {
	d.mu.Lock()
	d.calls = append(d.calls, append([]int(nil), path.IDs...))
	result, ok := d.results[pathKeyForTest(path.IDs)]
	d.mu.Unlock()
	if !ok {
		<-ctx.Done()
		d.mu.Lock()
		d.canceled[pathKeyForTest(path.IDs)] = true
		d.mu.Unlock()
		return nil, relay.DialResult{}, ctx.Err()
	}
	if result.delay > 0 {
		if result.ignoreCancel {
			time.Sleep(result.delay)
		} else {
			select {
			case <-time.After(result.delay):
			case <-ctx.Done():
				d.mu.Lock()
				d.canceled[pathKeyForTest(path.IDs)] = true
				d.mu.Unlock()
				return nil, relay.DialResult{}, ctx.Err()
			}
		}
	}
	if result.err != nil {
		return nil, relay.DialResult{}, result.err
	}
	return result.conn, relay.DialResult{}, nil
}

func (d *fakePathDialer) wasCanceled(path []int) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.canceled[pathKeyForTest(path)]
}

func (d *fakePathDialer) calledPaths() [][]int {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([][]int, len(d.calls))
	for i, call := range d.calls {
		out[i] = append([]int(nil), call...)
	}
	return out
}

func TestRacerReturnsFirstSuccessfulPathAndCancelsLosers(t *testing.T) {
	dialer := newFakePathDialer()
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	dialer.set([]int{2}, fakeDialResult{conn: clientConn})
	racer := Racer{Dialer: dialer, Concurrency: 2, MaxPaths: 8}

	result, err := racer.Race(context.Background(), Request{
		Network: "tcp",
		Target:  "backend:443",
		Paths:   []Path{{IDs: []int{1}}, {IDs: []int{2}}},
	})
	if err != nil {
		t.Fatalf("Race() error = %v", err)
	}
	defer result.Conn.Close()
	if !reflect.DeepEqual(result.Selected.IDs, []int{2}) {
		t.Fatalf("selected path = %#v, want [2]", result.Selected.IDs)
	}
	if !waitForRelayplanCondition(200*time.Millisecond, func() bool {
		return dialer.wasCanceled([]int{1})
	}) {
		t.Fatal("loser path was not canceled")
	}
}

func TestRacerReturnsWinnerBeforeSlowLoserObservesCancellation(t *testing.T) {
	dialer := newFakePathDialer()
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	dialer.set([]int{1}, fakeDialResult{err: errors.New("slow loser"), delay: 300 * time.Millisecond, ignoreCancel: true})
	dialer.set([]int{2}, fakeDialResult{conn: clientConn, delay: 10 * time.Millisecond})
	racer := Racer{Dialer: dialer, Concurrency: 2, MaxPaths: 8}

	startedAt := time.Now()
	result, err := racer.Race(context.Background(), Request{
		Network: "tcp",
		Target:  "backend:443",
		Paths:   []Path{{IDs: []int{1}}, {IDs: []int{2}}},
	})
	elapsed := time.Since(startedAt)
	if err != nil {
		t.Fatalf("Race() error = %v", err)
	}
	defer result.Conn.Close()
	if !reflect.DeepEqual(result.Selected.IDs, []int{2}) {
		t.Fatalf("selected path = %#v, want [2]", result.Selected.IDs)
	}
	if elapsed >= 150*time.Millisecond {
		t.Fatalf("Race() returned after %s, want before slow loser delay", elapsed)
	}
}

func TestRacerReturnsAggregateErrorWhenAllPathsFail(t *testing.T) {
	dialer := newFakePathDialer()
	dialer.set([]int{1}, fakeDialResult{err: errors.New("first failed")})
	dialer.set([]int{2}, fakeDialResult{err: errors.New("second failed")})
	racer := Racer{Dialer: dialer, Concurrency: 2, MaxPaths: 8}

	_, err := racer.Race(context.Background(), Request{
		Network: "tcp",
		Target:  "backend:443",
		Paths:   []Path{{IDs: []int{1}}, {IDs: []int{2}}},
	})
	if err == nil || !errors.Is(err, ErrNoRelayPathSucceeded) {
		t.Fatalf("Race() error = %v, want ErrNoRelayPathSucceeded", err)
	}
}

func TestRacerOrdersPathsByAdaptiveObservations(t *testing.T) {
	dialer := newFakePathDialer()
	conn, peer := net.Pipe()
	defer peer.Close()
	dialer.set([]int{2}, fakeDialResult{conn: conn})
	cache := backends.NewCache(backends.Config{})
	scope := "relay_path|backend:443"
	cache.ObserveBackendSuccess(backends.BackendObservationKey(scope, PathKey("relay_path", []int{1}, "backend:443")), 80*time.Millisecond, 100*time.Millisecond, 128*1024)
	cache.ObserveBackendSuccess(backends.BackendObservationKey(scope, PathKey("relay_path", []int{2}, "backend:443")), 10*time.Millisecond, 20*time.Millisecond, 128*1024)
	racer := Racer{Dialer: dialer, Cache: cache, Concurrency: 1, MaxPaths: 8}

	result, err := racer.Race(context.Background(), Request{
		Network: "tcp",
		Target:  "backend:443",
		Paths: []Path{
			{IDs: []int{1}, Key: PathKey("relay_path", []int{1}, "backend:443")},
			{IDs: []int{2}, Key: PathKey("relay_path", []int{2}, "backend:443")},
		},
	})
	if err != nil {
		t.Fatalf("Race() error = %v", err)
	}
	defer result.Conn.Close()
	if !reflect.DeepEqual(result.Selected.IDs, []int{2}) {
		t.Fatalf("selected path = %#v, want [2]", result.Selected.IDs)
	}
	if got := dialer.calledPaths(); len(got) == 0 || !reflect.DeepEqual(got[0], []int{2}) {
		t.Fatalf("first dialed path = %#v, want [2]", got)
	}
}

func TestRacerSkipsBackedOffPathsWhenAlternatesAvailable(t *testing.T) {
	dialer := newFakePathDialer()
	conn, peer := net.Pipe()
	defer peer.Close()
	dialer.set([]int{2}, fakeDialResult{conn: conn})
	cache := backends.NewCache(backends.Config{})
	scope := "relay_path|backend:443"
	cache.ObserveBackendFailure(backends.BackendObservationKey(scope, PathKey("relay_path", []int{1}, "backend:443")))
	racer := Racer{Dialer: dialer, Cache: cache, Concurrency: 3, MaxPaths: 8}

	result, err := racer.Race(context.Background(), Request{
		Network: "tcp",
		Target:  "backend:443",
		Paths: []Path{
			{IDs: []int{1}, Key: PathKey("relay_path", []int{1}, "backend:443")},
			{IDs: []int{2}, Key: PathKey("relay_path", []int{2}, "backend:443")},
			{IDs: []int{3}, Key: PathKey("relay_path", []int{3}, "backend:443")},
		},
	})
	if err != nil {
		t.Fatalf("Race() error = %v", err)
	}
	defer result.Conn.Close()
	for _, call := range dialer.calledPaths() {
		if reflect.DeepEqual(call, []int{1}) {
			t.Fatalf("backed-off path was dialed: calls=%+v", dialer.calledPaths())
		}
	}
}

func TestRacerDialsBackedOffPathsWhenEveryPathBackedOff(t *testing.T) {
	dialer := newFakePathDialer()
	conn, peer := net.Pipe()
	defer peer.Close()
	dialer.set([]int{2}, fakeDialResult{conn: conn})
	cache := backends.NewCache(backends.Config{})
	scope := "relay_path|backend:443"
	for _, ids := range [][]int{{1}, {2}} {
		cache.ObserveBackendFailure(backends.BackendObservationKey(scope, PathKey("relay_path", ids, "backend:443")))
	}
	racer := Racer{Dialer: dialer, Cache: cache, Concurrency: 2, MaxPaths: 8}

	result, err := racer.Race(context.Background(), Request{
		Network: "tcp",
		Target:  "backend:443",
		Paths: []Path{
			{IDs: []int{1}, Key: PathKey("relay_path", []int{1}, "backend:443")},
			{IDs: []int{2}, Key: PathKey("relay_path", []int{2}, "backend:443")},
		},
	})
	if err != nil {
		t.Fatalf("Race() error = %v", err)
	}
	defer result.Conn.Close()
	if len(dialer.calledPaths()) == 0 {
		t.Fatal("expected backed-off paths to be dialed when all paths are backed off")
	}
}

func TestRacerObservesSuccessfulAndFailedPathAttempts(t *testing.T) {
	dialer := newFakePathDialer()
	conn, peer := net.Pipe()
	defer peer.Close()
	dialer.set([]int{1}, fakeDialResult{err: errors.New("first failed")})
	dialer.set([]int{2}, fakeDialResult{conn: conn, delay: time.Millisecond})
	cache := backends.NewCache(backends.Config{})
	racer := Racer{Dialer: dialer, Cache: cache, Concurrency: 1, MaxPaths: 8}

	result, err := racer.Race(context.Background(), Request{
		Network: "tcp",
		Target:  "backend:443",
		Paths: []Path{
			{IDs: []int{1}, Key: PathKey("relay_path", []int{1}, "backend:443")},
			{IDs: []int{2}, Key: PathKey("relay_path", []int{2}, "backend:443")},
		},
	})
	if err != nil {
		t.Fatalf("Race() error = %v", err)
	}
	defer result.Conn.Close()

	scope := "relay_path|backend:443"
	failedSummary := cache.Summary(backends.BackendObservationKey(scope, PathKey("relay_path", []int{1}, "backend:443")))
	if failedSummary.RecentFailed != 1 {
		t.Fatalf("failed path RecentFailed = %d, want 1", failedSummary.RecentFailed)
	}
	successSummary := cache.Summary(backends.BackendObservationKey(scope, PathKey("relay_path", []int{2}, "backend:443")))
	if successSummary.RecentSucceeded != 1 || !successSummary.HasLatency {
		t.Fatalf("success path summary = %+v, want observed success with latency", successSummary)
	}
}

func pathKeyForTest(path []int) string {
	return PathKey("test", path, "target")
}

func waitForRelayplanCondition(timeout time.Duration, condition func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return condition()
}
