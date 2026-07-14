//go:build linux

package platform

import (
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"syscall"
	"time"
)

func SupportsHotRestart() bool {
	return runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64"
}

func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return true
	}
	fields := strings.Fields(string(stat[end+1:]))
	return len(fields) == 0 || fields[0] != "Z"
}

func ProcessIdentity(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", false
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", false
	}
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return "", false
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) <= 19 || fields[0] == "Z" {
		return "", false
	}
	return strings.TrimSpace(string(bootID)) + ":" + fields[19], true
}

func AcquireFileLock(path string, timeout time.Duration) (func() error, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() error {
				return errors.Join(syscall.Flock(int(file.Fd()), syscall.LOCK_UN), file.Close())
			}, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			_ = file.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			_ = file.Close()
			return nil, errors.New("timed out acquiring file lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func ListenerFile(listener net.Listener) (*os.File, error) {
	if !SupportsHotRestart() {
		return nil, errors.New("hot restart is unsupported on this linux architecture")
	}
	fileListener, ok := listener.(interface {
		File() (*os.File, error)
	})
	if !ok {
		return nil, errors.New("listener does not expose an inheritable file descriptor")
	}
	file, err := fileListener.File()
	if err != nil {
		return nil, err
	}
	if file == nil {
		return nil, errors.New("listener returned a nil file descriptor")
	}
	return file, nil
}

func ListenerFromFile(file *os.File) (net.Listener, error) {
	if !SupportsHotRestart() {
		return nil, errors.New("hot restart is unsupported on this linux architecture")
	}
	if file == nil {
		return nil, errors.New("listener file is required")
	}
	return net.FileListener(file)
}
