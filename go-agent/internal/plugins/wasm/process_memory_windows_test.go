//go:build windows

package wasm

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

type processMemoryCounters struct {
	Size                       uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
	PrivateUsage               uintptr
}

var getProcessMemoryInfo = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")
var queryWorkingSet = windows.NewLazySystemDLL("psapi.dll").NewProc("QueryWorkingSet")

func readProcessMemory() (processMemorySample, error) {
	counters := processMemoryCounters{Size: uint32(unsafe.Sizeof(processMemoryCounters{}))}
	result, _, callErr := getProcessMemoryInfo.Call(
		uintptr(windows.CurrentProcess()),
		uintptr(unsafe.Pointer(&counters)),
		uintptr(counters.Size),
	)
	if result == 0 {
		return processMemorySample{}, fmt.Errorf("GetProcessMemoryInfo: %w", callErr)
	}
	privateWorkingSet, err := readPrivateWorkingSet(uint64(counters.WorkingSetSize))
	if err != nil {
		return processMemorySample{}, err
	}
	return processMemorySample{RSSBytes: uint64(counters.WorkingSetSize), PrivateBytes: privateWorkingSet}, nil
}

func readPrivateWorkingSet(workingSetBytes uint64) (uint64, error) {
	pageSize := uint64(os.Getpagesize())
	entryCapacity := int(workingSetBytes/pageSize) + 4096
	for attempt := 0; attempt < 3; attempt++ {
		entries := make([]uintptr, entryCapacity+1)
		result, _, callErr := queryWorkingSet.Call(
			uintptr(windows.CurrentProcess()),
			uintptr(unsafe.Pointer(&entries[0])),
			uintptr(len(entries))*unsafe.Sizeof(entries[0]),
		)
		if result != 0 {
			count := int(entries[0])
			if count > entryCapacity {
				entryCapacity = count + 4096
				continue
			}
			var privatePages uint64
			for _, block := range entries[1 : count+1] {
				// PSAPI_WORKING_SET_BLOCK.Shared is bit 8. Non-shared
				// resident pages form the process private working set.
				if block&(1<<8) == 0 {
					privatePages++
				}
			}
			return privatePages * pageSize, nil
		}
		entryCapacity *= 2
		if attempt == 2 {
			return 0, fmt.Errorf("QueryWorkingSet: %w", callErr)
		}
	}
	return 0, fmt.Errorf("QueryWorkingSet did not stabilize")
}

func allocateNativeTestMemory(size int) (func() error, error) {
	address, err := windows.VirtualAlloc(0, uintptr(size), windows.MEM_RESERVE|windows.MEM_COMMIT, windows.PAGE_READWRITE)
	if err != nil {
		return nil, err
	}
	memory := unsafe.Slice((*byte)(unsafe.Pointer(address)), size)
	for offset := 0; offset < len(memory); offset += 4096 {
		memory[offset] = byte(offset)
	}
	return func() error {
		return windows.VirtualFree(address, 0, windows.MEM_RELEASE)
	}, nil
}
