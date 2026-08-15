package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
)

type transientPluginLogStore struct {
	*InMemory
	failures int
}

func (store *transientPluginLogStore) EnqueuePluginLogReports(batchID string, reports []model.PluginRuntimeLogReport) ([]model.PluginRuntimeLogReport, error) {
	if store.failures > 0 {
		store.failures--
		return nil, fmt.Errorf("injected transient fsync failure")
	}
	return store.InMemory.EnqueuePluginLogReports(batchID, reports)
}

type livePluginLogProcess struct{ done chan error }

type pluginLogTestSandbox struct{}

func (pluginLogTestSandbox) Available() bool                       { return true }
func (pluginLogTestSandbox) Provider() string                      { return "test-kernel-boundary" }
func (pluginLogTestSandbox) Validate(pluginprocess.Security) error { return nil }
func (pluginLogTestSandbox) Configure(*exec.Cmd, pluginprocess.Security) (func() error, func() error, func(int) error, error) {
	return func() error { return nil }, func() error { return nil }, func(int) error { return nil }, nil
}

func (process *livePluginLogProcess) PID() int               { return 77 }
func (process *livePluginLogProcess) Wait() error            { return <-process.done }
func (process *livePluginLogProcess) Signal(os.Signal) error { return nil }
func (process *livePluginLogProcess) Kill() error            { return nil }

type saturatingPluginLogRunner struct {
	process      *livePluginLogProcess
	writeStarted chan struct{}
	writeDone    chan error
}

func (runner saturatingPluginLogRunner) Start(_ context.Context, _ pluginprocess.InstanceSpec, _ pluginprocess.Sandbox, output io.Writer) (pluginprocess.ManagedProcess, func() error, error) {
	go func() {
		close(runner.writeStarted)
		_, err := io.WriteString(output, "live line after saturation\n")
		runner.writeDone <- err
	}()
	return runner.process, func() error { return nil }, nil
}

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
	if err := sink.CaptureRuntimeLogEvent(t.Context(), pluginLogSinkTestEvent(1, "captured before heartbeat")); err != nil {
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

func TestPluginRuntimeLogSinkSaturationBackpressuresUntilExactACK(t *testing.T) {
	store := NewInMemory()
	sink := NewPluginRuntimeLogSink(store)
	for index := 1; index <= model.MaxPendingPluginLogReports; index++ {
		if err := sink.CaptureRuntimeLogEvent(t.Context(), pluginLogSinkTestEvent(index, fmt.Sprintf("event-%d", index))); err != nil {
			t.Fatalf("capture %d: %v", index, err)
		}
	}
	pending, err := store.PendingPluginLogReports()
	if err != nil {
		t.Fatal(err)
	}
	captureDone := make(chan error, 1)
	captureStarted := make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	go func() {
		close(captureStarted)
		captureDone <- sink.CaptureRuntimeLogEvent(ctx, pluginLogSinkTestEvent(model.MaxPendingPluginLogReports+1, "must not drop"))
	}()
	<-captureStarted
	select {
	case err := <-captureDone:
		t.Fatalf("saturated capture did not backpressure: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if err := store.AcknowledgePluginLogReports(pending[:1]); err != nil {
		t.Fatal(err)
	}
	if err := <-captureDone; err != nil {
		t.Fatal(err)
	}
	pending, err = store.PendingPluginLogReports()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != model.MaxPendingPluginLogReports || pending[0].Entries[0].Message != "event-2" || pending[len(pending)-1].Entries[0].Message != "must not drop" || pending[len(pending)-1].Sequence != model.MaxPendingPluginLogReports+1 {
		t.Fatalf("saturation changed pending outbox: count=%d first=%+v last=%+v", len(pending), pending[0], pending[len(pending)-1])
	}
}

func TestPluginRuntimeLogSinkRetriesTransientFailureWithExactCaptureIdentity(t *testing.T) {
	store := &transientPluginLogStore{InMemory: NewInMemory(), failures: 2}
	sink := NewPluginRuntimeLogSink(store)
	if err := sink.CaptureRuntimeLogEvent(t.Context(), pluginLogSinkTestEvent(1, "transiently blocked")); err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingPluginLogReports()
	if err != nil || len(pending) != 1 || pending[0].Sequence != 1 || pending[0].Entries[0].Message != "transiently blocked" {
		t.Fatalf("transient retry changed capture: pending=%+v err=%v", pending, err)
	}
}

func TestPluginRuntimeLogSinkBackpressureIsCancellationAware(t *testing.T) {
	store := NewInMemory()
	sink := NewPluginRuntimeLogSink(store)
	for index := 1; index <= model.MaxPendingPluginLogReports; index++ {
		if err := sink.CaptureRuntimeLogEvent(t.Context(), pluginLogSinkTestEvent(index, fmt.Sprintf("prefill-%d", index))); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		done <- sink.CaptureRuntimeLogEvent(ctx, pluginLogSinkTestEvent(model.MaxPendingPluginLogReports+1, "cancelled line"))
	}()
	<-started
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled backpressure error = %v", err)
	}
	pending, err := store.PendingPluginLogReports()
	if err != nil || len(pending) != model.MaxPendingPluginLogReports {
		t.Fatalf("cancelled backpressure changed durable outbox: pending=%d err=%v", len(pending), err)
	}
}

func TestLivePluginProcessSaturationRecoversAfterExactACKWithoutLineLoss(t *testing.T) {
	store := NewInMemory()
	sink := NewPluginRuntimeLogSink(store)
	for index := 1; index <= model.MaxPendingPluginLogReports; index++ {
		if err := sink.CaptureRuntimeLogEvent(t.Context(), pluginLogSinkTestEvent(index, fmt.Sprintf("prefill-%d", index))); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := store.PendingPluginLogReports()
	if err != nil {
		t.Fatal(err)
	}
	process := &livePluginLogProcess{done: make(chan error, 1)}
	runner := saturatingPluginLogRunner{process: process, writeStarted: make(chan struct{}), writeDone: make(chan error, 1)}
	supervisor := pluginprocess.NewSupervisor(runner, pluginLogTestSandbox{}, io.Discard)
	supervisor.SetRuntimeLogSink(sink)
	t.Cleanup(func() { _ = supervisor.Close(t.Context()) })
	handle, err := supervisor.StartOnce(t.Context(), pluginprocess.InstanceSpec{
		ID: "live-saturation", Executable: "synthetic", RuntimeLogIdentity: pluginLogSinkTestEvent(999, "unused").Identity,
	})
	if err != nil {
		t.Fatal(err)
	}
	<-runner.writeStarted
	select {
	case err := <-runner.writeDone:
		t.Fatalf("live process writer escaped backpressure before ACK: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if err := store.AcknowledgePluginLogReports(pending[:1]); err != nil {
		t.Fatal(err)
	}
	if err := <-runner.writeDone; err != nil {
		t.Fatal(err)
	}
	process.done <- nil
	select {
	case <-handle.Done():
	case <-time.After(time.Second):
		t.Fatal("live process did not terminate after recovered write")
	}
	pending, err = store.PendingPluginLogReports()
	if err != nil || len(pending) != model.MaxPendingPluginLogReports || pending[len(pending)-1].Entries[0].Message != "live line after saturation" || pending[len(pending)-1].Sequence != model.MaxPendingPluginLogReports+1 {
		t.Fatalf("live recovery pending = %+v, %v", pending, err)
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
