package app

import "testing"

func TestNewAppBuildsDefaultRuntime(t *testing.T) {
	app, err := New(Config{AgentID: "local"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if app == nil {
		t.Fatal("expected non-nil app")
	}
}
