//go:build darwin

package hostmetrics

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/unix"
)

func defaultCPUPercentSampler() cpuPercentFunc {
	return readDarwinCPUPercent
}

func readDarwinCPUPercent(ctx context.Context) (float64, error) {
	output, err := runDarwinMetricCommand(ctx, "/usr/bin/top", "-l", "1", "-n", "0")
	if err != nil {
		return 0, err
	}
	return parseDarwinCPUUsage(output)
}

func readMemory(ctx context.Context) (*memorySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return nil, fmt.Errorf("read hw.memsize: %w", err)
	}
	if total == 0 {
		return nil, errors.New("physical memory total is unavailable")
	}
	output, err := runDarwinMetricCommand(ctx, "/usr/bin/vm_stat")
	if err != nil {
		return nil, err
	}
	available, err := parseDarwinAvailableMemory(output)
	if err != nil {
		return nil, err
	}
	if available > total {
		available = total
	}
	used := total - available
	return &memorySnapshot{
		Total:       total,
		Used:        used,
		UsedPercent: float64(used) / float64(total) * 100,
	}, nil
}

func readDiskUsage(ctx context.Context, path string) (*diskSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return nil, fmt.Errorf("stat filesystem %q: %w", path, err)
	}
	if stat.Bsize == 0 {
		return nil, errors.New("filesystem block size is unavailable")
	}
	freeBlocks := stat.Bfree
	if freeBlocks > stat.Blocks {
		freeBlocks = stat.Blocks
	}
	used := (stat.Blocks - freeBlocks) * uint64(stat.Bsize)
	available := stat.Bavail * uint64(stat.Bsize)
	usedPercent := float64(0)
	if used+available > 0 {
		usedPercent = float64(used) / float64(used+available) * 100
	}
	return &diskSnapshot{
		Total:       stat.Blocks * uint64(stat.Bsize),
		Used:        used,
		UsedPercent: usedPercent,
	}, nil
}

func readNetworkCounters(ctx context.Context) ([]networkCounter, error) {
	output, err := runDarwinMetricCommand(ctx, "/usr/sbin/netstat", "-ibdnW")
	if err != nil {
		return nil, err
	}
	return parseDarwinNetworkCounters(output)
}

func runDarwinMetricCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	output, err := cmd.CombinedOutput()
	if err == nil {
		return output, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return nil, fmt.Errorf("run %s: %w", name, err)
	}
	return nil, fmt.Errorf("run %s: %w: %s", name, err, detail)
}
