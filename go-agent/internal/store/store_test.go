package store

import "testing"

func TestSaveSnapshotPersistsDesiredVersion(t *testing.T) {
	s := NewInMemory()
	err := s.SaveSnapshot(Snapshot{DesiredVersion: "1.2.3"})
	if err != nil {
		t.Fatalf("SaveSnapshot returned error: %v", err)
	}
	got, _ := s.LoadSnapshot()
	if got.DesiredVersion != "1.2.3" {
		t.Fatalf("expected desired version 1.2.3, got %q", got.DesiredVersion)
	}
}
