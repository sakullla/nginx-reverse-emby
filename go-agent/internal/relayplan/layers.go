package relayplan

import (
	"fmt"
	"strconv"
	"strings"
)

func NormalizeLayers(chain []int, layers [][]int) [][]int {
	if len(layers) > 0 {
		return cloneLayers(layers)
	}
	if len(chain) == 0 {
		return nil
	}
	out := make([][]int, 0, len(chain))
	for _, id := range chain {
		out = append(out, []int{id})
	}
	return out
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
	parts := make([]string, 0, len(path))
	for _, id := range path {
		parts = append(parts, strconv.Itoa(id))
	}
	return prefix + "|" + strings.Join(parts, "-") + "|" + strings.TrimSpace(target)
}

func cloneLayers(layers [][]int) [][]int {
	out := make([][]int, len(layers))
	for i, layer := range layers {
		out[i] = append([]int(nil), layer...)
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
