package store

import "testing"

func TestSaveSnapshotPersistsDesiredVersion(t *testing.T) {
	s := NewInMemory()
	if err := s.SaveDesiredSnapshot(Snapshot{DesiredVersion: "1.2.3"}); err != nil {
		t.Fatalf("SaveDesiredSnapshot returned error: %v", err)
	}

	got, err := s.LoadDesiredSnapshot()
	if err != nil {
		t.Fatalf("LoadDesiredSnapshot returned error: %v", err)
	}

	if got.DesiredVersion != "1.2.3" {
		t.Fatalf("expected desired version 1.2.3, got %q", got.DesiredVersion)
	}
}
