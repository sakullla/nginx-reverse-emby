//go:build !windows && !linux

package wasm

import "errors"

func readProcessMemory() (processMemorySample, error) {
	return processMemorySample{}, errors.New("process RSS/private-memory sampling is unsupported on this platform")
}

func allocateNativeTestMemory(int) (func() error, error) {
	return nil, errors.New("native test allocation is unsupported on this platform")
}
