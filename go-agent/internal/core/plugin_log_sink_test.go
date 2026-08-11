package core

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
)

func TestPluginRuntimeLogSinkPersistsBeforeHeartbeatAndSurvivesRestart(t *testing.T) {
	pluginprocess.DrainRuntimeLogEvents()
	t.Cleanup(func() { pluginprocess.DrainRuntimeLogEvents() })
	directory := t.TempDir()
	store, err := NewFilesystem(directory)
	if err != nil {
		t.Fatal(err)
	}
	sink := NewPluginRuntimeLogSink(store)
	if sink == nil {
		t.Fatal("durable plugin log sink is unavailable")
	}
	if err := sink.CaptureRuntimeLogEvent(pluginLogSinkTestEvent(1, "captured before heartbeat")); err != nil {
		t.Fatal(err)
	}
	if cached := pluginprocess.SnapshotRuntimeLogEvents(); len(cached) != 0 {
		t.Fatalf("durably captured event leaked into volatile cache: %+v", cached)
	}
	restarted, err := NewFilesystem(directory)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := restarted.PendingPluginLogReports()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Sequence != 1 || pending[0].Entries[0].Message != "captured before heartbeat" {
		t.Fatalf("restart pending = %+v", pending)
	}
}

func TestPluginRuntimeLogSinkSaturationFailsClosedWithoutDroppingPending(t *testing.T) {
	store := NewInMemory()
	sink := NewPluginRuntimeLogSink(store)
	for index := 1; index <= model.MaxPendingPluginLogReports; index++ {
		if err := sink.CaptureRuntimeLogEvent(pluginLogSinkTestEvent(index, fmt.Sprintf("event-%d", index))); err != nil {
			t.Fatalf("capture %d: %v", index, err)
		}
	}
	if err := sink.CaptureRuntimeLogEvent(pluginLogSinkTestEvent(model.MaxPendingPluginLogReports+1, "must not drop")); err == nil {
		t.Fatal("saturated durable sink silently accepted an unpersisted event")
	}
	pending, err := store.PendingPluginLogReports()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != model.MaxPendingPluginLogReports || pending[0].Entries[0].Message != "event-1" || pending[len(pending)-1].Entries[0].Message != fmt.Sprintf("event-%d", model.MaxPendingPluginLogReports) {
		t.Fatalf("saturation changed pending outbox: count=%d first=%+v last=%+v", len(pending), pending[0], pending[len(pending)-1])
	}
}

func pluginLogSinkTestEvent(index int, message string) pluginprocess.RuntimeLogEvent {
	return pluginprocess.RuntimeLogEvent{
		CaptureID: fmt.Sprintf("%064x", index),
		Identity: pluginprocess.RuntimeLogIdentity{
			Revision: 7, ProviderGenerationID: "generation-7", InstanceID: "instance-7", PluginID: "example.rpc", AgentID: "edge-7",
			PackageDigest: strings.Repeat("a", 64), ArtifactDigest: strings.Repeat("b", 64),
		},
		Entry: pluginprocess.RuntimeLogEntry{Level: "info", Message: message},
	}
}
