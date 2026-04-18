package service

import "testing"

func assertRevisionAboveFloor(t *testing.T, label string, got int, floor int) {
	t.Helper()
	if got <= floor {
		t.Fatalf("%s = %d, want > %d", label, got, floor)
	}
}

func assertRevisionNotBehind(t *testing.T, label string, got int, minimum int) {
	t.Helper()
	if got < minimum {
		t.Fatalf("%s = %d, want >= %d", label, got, minimum)
	}
}
