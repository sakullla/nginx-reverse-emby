//go:build linux && amd64

package process

import "golang.org/x/sys/unix"

const (
	linuxSandboxArchSupported = true
	linuxSeccompAuditArch     = uint32(unix.AUDIT_ARCH_X86_64)
	linuxSeccompX32Bit        = uint32(0x40000000)
)
