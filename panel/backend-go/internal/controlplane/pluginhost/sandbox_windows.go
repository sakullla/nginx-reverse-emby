//go:build windows

package pluginhost

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"os/exec"
	"syscall"
	"unsafe"
)

var backendCreateRestrictedToken = windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateRestrictedToken")

type backendJobCPU struct{ Flags, Rate uint32 }

func validatePlatformSandbox(c Candidate) error {
	return errors.New("windows restricted token and Job Object do not provide a complete control-plane plugin sandbox boundary")
}
func validateWindowsDefenseBudget(c Candidate) error {
	budget := c.Requirement.Budget()
	if budget.CPUMillis <= 0 || budget.CPUMillis > 1000 {
		return errors.New("windows control-plane plugin sandbox requires cpu_millis within 1..1000")
	}
	if budget.MemoryBytes <= 0 || budget.Processes <= 0 {
		return errors.New("windows control-plane plugin sandbox requires memory and process budgets")
	}
	return nil
}
func configurePlatformSandbox(cmd *exec.Cmd, c Candidate) (func() error, func(int) (func() error, error), error) {
	if !hasUnsandboxedGrant(c.Grants) {
		return nil, nil, validatePlatformSandbox(c)
	}
	if err := validateWindowsDefenseBudget(c); err != nil {
		return nil, nil, err
	}
	var source windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_DUPLICATE|windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_QUERY, &source); err != nil {
		return nil, nil, err
	}
	defer source.Close()
	var restricted windows.Token
	ok, _, callErr := backendCreateRestrictedToken.Call(uintptr(source), 0x1|0x4|0x8, 0, 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&restricted)))
	if ok == 0 {
		return nil, nil, callErr
	}
	job, err := newBackendJob(c.Requirement.Budget())
	if err != nil {
		restricted.Close()
		return nil, nil, err
	}
	cmd.SysProcAttr = backendSandboxSysProcAttr(restricted)
	attach := func(pid int) (cleanup func() error, resultErr error) {
		failed := true
		defer func() {
			if failed {
				_ = windows.CloseHandle(job)
			}
		}()
		processHandle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if err != nil {
			return nil, err
		}
		defer windows.CloseHandle(processHandle)
		if err := windows.AssignProcessToJobObject(job, processHandle); err != nil {
			return nil, fmt.Errorf("assign suspended plugin job: %w", err)
		}
		thread, err := backendSuspendedThread(uint32(pid))
		if err != nil {
			return nil, err
		}
		defer windows.CloseHandle(thread)
		if _, err := windows.ResumeThread(thread); err != nil {
			return nil, fmt.Errorf("resume sandboxed plugin: %w", err)
		}
		failed = false
		return func() error { return windows.CloseHandle(job) }, nil
	}
	return restricted.Close, attach, nil
}

func backendSandboxSysProcAttr(token windows.Token) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Token: syscall.Token(token), NoInheritHandles: true, CreationFlags: windows.CREATE_SUSPENDED}
}
func newBackendJob(b ProcessBudget) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY
	limits.BasicLimitInformation.ActiveProcessLimit = uint32(b.Processes)
	limits.ProcessMemoryLimit = uintptr(b.MemoryBytes)
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	cpu := backendJobCPU{Flags: 5, Rate: uint32(b.CPUMillis * 10)}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectCpuRateControlInformation, uintptr(unsafe.Pointer(&cpu)), uint32(unsafe.Sizeof(cpu))); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}
func backendSuspendedThread(pid uint32) (windows.Handle, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return 0, err
	}
	for {
		if entry.OwnerProcessID == pid {
			return windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return 0, errors.New("suspended plugin thread not found")
}
