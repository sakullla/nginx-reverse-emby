package update

import "testing"

func TestNeedsUpdate(t *testing.T) {
	if !NeedsUpdate("1.2.3", "1.2.4") {
		t.Fatal("expected update to be required")
	}
}

func TestNeedsUpdateFalseWhenDesiredEmpty(t *testing.T) {
	if NeedsUpdate("1.2.3", "") {
		t.Fatal("expected no update when desired is empty")
	}
}

func TestNeedsUpdateFalseWhenDesiredEqualsCurrent(t *testing.T) {
	if NeedsUpdate("1.2.3", "1.2.3") {
		t.Fatal("expected no update when desired equals current")
	}
}

func TestErrRestartRequestedIsDefined(t *testing.T) {
	if ErrRestartRequested == nil {
		t.Fatal("expected restart sentinel error")
	}
}
