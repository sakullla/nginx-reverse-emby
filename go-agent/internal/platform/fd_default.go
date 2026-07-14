//go:build !linux

package platform

import (
	"errors"
	"net"
	"os"
)

func SupportsHotRestart() bool { return false }

func ProcessAlive(pid int) bool { return pid == os.Getpid() }

func ListenerFile(net.Listener) (*os.File, error) {
	return nil, errors.New("hot restart listener inheritance is only supported on linux")
}

func ListenerFromFile(*os.File) (net.Listener, error) {
	return nil, errors.New("hot restart listener inheritance is only supported on linux")
}
