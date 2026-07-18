package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestDDNSSweepAndHeartbeatAreSerializedWithFreshRerun(t *testing.T) {
	store := newRegressionDDNSStore(regressionDDNSRow("203.0.113.10", storage.DdnsStatus{}))
	cf := &blockingRegressionCF{
		firstEntered: make(chan struct{}),
		releaseFirst: make(chan struct{}),
		calls:        make(chan string, 4),
	}
	svc := NewDDNSService(regressionDDNSConfig(), store, cf, time.Now)
	svc.Start()
	t.Cleanup(svc.Close)
	releaseFirst := func() {
		select {
		case <-cf.releaseFirst:
		default:
			close(cf.releaseFirst)
		}
	}
	t.Cleanup(releaseFirst)

	svc.ReconcileAfterHeartbeat(t.Context(), "edge-1")
	select {
	case <-cf.firstEntered:
	case <-time.After(time.Second):
		t.Fatal("first DDNS request did not start")
	}
	store.setIPv4("203.0.113.20")
	svc.sweep(t.Context())
	releaseFirst()

	var contents []string
	for len(contents) < 2 {
		select {
		case content := <-cf.calls:
			contents = append(contents, content)
		case <-time.After(time.Second):
			t.Fatalf("DDNS rerun calls = %v", contents)
		}
	}
	if contents[0] != "203.0.113.10" || contents[1] != "203.0.113.20" {
		t.Fatalf("DDNS contents = %v", contents)
	}
	if cf.maxConcurrent.Load() != 1 {
		t.Fatalf("maximum concurrent Cloudflare calls = %d", cf.maxConcurrent.Load())
	}
	for index := 0; index < 2; index++ {
		select {
		case <-store.writes:
		case <-time.After(time.Second):
			t.Fatal("DDNS status writes did not complete")
		}
	}
	if status := store.status(); status.LastResolvedIPv4 != "203.0.113.20" {
		t.Fatalf("final DDNS status = %+v", status)
	}
}

func TestDDNSErrorRetainsLastSuccessTime(t *testing.T) {
	store := newRegressionDDNSStore(regressionDDNSRow("203.0.113.10", storage.DdnsStatus{
		Status: "ok", LastSuccessAtUnix: 1234, LastResolvedIPv4: "203.0.113.9",
	}))
	svc := NewDDNSService(regressionDDNSConfig(), store, regressionErrorCF{}, func() time.Time { return time.Unix(2000, 0) })
	svc.reconcileAgent(t.Context(), "edge-1")
	if status := store.status(); status.Status != "error" || status.LastSuccessAtUnix != 1234 {
		t.Fatalf("DDNS error status = %+v", status)
	}
}

func TestDDNSDispatcherRequeuesDirtyAgentBehindPendingWork(t *testing.T) {
	dispatcher := newDDNSDispatcher(4)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	processed := make(chan string, 4)
	var calls atomic.Int32
	dispatcher.start(ctx, func(_ context.Context, agentID string) {
		processed <- agentID
		if calls.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
		}
	})
	t.Cleanup(dispatcher.stop)

	dispatcher.enqueue("edge-a")
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first dispatcher call did not start")
	}
	dispatcher.enqueue("edge-b")
	dispatcher.enqueue("edge-a")
	close(releaseFirst)

	got := make([]string, 0, 3)
	for len(got) < 3 {
		select {
		case agentID := <-processed:
			got = append(got, agentID)
		case <-time.After(time.Second):
			t.Fatalf("dispatcher order = %v", got)
		}
	}
	if got[0] != "edge-a" || got[1] != "edge-b" || got[2] != "edge-a" {
		t.Fatalf("dispatcher order = %v, want [edge-a edge-b edge-a]", got)
	}
}

func TestDDNSSweepRetainsWorkWhenQueueIsFull(t *testing.T) {
	configJSON, err := json.Marshal(storage.DDNSConfig{Enabled: true, Domain: "media.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]storage.AgentRow, 5)
	for index := range rows {
		rows[index] = storage.AgentRow{ID: fmt.Sprintf("edge-%d", index+1), DdnsConfigJSON: string(configJSON)}
	}
	store := &regressionDDNSListStore{rows: rows}
	dispatcher := newDDNSDispatcher(2)
	svc := &DDNSService{store: store, dispatcher: dispatcher}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	sweepDone := make(chan struct{})
	go func() {
		svc.sweep(ctx)
		close(sweepDone)
	}()
	// Let the sweep fill the queue before a worker begins draining it.
	time.Sleep(20 * time.Millisecond)
	processed := make(chan string, len(rows))
	dispatcher.start(ctx, func(_ context.Context, agentID string) { processed <- agentID })
	t.Cleanup(dispatcher.stop)

	select {
	case <-sweepDone:
	case <-ctx.Done():
		t.Fatal("sweep did not finish after the dispatcher started")
	}
	seen := make(map[string]bool, len(rows))
	for len(seen) < len(rows) {
		select {
		case agentID := <-processed:
			seen[agentID] = true
		case <-ctx.Done():
			t.Fatalf("processed agents = %v, want all %d", seen, len(rows))
		}
	}
}

type regressionDDNSStore struct {
	mu     sync.Mutex
	row    storage.AgentRow
	writes chan struct{}
}

type regressionDDNSListStore struct {
	rows []storage.AgentRow
}

func (s *regressionDDNSListStore) ListAgents(context.Context) ([]storage.AgentRow, error) {
	return append([]storage.AgentRow(nil), s.rows...), nil
}

func (*regressionDDNSListStore) UpdateDdnsStatusColumn(context.Context, string, string) error {
	return nil
}

func newRegressionDDNSStore(row storage.AgentRow) *regressionDDNSStore {
	return &regressionDDNSStore{row: row, writes: make(chan struct{}, 8)}
}

func (s *regressionDDNSStore) ListAgents(context.Context) ([]storage.AgentRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return []storage.AgentRow{s.row}, nil
}

func (s *regressionDDNSStore) UpdateDdnsStatusColumn(_ context.Context, _ string, value string) error {
	s.mu.Lock()
	s.row.DdnsStatusJSON = value
	s.mu.Unlock()
	s.writes <- struct{}{}
	return nil
}

func (s *regressionDDNSStore) setIPv4(value string) {
	s.mu.Lock()
	s.row.LastSeenIPv4 = value
	s.mu.Unlock()
}

func (s *regressionDDNSStore) status() storage.DdnsStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	var status storage.DdnsStatus
	_ = json.Unmarshal([]byte(s.row.DdnsStatusJSON), &status)
	return status
}

func regressionDDNSRow(ipv4 string, status storage.DdnsStatus) storage.AgentRow {
	configJSON, _ := json.Marshal(storage.DDNSConfig{
		Enabled: true, Domain: "media.example.com",
		IPv4: storage.DDNSFamily{Enabled: true, Source: "public_api"},
	})
	statusJSON, _ := json.Marshal(status)
	return storage.AgentRow{ID: "edge-1", DdnsConfigJSON: string(configJSON), DdnsStatusJSON: string(statusJSON), LastSeenIPv4: ipv4}
}

func regressionDDNSConfig() config.Config {
	return config.Config{DDNS: config.DDNSRuntimeConfig{Enabled: true, Token: "token", TTL: 120, Interval: time.Hour}}
}

type blockingRegressionCF struct {
	firstEntered  chan struct{}
	releaseFirst  chan struct{}
	calls         chan string
	count         atomic.Int32
	concurrent    atomic.Int32
	maxConcurrent atomic.Int32
}

func (c *blockingRegressionCF) EnsureRecord(_ context.Context, _, _, _, content string, _ int) (cloudflareRecordOutcome, error) {
	concurrent := c.concurrent.Add(1)
	defer c.concurrent.Add(-1)
	for {
		maximum := c.maxConcurrent.Load()
		if concurrent <= maximum || c.maxConcurrent.CompareAndSwap(maximum, concurrent) {
			break
		}
	}
	if c.count.Add(1) == 1 {
		close(c.firstEntered)
		<-c.releaseFirst
	}
	c.calls <- content
	return cloudflareRecordOutcome{Action: "updated"}, nil
}

type regressionErrorCF struct{}

func (regressionErrorCF) EnsureRecord(context.Context, string, string, string, string, int) (cloudflareRecordOutcome, error) {
	return cloudflareRecordOutcome{}, errors.New("temporary Cloudflare failure")
}
