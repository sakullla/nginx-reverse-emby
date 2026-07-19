//go:build linux

package hostmetrics

import (
	"context"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func readCPUTimes(ctx context.Context) (cpuTimes, error) {
	if err := ctx.Err(); err != nil {
		return cpuTimes{}, err
	}
	file, err := os.Open("/proc/stat")
	if err != nil {
		return cpuTimes{}, fmt.Errorf("open /proc/stat: %w", err)
	}
	defer file.Close()
	return parseProcStat(file)
}

func readMemory(ctx context.Context) (*memorySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return nil, fmt.Errorf("open /proc/meminfo: %w", err)
	}
	defer file.Close()
	return parseProcMeminfo(file)
}

func readDiskUsage(ctx context.Context, path string) (*diskSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return nil, fmt.Errorf("stat filesystem %q: %w", path, err)
	}
	if stat.Bsize <= 0 {
		return nil, errors.New("filesystem block size is unavailable")
	}

	blockSize := uint64(stat.Bsize)
	blocks := uint64(stat.Blocks)
	freeBlocks := uint64(stat.Bfree)
	if freeBlocks > blocks {
		freeBlocks = blocks
	}
	used := (blocks - freeBlocks) * blockSize
	available := uint64(stat.Bavail) * blockSize
	usedPercent := float64(0)
	if used+available > 0 {
		usedPercent = float64(used) / float64(used+available) * 100
	}
	return &diskSnapshot{Total: blocks * blockSize, Used: used, UsedPercent: usedPercent}, nil
}

func readNetworkCounters(ctx context.Context) ([]networkCounter, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open("/proc/net/dev")
	if err != nil {
		return nil, fmt.Errorf("open /proc/net/dev: %w", err)
	}
	defer file.Close()
	return parseProcNetDev(file)
}
