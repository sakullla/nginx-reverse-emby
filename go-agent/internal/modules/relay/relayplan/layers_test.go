package relayplan

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeLayersUsesRelayLayersOnly(t *testing.T) {
	got := NormalizeLayers([]int{9}, [][]int{{1, 2}, {3}})
	want := [][]int{{1, 2}, {3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeLayers() = %#v, want %#v", got, want)
	}
}

func TestNormalizeLayersIgnoresRelayChainOnly(t *testing.T) {
	got := NormalizeLayers([]int{1, 2, 3}, nil)
	if got != nil {
		t.Fatalf("NormalizeLayers() = %#v, want nil", got)
	}
}

func TestNormalizeLayersFiltersEmptyLayers(t *testing.T) {
	got := NormalizeLayers(nil, [][]int{{1, 2}, {}, {3}})
	want := [][]int{{1, 2}, {3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeLayers() = %#v, want %#v", got, want)
	}
}

func TestExpandPathsBuildsCartesianProduct(t *testing.T) {
	got, err := ExpandPaths([][]int{{1, 2}, {3, 4}}, 8)
	if err != nil {
		t.Fatalf("ExpandPaths() error = %v", err)
	}
	want := [][]int{{1, 3}, {1, 4}, {2, 3}, {2, 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExpandPaths() = %#v, want %#v", got, want)
	}
}

func TestExpandPathsRejectsDuplicateWithinPath(t *testing.T) {
	_, err := ExpandPaths([][]int{{1, 2}, {1}}, 32)
	if err == nil {
		t.Fatal("ExpandPaths() error = nil, want duplicate listener error")
	}
}

func TestExpandPathsHonorsMaximum(t *testing.T) {
	_, err := ExpandPaths([][]int{{1, 2, 3}, {4, 5, 6}}, 8)
	if err == nil {
		t.Fatal("ExpandPaths() error = nil, want maximum path error")
	}
}

func TestExpandPathsReportsDuplicateBeforeMaximum(t *testing.T) {
	_, err := ExpandPaths([][]int{{1, 2, 3}, {1, 2, 3}}, 8)
	if err == nil || !strings.Contains(err.Error(), "duplicate listener") {
		t.Fatalf("ExpandPaths() error = %v, want duplicate listener error", err)
	}
}

func TestExpandPathsAllocations(t *testing.T) {
	layers := [][]int{
		{1, 2, 3},
		{4, 5, 6},
		{7, 8, 9},
	}
	allocs := testing.AllocsPerRun(1000, func() {
		paths, err := ExpandPaths(layers, 32)
		if err != nil {
			t.Fatalf("ExpandPaths() error = %v", err)
		}
		if len(paths) != 27 {
			t.Fatalf("ExpandPaths() paths = %d, want 27", len(paths))
		}
	})
	if allocs > 35 {
		t.Fatalf("ExpandPaths() allocations = %.2f, want <= 35", allocs)
	}
}

func TestPathKeyAllocations(t *testing.T) {
	allocs := testing.AllocsPerRun(1000, func() {
		_ = PathKey("relay_path", []int{1, 23, 456}, " backend.example:443 ")
	})
	if allocs > 2 {
		t.Fatalf("PathKey() allocations = %.2f, want <= 2", allocs)
	}
}
