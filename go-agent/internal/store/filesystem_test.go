package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestFilesystemStorePersistsAppliedSnapshot(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem returned error: %v", err)
	}

	err = s.SaveAppliedSnapshot(model.Snapshot{DesiredVersion: "1.2.3"})
	if err != nil {
		t.Fatalf("SaveAppliedSnapshot returned error: %v", err)
	}

	s2, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem returned error: %v", err)
	}

	got, err := s2.LoadAppliedSnapshot()
	if err != nil {
		t.Fatalf("LoadAppliedSnapshot returned error: %v", err)
	}

	if got.DesiredVersion != "1.2.3" {
		t.Fatalf("expected applied desired version 1.2.3, got %q", got.DesiredVersion)
	}
}

func TestFilesystemStorePersistsDesiredSnapshot(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem returned error: %v", err)
	}

	err = s.SaveDesiredSnapshot(model.Snapshot{DesiredVersion: "9.9.9"})
	if err != nil {
		t.Fatalf("SaveDesiredSnapshot returned error: %v", err)
	}

	s2, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem returned error: %v", err)
	}

	got, err := s2.LoadDesiredSnapshot()
	if err != nil {
		t.Fatalf("LoadDesiredSnapshot returned error: %v", err)
	}

	if got.DesiredVersion != "9.9.9" {
		t.Fatalf("expected desired version 9.9.9, got %q", got.DesiredVersion)
	}
}

func TestFilesystemStorePersistsRuntimeState(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem returned error: %v", err)
	}

	expected := model.RuntimeState{
		NodeID: "agent-42",
		Metadata: map[string]string{
			"session": "abc123",
		},
	}

	if err := s.SaveRuntimeState(expected); err != nil {
		t.Fatalf("SaveRuntimeState returned error: %v", err)
	}

	s2, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem returned error: %v", err)
	}

	got, err := s2.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState returned error: %v", err)
	}

	if got.NodeID != expected.NodeID {
		t.Fatalf("expected node ID %q, got %q", expected.NodeID, got.NodeID)
	}

	if val := got.Metadata["session"]; val != "abc123" {
		t.Fatalf("expected metadata session=abc123, got %q", val)
	}
}

func TestFilesystemStoreWritesSeparateFiles(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem returned error: %v", err)
	}

	if err := s.SaveDesiredSnapshot(model.Snapshot{DesiredVersion: "desired"}); err != nil {
		t.Fatalf("SaveDesiredSnapshot returned error: %v", err)
	}
	if err := s.SaveAppliedSnapshot(model.Snapshot{DesiredVersion: "applied"}); err != nil {
		t.Fatalf("SaveAppliedSnapshot returned error: %v", err)
	}
	expected := model.RuntimeState{
		NodeID: "node-b",
	}
	if err := s.SaveRuntimeState(expected); err != nil {
		t.Fatalf("SaveRuntimeState returned error: %v", err)
	}

	readSnapshot := func(name string, dest interface{}) {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("ReadFile %s failed: %v", name, err)
		}
		if err := json.Unmarshal(data, dest); err != nil {
			t.Fatalf("Unmarshal %s failed: %v", name, err)
		}
	}

	var desired model.Snapshot
	readSnapshot(desiredSnapshotFile, &desired)
	if desired.DesiredVersion != "desired" {
		t.Fatalf("desired file content mismatch: %s", desired.DesiredVersion)
	}

	var applied model.Snapshot
	readSnapshot(appliedSnapshotFile, &applied)
	if applied.DesiredVersion != "applied" {
		t.Fatalf("applied file content mismatch: %s", applied.DesiredVersion)
	}

	var runtime model.RuntimeState
	readSnapshot(runtimeStateFile, &runtime)
	if runtime.NodeID != expected.NodeID {
		t.Fatalf("runtime file content mismatch: %s", runtime.NodeID)
	}
}

func TestInMemoryRuntimeStateCopiesMetadata(t *testing.T) {
	s := NewInMemory()
	original := map[string]string{
		"key": "value",
	}
	if err := s.SaveRuntimeState(RuntimeState{
		NodeID:   "node-x",
		Metadata: original,
	}); err != nil {
		t.Fatalf("SaveRuntimeState returned error: %v", err)
	}

	original["key"] = "mutated"

	loaded, err := s.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState returned error: %v", err)
	}
	if got := loaded.Metadata["key"]; got != "value" {
		t.Fatalf("metadata aliasing detected on load: %s", got)
	}

	loaded.Metadata["key"] = "changed-after-load"
	newLoad, err := s.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState returned error: %v", err)
	}
	if got := newLoad.Metadata["key"]; got != "value" {
		t.Fatalf("metadata aliasing detected on subsequent load: %s", got)
	}
}
