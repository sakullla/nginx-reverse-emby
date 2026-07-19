//go:build windows

package hostmetrics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	hostMetricsKernel32           = windows.NewLazySystemDLL("kernel32.dll")
	hostMetricsGetSystemTimes     = hostMetricsKernel32.NewProc("GetSystemTimes")
	hostMetricsGlobalMemoryStatus = hostMetricsKernel32.NewProc("GlobalMemoryStatusEx")
)

type memoryStatusEx struct {
	Length                   uint32
	MemoryLoad               uint32
	TotalPhysical            uint64
	AvailablePhysical        uint64
	TotalPageFile            uint64
	AvailablePageFile        uint64
	TotalVirtual             uint64
	AvailableVirtual         uint64
	AvailableExtendedVirtual uint64
}

func readCPUTimes(ctx context.Context) (cpuTimes, error) {
	if err := ctx.Err(); err != nil {
		return cpuTimes{}, err
	}
	var idle windows.Filetime
	var kernel windows.Filetime
	var user windows.Filetime
	result, _, callErr := hostMetricsGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if result == 0 {
		return cpuTimes{}, windowsCallError("GetSystemTimes", callErr)
	}
	return cpuTimes{
		Total: filetimeTicks(kernel) + filetimeTicks(user),
		Idle:  filetimeTicks(idle),
	}, nil
}

func readMemory(ctx context.Context) (*memorySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	result, _, callErr := hostMetricsGlobalMemoryStatus.Call(uintptr(unsafe.Pointer(&info)))
	if result == 0 {
		return nil, windowsCallError("GlobalMemoryStatusEx", callErr)
	}
	if info.TotalPhysical == 0 {
		return nil, errors.New("physical memory total is unavailable")
	}
	available := info.AvailablePhysical
	if available > info.TotalPhysical {
		available = info.TotalPhysical
	}
	return &memorySnapshot{
		Total:       info.TotalPhysical,
		Used:        info.TotalPhysical - available,
		UsedPercent: float64(info.MemoryLoad),
	}, nil
}

func readDiskUsage(ctx context.Context, path string) (*diskSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve filesystem path %q: %w", path, err)
	}
	pathPointer, err := windows.UTF16PtrFromString(absolutePath)
	if err != nil {
		return nil, fmt.Errorf("encode filesystem path %q: %w", absolutePath, err)
	}
	var available uint64
	var total uint64
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(pathPointer, &available, &total, &free); err != nil {
		return nil, fmt.Errorf("stat filesystem %q: %w", absolutePath, err)
	}
	if free > total {
		free = total
	}
	used := total - free
	usedPercent := float64(0)
	if total > 0 {
		usedPercent = float64(used) / float64(total) * 100
	}
	return &diskSnapshot{Total: total, Used: used, UsedPercent: usedPercent}, nil
}

func readNetworkCounters(ctx context.Context) ([]networkCounter, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list network interfaces: %w", err)
	}
	counters := make([]networkCounter, 0, len(interfaces))
	for _, networkInterface := range interfaces {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := windows.MibIfRow2{InterfaceIndex: uint32(networkInterface.Index)}
		if err := windows.GetIfEntry2Ex(windows.MibIfTableNormal, &row); err != nil {
			return nil, fmt.Errorf("read network interface %q: %w", networkInterface.Name, err)
		}
		counters = append(counters, networkCounter{
			Name:      networkInterface.Name,
			BytesRecv: row.InOctets,
			BytesSent: row.OutOctets,
		})
	}
	return counters, nil
}

func filetimeTicks(value windows.Filetime) uint64 {
	return uint64(value.HighDateTime)<<32 | uint64(value.LowDateTime)
}

func windowsCallError(name string, callErr error) error {
	if callErr == nil || callErr == windows.ERROR_SUCCESS {
		return fmt.Errorf("%s failed", name)
	}
	return fmt.Errorf("%s: %w", name, callErr)
}
