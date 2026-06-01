package traffic

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic/hosttraffic"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
)

const (
	testRuntimeMetaTrafficStatsInterval       = "traffic_stats_interval"
	testRuntimeMetaLastTrafficStatsReportUnix = "last_traffic_stats_report_unix"
)

func TestReporterReturnsExplicitEmptyStatsWhenTrafficDisabled(t *testing.T) {
	reporter := NewReporter(ReporterConfig{
		Enabled: func() bool { return false },
		SnapshotNonZero: func() map[string]any {
			t.Fatal("SnapshotNonZero should not be called when traffic is disabled")
			return nil
		},
		Now: fixedTrafficReportTime,
	})

	report, err := reporter.TrafficReport(context.Background(), nil)
	if err != nil {
		t.Fatalf("TrafficReport() error = %v", err)
	}
	if !report.StatsPresent {
		t.Fatal("StatsPresent = false, want true for disabled traffic reporting")
	}
	if report.Stats == nil || len(report.Stats) != 0 {
		t.Fatalf("Stats = %#v, want explicit empty stats", report.Stats)
	}
	if len(report.RuntimeMetadata) != 0 {
		t.Fatalf("RuntimeMetadata = %+v, want none when disabled", report.RuntimeMetadata)
	}
}

func TestReporterSuppressesStatsBeforeTrafficStatsIntervalElapses(t *testing.T) {
	reporter := NewReporter(ReporterConfig{
		Enabled: func() bool { return true },
		SnapshotNonZero: func() map[string]any {
			t.Fatal("SnapshotNonZero should not be called before interval elapses")
			return nil
		},
		Now: fixedTrafficReportTime,
	})
	meta := map[string]string{
		testRuntimeMetaTrafficStatsInterval:       "1h",
		testRuntimeMetaLastTrafficStatsReportUnix: strconv.FormatInt(fixedTrafficReportTime().Add(-time.Minute).Unix(), 10),
	}

	report, err := reporter.TrafficReport(context.Background(), meta)
	if err != nil {
		t.Fatalf("TrafficReport() error = %v", err)
	}
	if report.Stats != nil || report.StatsPresent {
		t.Fatalf("report = %+v, want no stats before interval elapses", report)
	}
	if len(report.RuntimeMetadata) != 0 {
		t.Fatalf("RuntimeMetadata = %+v, want none when suppressed", report.RuntimeMetadata)
	}
}

func TestReporterReportsInternalTrafficStatsAfterIntervalElapses(t *testing.T) {
	reporter := NewReporter(ReporterConfig{
		Enabled:         func() bool { return true },
		SnapshotNonZero: nonzeroTrafficSnapshot,
		Now:             fixedTrafficReportTime,
	})
	meta := map[string]string{
		testRuntimeMetaTrafficStatsInterval:       "1s",
		testRuntimeMetaLastTrafficStatsReportUnix: strconv.FormatInt(fixedTrafficReportTime().Add(-time.Hour).Unix(), 10),
	}

	report, err := reporter.TrafficReport(context.Background(), meta)
	if err != nil {
		t.Fatalf("TrafficReport() error = %v", err)
	}
	if !report.StatsPresent || report.Stats == nil {
		t.Fatalf("report = %+v, want traffic stats", report)
	}
	trafficStats := report.Stats["traffic"].(map[string]any)
	total := trafficStats["total"].(map[string]uint64)
	if total["rx_bytes"] != 11 || total["tx_bytes"] != 22 {
		t.Fatalf("total stats = %+v, want 11/22", total)
	}
	if got := report.RuntimeMetadata[testRuntimeMetaLastTrafficStatsReportUnix]; got != strconv.FormatInt(fixedTrafficReportTime().Unix(), 10) {
		t.Fatalf("last report metadata = %q, want fixed report time", got)
	}
}

func TestReporterMergesHostTrafficSnapshotIntoPayload(t *testing.T) {
	reporter := NewReporter(ReporterConfig{
		Enabled:         func() bool { return true },
		SnapshotNonZero: nonzeroTrafficSnapshot,
		HostSnapshotter: staticHostSnapshotter{snapshot: hosttraffic.Snapshot{
			BootID: "boot-123",
			Total:  hosttraffic.Counters{RXBytes: 1000, TXBytes: 2000},
			Interfaces: map[string]hosttraffic.Counters{
				"eth0": {RXBytes: 900, TXBytes: 1800},
			},
		}},
		Now: fixedTrafficReportTime,
	})

	report, err := reporter.TrafficReport(context.Background(), nil)
	if err != nil {
		t.Fatalf("TrafficReport() error = %v", err)
	}
	trafficStats := report.Stats["traffic"].(map[string]any)
	host := trafficStats["host"].(map[string]any)
	total := host["total"].(map[string]any)
	if total["rx_bytes"] != uint64(1000) || total["tx_bytes"] != uint64(2000) {
		t.Fatalf("host total stats = %+v, want 1000/2000", total)
	}
	if host["boot_id"] != "boot-123" {
		t.Fatalf("host boot_id = %#v, want boot-123", host["boot_id"])
	}
	ifaces := host["interfaces"].(map[string]any)
	eth0 := ifaces["eth0"].(map[string]any)
	if eth0["rx_bytes"] != uint64(900) || eth0["tx_bytes"] != uint64(1800) {
		t.Fatalf("eth0 stats = %+v, want 900/1800", eth0)
	}
}

func TestReporterOnlyAddsLastReportMetadataWhenIntervalActiveAndStatsReported(t *testing.T) {
	tests := []struct {
		name string
		meta map[string]string
		want bool
	}{
		{
			name: "no interval",
			meta: nil,
			want: false,
		},
		{
			name: "invalid interval",
			meta: map[string]string{testRuntimeMetaTrafficStatsInterval: "bad"},
			want: false,
		},
		{
			name: "active interval",
			meta: map[string]string{testRuntimeMetaTrafficStatsInterval: "1s"},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reporter := NewReporter(ReporterConfig{
				Enabled:         func() bool { return true },
				SnapshotNonZero: nonzeroTrafficSnapshot,
				Now:             fixedTrafficReportTime,
			})

			report, err := reporter.TrafficReport(context.Background(), tc.meta)
			if err != nil {
				t.Fatalf("TrafficReport() error = %v", err)
			}
			_, got := report.RuntimeMetadata[testRuntimeMetaLastTrafficStatsReportUnix]
			if got != tc.want {
				t.Fatalf("last report metadata present = %v, want %v; metadata=%+v", got, tc.want, report.RuntimeMetadata)
			}
		})
	}
}

func TestReporterPendingTimestampPersistsOnlyAfterSuccessfulSync(t *testing.T) {
	lastReportedAt := fixedTrafficReportTime().Add(-time.Hour).Unix()
	st := store.NewInMemory()
	if err := st.SaveRuntimeState(store.RuntimeState{Metadata: map[string]string{
		testRuntimeMetaTrafficStatsInterval:       "1s",
		testRuntimeMetaLastTrafficStatsReportUnix: strconv.FormatInt(lastReportedAt, 10),
	}}); err != nil {
		t.Fatalf("SaveRuntimeState() error = %v", err)
	}
	reporter := NewReporter(ReporterConfig{
		Enabled:         func() bool { return true },
		SnapshotNonZero: nonzeroTrafficSnapshot,
		Now:             fixedTrafficReportTime,
	})
	controller := &core.SyncController{
		Store:      st,
		Runtime:    core.NewRuntime(),
		SyncClient: trafficSyncClient{err: errors.New("sync failed")},
		Traffic:    reporter,
	}

	plan, err := controller.BuildSyncPlan(context.Background(), model.Snapshot{Revision: 7})
	if err != nil {
		t.Fatalf("BuildSyncPlan() error = %v", err)
	}
	if plan.RuntimeMetadata[testRuntimeMetaLastTrafficStatsReportUnix] != strconv.FormatInt(fixedTrafficReportTime().Unix(), 10) {
		t.Fatalf("BuildSyncPlan RuntimeMetadata = %+v, want pending timestamp", plan.RuntimeMetadata)
	}
	if err := controller.PerformSyncPlan(context.Background(), plan); err == nil {
		t.Fatal("PerformSyncPlan() error = nil, want sync failure")
	}
	state, err := st.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState() error = %v", err)
	}
	if state.Metadata[testRuntimeMetaLastTrafficStatsReportUnix] != strconv.FormatInt(lastReportedAt, 10) {
		t.Fatalf("last report timestamp = %q, want unchanged %d after failure", state.Metadata[testRuntimeMetaLastTrafficStatsReportUnix], lastReportedAt)
	}

	controller.SyncClient = trafficSyncClient{snapshot: model.Snapshot{DesiredVersion: "ok", Revision: 7}}
	if err := controller.PerformSyncPlan(context.Background(), plan); err != nil {
		t.Fatalf("PerformSyncPlan() success path error = %v", err)
	}
	state, err = st.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState() after success error = %v", err)
	}
	if state.Metadata[testRuntimeMetaLastTrafficStatsReportUnix] != strconv.FormatInt(fixedTrafficReportTime().Unix(), 10) {
		t.Fatalf("last report timestamp = %q, want persisted pending timestamp", state.Metadata[testRuntimeMetaLastTrafficStatsReportUnix])
	}
}

func TestModuleExposesTrafficStatsCapability(t *testing.T) {
	module := NewModule()
	capabilities := module.Capabilities(model.Snapshot{})
	want := []string{"traffic_stats"}
	var got []string
	for _, capability := range capabilities {
		if capability.Enabled {
			got = append(got, capability.Name)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("enabled capabilities = %+v, want %+v", got, want)
	}
}

func fixedTrafficReportTime() time.Time {
	return time.Unix(1700000000, 0)
}

func nonzeroTrafficSnapshot() map[string]any {
	return map[string]any{
		"traffic": map[string]any{
			"total": map[string]uint64{
				"rx_bytes": 11,
				"tx_bytes": 22,
			},
		},
	}
}

type staticHostSnapshotter struct {
	snapshot hosttraffic.Snapshot
	err      error
}

func (s staticHostSnapshotter) Snapshot() (hosttraffic.Snapshot, error) {
	return s.snapshot, s.err
}

type trafficSyncClient struct {
	snapshot model.Snapshot
	err      error
}

func (c trafficSyncClient) Sync(context.Context, control.SyncRequest) (model.Snapshot, error) {
	return c.snapshot, c.err
}
