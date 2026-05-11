package relayplan

import (
	"fmt"
	"strconv"
	"strings"
)

func NormalizeLayers(_ []int, layers [][]int) [][]int {
	return cloneLayers(layers)
}

func ExpandPaths(layers [][]int, maxPaths int) ([][]int, error) {
	if len(layers) == 0 {
		return nil, nil
	}
	if maxPaths <= 0 {
		return nil, fmt.Errorf("max relay paths must be positive")
	}
	paths := [][]int{{}}
	for layerIndex, layer := range layers {
		if len(layer) == 0 {
			return nil, fmt.Errorf("relay layer %d is empty", layerIndex)
		}
		next := make([][]int, 0, len(paths)*len(layer))
		for _, path := range paths {
			for _, id := range layer {
				candidate := append(append([]int(nil), path...), id)
				if hasDuplicate(candidate) {
					return nil, fmt.Errorf("relay path contains duplicate listener id %d", id)
				}
				next = append(next, candidate)
				if len(next) > maxPaths {
					return nil, fmt.Errorf("relay paths exceed maximum %d", maxPaths)
				}
			}
		}
		paths = next
	}
	return paths, nil
}

func PathKey(prefix string, path []int, target string) string {
	target = strings.TrimSpace(target)
	var builder strings.Builder
	builder.Grow(len(prefix) + len(target) + pathStringLen(path) + len("||"))
	builder.WriteString(prefix)
	builder.WriteByte('|')
	writePathIDs(&builder, path)
	builder.WriteByte('|')
	builder.WriteString(target)
	return builder.String()
}

func pathStringLen(path []int) int {
	if len(path) == 0 {
		return 0
	}
	size := len(path) - 1
	for _, id := range path {
		size += intStringLen(id)
	}
	return size
}

func writePathIDs(builder *strings.Builder, path []int) {
	var scratch [20]byte
	for i, id := range path {
		if i > 0 {
			builder.WriteByte('-')
		}
		builder.Write(strconv.AppendInt(scratch[:0], int64(id), 10))
	}
}

func intStringLen(value int) int {
	var scratch [20]byte
	return len(strconv.AppendInt(scratch[:0], int64(value), 10))
}

func cloneLayers(layers [][]int) [][]int {
	out := make([][]int, 0, len(layers))
	for _, layer := range layers {
		if len(layer) == 0 {
			continue
		}
		out = append(out, append([]int(nil), layer...))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func hasDuplicate(path []int) bool {
	seen := make(map[int]struct{}, len(path))
	for _, id := range path {
		if _, ok := seen[id]; ok {
			return true
		}
		seen[id] = struct{}{}
	}
	return false
}
