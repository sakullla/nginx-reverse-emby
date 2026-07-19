package traffic

import "testing"

func TestBlockStateValueNormalizesReasonOnStoreAndLoad(t *testing.T) {
	var value BlockStateValue
	value.Store(BlockState{Blocked: true, Reason: " monthly quota exceeded "})
	got := value.Load()
	if !got.Blocked || got.Reason != "monthly quota exceeded" {
		t.Fatalf("Load() = %+v, want normalized blocked state", got)
	}
}

func TestNilBlockStateValueLoadReturnsZero(t *testing.T) {
	var value *BlockStateValue
	if got := value.Load(); got.Blocked || got.Reason != "" {
		t.Fatalf("nil Load() = %+v, want zero state", got)
	}
}
