//go:build integration

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
)

const (
	testRuntimeMetaTrafficStatsInterval       = "traffic_stats_interval"
	testRuntimeMetaLastTrafficStatsReportUnix = "last_traffic_stats_report_unix"
)

func TestIntegrationReporterSuppressesStatsBeforeTrafficStatsIntervalElapses(t *testing.T) {
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

func TestIntegrationReporterReportsInternalTrafficStatsAfterIntervalElapses(t *testing.T) {
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

func TestIntegrationReporterPendingTimestampPersistsOnlyAfterSuccessfulSync(t *testing.T) {
	lastReportedAt := fixedTrafficReportTime().Add(-time.Hour).Unix()
	st := core.NewInMemory()
	if err := st.SaveRuntimeState(core.RuntimeState{Metadata: map[string]string{
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

func TestIntegrationModuleExposesTrafficStatsCapability(t *testing.T) {
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

type trafficSyncClient struct {
	snapshot model.Snapshot
	err      error
}

func (c trafficSyncClient) Sync(context.Context, control.SyncRequest) (model.Snapshot, error) {
	return c.snapshot, c.err
}
