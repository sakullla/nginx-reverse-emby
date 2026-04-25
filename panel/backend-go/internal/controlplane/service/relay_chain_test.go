package service

import "testing"

func TestNormalizeRelayLayersInputRejectsFanoutOverLimit(t *testing.T) {
	_, err := normalizeRelayLayersInput([][]int{
		{1, 2, 3, 4},
		{5, 6, 7, 8},
		{9, 10, 11},
	}, "tcp")
	if err == nil {
		t.Fatal("normalizeRelayLayersInput() error = nil")
	}
	if err.Error() != "invalid argument: relay_layers expand to more than 32 relay paths" {
		t.Fatalf("normalizeRelayLayersInput() error = %v", err)
	}
}
