//go:build windows

package process

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsJobSandbox struct{}
type jobObjectCPURateControlInformation struct{ ControlFlags, CPURate uint32 }

var createRestrictedToken = windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateRestrictedToken")

const (
	disableMaxPrivilege = 0x1
	jobObjectCPUEnable  = 0x1
	jobObjectCPUHardCap = 0x4
)

func newPlatformSandbox() Sandbox          { return windowsJobSandbox{} }
func (windowsJobSandbox) Available() bool  { return true }
func (windowsJobSandbox) Provider() string { return "windows-restricted-token-job-object" }
func (windowsJobSandbox) Validate(security Security) error {
	if security.Budget.CPUMillis <= 0 || security.Budget.CPUMillis > 1000 {
		return errors.New("windows plugin sandbox requires cpu_millis within 1..1000")
	}
	if security.Budget.MemoryBytes <= 0 {
		return errors.New("windows plugin sandbox requires memory_bytes")
	}
	if security.Budget.Processes <= 0 {
		return errors.New("windows plugin sandbox requires a positive process limit")
	}
	if security.Budget.Files > 0 {
		return errors.New("windows plugin sandbox cannot enforce a per-process file-handle budget")
	}
	if !security.Budget.Network {
		return errors.New("windows plugin sandbox cannot enforce network denial")
	}
	return nil
}
func (windowsJobSandbox) Configure(cmd *exec.Cmd, security Security) (func() error, func() error, func(int) error, error) {
	if cmd == nil {
		return nil, nil, nil, errors.New("plugin process command is required")
	}
	if err := (windowsJobSandbox{}).Validate(security); err != nil {
		return nil, nil, nil, err
	}
	var source windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_DUPLICATE|windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_QUERY, &source); err != nil {
		return nil, nil, nil, fmt.Errorf("open plugin process source token: %w", err)
	}
	defer source.Close()
	var restricted windows.Token
	result, _, callErr := createRestrictedToken.Call(uintptr(source), disableMaxPrivilege, 0, 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&restricted)))
	if result == 0 {
		return nil, nil, nil, fmt.Errorf("create restricted plugin process token: %w", callErr)
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		restricted.Close()
		return nil, nil, nil, fmt.Errorf("create plugin process job object: %w", err)
	}
	closeJob := func() error { return windows.CloseHandle(job) }
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY
	limits.BasicLimitInformation.ActiveProcessLimit = uint32(security.Budget.Processes)
	limits.ProcessMemoryLimit = uintptr(security.Budget.MemoryBytes)
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		_ = closeJob()
		restricted.Close()
		return nil, nil, nil, fmt.Errorf("configure plugin process job object: %w", err)
	}
	cpu := jobObjectCPURateControlInformation{ControlFlags: jobObjectCPUEnable | jobObjectCPUHardCap, CPURate: uint32(security.Budget.CPUMillis * 10)}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectCpuRateControlInformation, uintptr(unsafe.Pointer(&cpu)), uint32(unsafe.Sizeof(cpu))); err != nil {
		_ = closeJob()
		restricted.Close()
		return nil, nil, nil, fmt.Errorf("configure plugin process CPU budget: %w", err)
	}
	cmd.SysProcAttr = windowsSandboxSysProcAttr(restricted)
	afterStart := func(pid int) error {
		processHandle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if err != nil {
			return fmt.Errorf("open suspended plugin process for sandbox: %w", err)
		}
		defer windows.CloseHandle(processHandle)
		if err := windows.AssignProcessToJobObject(job, processHandle); err != nil {
			return fmt.Errorf("assign suspended plugin process to job object: %w", err)
		}
		thread, err := suspendedProcessThread(uint32(pid))
		if err != nil {
			return err
		}
		defer windows.CloseHandle(thread)
		if _, err := windows.ResumeThread(thread); err != nil {
			return fmt.Errorf("resume sandboxed plugin process: %w", err)
		}
		return nil
	}
	return restricted.Close, closeJob, afterStart, nil
}

func windowsSandboxSysProcAttr(token windows.Token) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Token: syscall.Token(token), NoInheritHandles: true, CreationFlags: windows.CREATE_SUSPENDED}
}

func suspendedProcessThread(pid uint32) (windows.Handle, error) {
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
	return 0, errors.New("suspended plugin process thread was not found")
}
