package relayroute

import (
	"strconv"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func BenchmarkResolvePathsLayeredFanout(b *testing.B) {
	listeners := benchmarkRelayListeners(12)
	layers := [][]int{
		{1, 2, 3},
		{4, 5, 6},
		{7, 8, 9},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		paths, err := ResolvePaths("benchmark rule", nil, layers, listeners, "backend.example:443")
		if err != nil {
			b.Fatalf("ResolvePaths() error = %v", err)
		}
		if len(paths) != 27 {
			b.Fatalf("ResolvePaths() paths = %d, want 27", len(paths))
		}
	}
}

func BenchmarkClonePathsWithTargetLayeredFanout(b *testing.B) {
	listeners := benchmarkRelayListeners(12)
	paths, err := ResolvePaths("benchmark rule", nil, [][]int{
		{1, 2, 3},
		{4, 5, 6},
		{7, 8, 9},
	}, listeners, "backend.example:443")
	if err != nil {
		b.Fatalf("ResolvePaths() error = %v", err)
	}
	target := "backend.example:443"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cloned := ClonePathsWithTarget(paths, target)
		if len(cloned) != len(paths) {
			b.Fatalf("ClonePathsWithTarget() paths = %d, want %d", len(cloned), len(paths))
		}
	}
}

func BenchmarkClonePathsWithoutKeysLayeredFanout(b *testing.B) {
	listeners := benchmarkRelayListeners(12)
	paths, err := ResolvePaths("benchmark rule", nil, [][]int{
		{1, 2, 3},
		{4, 5, 6},
		{7, 8, 9},
	}, listeners, "backend.example:443")
	if err != nil {
		b.Fatalf("ResolvePaths() error = %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cloned := ClonePathsWithoutKeys(paths)
		if len(cloned) != len(paths) {
			b.Fatalf("ClonePathsWithoutKeys() paths = %d, want %d", len(cloned), len(paths))
		}
	}
}

func benchmarkRelayListeners(total int) []model.RelayListener {
	listeners := make([]model.RelayListener, 0, total)
	for id := 1; id <= total; id++ {
		listeners = append(listeners, model.RelayListener{
			ID:         id,
			ListenHost: "127.0.0.1",
			ListenPort: 8000 + id,
			PublicHost: "relay-" + strconv.Itoa(id) + ".example.com",
			PublicPort: 9000 + id,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "abc",
			}},
		})
	}
	return listeners
}
