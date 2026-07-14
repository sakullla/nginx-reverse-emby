//go:build !linux

package platform

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

var processFileLocks sync.Map

func SupportsHotRestart() bool { return false }

func ProcessAlive(pid int) bool { return pid == os.Getpid() }

func ProcessIdentity(pid int) (string, bool) {
	if pid != os.Getpid() {
		return "", false
	}
	return fmt.Sprintf("pid:%d", pid), true
}

func AcquireFileLock(path string, _ time.Duration) (func() error, error) {
	value, _ := processFileLocks.LoadOrStore(path, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return func() error {
		lock.Unlock()
		return nil
	}, nil
}

func ListenerFile(net.Listener) (*os.File, error) {
	return nil, errors.New("hot restart listener inheritance is only supported on linux")
}

func ListenerFromFile(*os.File) (net.Listener, error) {
	return nil, errors.New("hot restart listener inheritance is only supported on linux")
}
