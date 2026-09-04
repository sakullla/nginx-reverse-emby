//go:build linux

package wasm

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func readProcessMemory() (processMemorySample, error) {
	file, err := os.Open("/proc/self/smaps_rollup")
	if err != nil {
		return processMemorySample{}, err
	}
	defer file.Close()
	var sample processMemorySample
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		kilobytes, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr != nil {
			continue
		}
		switch fields[0] {
		case "Rss:":
			sample.RSSBytes = kilobytes << 10
		case "Private_Clean:", "Private_Dirty:":
			sample.PrivateBytes += kilobytes << 10
		}
	}
	if err := scanner.Err(); err != nil {
		return processMemorySample{}, err
	}
	if sample.RSSBytes == 0 || sample.PrivateBytes == 0 {
		return processMemorySample{}, fmt.Errorf("incomplete smaps_rollup sample: %+v", sample)
	}
	return sample, nil
}

func allocateNativeTestMemory(size int) (func() error, error) {
	memory, err := unix.Mmap(-1, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE|unix.MAP_ANON)
	if err != nil {
		return nil, err
	}
	for offset := 0; offset < len(memory); offset += 4096 {
		memory[offset] = byte(offset)
	}
	return func() error { return unix.Munmap(memory) }, nil
}
