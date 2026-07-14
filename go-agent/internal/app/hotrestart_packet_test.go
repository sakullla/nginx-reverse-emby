package app

import (
	"errors"
	"reflect"
	"testing"
)

type recordingProcessPacketAuthority struct {
	order       *[]string
	flushErr    error
	finalizeErr error
}

func (a recordingProcessPacketAuthority) BeginForwarding() error {
	*a.order = append(*a.order, "forward")
	return nil
}
func (a recordingProcessPacketAuthority) Pause() error {
	*a.order = append(*a.order, "packet-pause")
	return nil
}
func (a recordingProcessPacketAuthority) FlushForwarding() error {
	*a.order = append(*a.order, "flush")
	return a.flushErr
}
func (a recordingProcessPacketAuthority) Resume() error {
	*a.order = append(*a.order, "packet-resume")
	return nil
}
func (a recordingProcessPacketAuthority) FinalizeForwarding() error {
	*a.order = append(*a.order, "finalize")
	return a.finalizeErr
}

func TestHotRestartResourceProcessUsesForwardingBeforePhysicalAuthority(t *testing.T) {
	var order []string
	process := &hotRestartResourceProcess{
		hotRestartProcess: &recordingHotRestartProcess{order: &order},
		streams:           recordingProcessStreamAuthority{order: &order},
		packets:           recordingProcessPacketAuthority{order: &order},
	}
	if err := process.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := process.TransferAuthority(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := []string{"pause", "forward", "activate", "packet-pause", "flush", "authority", "finalize"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("resource handoff order = %v, want %v", order, want)
	}
}

func TestHotRestartResourceActivationFailureAbortsBeforeParentRollback(t *testing.T) {
	var order []string
	process := &hotRestartResourceProcess{
		hotRestartProcess: &recordingHotRestartProcess{order: &order, activateErr: errors.New("activation failed")},
		streams:           recordingProcessStreamAuthority{order: &order},
		packets:           recordingProcessPacketAuthority{order: &order},
	}
	if err := process.Activate(t.Context()); err == nil {
		t.Fatal("Activate() succeeded")
	}
	want := []string{"pause", "forward", "activate", "abort", "packet-resume", "resume"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("resource rollback order = %v, want %v", order, want)
	}
}

func TestHotRestartResourceFlushFailureAbortsBeforeParentRollback(t *testing.T) {
	var order []string
	flushErr := errors.New("forwarding barrier failed")
	process := &hotRestartResourceProcess{
		hotRestartProcess: &recordingHotRestartProcess{order: &order},
		streams:           recordingProcessStreamAuthority{order: &order},
		packets:           recordingProcessPacketAuthority{order: &order, flushErr: flushErr},
	}
	if err := process.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := process.TransferAuthority(t.Context()); !errors.Is(err, flushErr) {
		t.Fatalf("TransferAuthority() error = %v, want barrier failure", err)
	}
	want := []string{"pause", "forward", "activate", "packet-pause", "flush", "abort", "packet-resume", "resume"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("barrier rollback order = %v, want %v", order, want)
	}
}

func TestHotRestartResourcePostAckCleanupFailureRemainsCommitted(t *testing.T) {
	var order []string
	cleanupErr := errors.New("forwarder close failed")
	process := &hotRestartResourceProcess{
		hotRestartProcess: &recordingHotRestartProcess{order: &order},
		streams:           recordingProcessStreamAuthority{order: &order},
		packets:           recordingProcessPacketAuthority{order: &order, finalizeErr: cleanupErr},
	}
	if err := process.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := process.TransferAuthority(t.Context()); err != nil {
		t.Fatalf("TransferAuthority() reported committed cleanup debt as transfer failure: %v", err)
	}
	if err := process.Wait(); !errors.Is(err, cleanupErr) {
		t.Fatalf("Wait() error = %v, want cleanup debt", err)
	}
	if err := process.Abort(); !errors.Is(err, cleanupErr) {
		t.Fatalf("Abort() error = %v, want committed cleanup debt", err)
	}
	want := []string{"pause", "forward", "activate", "packet-pause", "flush", "authority", "finalize", "wait", "abort"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("post-ack cleanup order = %v, want %v", order, want)
	}
}
