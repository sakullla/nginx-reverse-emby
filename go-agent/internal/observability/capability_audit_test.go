//go:build !integration

package observability

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/hostapi"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestCapabilityAuditJournalDurableRecoveryLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "capabilities.jsonl")
	redactedEvent := capabilityAuditTestEvent("secret=must-not-leak", "provider secret=must-not-leak")

	journal, err := newCapabilityAuditJournal(path, 2_048, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Audit(t.Context(), redactedEvent); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(data), "must-not-leak") || !strings.Contains(string(data), `"plugin_id":"invalid"`) || !strings.HasSuffix(string(data), "\n") {
		t.Fatalf("durable redacted journal = %q, error = %v", data, err)
	}
	if err := journal.Audit(t.Context(), redactedEvent); err == nil {
		t.Fatal("closed journal acknowledged an audit event")
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprint(file, `{"partial":"must-not-survive"}`); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := newCapabilityAuditJournal(path, 2_048, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Audit(t.Context(), capabilityAuditTestEvent("official.policy", "allowed")); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil || strings.Contains(string(data), "must-not-survive") || strings.Count(string(data), "\n") != 2 {
		t.Fatalf("recovered journal = %q, error = %v", data, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	rotating, err := newCapabilityAuditJournal(path, info.Size()+1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := rotating.Audit(t.Context(), capabilityAuditTestEvent("official.policy", "rotated")); err != nil {
		t.Fatal(err)
	}
	if err := rotating.Close(); err != nil {
		t.Fatal(err)
	}
	for _, retained := range []string{path, path + ".1"} {
		if retainedInfo, err := os.Stat(retained); err != nil || retainedInfo.Size() == 0 {
			t.Fatalf("retained audit %s: size=%v error=%v", retained, retainedInfo, err)
		}
	}
	if _, err := os.Stat(path + ".2"); !os.IsNotExist(err) {
		t.Fatalf("audit retention exceeded one archive: %v", err)
	}
}

type capabilityAuditWriterFixture struct {
	mu         sync.Mutex
	started    chan struct{}
	release    chan struct{}
	closeBlock chan struct{}
	err        error
	batches    int
	events     int
	startOnce  sync.Once
}

func (writer *capabilityAuditWriterFixture) writeBatch(events []hostapi.AuditEvent, _ time.Time) error {
	writer.startOnce.Do(func() {
		if writer.started != nil {
			close(writer.started)
		}
	})
	if writer.release != nil {
		<-writer.release
	}
	writer.mu.Lock()
	writer.batches++
	writer.events += len(events)
	writer.mu.Unlock()
	return writer.err
}

func (*capabilityAuditWriterFixture) maintain(time.Time) error { return nil }

func (writer *capabilityAuditWriterFixture) Close() error {
	if writer.closeBlock != nil {
		<-writer.closeBlock
	}
	return nil
}

func TestAsyncCapabilityAuditorIsNonBlockingAndBounded(t *testing.T) {
	writer := &capabilityAuditWriterFixture{started: make(chan struct{}), release: make(chan struct{})}
	cfg := model.DefaultCapabilityAuditConfig()
	cfg.Enabled, cfg.QueueSize, cfg.BatchSize, cfg.FlushInterval = true, 1, 1, time.Minute
	auditor, err := newAsyncCapabilityAuditor(filepath.Join(t.TempDir(), "audit", "plugin-capabilities.jsonl"), cfg, capabilityAuditAsyncOptions{
		freeSpace: func(string) (uint64, error) { return math.MaxUint64, nil },
		open: func(string, model.CapabilityAuditConfig, time.Time) (capabilityAuditBatchWriter, error) {
			return writer, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	event := capabilityAuditTestEvent("official.policy", "allowed")
	started := time.Now()
	if err := auditor.Audit(t.Context(), event); err != nil || time.Since(started) > 50*time.Millisecond {
		t.Fatalf("first enqueue blocked or failed: elapsed=%v err=%v", time.Since(started), err)
	}
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("background writer did not receive first event")
	}
	if err := auditor.Audit(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	if err := auditor.Audit(t.Context(), event); err != nil || time.Since(started) > 50*time.Millisecond {
		t.Fatalf("full queue blocked or failed: elapsed=%v err=%v", time.Since(started), err)
	}
	if status := auditor.Status(); status.Dropped != 1 || status.Queued != 2 || status.LastError != "" {
		t.Fatalf("bounded queue status = %+v", status)
	}
	close(writer.release)
	waitCapabilityAuditCondition(t, func() bool { return !auditor.Status().LastFlush.IsZero() && auditor.Status().Queued == 0 })
	if err := auditor.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCapabilityAuditQueueSanitizesBeforeRetention(t *testing.T) {
	secret := strings.Repeat("secret", 100_000)
	event := capabilityAuditTestEvent(secret, secret)
	event.Call.QuotaMetric = secret
	bounded := boundedCapabilityAuditEvent(event)
	if bounded.Call.PluginID != "invalid" || bounded.Reason != "invalid" || bounded.Call.QuotaMetric != "" || len(bounded.Call.PluginID)+len(bounded.Reason) > 32 {
		t.Fatalf("queued event retained unbounded or sensitive input: %+v", bounded)
	}
}

func TestAsyncCapabilityAuditorWriteFailureAndLowSpaceRecover(t *testing.T) {
	for _, test := range []struct {
		name      string
		writerErr error
		openErr   error
		lowFirst  bool
	}{
		{name: "writer failure", writerErr: errors.New("writer unavailable")},
		{name: "nonwritable path", openErr: errors.New("open permission denied")},
		{name: "low space recovers", lowFirst: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := &capabilityAuditWriterFixture{err: test.writerErr}
			var low atomic.Bool
			low.Store(test.lowFirst)
			cfg := model.DefaultCapabilityAuditConfig()
			cfg.Enabled, cfg.QueueSize, cfg.BatchSize, cfg.FlushInterval = true, 4, 1, 10*time.Millisecond
			auditor, err := newAsyncCapabilityAuditor(filepath.Join(t.TempDir(), "audit", "plugin-capabilities.jsonl"), cfg, capabilityAuditAsyncOptions{
				freeSpace: func(string) (uint64, error) {
					if low.Load() {
						return 0, nil
					}
					return math.MaxUint64, nil
				},
				open: func(string, model.CapabilityAuditConfig, time.Time) (capabilityAuditBatchWriter, error) {
					if test.openErr != nil {
						return nil, test.openErr
					}
					return writer, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := auditor.Audit(t.Context(), capabilityAuditTestEvent("official.policy", "allowed")); err != nil {
				t.Fatal(err)
			}
			waitCapabilityAuditCondition(t, func() bool { return auditor.Status().Dropped >= 1 })
			failed := auditor.Status()
			if failed.WriteErrors == 0 || failed.LastError == "" || failed.Queued != 0 || failed.LowSpace != test.lowFirst {
				t.Fatalf("failure status = %+v", failed)
			}
			if test.lowFirst {
				low.Store(false)
				if err := auditor.Audit(t.Context(), capabilityAuditTestEvent("official.policy", "allowed")); err != nil {
					t.Fatal(err)
				}
				waitCapabilityAuditCondition(t, func() bool { return !auditor.Status().LastFlush.IsZero() })
				recovered := auditor.Status()
				if recovered.LowSpace || recovered.LastError != "" {
					t.Fatalf("recovered status = %+v", recovered)
				}
			}
			if err := auditor.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAsyncCapabilityAuditorCloseTimeoutIsBounded(t *testing.T) {
	writer := &capabilityAuditWriterFixture{started: make(chan struct{}), release: make(chan struct{})}
	cfg := model.DefaultCapabilityAuditConfig()
	cfg.Enabled, cfg.QueueSize, cfg.BatchSize, cfg.FlushInterval, cfg.CloseTimeout = true, 1, 1, time.Minute, 20*time.Millisecond
	auditor, err := newAsyncCapabilityAuditor(filepath.Join(t.TempDir(), "audit", "plugin-capabilities.jsonl"), cfg, capabilityAuditAsyncOptions{
		freeSpace: func(string) (uint64, error) { return math.MaxUint64, nil },
		open: func(string, model.CapabilityAuditConfig, time.Time) (capabilityAuditBatchWriter, error) {
			return writer, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = auditor.Audit(t.Context(), capabilityAuditTestEvent("official.policy", "allowed"))
	<-writer.started
	started := time.Now()
	if err := auditor.Close(); err == nil || time.Since(started) > 250*time.Millisecond {
		t.Fatalf("Close() error=%v elapsed=%v", err, time.Since(started))
	}
	close(writer.release)
	select {
	case <-auditor.done:
	case <-time.After(time.Second):
		t.Fatal("auditor did not finish after blocked writer recovered")
	}
}

func TestCapabilityAuditRetentionCapacityAndOwnership(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "plugin-capabilities.jsonl")
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	files := map[string]struct {
		size int
		age  time.Duration
	}{
		path:                                    {size: 80, age: time.Minute},
		path + ".1":                             {size: 80, age: 25 * time.Hour},
		path + ".2":                             {size: 80, age: 2 * time.Hour},
		path + ".99":                            {size: 80, age: 48 * time.Hour},
		filepath.Join(directory, "dataset.bin"): {size: 80, age: 48 * time.Hour},
	}
	for name, spec := range files {
		if err := os.WriteFile(name, bytes.Repeat([]byte("x"), spec.size), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(name, now.Add(-spec.age), now.Add(-spec.age)); err != nil {
			t.Fatal(err)
		}
	}
	if err := cleanupCapabilityAuditFiles(path, 3, 24*time.Hour, 100, now, true); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{path + ".1", path + ".2"} {
		if _, err := os.Stat(removed); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("owned expired/over-capacity file remained: %s err=%v", removed, err)
		}
	}
	for _, kept := range []string{path, path + ".99", filepath.Join(directory, "dataset.bin")} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("cleanup removed non-owned or active file %s: %v", kept, err)
		}
	}
}

func TestCapabilityAuditPeriodicMaintenanceExpiresActiveHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "plugin-capabilities.jsonl")
	now := time.Now().UTC()
	journal, err := newCapabilityAuditJournalWithLimits(path, 2_048, 8_192, 3, time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.writeBatch([]hostapi.AuditEvent{capabilityAuditTestEvent("official.policy", "allowed")}, now); err != nil {
		t.Fatal(err)
	}
	if err := journal.writeBatch([]hostapi.AuditEvent{capabilityAuditTestEvent("official.policy", "allowed")}, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.Count(string(data), "\n") != 1 {
		t.Fatalf("periodic retention kept expired active history: %q %v", data, err)
	}
	for _, archive := range []string{path + ".1", path + ".2", path + ".3"} {
		if _, err := os.Stat(archive); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expired archive remained after periodic maintenance: %s %v", archive, err)
		}
	}
}

func TestCapabilityAuditRecoveryBoundsOversizedPartialTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "plugin-capabilities.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data := append([]byte("{\"valid\":true}\n"), bytes.Repeat([]byte("x"), 1<<20)...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	journal, err := newCapabilityAuditJournal(path, 2<<20, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != 0 {
		t.Fatalf("oversized partial recovery size=%v err=%v", info, err)
	}
}

func waitCapabilityAuditCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("capability audit condition was not met")
}

func capabilityAuditTestEvent(pluginID, reason string) hostapi.AuditEvent {
	return hostapi.AuditEvent{Call: pluginsdk.HostCapabilityCall{
		PluginID: pluginID, InstanceID: "instance-1", Generation: "generation-1",
		Capability: pluginsdk.CapabilityPolicyTrustedSource,
		Actor:      pluginsdk.HostActor{ID: "actor-1", ResourceGroupID: "group-1"},
		Target:     pluginsdk.HostTarget{Kind: "plugin.instance", ID: "instance-1", ResourceGroupID: "group-1"},
	}, Outcome: "allowed", Reason: reason}
}
