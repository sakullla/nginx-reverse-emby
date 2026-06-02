package relayplan

import (
	"fmt"
	"slices"
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
	for layerIndex, layer := range layers {
		if len(layer) == 0 {
			return nil, fmt.Errorf("relay layer %d is empty", layerIndex)
		}
	}
	total := 1
	for _, layer := range layers {
		if total <= maxPaths && total > maxPaths/len(layer) {
			total = maxPaths + 1
			break
		}
		total *= len(layer)
	}
	capacity := total
	if capacity > maxPaths {
		capacity = maxPaths + 1
	}
	paths := make([][]int, 0, capacity)
	current := make([]int, len(layers))
	seen := make(map[int]struct{}, len(layers))
	var walk func(int) error
	walk = func(layerIndex int) error {
		if layerIndex == len(layers) {
			paths = append(paths, slices.Clone(current))
			if len(paths) > maxPaths {
				return fmt.Errorf("relay paths exceed maximum %d", maxPaths)
			}
			return nil
		}
		for _, id := range layers[layerIndex] {
			if _, ok := seen[id]; ok {
				return fmt.Errorf("relay path contains duplicate listener id %d", id)
			}
			seen[id] = struct{}{}
			current[layerIndex] = id
			if err := walk(layerIndex + 1); err != nil {
				return err
			}
			delete(seen, id)
		}
		return nil
	}
	if err := walk(0); err != nil {
		return nil, err
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
		out = append(out, slices.Clone(layer))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
