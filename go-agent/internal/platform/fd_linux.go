//go:build linux

package platform

import (
	"errors"
	"net"
	"os"
	"runtime"
	"syscall"
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
