package service

import (
	"context"
	"encoding/json"
	"errors"
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

type regressionDDNSStore struct {
	mu     sync.Mutex
	row    storage.AgentRow
	writes chan struct{}
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
