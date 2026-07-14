//go:build linux

package platform

import (
	"errors"
	"net"
	"os"
	"runtime"
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
	return err == nil || errors.Is(err, syscall.EPERM)
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
