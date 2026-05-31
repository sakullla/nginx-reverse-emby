//go:build linux

package wireguard

import (
	"fmt"
	"net"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	hostSocketBufferSize = 7 << 20
	hostSizeOfGSOData    = 2
)

var hostGSOControlSize = unix.CmsgSpace(hostSizeOfGSOData)

func hostListenConfig() *net.ListenConfig {
	return &net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				_ = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUF, hostSocketBufferSize)
				_ = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_SNDBUF, hostSocketBufferSize)
				_ = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUFFORCE, hostSocketBufferSize)
				_ = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_SNDBUFFORCE, hostSocketBufferSize)
				if network == "udp6" && runtime.GOOS != "android" {
					_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_V6ONLY, 1)
				}
				if hostKernelSupportsUDPGROGetsockopt() {
					_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_UDP, unix.UDP_GRO, 1)
				}
			})
		},
	}
}

func hostSupportsUDPOffload(conn *net.UDPConn) (txOffload, rxOffload bool) {
	rc, err := conn.SyscallConn()
	if err != nil {
		return false, false
	}
	err = rc.Control(func(fd uintptr) {
		_, errSyscall := unix.GetsockoptInt(int(fd), unix.IPPROTO_UDP, unix.UDP_SEGMENT)
		txOffload = errSyscall == nil
		opt, errSyscall := unix.GetsockoptInt(int(fd), unix.IPPROTO_UDP, unix.UDP_GRO)
		rxOffload = errSyscall == nil && opt == 1
	})
	if err != nil {
		return false, false
	}
	return txOffload, rxOffload
}

func hostGetGSOSize(control []byte) (int, error) {
	rem := control
	for len(rem) > unix.SizeofCmsghdr {
		hdr, data, next, err := unix.ParseOneSocketControlMessage(rem)
		if err != nil {
			return 0, fmt.Errorf("error parsing socket control message: %w", err)
		}
		if hdr.Level == unix.SOL_UDP && hdr.Type == unix.UDP_GRO && len(data) >= hostSizeOfGSOData {
			var gso uint16
			copy(unsafe.Slice((*byte)(unsafe.Pointer(&gso)), hostSizeOfGSOData), data[:hostSizeOfGSOData])
			return int(gso), nil
		}
		rem = next
	}
	return 0, nil
}

func hostSetGSOSize(control *[]byte, gsoSize uint16) {
	existingLen := len(*control)
	space := unix.CmsgSpace(hostSizeOfGSOData)
	if cap(*control)-existingLen < space {
		return
	}
	*control = (*control)[:cap(*control)]
	gsoControl := (*control)[existingLen:]
	hdr := (*unix.Cmsghdr)(unsafe.Pointer(&gsoControl[0]))
	hdr.Level = unix.SOL_UDP
	hdr.Type = unix.UDP_SEGMENT
	hdr.SetLen(unix.CmsgLen(hostSizeOfGSOData))
	copy(gsoControl[unix.CmsgLen(0):], unsafe.Slice((*byte)(unsafe.Pointer(&gsoSize)), hostSizeOfGSOData))
	*control = (*control)[:existingLen+space]
}

func hostKernelSupportsUDPGROGetsockopt() bool {
	var uname unix.Utsname
	if err := unix.Uname(&uname); err != nil {
		return false
	}
	var values [2]int
	value := 0
	index := 0
	for _, c := range uname.Release {
		if '0' <= c && c <= '9' {
			value = value*10 + int(c-'0')
			continue
		}
		values[index] = value
		index++
		if index >= len(values) {
			break
		}
		value = 0
	}
	return values[0] > 5 || values[0] == 5 && values[1] >= 12
}
