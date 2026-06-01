package agentutil

import "testing"

func TestCloneStringMapPreservesNil(t *testing.T) {
	if got := CloneStringMap(nil); got != nil {
		t.Fatalf("CloneStringMap(nil) = %#v, want nil", got)
	}
}

func TestCloneStringMapIsIndependent(t *testing.T) {
	src := map[string]string{"alpha": "one"}

	got := CloneStringMap(src)
	src["alpha"] = "changed"
	got["beta"] = "two"

	if got["alpha"] != "one" {
		t.Fatalf("clone alpha = %q, want one", got["alpha"])
	}
	if _, exists := src["beta"]; exists {
		t.Fatalf("source received clone mutation: %#v", src)
	}
}

func TestEnsureStringMap(t *testing.T) {
	created := EnsureStringMap(nil)
	if created == nil {
		t.Fatal("EnsureStringMap(nil) = nil, want empty map")
	}
	created["key"] = "value"

	existing := map[string]string{"same": "map"}
	if got := EnsureStringMap(existing); got["same"] != "map" {
		t.Fatalf("EnsureStringMap(existing) = %#v, want original contents", got)
	}
}
