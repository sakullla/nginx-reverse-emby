package update

import "testing"

func TestNeedsUpdate(t *testing.T) {
	if !NeedsUpdate("1.2.3", "1.2.4") {
		t.Fatal("expected update to be required")
	}
}
