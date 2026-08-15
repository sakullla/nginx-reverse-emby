//go:build linux && arm64

package process

import "golang.org/x/sys/unix"

const (
	linuxSandboxArchSupported = true
	linuxSeccompAuditArch     = uint32(unix.AUDIT_ARCH_AARCH64)
	linuxSeccompX32Bit        = uint32(0)
)

var linuxSeccompProcessCreationSyscalls = []uint32{unix.SYS_CLONE3}
